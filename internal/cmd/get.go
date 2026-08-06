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
	"github.com/daiksud/gh-qw/internal/ghauth"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/procio"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const getParallelLimit = 6

// ErrGetUsage identifies get command failures that require exit status 2.
var ErrGetUsage = errors.New("invalid get usage")

// GetUsageError wraps a get-specific usage failure.
type GetUsageError struct {
	Err error
}

// Error implements error.
func (e *GetUsageError) Error() string {
	if e == nil || e.Err == nil {
		return ErrGetUsage.Error()
	}
	return e.Err.Error()
}

// Unwrap preserves the underlying parser or validation error.
func (e *GetUsageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is makes get usage failures discoverable by the root application.
func (e *GetUsageError) Is(target error) bool {
	return target == ErrGetUsage
}

// GetAggregateError reports every failure observed by a parallel get. Usage
// failures take precedence for exit-status selection.
type GetAggregateError struct {
	usageErrors   []error
	runtimeErrors []error
}

// Error implements error.
func (e *GetAggregateError) Error() string {
	if e == nil {
		return "parallel get failed"
	}

	errorsForMessage := e.runtimeErrors
	kind := "runtime"
	if len(e.usageErrors) != 0 {
		errorsForMessage = e.usageErrors
		kind = "usage"
	}
	switch len(errorsForMessage) {
	case 0:
		return "parallel get failed"
	case 1:
		return errorsForMessage[0].Error()
	default:
		return fmt.Sprintf(
			"parallel get failed with %d usage and %d runtime errors; first %s error: %v",
			len(e.usageErrors),
			len(e.runtimeErrors),
			kind,
			errorsForMessage[0],
		)
	}
}

// Is exposes usage precedence without hiding runtime causes.
func (e *GetAggregateError) Is(target error) bool {
	return target == ErrGetUsage && e != nil && len(e.usageErrors) != 0
}

// Unwrap exposes every item failure to errors.Is and errors.As.
func (e *GetAggregateError) Unwrap() []error {
	if e == nil {
		return nil
	}
	result := make([]error, 0, len(e.usageErrors)+len(e.runtimeErrors))
	result = append(result, e.usageErrors...)
	result = append(result, e.runtimeErrors...)
	return result
}

// UsageErrors returns a defensive copy of invalid item failures.
func (e *GetAggregateError) UsageErrors() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.usageErrors...)
}

// RuntimeErrors returns a defensive copy of operational item failures.
func (e *GetAggregateError) RuntimeErrors() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.runtimeErrors...)
}

// GetGitOperations is the mutation surface required by get. Both operations
// delegate to gh so that GitHub authentication and host resolution follow
// gh's own rules instead of an ambient Git credential helper.
type GetGitOperations interface {
	RepoClone(context.Context, ghcmd.CloneOptions) error
	RepoSync(context.Context, string, ghcmd.SyncOptions) error
}

// GetGitFactory creates Git operations with command-scoped streams. When
// token is non-empty, the returned GetGitOperations authenticates gh
// subprocesses as that account instead of gh's own active account (see
// internal/ghauth). The gh subprocess never receives the caller's own
// standard input; see ghcmd.Runner.
type GetGitFactory func(stdout, stderr io.Writer, token string) GetGitOperations

// GetDiscoverFunc discovers ordinary main repositories below configured roots.
type GetDiscoverFunc func(context.Context, []string) (local.DiscoveryResult, error)

// GetIdentityResolver completes a bare repository name from gh authentication.
type GetIdentityResolver interface {
	ResolveIdentity(context.Context) (ghapi.Identity, error)
}

// GetTerminalDetector reports whether a reader is an interactive terminal.
type GetTerminalDetector func(io.Reader) bool

// GetDependencies supplies all external operations used by NewGetCommand.
type GetDependencies struct {
	RootResolver     RootResolver
	GitFactory       GetGitFactory
	AccountResolver  AccountResolver
	MkdirAll         func(string, fs.FileMode) error
	Discover         GetDiscoverFunc
	IdentityResolver GetIdentityResolver
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	IsTerminal       GetTerminalDetector
	WarningSink      local.WarningSink
	WorkingDir       string
}

type getFlags struct {
	update      bool
	ssh         bool
	shallow     bool
	branch      string
	noRecursive bool
	silent      bool
	parallel    bool
	partial     string
}

type getResolvedDependencies struct {
	rootResolver     RootResolver
	gitFactory       GetGitFactory
	accountResolver  AccountResolver
	mkdirAll         func(string, fs.FileMode) error
	discover         GetDiscoverFunc
	identityResolver GetIdentityResolver
	isTerminal       GetTerminalDetector
	warningSink      local.WarningSink
	workingDir       string
}

type getTarget struct {
	exists     bool
	repository local.Repository
}

type getParsedItem struct {
	input string
	spec  repospec.Spec
}

type getParallelJob struct {
	item       getParsedItem
	state      *getTarget
	resolution ghauth.Resolution
	wait       <-chan struct{}
	done       chan struct{}
}

type getParallelResult struct {
	input string
	path  string
	err   error
}

// getLockedWriter serializes writes from multiple goroutines (a --parallel
// get) onto a single diagnostic stream. passthrough additionally lets an
// ordered (non-parallel) get expose the *os.File behind writer, when there
// is one, so a gh or git subprocess can inherit gh-qw's own standard error
// descriptor directly instead of being copied to over a pipe — restoring
// terminal detection for interactive progress and color. passthrough must
// stay false for a parallel run: handing a subprocess the raw descriptor
// bypasses mu, which is only safe when at most one subprocess writes to it
// at a time.
type getLockedWriter struct {
	mu          sync.Mutex
	writer      io.Writer
	passthrough bool
}

func (w *getLockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

// PassthroughFile implements procio.FileProvider.
func (w *getLockedWriter) PassthroughFile() *os.File {
	if w == nil || !w.passthrough {
		return nil
	}
	return procio.PassthroughFile(w.writer)
}

// NewGetCommand returns the repository acquisition command.
func NewGetCommand(deps GetDependencies) *cobra.Command {
	resolved, stdin, stdout, stderr := getResolveDependencies(deps)
	flags := getFlags{}

	command := &cobra.Command{
		Use:           "get [flags] [<repo>...]",
		Aliases:       []string{"clone"},
		Short:         "Clone or update repositories",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, args []string) error {
			filter, err := getPartialFilter(flags.partial)
			if err != nil {
				return getUsageError(err)
			}
			if flags.branch != "" {
				if err := local.ValidateBranch(flags.branch); err != nil {
					return getUsageError(fmt.Errorf("invalid --branch value %q: %w", flags.branch, err))
				}
			}

			inputs, err := getRepositoryInputs(
				command.InOrStdin(),
				args,
				resolved.isTerminal,
			)
			if err != nil {
				return err
			}
			if len(inputs) == 0 {
				return nil
			}

			roots, err := resolved.rootResolver.Resolve()
			if err != nil {
				return err
			}
			if len(roots.RepositoryRoots) == 0 || roots.Primary() == "" {
				return getUsageError(errors.New("no repository root is configured"))
			}

			diagnostic := &getLockedWriter{writer: command.ErrOrStderr(), passthrough: !flags.parallel}
			warn := resolved.warningSink
			if warn == nil {
				warn = getStderrWarningSink(diagnostic)
			}

			if flags.parallel {
				return getRunParallel(
					command.Context(),
					inputs,
					roots.RepositoryRoots,
					flags,
					filter,
					resolved,
					warn,
					command.OutOrStdout(),
					diagnostic,
				)
			}
			return getRunOrdered(
				command.Context(),
				inputs,
				roots.RepositoryRoots,
				flags,
				filter,
				resolved,
				warn,
				command.OutOrStdout(),
				diagnostic,
			)
		},
	}

	command.Flags().BoolVarP(&flags.update, "update", "u", false, "Fast-forward an existing repository")
	command.Flags().BoolVarP(&flags.ssh, "p", "p", false, "Use SSH for HTTPS and shorthand repositories")
	command.Flags().BoolVar(&flags.shallow, "shallow", false, "Clone with depth 1")
	command.Flags().StringVarP(&flags.branch, "branch", "b", "", "Clone a single branch")
	command.Flags().BoolVar(&flags.noRecursive, "no-recursive", false, "Disable recursive submodules")
	command.Flags().BoolVarP(&flags.silent, "silent", "s", false, "Suppress ordinary Git progress")
	command.Flags().BoolVarP(&flags.parallel, "parallel", "P", false, "Process at most six repositories concurrently")
	command.Flags().StringVar(&flags.partial, "partial", "", "Partial clone mode: blobless or treeless")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return getUsageError(err)
	})
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)

	return command
}

func getResolveDependencies(
	deps GetDependencies,
) (getResolvedDependencies, io.Reader, io.Writer, io.Writer) {
	stdin := deps.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := deps.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := deps.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	rootResolver := deps.RootResolver
	if rootResolver == nil {
		rootResolver = rootpkg.NewResolver()
	}
	isTerminal := deps.IsTerminal
	if isTerminal == nil {
		isTerminal = getIsTerminal
	}
	gitFactory := deps.GitFactory
	if gitFactory == nil {
		gitFactory = func(stdout, stderr io.Writer, token string) GetGitOperations {
			runner := ghcmd.Runner{
				Stdout: stdout,
				Stderr: stderr,
			}.WithToken(token)
			return &runner
		}
	}
	accountResolver := deps.AccountResolver
	if accountResolver == nil {
		accountResolver = ghauth.NewResolver(ghauth.ResolverOptions{
			Runner:     ghcmd.NewRunner(),
			Cache:      ghauth.NewCache(),
			Stdin:      stdin,
			Stderr:     stderr,
			IsTerminal: isTerminal,
		})
	}
	mkdirAll := deps.MkdirAll
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	discover := deps.Discover
	if discover == nil {
		discover = func(ctx context.Context, roots []string) (local.DiscoveryResult, error) {
			return local.DiscoverRepositories(ctx, roots)
		}
	}
	identityResolver := deps.IdentityResolver
	if identityResolver == nil {
		identityResolver = ghapi.NewClient()
	}

	return getResolvedDependencies{
		rootResolver:     rootResolver,
		gitFactory:       gitFactory,
		accountResolver:  accountResolver,
		mkdirAll:         mkdirAll,
		discover:         discover,
		identityResolver: identityResolver,
		isTerminal:       isTerminal,
		warningSink:      deps.WarningSink,
		workingDir:       deps.WorkingDir,
	}, stdin, stdout, stderr
}

func getRunOrdered(
	ctx context.Context,
	inputs []string,
	roots []string,
	flags getFlags,
	filter ghcmd.PartialFilter,
	deps getResolvedDependencies,
	warn local.WarningSink,
	stdout, stderr io.Writer,
) error {
	discovery, err := deps.discover(ctx, roots)
	if err != nil {
		return fmt.Errorf("discover existing repositories: %w", err)
	}
	getEmitDiscoveryWarnings(discovery.Warnings, warn)

	states := getInitialTargetStates(discovery.Repositories)
	for _, input := range inputs {
		spec, err := getParseRepository(ctx, input, roots, flags.ssh, deps)
		if err != nil {
			return getUsageError(err)
		}

		state, err := getResolveTarget(states, spec, roots[0])
		if err != nil {
			return fmt.Errorf("prepare repository %q: %w", input, err)
		}
		// Resolve the gh account only when a network operation will
		// actually happen (a new clone, or an existing checkout with
		// --update); an already-satisfied get never lists gh accounts or
		// prompts.
		var resolution ghauth.Resolution
		if !state.exists || flags.update {
			resolution, err = getResolveAccount(ctx, deps.accountResolver, spec.Host, spec.Owner)
			if err != nil {
				return fmt.Errorf("get %q: %w", input, err)
			}
		}
		path, err := getAcquire(
			ctx,
			spec,
			state,
			flags,
			filter,
			deps,
			resolution,
			stderr,
		)
		if err != nil {
			return fmt.Errorf("get %q: %w", input, err)
		}
		if err := getWritePath(stdout, path); err != nil {
			return err
		}
	}
	return nil
}

func getRunParallel(
	ctx context.Context,
	inputs []string,
	roots []string,
	flags getFlags,
	filter ghcmd.PartialFilter,
	deps getResolvedDependencies,
	warn local.WarningSink,
	stdout, stderr io.Writer,
) error {
	aggregate := &GetAggregateError{}
	parsed := make([]getParsedItem, 0, len(inputs))
	for _, input := range inputs {
		spec, err := getParseRepository(ctx, input, roots, flags.ssh, deps)
		if err != nil {
			aggregate.usageErrors = append(
				aggregate.usageErrors,
				fmt.Errorf("get %q: %w", input, err),
			)
			continue
		}
		parsed = append(parsed, getParsedItem{input: input, spec: spec})
	}

	if len(parsed) == 0 {
		if len(aggregate.usageErrors) != 0 {
			return aggregate
		}
		return nil
	}

	discovery, err := deps.discover(ctx, roots)
	if err != nil {
		aggregate.runtimeErrors = append(
			aggregate.runtimeErrors,
			fmt.Errorf("discover existing repositories: %w", err),
		)
		return aggregate
	}
	getEmitDiscoveryWarnings(discovery.Warnings, warn)

	states := getInitialTargetStates(discovery.Repositories)
	previous := make(map[string]<-chan struct{})
	jobs := make([]getParallelJob, 0, len(parsed))
	// Resolve every distinct host/owner's gh account once, before spawning
	// any worker, and only for items that will actually perform a network
	// operation (a new clone, or an existing checkout with --update).
	// Resolving up front (rather than inside each worker goroutine) keeps
	// any interactive account-selection prompt (see internal/ghauth)
	// single-threaded and deterministic instead of racing concurrent
	// workers for the controlling terminal; skipping it entirely for an
	// already-satisfied item matches get's non-parallel behavior.
	resolutions := make(map[string]ghauth.Resolution)
	resolutionErrors := make(map[string]error)
	for _, item := range parsed {
		state, err := getResolveTarget(states, item.spec, roots[0])
		if err != nil {
			aggregate.runtimeErrors = append(
				aggregate.runtimeErrors,
				fmt.Errorf("prepare repository %q: %w", item.input, err),
			)
			continue
		}

		if !state.exists || flags.update {
			key := strings.ToLower(item.spec.Host) + "/" + strings.ToLower(item.spec.Owner)
			resolution, resolved := resolutions[key]
			resolutionErr, failed := resolutionErrors[key]
			if !resolved && !failed {
				resolution, resolutionErr = getResolveAccount(
					ctx,
					deps.accountResolver,
					item.spec.Host,
					item.spec.Owner,
				)
				if resolutionErr != nil {
					resolutionErrors[key] = resolutionErr
				} else {
					resolutions[key] = resolution
				}
			}
			if resolutionErr != nil {
				aggregate.runtimeErrors = append(
					aggregate.runtimeErrors,
					fmt.Errorf("get %q: %w", item.input, resolutionErr),
				)
				continue
			}
		}

		key := strings.ToLower(item.spec.Host) + "/" + strings.ToLower(item.spec.Owner)
		done := make(chan struct{})
		jobs = append(jobs, getParallelJob{
			item:       item,
			state:      state,
			resolution: resolutions[key],
			wait:       previous[item.spec.Identity],
			done:       done,
		})
		previous[item.spec.Identity] = done
	}

	results := make(chan getParallelResult, len(jobs))
	semaphore := make(chan struct{}, getParallelLimit)
	var workers sync.WaitGroup
	for _, job := range jobs {
		workers.Add(1)
		go func(job getParallelJob) {
			defer workers.Done()
			if job.wait != nil {
				select {
				case <-job.wait:
				case <-ctx.Done():
					results <- getParallelResult{input: job.item.input, err: ctx.Err()}
					close(job.done)
					return
				}
			}

			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- getParallelResult{input: job.item.input, err: ctx.Err()}
				close(job.done)
				return
			}
			path, err := getAcquire(
				ctx,
				job.item.spec,
				job.state,
				flags,
				filter,
				deps,
				job.resolution,
				stderr,
			)
			<-semaphore
			results <- getParallelResult{input: job.item.input, path: path, err: err}
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
				fmt.Errorf("get %q: %w", result.input, result.err),
			)
			continue
		}
		if err := getWritePath(stdout, result.path); err != nil {
			aggregate.runtimeErrors = append(aggregate.runtimeErrors, err)
		}
	}

	if len(aggregate.usageErrors) != 0 || len(aggregate.runtimeErrors) != 0 {
		return aggregate
	}
	return nil
}

func getParseRepository(
	ctx context.Context,
	input string,
	roots []string,
	ssh bool,
	deps getResolvedDependencies,
) (repospec.Spec, error) {
	return repospec.Parse(input, repospec.Options{
		SSH:        ssh,
		Roots:      roots,
		WorkingDir: deps.workingDir,
		// get clones through `gh repo clone`, which never accepts a file://
		// URL, so file:// input is rejected here rather than failing later
		// as an opaque gh error.
		RejectFileScheme: true,
		ResolveIdentity: func() (string, string, error) {
			identity, err := deps.identityResolver.ResolveIdentity(ctx)
			if err != nil {
				return "", "", err
			}
			return identity.Host, identity.Login, nil
		},
	})
}

// getResolveAccount determines which gh account to use for host/owner.
func getResolveAccount(
	ctx context.Context,
	resolver AccountResolver,
	host, owner string,
) (ghauth.Resolution, error) {
	if resolver == nil {
		return ghauth.Resolution{}, errors.New("resolve gh account: resolver is required")
	}
	return resolver.Resolve(ctx, host, owner)
}

func getInitialTargetStates(repositories []local.Repository) map[string]*getTarget {
	states := make(map[string]*getTarget, len(repositories))
	for _, repository := range repositories {
		current, found := states[repository.Identity]
		if found && current.repository.RootIndex <= repository.RootIndex {
			continue
		}
		repository.Path = filepath.Clean(repository.Path)
		repository.Root = filepath.Clean(repository.Root)
		states[repository.Identity] = &getTarget{
			exists:     true,
			repository: repository,
		}
	}
	return states
}

func getResolveTarget(
	states map[string]*getTarget,
	spec repospec.Spec,
	primaryRoot string,
) (*getTarget, error) {
	if state, found := states[spec.Identity]; found {
		return state, nil
	}

	path, err := local.MainPath(primaryRoot, spec.Host, spec.Owner, spec.Repo)
	if err != nil {
		return nil, err
	}
	state := &getTarget{
		repository: local.Repository{
			Identity:  spec.Identity,
			Host:      spec.Host,
			Owner:     spec.Owner,
			Repo:      spec.Repo,
			Path:      path,
			Root:      primaryRoot,
			RootIndex: 0,
		},
	}
	states[spec.Identity] = state
	return state, nil
}

func getAcquire(
	ctx context.Context,
	spec repospec.Spec,
	state *getTarget,
	flags getFlags,
	filter ghcmd.PartialFilter,
	deps getResolvedDependencies,
	resolution ghauth.Resolution,
	stderr io.Writer,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if state == nil {
		return "", errors.New("repository target state is nil")
	}

	if state.exists {
		if !flags.update {
			return getAbsolutePath(state.repository.Path)
		}
		path, err := rootpkg.PhysicalizeTarget(
			state.repository.Root,
			state.repository.Path,
			rootpkg.StrictlyUnder,
		)
		if err != nil {
			return "", fmt.Errorf("revalidate existing repository containment: %w", err)
		}

		git, err := getGitOperations(deps.gitFactory, stderr, flags.silent || flags.parallel, resolution.Token)
		if err != nil {
			return "", err
		}
		err = git.RepoSync(ctx, path, ghcmd.SyncOptions{
			Source: state.repository.Owner + "/" + state.repository.Repo,
		})
		if err != nil {
			return "", getPreserveSilentGhError(wrapAccountFailureHint(err, resolution), stderr, flags.silent || flags.parallel)
		}
		state.repository.Path = path
		return getAbsolutePath(path)
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
	if err := deps.mkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create clone parent %q: %w", parent, err)
	}
	path, err = rootpkg.PhysicalizeTarget(
		state.repository.Root,
		path,
		rootpkg.StrictlyUnder,
	)
	if err != nil {
		return "", fmt.Errorf("revalidate clone destination after creating parents: %w", err)
	}

	branch := flags.branch
	if spec.Branch != "" {
		branch = spec.Branch
	}
	git, err := getGitOperations(deps.gitFactory, stderr, flags.silent || flags.parallel, resolution.Token)
	if err != nil {
		return "", err
	}
	err = git.RepoClone(ctx, ghcmd.CloneOptions{
		URL:         spec.CloneURL,
		Destination: path,
		Shallow:     flags.shallow,
		Branch:      branch,
		Submodules:  getSubmoduleMode(flags.noRecursive),
		Filter:      filter,
	})
	if err != nil {
		return "", getPreserveSilentGhError(wrapAccountFailureHint(err, resolution), stderr, flags.silent || flags.parallel)
	}

	state.exists = true
	state.repository.Path = path
	return getAbsolutePath(path)
}

// getGitOperations builds the Git operations used for one repository. gh's
// own stdout and stderr are both directed at progress (gh-qw's stderr,
// or io.Discard when silent): sending gh's stdout there too, rather than to
// gh-qw's own stdout, keeps gh-qw's stdout contract intact (result paths
// only) while still surfacing gh's output. When progress is not silenced
// and ultimately wraps gh-qw's real stderr file (see
// getLockedWriter.PassthroughFile), the Runner returned by factory detects
// that and lets gh inherit the file descriptor directly instead of writing
// through a pipe, restoring gh's own terminal detection for interactive
// progress and color.
func getGitOperations(
	factory GetGitFactory,
	stderr io.Writer,
	silent bool,
	token string,
) (GetGitOperations, error) {
	progress := stderr
	if silent {
		progress = io.Discard
	}
	git := factory(progress, progress, token)
	if git == nil {
		return nil, errors.New("create Git operations: factory returned nil")
	}
	return git, nil
}

func getSubmoduleMode(noRecursive bool) ghcmd.SubmoduleMode {
	if noRecursive {
		return ghcmd.SubmodulesDisabled
	}
	return ghcmd.SubmodulesRecursive
}

func getPartialFilter(value string) (ghcmd.PartialFilter, error) {
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

func getRepositoryInputs(
	stdin io.Reader,
	args []string,
	isTerminal GetTerminalDetector,
) ([]string, error) {
	if len(args) != 0 {
		return append([]string(nil), args...), nil
	}
	if isTerminal(stdin) {
		return nil, getUsageError(errors.New("get requires a repository argument or non-terminal stdin"))
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

func getAbsolutePath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("result path %q is not absolute", path)
	}
	return filepath.Clean(path), nil
}

func getWritePath(output io.Writer, path string) error {
	path, err := getAbsolutePath(path)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, local.NormalizePathForOutput(path)); err != nil {
		return fmt.Errorf("write repository path: %w", err)
	}
	return nil
}

func getEmitDiscoveryWarnings(warnings []local.Warning, sink local.WarningSink) {
	for _, warning := range warnings {
		sink(warning)
	}
}

func getStderrWarningSink(stderr io.Writer) local.WarningSink {
	return func(warning local.Warning) {
		_, _ = fmt.Fprintf(stderr, "gh-qw: warning: %v\n", warning)
	}
}

// getStderrOutputter is satisfied by errors that retain a bounded stderr
// tail, such as *ghcmd.CommandError. Depending on this narrow capability
// instead of the concrete type keeps getPreserveSilentGhError usable with any
// gh operation failure, including test doubles.
type getStderrOutputter interface {
	StderrOutput() []byte
}

func getPreserveSilentGhError(err error, stderr io.Writer, silent bool) error {
	if !silent {
		return err
	}

	var outputter getStderrOutputter
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

func getUsageError(err error) error {
	if err == nil {
		return nil
	}
	var usage *GetUsageError
	if errors.As(err, &usage) {
		return err
	}
	return &GetUsageError{Err: err}
}

func getIsTerminal(reader io.Reader) bool {
	descriptor, ok := reader.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(descriptor.Fd()))
}
