package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/removal"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// WorktreeRemoveGit is the Git capability required by worktree remove.
type WorktreeRemoveGit interface {
	local.Git
	BranchUpstreams(context.Context, string) ([]gitcmd.BranchUpstream, error)
	RefExists(context.Context, string, string) (bool, error)
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
	Enumerate func(
		context.Context,
		local.Repository,
		string,
		...local.WorktreeOptions,
	) ([]local.Worktree, error)
	ValidateAssociation func(
		context.Context,
		local.Repository,
		local.Worktree,
		string,
		...local.AssociationOptions,
	) error
	Git        WorktreeRemoveGit
	Filesystem local.FilesystemOptions
	Prompt     RemovePrompt
	// OpenTerminal opens the controlling terminal used by the --gone bulk
	// confirmation. Nil uses the platform controlling terminal.
	OpenTerminal func() (io.ReadCloser, error)
	Getwd        func() (string, error)
	Remove       func(string) error
	Stdout       io.Writer
	Stderr       io.Writer
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
	enumerate func(
		context.Context,
		local.Repository,
		string,
		...local.WorktreeOptions,
	) ([]local.Worktree, error)
	validateAssociation func(
		context.Context,
		local.Repository,
		local.Worktree,
		string,
		...local.AssociationOptions,
	) error
	git        WorktreeRemoveGit
	filesystem local.FilesystemOptions
	fs         removeFilesystem
	prompt     RemovePrompt
	getwd      func() (string, error)
	stdout     io.Writer
	stderr     io.Writer
}

type worktreeRemoveRequest struct {
	selector    string
	selectorSet bool
	branch      string
	force       bool
	gone        bool
	dryRun      bool
	yes         bool
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

// NewWorktreeRemoveCommand returns the command that removes one deterministic
// linked worktree or safely plans removal of all gone-upstream worktrees.
func NewWorktreeRemoveCommand(dependencies WorktreeRemoveDependencies) *cobra.Command {
	commandRuntime := worktreeRemovePrepareRuntime(dependencies)

	var (
		selector string
		force    bool
		gone     bool
		dryRun   bool
		yes      bool
	)

	command := &cobra.Command{
		Use:   "remove [flags] (<branch>|--gone)",
		Short: "Remove a linked worktree",
		Long: "Remove one deterministic linked worktree, or use --gone to plan and remove " +
			"all safe linked worktrees whose upstream refs are absent.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if gone {
				if err := cobra.NoArgs(command, args); err != nil {
					return worktreeRemoveNewUsageError(
						fmt.Errorf("--gone and <branch> are mutually exclusive: %w", err),
					)
				}
				return nil
			}
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return worktreeRemoveNewUsageError(err)
			}
			if command.Flags().Changed("dry-run") || command.Flags().Changed("yes") {
				return worktreeRemoveNewUsageError(errors.New("--dry-run and --yes require --gone"))
			}
			if err := local.ValidateBranch(args[0]); err != nil {
				return worktreeRemoveNewUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			request := worktreeRemoveRequest{
				selector:    selector,
				selectorSet: command.Flags().Changed("repo"),
				force:       force,
				gone:        gone,
				dryRun:      dryRun,
				yes:         yes,
			}
			if gone {
				return worktreeRemoveGoneRun(command.Context(), commandRuntime, request)
			}
			request.branch = args[0]
			return worktreeRemoveRun(command.Context(), commandRuntime, request)
		},
	}

	flags := command.Flags()
	flags.StringVarP(&selector, "repo", "R", "", "Select an existing repository")
	flags.BoolVarP(&force, "force", "f", false, "Remove a dirty linked worktree")
	flags.BoolVar(&gone, "gone", false, "Remove linked worktrees whose upstream refs are gone")
	flags.BoolVarP(&dryRun, "dry-run", "n", false, "Print the bulk removal plan without changing files")
	flags.BoolVarP(&yes, "yes", "y", false, "Skip the bulk removal confirmation")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return worktreeRemoveNewUsageError(err)
	})
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
	if dependencies.Enumerate == nil {
		dependencies.Enumerate = local.EnumerateWorktrees
	}
	if dependencies.ValidateAssociation == nil {
		dependencies.ValidateAssociation = local.ValidateWorktreeAssociation
	}
	getwd := dependencies.Getwd
	if getwd == nil {
		getwd = os.Getwd
	}
	remove := dependencies.Remove
	if remove == nil {
		remove = os.Remove
	}
	readDir := dependencies.Filesystem.ReadDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	lstat := dependencies.Filesystem.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	evalSymlinks := dependencies.Filesystem.EvalSymlinks
	if evalSymlinks == nil {
		evalSymlinks = filepath.EvalSymlinks
	}
	sameFile := dependencies.Filesystem.SameFile
	if sameFile == nil {
		sameFile = os.SameFile
	}
	openTerminal := dependencies.OpenTerminal
	if openTerminal == nil {
		openTerminal = removeOpenControllingTerminal
	}
	prompt := dependencies.Prompt
	if prompt == nil {
		prompt = func(ctx context.Context, writer io.Writer, message string) (bool, error) {
			return removeConfirm(ctx, writer, message, openTerminal)
		}
	}
	return worktreeRemoveRuntime{
		resolver:            resolver,
		discover:            dependencies.Discover,
		current:             dependencies.Current,
		resolveManaged:      dependencies.ResolveManaged,
		enumerate:           dependencies.Enumerate,
		validateAssociation: dependencies.ValidateAssociation,
		git:                 git,
		filesystem:          dependencies.Filesystem,
		fs: removeFilesystem{
			readDir:      readDir,
			lstat:        lstat,
			evalSymlinks: evalSymlinks,
			sameFile:     sameFile,
			remove:       remove,
			removeAll:    os.RemoveAll,
		},
		prompt: prompt,
		getwd:  getwd,
		stdout: stdout,
		stderr: stderr,
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

	worktree, err := commandRuntime.observer().ResolveWorktree(
		ctx,
		repository,
		roots.WorktreeRoot,
		request.branch,
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

	filesystem := commandRuntime.fs
	cleanupBase, err := removal.CaptureNode(filesystem.observations(), "", base, removal.Directory, false)
	if err != nil {
		return fmt.Errorf("validate worktree cleanup boundary: %w", err)
	}
	cleanup, err := removal.CaptureCleanupChain(filesystem.observations(), base, filepath.Dir(expectedPath))
	if err != nil {
		return fmt.Errorf("capture worktree cleanup parents: %w", err)
	}

	result := filesystem.executor(commandRuntime.git).RemoveLinked(
		ctx, repository.Path,
		gitcmd.WorktreeRemoveOptions{Path: worktree.Path, Force: request.force},
		cleanupBase, cleanup,
	)
	normalizedPath := local.NormalizePathForOutput(worktree.Path)
	if result.Err != nil && result.FailedStep != removal.CleanupParents {
		operation := fmt.Sprintf("remove worktree %q", normalizedPath)
		switch result.FailedStep {
		case removal.VerifyPath:
			operation = fmt.Sprintf("worktree %q was removed by Git but its directory is unsafe", normalizedPath)
		case removal.ReadRegistrations, removal.VerifyRegistration:
			operation = fmt.Sprintf("worktree %q was removed by Git but its registration removal could not be verified", normalizedPath)
		}
		return fmt.Errorf("%s: %w", operation, result.Err)
	}
	cleanupErr := result.Err
	if errors.Is(cleanupErr, removal.ErrCleanupParentShape) {
		cleanupErr = nil
	}
	if errors.Is(cleanupErr, removal.ErrCleanupBoundary) {
		cleanupErr = fmt.Errorf("worktree base %q changed after Git removal: %w", base, cleanupErr)
	}
	diagnosticErr := worktreeRemoveWriteDiagnostic(commandRuntime.stderr, normalizedPath)
	return errors.Join(
		worktreeRemoveWrapError(
			fmt.Sprintf("worktree %q was removed by Git but parent cleanup failed", normalizedPath),
			cleanupErr,
		),
		worktreeRemoveWrapError(
			fmt.Sprintf("worktree %q was removed but write removal diagnostic", normalizedPath),
			diagnosticErr,
		),
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
	case !physicalpath.SamePath(worktree.Path, expectedPath):
		return unsafe("managed lookup returned a non-deterministic worktree path")
	default:
		return nil
	}
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
	return removeWriteAll(writer, data)
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

func (commandRuntime worktreeRemoveRuntime) observer() removal.Observer {
	return removal.Observer{Git: commandRuntime.git, Filesystem: commandRuntime.fs.observations(), LocalFilesystem: commandRuntime.filesystem, ResolveManaged: commandRuntime.resolveManaged, ValidateAssociation: commandRuntime.validateAssociation}
}
