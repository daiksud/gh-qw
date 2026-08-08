package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/herdr"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// WorktreeRemoveGit is the Git capability required by worktree remove.
type WorktreeRemoveGit interface {
	local.Git
	WorktreeRemove(context.Context, string, gitcmd.WorktreeRemoveOptions) error
}

// WorktreeRemoveDependencies supplies command integration and test seams.
type WorktreeRemoveDependencies struct {
	Resolver RootResolver
	Discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	Current func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)
	ResolveManaged func(
		context.Context,
		local.Repository,
		string,
		string,
		...local.ManagedWorktreeOptions,
	) (local.Worktree, error)
	Git        WorktreeRemoveGit
	Filesystem local.FilesystemOptions
	Getwd      func() (string, error)
	Remove     func(string) error
	// Herdr closes the workspace open for the linked worktree being
	// removed when --herdr (or GHQW_HERDR/configuration outside a
	// Herdr-managed pane) enables the integration. Nil uses
	// herdr.NewRunner().
	Herdr HerdrCloser
	// LookupEnv resolves HERDR_ENV to decide whether this process is
	// running inside a Herdr-managed pane. Nil uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
	Stdout    io.Writer
	Stderr    io.Writer
}

type worktreeRemoveRuntime struct {
	resolver RootResolver
	discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	current func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)
	resolveManaged func(
		context.Context,
		local.Repository,
		string,
		string,
		...local.ManagedWorktreeOptions,
	) (local.Worktree, error)
	git        WorktreeRemoveGit
	filesystem local.FilesystemOptions
	getwd      func() (string, error)
	remove     func(string) error
	herdr      HerdrCloser
	lookupEnv  func(string) (string, bool)
	stdout     io.Writer
	stderr     io.Writer
}

type worktreeRemoveRequest struct {
	selector    string
	selectorSet bool
	branch      string
	force       bool
	herdr       herdrIntent
}

type worktreeRemoveUsageError struct {
	err error
}

func (err *worktreeRemoveUsageError) Error() string {
	if err == nil || err.err == nil {
		return repospec.ErrUsage.Error()
	}
	return err.err.Error()
}

func (err *worktreeRemoveUsageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *worktreeRemoveUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

type worktreeRemoveFilesystem struct {
	readDir      func(string) ([]os.DirEntry, error)
	lstat        func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool
	remove       func(string) error
}

// NewWorktreeRemoveCommand returns the command that removes one deterministic
// linked worktree.
func NewWorktreeRemoveCommand(dependencies WorktreeRemoveDependencies) *cobra.Command {
	commandRuntime := worktreeRemovePrepareRuntime(dependencies)

	var (
		selector   string
		force      bool
		herdrFlags herdrFlagValues
	)

	command := &cobra.Command{
		Use:           "remove [-R|--repo selector] [-f|--force] [--herdr|--no-herdr] <branch>",
		Short:         "Remove a linked worktree",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return worktreeRemoveNewUsageError(err)
			}
			if err := local.ValidateBranch(args[0]); err != nil {
				return worktreeRemoveNewUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			return worktreeRemoveRun(command.Context(), commandRuntime, worktreeRemoveRequest{
				selector:    selector,
				selectorSet: command.Flags().Changed("repo"),
				branch:      args[0],
				force:       force,
				herdr:       newHerdrIntent(command),
			})
		},
	}

	flags := command.Flags()
	flags.StringVarP(&selector, "repo", "R", "", "Select an existing repository")
	flags.BoolVarP(&force, "force", "f", false, "Remove a dirty linked worktree")
	registerHerdrFlags(command, &herdrFlags, "Close")
	command.SetOut(commandRuntime.stdout)
	command.SetErr(commandRuntime.stderr)

	return command
}

func worktreeRemovePrepareRuntime(
	dependencies WorktreeRemoveDependencies,
) worktreeRemoveRuntime {
	stdout := dependencies.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	resolver := dependencies.Resolver
	if resolver == nil {
		resolver = rootpkg.NewResolver()
	}
	git := dependencies.Git
	if git == nil {
		git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     io.Discard,
			Stderr:     stderr,
		}
	}
	if dependencies.Discover == nil {
		dependencies.Discover = local.DiscoverRepositories
	}
	if dependencies.Current == nil {
		dependencies.Current = local.DiscoverCurrent
	}
	if dependencies.ResolveManaged == nil {
		dependencies.ResolveManaged = local.ResolveManagedWorktree
	}
	getwd := dependencies.Getwd
	if getwd == nil {
		getwd = os.Getwd
	}
	remove := dependencies.Remove
	if remove == nil {
		remove = os.Remove
	}
	herdrRunner := dependencies.Herdr
	if herdrRunner == nil {
		herdrRunner = herdr.NewRunner()
	}
	lookupEnv := dependencies.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	return worktreeRemoveRuntime{
		resolver:       resolver,
		discover:       dependencies.Discover,
		current:        dependencies.Current,
		resolveManaged: dependencies.ResolveManaged,
		git:            git,
		filesystem:     dependencies.Filesystem,
		getwd:          getwd,
		remove:         remove,
		herdr:          herdrRunner,
		lookupEnv:      lookupEnv,
		stdout:         stdout,
		stderr:         stderr,
	}
}

func worktreeRemoveRun(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	request worktreeRemoveRequest,
) error {
	roots, err := commandRuntime.resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve roots: %w", err)
	}

	herdrEnabled, err := resolveHerdrIntegration(
		request.herdr,
		roots.Herdr,
		commandRuntime.lookupEnv,
		commandRuntime.stderr,
	)
	if err != nil {
		return worktreeRemoveNewUsageError(err)
	}

	discovery, discoveryErr := commandRuntime.discover(
		ctx,
		roots.RepositoryRoots,
		local.DiscoveryOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	warningErr := worktreeRemoveWriteWarnings(commandRuntime.stderr, discovery.Warnings)
	if discoveryErr != nil || warningErr != nil {
		return errors.Join(
			worktreeRemoveWrapError("discover repositories", discoveryErr),
			worktreeRemoveWrapError("write discovery warnings", warningErr),
		)
	}

	repository, err := worktreeRemoveSelectRepository(
		ctx,
		commandRuntime,
		roots.WorktreeRoot,
		discovery.Repositories,
		request.selector,
		request.selectorSet,
	)
	if err != nil {
		return err
	}

	base, err := local.WorktreeBasePath(
		roots.WorktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return fmt.Errorf("derive worktree base: %w", err)
	}
	expectedPath, err := local.WorktreePath(
		roots.WorktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		request.branch,
	)
	if err != nil {
		return fmt.Errorf("derive worktree path: %w", err)
	}
	filesystem := worktreeRemoveNewFilesystem(commandRuntime)
	cleanupBaseInfo, err := worktreeRemoveCaptureCleanupBase(
		filesystem,
		base,
		expectedPath,
	)
	if err != nil {
		return fmt.Errorf("validate worktree cleanup boundary: %w", err)
	}

	worktree, err := commandRuntime.resolveManaged(
		ctx,
		repository,
		roots.WorktreeRoot,
		request.branch,
		local.ManagedWorktreeOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"resolve managed worktree %q for %q: %w",
			request.branch,
			repository.Identity,
			err,
		)
	}
	if err := worktreeRemoveValidateResolved(
		repository,
		worktree,
		request.branch,
		expectedPath,
	); err != nil {
		return err
	}

	// The Herdr workspace open for this worktree, if any, must be
	// resolved before removal: herdr's own worktree listing depends on
	// Git's registration, which WorktreeRemove is about to erase. A find
	// failure never blocks removal itself (see the join below) — it is
	// surfaced only once the primary Git operation has already run to
	// completion, exactly like a close failure.
	var herdrWorkspaceID string
	var herdrWorkspaceFound bool
	var herdrFindErr error
	if herdrEnabled {
		herdrWorkspaceID, herdrWorkspaceFound, herdrFindErr = commandRuntime.herdr.FindWorkspaceForPath(
			ctx,
			repository.Path,
			worktree.Path,
		)
		if herdrFindErr != nil {
			herdrFindErr = fmt.Errorf(
				"find Herdr workspace for %q: %w",
				local.NormalizePathForOutput(worktree.Path),
				herdrFindErr,
			)
		}
	}

	if err := commandRuntime.git.WorktreeRemove(
		ctx,
		repository.Path,
		gitcmd.WorktreeRemoveOptions{
			Path:  worktree.Path,
			Force: request.force,
		},
	); err != nil {
		return fmt.Errorf(
			"remove worktree %q: %w",
			local.NormalizePathForOutput(worktree.Path),
			err,
		)
	}

	normalizedPath := local.NormalizePathForOutput(worktree.Path)
	if err := worktreeRemoveVerifyRemovedPath(filesystem, worktree.Path); err != nil {
		return fmt.Errorf(
			"worktree %q was removed by Git but its directory is unsafe: %w",
			normalizedPath,
			err,
		)
	}

	// Close is attempted only when Find both ran and actually located an
	// open workspace; a Find failure already has its own error and no
	// reliable workspace ID to close.
	var herdrCloseErr error
	if herdrEnabled && herdrFindErr == nil && herdrWorkspaceFound {
		if closeErr := commandRuntime.herdr.CloseWorkspace(ctx, herdrWorkspaceID); closeErr != nil {
			herdrCloseErr = fmt.Errorf(
				"close Herdr workspace for %q: %w",
				normalizedPath,
				closeErr,
			)
		}
	}

	cleanupErr := worktreeRemoveCleanupParents(
		filesystem,
		base,
		worktree.Path,
		cleanupBaseInfo,
	)
	diagnosticErr := worktreeRemoveWriteDiagnostic(commandRuntime.stderr, normalizedPath)
	return errors.Join(
		worktreeRemoveWrapError(
			fmt.Sprintf("worktree %q was removed by Git but parent cleanup failed", normalizedPath),
			cleanupErr,
		),
		worktreeRemoveWrapError(
			fmt.Sprintf(
				"worktree %q was removed but write removal diagnostic",
				normalizedPath,
			),
			diagnosticErr,
		),
		herdrFindErr,
		herdrCloseErr,
	)
}

func worktreeRemoveSelectRepository(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	worktreeRoot string,
	repositories []local.Repository,
	selector string,
	selectorSet bool,
) (local.Repository, error) {
	if selectorSet {
		repository, err := local.ResolveRepositoryForMutation(repositories, selector)
		if err != nil {
			return local.Repository{}, worktreeRemoveNewUsageError(
				fmt.Errorf("resolve repository %q: %w", selector, err),
			)
		}
		return repository, nil
	}

	cwd, err := commandRuntime.getwd()
	if err != nil {
		return local.Repository{}, fmt.Errorf("get current directory: %w", err)
	}
	current, err := commandRuntime.current(
		ctx,
		cwd,
		worktreeRoot,
		repositories,
		local.CurrentOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return local.Repository{}, worktreeRemoveNewUsageError(
			fmt.Errorf(
				"resolve repository from current directory (use -R outside a discovered repository): %w",
				err,
			),
		)
	}

	if err := local.ValidateRepository(current.Repository); err != nil {
		return local.Repository{}, worktreeRemoveNewUsageError(
			fmt.Errorf("validate current repository for mutation: %w", err),
		)
	}
	return current.Repository, nil
}

func worktreeRemoveValidateResolved(
	repository local.Repository,
	worktree local.Worktree,
	slot, expectedPath string,
) error {
	unsafe := func(reason string) error {
		return &local.WorktreeError{
			Kind:       local.ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       slot,
			Path:       worktree.Path,
			OtherPath:  expectedPath,
			Reason:     reason,
		}
	}

	switch {
	case worktree.Main:
		return unsafe("managed lookup returned the main worktree")
	case worktree.Bare:
		return &local.WorktreeError{
			Kind:       local.ErrBareWorktree,
			Repository: repository.Identity,
			Slot:       slot,
			Path:       worktree.Path,
		}
	case worktree.Repository.Identity != repository.Identity:
		return unsafe("managed lookup returned a worktree for a different repository")
	case worktree.Slot != slot:
		return unsafe("managed lookup returned a different worktree slot")
	case worktree.Identity != repository.Identity+"@"+slot:
		return unsafe("managed lookup returned an inconsistent worktree identity")
	case !filepath.IsAbs(worktree.Path):
		return unsafe("managed lookup returned a non-absolute worktree path")
	case !worktreeRemoveSamePath(worktree.Path, expectedPath):
		return unsafe("managed lookup returned a non-deterministic worktree path")
	default:
		return nil
	}
}

func worktreeRemoveVerifyRemovedPath(
	filesystem worktreeRemoveFilesystem,
	worktreePath string,
) error {
	if _, err := filesystem.lstat(worktreePath); err == nil {
		return fmt.Errorf(
			"removed worktree directory %q still exists",
			local.NormalizePathForOutput(worktreePath),
		)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("verify removed worktree directory %q: %w", worktreePath, err)
	}
	return nil
}

func worktreeRemoveCaptureCleanupBase(
	filesystem worktreeRemoveFilesystem,
	base, worktreePath string,
) (fs.FileInfo, error) {
	parent := filepath.Dir(filepath.Clean(worktreePath))
	base = filepath.Clean(base)
	if worktreeRemoveSamePath(parent, base) {
		return nil, nil
	}
	if !worktreeRemovePathStrictlyWithin(base, parent) {
		return nil, fmt.Errorf("parent %q is outside worktree base %q", parent, base)
	}
	info, err := worktreeRemoveInspectPhysicalDirectory(filesystem, base, base, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect worktree base %q: %w", base, err)
	}
	return info, nil
}

func worktreeRemoveCleanupParents(
	filesystem worktreeRemoveFilesystem,
	base, worktreePath string,
	cleanupBaseInfo fs.FileInfo,
) error {
	parent := filepath.Dir(filepath.Clean(worktreePath))
	base = filepath.Clean(base)
	if worktreeRemoveSamePath(parent, base) {
		return nil
	}
	if !worktreeRemovePathStrictlyWithin(base, parent) {
		return fmt.Errorf(
			"parent %q is outside worktree base %q",
			parent,
			base,
		)
	}
	revalidatedBase, err := worktreeRemoveInspectPhysicalDirectory(
		filesystem,
		base,
		base,
		false,
	)
	if err != nil {
		return fmt.Errorf("revalidate worktree base %q: %w", base, err)
	}
	if cleanupBaseInfo == nil || !filesystem.sameFile(cleanupBaseInfo, revalidatedBase) {
		return fmt.Errorf("worktree base %q changed after Git removal", base)
	}

	for current := parent; !worktreeRemoveSamePath(current, base); current = filepath.Dir(current) {
		if !worktreeRemovePathStrictlyWithin(base, current) {
			return fmt.Errorf(
				"cleanup directory %q escaped worktree base %q",
				current,
				base,
			)
		}

		info, err := filesystem.lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect cleanup directory %q: %w", current, err)
		}
		if err := fsidentity.Prime(info, filesystem.sameFile); err != nil {
			return fmt.Errorf("capture cleanup directory identity %q: %w", current, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return nil
		}
		if _, err := worktreeRemoveInspectPhysicalDirectory(
			filesystem,
			base,
			current,
			true,
		); err != nil {
			return fmt.Errorf("revalidate cleanup directory %q: %w", current, err)
		}

		entries, err := filesystem.readDir(current)
		if err != nil {
			return fmt.Errorf("read cleanup directory %q: %w", current, err)
		}
		if len(entries) != 0 {
			return nil
		}

		revalidated, err := filesystem.lstat(current)
		if err != nil {
			return fmt.Errorf("reinspect cleanup directory %q: %w", current, err)
		}
		if revalidated.Mode()&fs.ModeSymlink != 0 || !revalidated.IsDir() {
			return nil
		}
		if !filesystem.sameFile(info, revalidated) {
			return fmt.Errorf("cleanup directory %q changed during validation", current)
		}

		if err := filesystem.remove(current); err != nil {
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
				return nil
			}
			return fmt.Errorf("remove empty cleanup directory %q: %w", current, err)
		}
	}
	return nil
}

func worktreeRemoveNewFilesystem(
	commandRuntime worktreeRemoveRuntime,
) worktreeRemoveFilesystem {
	readDir := commandRuntime.filesystem.ReadDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	lstat := commandRuntime.filesystem.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	evalSymlinks := commandRuntime.filesystem.EvalSymlinks
	if evalSymlinks == nil {
		evalSymlinks = filepath.EvalSymlinks
	}
	sameFile := commandRuntime.filesystem.SameFile
	if sameFile == nil {
		sameFile = os.SameFile
	}
	return worktreeRemoveFilesystem{
		readDir:      readDir,
		lstat:        lstat,
		evalSymlinks: evalSymlinks,
		sameFile:     sameFile,
		remove:       commandRuntime.remove,
	}
}

func worktreeRemoveInspectPhysicalDirectory(
	filesystem worktreeRemoveFilesystem,
	base, path string,
	strictlyWithin bool,
) (fs.FileInfo, error) {
	info, err := filesystem.lstat(path)
	if err != nil {
		return nil, err
	}
	if err := fsidentity.Prime(info, filesystem.sameFile); err != nil {
		return nil, fmt.Errorf("capture directory identity: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("path is not a real directory")
	}

	physical, err := filesystem.evalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(physical) {
		return nil, fmt.Errorf("resolved path %q is not absolute", physical)
	}
	physical = filepath.Clean(physical)
	if !worktreeRemoveSamePath(physical, path) {
		return nil, fmt.Errorf("path resolves through a symbolic link to %q", physical)
	}
	if strictlyWithin && !worktreeRemovePathStrictlyWithin(base, physical) {
		return nil, fmt.Errorf("resolved path %q is outside worktree base %q", physical, base)
	}
	return info, nil
}

func worktreeRemovePathStrictlyWithin(base, candidate string) bool {
	base = filepath.Clean(base)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func worktreeRemoveSamePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if first == second {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(first, second)
}

func worktreeRemoveWriteWarnings(writer io.Writer, warnings []local.Warning) error {
	if len(warnings) == 0 {
		return nil
	}

	var output bytes.Buffer
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(&output, "gh-qw: warning: %v\n", warning)
	}
	return worktreeRemoveWriteAll(writer, output.Bytes())
}

func worktreeRemoveWriteDiagnostic(writer io.Writer, normalizedPath string) error {
	return worktreeRemoveWriteAll(
		writer,
		[]byte(fmt.Sprintf("removed worktree %s\n", normalizedPath)),
	)
}

func worktreeRemoveWriteAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written < 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func worktreeRemoveNewUsageError(err error) error {
	return &worktreeRemoveUsageError{err: err}
}

func worktreeRemoveWrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
