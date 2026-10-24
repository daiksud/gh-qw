package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/procio"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const cloneParallelLimit = 6

// ErrCloneUsage identifies clone command failures that require exit status 2.
var ErrCloneUsage = errors.New("invalid clone usage")

// CloneUsageError wraps a clone-specific usage failure.
type CloneUsageError struct {
	Err error
}

// Error implements error.
func (e *CloneUsageError) Error() string {
	if e == nil || e.Err == nil {
		return ErrCloneUsage.Error()
	}
	return e.Err.Error()
}

// Unwrap preserves the underlying parser or validation error.
func (e *CloneUsageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is makes clone usage failures discoverable by the root application.
func (e *CloneUsageError) Is(target error) bool {
	return target == ErrCloneUsage
}

// CloneAggregateError reports every failure observed by a parallel clone. Usage
// failures take precedence for exit-status selection.
type CloneAggregateError struct {
	usageErrors   []error
	runtimeErrors []error
}

// Error implements error.
func (e *CloneAggregateError) Error() string {
	if e == nil {
		return "parallel clone failed"
	}

	errorsForMessage := e.runtimeErrors
	kind := "runtime"
	if len(e.usageErrors) != 0 {
		errorsForMessage = e.usageErrors
		kind = "usage"
	}
	switch len(errorsForMessage) {
	case 0:
		return "parallel clone failed"
	case 1:
		return errorsForMessage[0].Error()
	default:
		return fmt.Sprintf(
			"parallel clone failed with %d usage and %d runtime errors; first %s error: %v",
			len(e.usageErrors),
			len(e.runtimeErrors),
			kind,
			errorsForMessage[0],
		)
	}
}

// Is exposes usage precedence without hiding runtime causes.
func (e *CloneAggregateError) Is(target error) bool {
	return target == ErrCloneUsage && e != nil && len(e.usageErrors) != 0
}

// Unwrap exposes every item failure to errors.Is and errors.As.
func (e *CloneAggregateError) Unwrap() []error {
	if e == nil {
		return nil
	}
	result := make([]error, 0, len(e.usageErrors)+len(e.runtimeErrors))
	result = append(result, e.usageErrors...)
	result = append(result, e.runtimeErrors...)
	return result
}

// UsageErrors returns a defensive copy of invalid item failures.
func (e *CloneAggregateError) UsageErrors() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.usageErrors...)
}

// RuntimeErrors returns a defensive copy of operational item failures.
func (e *CloneAggregateError) RuntimeErrors() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.runtimeErrors...)
}

// CloneGitOperations is the mutation surface required by clone. It delegates
// acquisition to gh so GitHub authentication and host resolution follow gh's
// own rules instead of an ambient Git credential helper.
type CloneGitOperations interface {
	RepoClone(context.Context, ghcmd.CloneOptions) error
}

// CloneGitFactory creates gh operations with command-scoped streams and the
// standard gh environment. The subprocess never receives the caller's stdin.
type CloneGitFactory func(stdout, stderr io.Writer) CloneGitOperations

// CloneDiscoverFunc discovers ordinary main repositories below configured roots.
type CloneDiscoverFunc func(context.Context, []string) (local.DiscoveryResult, error)

// CloneIdentityResolver completes a bare repository name from gh authentication.
type CloneIdentityResolver interface {
	ResolveIdentity(context.Context) (ghapi.Identity, error)
}

// CloneTerminalDetector reports whether a reader is an interactive terminal.
type CloneTerminalDetector func(io.Reader) bool

// CloneDependencies supplies all external operations used by NewCloneCommand.
type CloneDependencies struct {
	RootResolver     RootResolver
	GitFactory       CloneGitFactory
	MkdirAll         func(string, fs.FileMode) error
	Lstat            func(string) (fs.FileInfo, error)
	Discover         CloneDiscoverFunc
	IdentityResolver CloneIdentityResolver
	OnAcquired       func(string, string) error
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	IsTerminal       CloneTerminalDetector
	WarningSink      local.WarningSink
	WorkingDir       string
}

type cloneFlags struct {
	ssh         bool
	shallow     bool
	branch      string
	noRecursive bool
	silent      bool
	parallel    bool
	partial     string
}

type cloneTarget struct {
	exists     bool
	repository local.Repository
}

type cloneParsedItem struct {
	input string
	spec  repospec.Spec
}

type cloneParallelJob struct {
	item  cloneParsedItem
	state *cloneTarget
	wait  <-chan struct{}
	done  chan struct{}
}

type cloneParallelResult struct {
	input string
	path  string
	err   error
}

// cloneLockedWriter serializes writes from multiple goroutines (a --parallel
// clone) onto a single diagnostic stream. passthrough additionally lets an
// ordered (non-parallel) clone expose the *os.File behind writer, when there
// is one, so a gh or git subprocess can inherit gh-qw's own standard error
// descriptor directly instead of being copied to over a pipe — restoring
// terminal detection for interactive progress and color. passthrough must
// stay false for a parallel run: handing a subprocess the raw descriptor
// bypasses mu, which is only safe when at most one subprocess writes to it
// at a time.
type cloneLockedWriter struct {
	mu          sync.Mutex
	writer      io.Writer
	passthrough bool
}

func (w *cloneLockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

// PassthroughFile implements procio.FileProvider.
func (w *cloneLockedWriter) PassthroughFile() *os.File {
	if w == nil || !w.passthrough {
		return nil
	}
	return procio.PassthroughFile(w.writer)
}

// NewCloneCommand returns the repository acquisition command.
func NewCloneCommand(deps CloneDependencies) *cobra.Command {
	resolved := cloneResolveDependencies(deps)
	stdin, stdout, stderr := resolved.Stdin, resolved.Stdout, resolved.Stderr
	flags := cloneFlags{}

	command := &cobra.Command{
		Use:           "clone [flags] [<repo>...]",
		Short:         "Clone and register new repositories",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, args []string) error {
			filter, err := clonePartialFilter(flags.partial)
			if err != nil {
				return cloneUsageError(err)
			}
			if flags.branch != "" {
				if err := local.ValidateBranch(command.Context(), flags.branch); err != nil {
					if errors.Is(err, local.ErrInvalidBranch) {
						return cloneUsageError(fmt.Errorf("invalid --branch value %q: %w", flags.branch, err))
					}
					return err
				}
			}

			inputs, err := cloneRepositoryInputs(
				command.InOrStdin(),
				args,
				resolved.IsTerminal,
			)
			if err != nil {
				return err
			}
			if len(inputs) == 0 {
				return nil
			}

			diagnostic := &cloneLockedWriter{writer: command.ErrOrStderr(), passthrough: !flags.parallel}
			warn := resolved.WarningSink

			if flags.parallel {
				return cloneRunParallel(
					command.Context(),
					inputs,
					flags,
					filter,
					resolved,
					warn,
					command.OutOrStdout(),
					diagnostic,
				)
			}
			return cloneRunOrdered(
				command.Context(),
				inputs,
				flags,
				filter,
				resolved,
				warn,
				command.OutOrStdout(),
				diagnostic,
			)
		},
	}

	command.Flags().BoolVarP(&flags.ssh, "p", "p", false, "Use SSH for HTTPS and shorthand repositories")
	command.Flags().BoolVar(&flags.shallow, "shallow", false, "Clone with depth 1")
	command.Flags().StringVarP(&flags.branch, "branch", "b", "", "Clone a single branch")
	command.Flags().BoolVar(&flags.noRecursive, "no-recursive", false, "Disable recursive submodules")
	command.Flags().BoolVarP(&flags.silent, "silent", "s", false, "Suppress ordinary Git progress")
	command.Flags().BoolVarP(&flags.parallel, "parallel", "P", false, "Process at most six repositories concurrently")
	command.Flags().StringVar(&flags.partial, "partial", "", "Partial clone mode: blobless or treeless")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return cloneUsageError(err)
	})
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)

	return command
}

func cloneResolveDependencies(deps CloneDependencies) CloneDependencies {
	deps.Stdin = commandStdin(deps.Stdin)
	deps.Stdout = commandStdout(deps.Stdout)
	deps.Stderr = commandStderr(deps.Stderr)
	if deps.RootResolver == nil {
		deps.RootResolver = rootpkg.NewResolver()
	}
	if deps.IsTerminal == nil {
		deps.IsTerminal = cloneIsTerminal
	}
	if deps.GitFactory == nil {
		deps.GitFactory = func(stdout, stderr io.Writer) CloneGitOperations {
			return &ghcmd.Runner{Stdout: stdout, Stderr: stderr}
		}
	}
	if deps.MkdirAll == nil {
		deps.MkdirAll = os.MkdirAll
	}
	if deps.Lstat == nil {
		deps.Lstat = os.Lstat
	}
	if deps.Discover == nil {
		deps.Discover = func(ctx context.Context, roots []string) (local.DiscoveryResult, error) {
			return local.DiscoverRepositories(ctx, roots)
		}
	}
	if deps.IdentityResolver == nil {
		deps.IdentityResolver = ghapi.NewClient(deps.Stderr)
	}
	return deps
}

func cloneRunOrdered(
	ctx context.Context,
	inputs []string,
	flags cloneFlags,
	filter ghcmd.PartialFilter,
	deps CloneDependencies,
	warn local.WarningSink,
	stdout, stderr io.Writer,
) error {
	var roots []string
	var states map[string]*cloneTarget
	for _, input := range inputs {
		parsed, err := repospec.Parse(input)
		if err != nil {
			return cloneUsageError(err)
		}
		if roots == nil {
			roots, err = cloneResolveRoots(deps.RootResolver)
			if err != nil {
				return err
			}
		}
		spec, err := cloneResolveRepository(ctx, parsed, roots, flags.ssh, deps)
		if err != nil {
			if errors.Is(err, repospec.ErrUsage) {
				return cloneUsageError(err)
			}
			return err
		}
		if states == nil {
			discovery, err := deps.Discover(ctx, roots)
			if err != nil {
				return fmt.Errorf("discover existing repositories: %w", err)
			}
			if err := cloneEmitDiscoveryWarnings(discovery.Warnings, warn, stderr); err != nil {
				return fmt.Errorf("write discovery warning: %w", err)
			}
			states = cloneInitialTargetStates(discovery.Repositories)
		}

		state, err := cloneResolveTarget(states, spec, roots[0])
		if err != nil {
			return fmt.Errorf("prepare repository %q: %w", input, err)
		}
		path, err := cloneAcquire(
			ctx,
			spec,
			state,
			flags,
			filter,
			deps,
			stderr,
		)
		if err != nil {
			return fmt.Errorf("clone %q: %w", input, err)
		}
		if deps.OnAcquired != nil {
			if err := deps.OnAcquired(spec.Identity.String(), path); err != nil {
				return fmt.Errorf("repository acquired at %q but location registration failed: %w", path, err)
			}
		}
		if err := commandWritePath(stdout, path); err != nil {
			return err
		}
	}
	return nil
}

func cloneRunParallel(
	ctx context.Context,
	inputs []string,
	flags cloneFlags,
	filter ghcmd.PartialFilter,
	deps CloneDependencies,
	warn local.WarningSink,
	stdout, stderr io.Writer,
) error {
	aggregate := &CloneAggregateError{}
	var roots []string
	parsed := make([]cloneParsedItem, 0, len(inputs))
	for _, input := range inputs {
		syntax, err := repospec.Parse(input)
		if err != nil {
			aggregate.usageErrors = append(
				aggregate.usageErrors,
				fmt.Errorf("clone %q: %w", input, err),
			)
			continue
		}
		if roots == nil {
			roots, err = cloneResolveRoots(deps.RootResolver)
			if err != nil {
				aggregate.runtimeErrors = append(aggregate.runtimeErrors, err)
				return aggregate
			}
		}
		spec, err := cloneResolveRepository(ctx, syntax, roots, flags.ssh, deps)
		if err != nil {
			err = fmt.Errorf("clone %q: %w", input, err)
			if errors.Is(err, repospec.ErrUsage) {
				aggregate.usageErrors = append(aggregate.usageErrors, err)
			} else {
				aggregate.runtimeErrors = append(aggregate.runtimeErrors, err)
			}
			continue
		}
		parsed = append(parsed, cloneParsedItem{input: input, spec: spec})
	}

	if len(parsed) == 0 {
		if len(aggregate.usageErrors) != 0 || len(aggregate.runtimeErrors) != 0 {
			return aggregate
		}
		return nil
	}

	discovery, err := deps.Discover(ctx, roots)
	if err != nil {
		aggregate.runtimeErrors = append(
			aggregate.runtimeErrors,
			fmt.Errorf("discover existing repositories: %w", err),
		)
		return aggregate
	}
	if err := cloneEmitDiscoveryWarnings(discovery.Warnings, warn, stderr); err != nil {
		aggregate.runtimeErrors = append(aggregate.runtimeErrors, fmt.Errorf("write discovery warning: %w", err))
		return aggregate
	}

	states := cloneInitialTargetStates(discovery.Repositories)
	previous := make(map[string]<-chan struct{})
	jobs := make([]cloneParallelJob, 0, len(parsed))
	for _, item := range parsed {
		state, err := cloneResolveTarget(states, item.spec, roots[0])
		if err != nil {
			aggregate.runtimeErrors = append(
				aggregate.runtimeErrors,
				fmt.Errorf("prepare repository %q: %w", item.input, err),
			)
			continue
		}

		done := make(chan struct{})
		jobs = append(jobs, cloneParallelJob{
			item:  item,
			state: state,
			wait:  previous[item.spec.Identity.String()],
			done:  done,
		})
		previous[item.spec.Identity.String()] = done
	}

	results := make(chan cloneParallelResult, len(jobs))
	semaphore := make(chan struct{}, cloneParallelLimit)
	var workers sync.WaitGroup
	for _, job := range jobs {
		workers.Add(1)
		go func(job cloneParallelJob) {
			defer workers.Done()
			if job.wait != nil {
				select {
				case <-job.wait:
				case <-ctx.Done():
					results <- cloneParallelResult{input: job.item.input, err: ctx.Err()}
					close(job.done)
					return
				}
			}

			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- cloneParallelResult{input: job.item.input, err: ctx.Err()}
				close(job.done)
				return
			}
			path, err := cloneAcquire(
				ctx,
				job.item.spec,
				job.state,
				flags,
				filter,
				deps,
				stderr,
			)
			<-semaphore
			if err == nil && deps.OnAcquired != nil {
				if registerErr := deps.OnAcquired(job.item.spec.Identity.String(), path); registerErr != nil {
					err = fmt.Errorf("repository acquired at %q but location registration failed: %w", path, registerErr)
				}
			}
			results <- cloneParallelResult{input: job.item.input, path: path, err: err}
			close(job.done)
		}(job)
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	for result := range results {
		if result.err != nil {
			aggregate.runtimeErrors = append(
				aggregate.runtimeErrors,
				fmt.Errorf("clone %q: %w", result.input, result.err),
			)
			continue
		}
		if err := commandWritePath(stdout, result.path); err != nil {
			aggregate.runtimeErrors = append(aggregate.runtimeErrors, err)
		}
	}

	if len(aggregate.usageErrors) != 0 || len(aggregate.runtimeErrors) != 0 {
		return aggregate
	}
	return nil
}

func cloneResolveRoots(resolver RootResolver) ([]string, error) {
	roots, err := resolver.Resolve()
	if err != nil {
		return nil, err
	}
	if len(roots.RepositoryRoots) == 0 || roots.Primary() == "" {
		return nil, cloneUsageError(errors.New("no repository root is configured"))
	}
	return roots.RepositoryRoots, nil
}

func cloneResolveRepository(
	ctx context.Context,
	parsed repospec.Input,
	roots []string,
	ssh bool,
	deps CloneDependencies,
) (repospec.Spec, error) {
	return parsed.Resolve(ctx, repospec.Options{
		DefaultHost: os.Getenv("GH_HOST"),
		SSH:         ssh,
		Roots:       roots,
		WorkingDir:  deps.WorkingDir,
		// clone clones through `gh repo clone`, which never accepts a file://
		// URL, so file:// input is rejected here rather than failing later
		// as an opaque gh error.
		RejectFileScheme: true,
		ResolveIdentity: func() (string, string, error) {
			identity, err := deps.IdentityResolver.ResolveIdentity(ctx)
			if err != nil {
				return "", "", err
			}
			return identity.Host, identity.Login, nil
		},
	})
}

func cloneInitialTargetStates(repositories []local.Repository) map[string]*cloneTarget {
	states := make(map[string]*cloneTarget, len(repositories))
	for _, repository := range repositories {
		current, found := states[repository.Identity]
		if found && current.repository.RootIndex <= repository.RootIndex {
			continue
		}
		repository.Path = filepath.Clean(repository.Path)
		repository.Root = filepath.Clean(repository.Root)
		states[repository.Identity] = &cloneTarget{
			exists:     true,
			repository: repository,
		}
	}
	return states
}

func cloneResolveTarget(
	states map[string]*cloneTarget,
	spec repospec.Spec,
	primaryRoot string,
) (*cloneTarget, error) {
	if state, found := states[spec.Identity.String()]; found {
		return state, nil
	}

	path, err := local.MainPath(primaryRoot, spec.Host, spec.Owner, spec.Repo)
	if err != nil {
		return nil, err
	}
	state := &cloneTarget{
		repository: local.Repository{
			Identity:  spec.Identity.String(),
			Host:      spec.Host,
			Owner:     spec.Owner,
			Repo:      spec.Repo,
			Path:      path,
			Root:      primaryRoot,
			RootIndex: 0,
		},
	}
	states[spec.Identity.String()] = state
	return state, nil
}

func cloneAcquire(
	ctx context.Context,
	spec repospec.Spec,
	state *cloneTarget,
	flags cloneFlags,
	filter ghcmd.PartialFilter,
	deps CloneDependencies,
	stderr io.Writer,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if state == nil {
		return "", errors.New("repository target state is nil")
	}

	if state.exists {
		return "", fmt.Errorf(
			"repository %q is already registered at %q; use add for existing checkouts",
			state.repository.Identity,
			state.repository.Path,
		)
	}

	if err := cloneRequireMissingDestination(deps.Lstat, state.repository.Path); err != nil {
		return "", err
	}

	path, err := rootpkg.PhysicalizeTarget(
		state.repository.Root,
		state.repository.Path,
		rootpkg.StrictlyUnder,
	)
	if err != nil {
		return "", fmt.Errorf("revalidate clone destination containment: %w", err)
	}
	parent, err := rootpkg.PhysicalizeTarget(
		state.repository.Root,
		filepath.Dir(path),
		rootpkg.StrictlyUnder,
	)
	if err != nil {
		return "", fmt.Errorf("revalidate clone parent containment: %w", err)
	}
	if err := deps.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create clone parent %q: %w", parent, err)
	}
	if err := cloneRequireMissingDestination(deps.Lstat, state.repository.Path); err != nil {
		return "", err
	}
	path, err = rootpkg.PhysicalizeTarget(
		state.repository.Root,
		path,
		rootpkg.StrictlyUnder,
	)
	if err != nil {
		return "", fmt.Errorf("revalidate clone destination after creating parents: %w", err)
	}
	if err := cloneRequireMissingDestination(deps.Lstat, path); err != nil {
		return "", err
	}

	branch := flags.branch
	if spec.Branch != "" {
		branch = spec.Branch
	}
	git, err := cloneGitOperations(deps.GitFactory, stderr, flags.silent || flags.parallel)
	if err != nil {
		return "", err
	}
	err = git.RepoClone(ctx, ghcmd.CloneOptions{
		URL:         spec.CloneURL,
		Destination: path,
		Shallow:     flags.shallow,
		Branch:      branch,
		Submodules:  cloneSubmoduleMode(flags.noRecursive),
		Filter:      filter,
	})
	if err != nil {
		return "", clonePreserveSilentGhError(err, stderr, flags.silent || flags.parallel)
	}

	state.exists = true
	state.repository.Path = path
	return cloneAbsolutePath(path)
}

func cloneRequireMissingDestination(
	lstat func(string) (fs.FileInfo, error),
	path string,
) error {
	if lstat == nil {
		return errors.New("check clone destination: lstat is nil")
	}
	if _, err := lstat(path); err == nil {
		return fmt.Errorf("clone destination %q already exists; use add for existing checkouts", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check clone destination %q: %w", path, err)
	}
	return nil
}

// cloneGitOperations builds the Git operations used for one repository. gh's
// own stdout and stderr are both directed at progress (gh-qw's stderr,
// or io.Discard when silent): sending gh's stdout there too, rather than to
// gh-qw's own stdout, keeps gh-qw's stdout contract intact (result paths
// only) while still surfacing gh's output. When progress is not silenced
// and ultimately wraps gh-qw's real stderr file (see
// cloneLockedWriter.PassthroughFile), the Runner returned by factory detects
// that and lets gh inherit the file descriptor directly instead of writing
// through a pipe, restoring gh's own terminal detection for interactive
// progress and color.
func cloneGitOperations(
	factory CloneGitFactory,
	stderr io.Writer,
	silent bool,
) (CloneGitOperations, error) {
	progress := stderr
	if silent {
		progress = io.Discard
	}
	git := factory(progress, progress)
	if git == nil {
		return nil, errors.New("create Git operations: factory returned nil")
	}
	return git, nil
}

func cloneSubmoduleMode(noRecursive bool) ghcmd.SubmoduleMode {
	if noRecursive {
		return ghcmd.SubmodulesDisabled
	}
	return ghcmd.SubmodulesRecursive
}

func clonePartialFilter(value string) (ghcmd.PartialFilter, error) {
	switch value {
	case "":
		return ghcmd.PartialFilterNone, nil
	case "blobless":
		return ghcmd.PartialFilterBlobless, nil
	case "treeless":
		return ghcmd.PartialFilterTreeless, nil
	default:
		return ghcmd.PartialFilterNone, fmt.Errorf(
			"invalid --partial value %q: must be blobless or treeless",
			value,
		)
	}
}

func cloneRepositoryInputs(
	stdin io.Reader,
	args []string,
	isTerminal CloneTerminalDetector,
) ([]string, error) {
	if len(args) != 0 {
		return append([]string(nil), args...), nil
	}
	if isTerminal(stdin) {
		return nil, cloneUsageError(errors.New("clone requires a repository argument or non-terminal stdin"))
	}

	var inputs []string
	reader := bufio.NewReader(stdin)
	for {
		line, err := reader.ReadString('\n')
		input := strings.TrimSpace(line)
		if input != "" {
			inputs = append(inputs, input)
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF):
			return inputs, nil
		default:
			return nil, fmt.Errorf("read repository specifications from stdin: %w", err)
		}
	}
}

func cloneAbsolutePath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("result path %q is not absolute", path)
	}
	return filepath.Clean(path), nil
}

func commandWritePath(output io.Writer, path string) error {
	path, err := cloneAbsolutePath(path)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, local.NormalizePathForOutput(path)); err != nil {
		return fmt.Errorf("write repository path: %w", err)
	}
	return nil
}

func cloneEmitDiscoveryWarnings(warnings []local.Warning, sink local.WarningSink, writer io.Writer) error {
	if sink == nil {
		return commandWriteWarnings(writer, warnings)
	}
	for _, warning := range warnings {
		sink(warning)
	}
	return nil
}

// cloneStderrOutputter is satisfied by errors that retain a bounded stderr
// tail, such as *ghcmd.CommandError. Depending on this narrow capability
// instead of the concrete type keeps clonePreserveSilentGhError usable with any
// gh operation failure, including test doubles.
type cloneStderrOutputter interface {
	StderrOutput() []byte
}

func clonePreserveSilentGhError(err error, stderr io.Writer, silent bool) error {
	if !silent {
		return err
	}

	var outputter cloneStderrOutputter
	if !errors.As(err, &outputter) {
		return err
	}
	diagnostic := outputter.StderrOutput()
	if len(diagnostic) == 0 {
		return err
	}
	if diagnostic[len(diagnostic)-1] != '\n' {
		diagnostic = append(diagnostic, '\n')
	}
	if _, writeErr := stderr.Write(diagnostic); writeErr != nil {
		return errors.Join(err, fmt.Errorf("write gh error output: %w", writeErr))
	}
	return err
}

func cloneUsageError(err error) error {
	if err == nil {
		return nil
	}
	var usage *CloneUsageError
	if errors.As(err, &usage) {
		return err
	}
	return &CloneUsageError{Err: err}
}

func cloneIsTerminal(reader io.Reader) bool {
	descriptor, ok := reader.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(descriptor.Fd()))
}
