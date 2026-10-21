package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	runtimepkg "runtime"
	"strings"
	"syscall"

	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// WorktreeAddGit is the Git capability required by worktree add.
type WorktreeAddGit interface {
	local.Git
	WorktreeAdd(context.Context, string, gitcmd.WorktreeAddOptions) error
	LocalBranchExists(context.Context, string, string) (bool, error)
}

// WorktreeAddGh is the gh capability required by worktree add to bring the
// API-selected default branch into the local repository when it is missing,
// so GitHub authentication and host resolution follow gh's own rules.
type WorktreeAddGh interface {
	RepoSync(context.Context, string, ghcmd.SyncOptions) error
}

// WorktreeAddAPI is the GitHub API capability required by worktree add.
type WorktreeAddAPI interface {
	DefaultBranch(ctx context.Context, host, owner, repo string) (string, error)
}

// WorktreeAddDependencies supplies command integration and test seams.
type WorktreeAddDependencies struct {
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
	Enumerate func(
		context.Context,
		local.Repository,
		string,
		...local.WorktreeOptions,
	) ([]local.Worktree, error)
	ValidateDestination func(
		context.Context,
		string,
		local.Repository,
		string,
		[]local.Worktree,
		...local.DestinationOptions,
	) (string, error)
	ValidateAssociation func(
		context.Context,
		local.Repository,
		local.Worktree,
		string,
		...local.AssociationOptions,
	) error

	Git        WorktreeAddGit
	Gh         WorktreeAddGh
	API        WorktreeAddAPI
	Filesystem local.FilesystemOptions
	Getwd      func() (string, error)
	Mkdir      func(string, fs.FileMode) error
	Remove     func(string) error
	Stdout     io.Writer
	Stderr     io.Writer
}

type worktreeAddMode uint8

const (
	worktreeAddModeAutomatic worktreeAddMode = iota
	worktreeAddModeNew
	worktreeAddModeReset
	worktreeAddModeDetach
	worktreeAddModeOrphan
)

type worktreeAddRuntime = WorktreeAddDependencies

type worktreeAddRequest struct {
	selector    string
	selectorSet bool
	branch      string
	commitish   string
	mode        worktreeAddMode
	force       bool
}

type worktreeAddPlanner struct {
	ctx        context.Context
	git        WorktreeAddGit
	gh         WorktreeAddGh
	api        WorktreeAddAPI
	repository local.Repository
}

type worktreeAddUsageError = repositoryUsageError

// NewWorktreeAddCommand returns the command that creates one deterministic
// linked worktree.
func NewWorktreeAddCommand(dependencies WorktreeAddDependencies) *cobra.Command {
	runtime := worktreeAddPrepareRuntime(dependencies)

	var (
		selector   string
		newMode    bool
		resetMode  bool
		detachMode bool
		orphanMode bool
		force      bool
	)

	command := &cobra.Command{
		Use: "add [-R|--repo selector] [-b] [-B] [--detach] [--orphan] [-f] " +
			"<branch> [commit-ish]",
		Short:         "Add a linked worktree",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.RangeArgs(1, 2)(command, args); err != nil {
				return worktreeAddNewUsageError(err)
			}
			modeCount := 0
			for _, enabled := range []bool{newMode, resetMode, detachMode, orphanMode} {
				if enabled {
					modeCount++
				}
			}
			if modeCount > 1 {
				return worktreeAddNewUsageError(
					errors.New("-b, -B, --detach, and --orphan are mutually exclusive"),
				)
			}
			if orphanMode && len(args) == 2 {
				return worktreeAddNewUsageError(
					errors.New("--orphan does not accept a commit-ish"),
				)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if err := local.ValidateSlot(command.Context(), args[0]); err != nil {
				if errors.Is(err, local.ErrInvalidBranch) {
					return worktreeAddNewUsageError(err)
				}
				return err
			}
			mode := worktreeAddModeAutomatic
			switch {
			case newMode:
				mode = worktreeAddModeNew
			case resetMode:
				mode = worktreeAddModeReset
			case detachMode:
				mode = worktreeAddModeDetach
			case orphanMode:
				mode = worktreeAddModeOrphan
			}

			request := worktreeAddRequest{
				selector:    selector,
				selectorSet: command.Flags().Changed("repo"),
				branch:      args[0],
				mode:        mode,
				force:       force,
			}
			if len(args) == 2 {
				request.commitish = args[1]
			}
			return worktreeAddRun(command.Context(), runtime, request)
		},
	}

	flags := command.Flags()
	flags.StringVarP(&selector, "repo", "R", "", "Select an existing repository")
	flags.BoolVarP(&newMode, "b", "b", false, "Create a new branch")
	flags.BoolVarP(&resetMode, "B", "B", false, "Create or reset a branch")
	flags.BoolVar(&detachMode, "detach", false, "Create a detached worktree")
	flags.BoolVar(&orphanMode, "orphan", false, "Create an unborn orphan branch")
	flags.BoolVarP(&force, "force", "f", false, "Override Git checkout safety")
	command.SetOut(runtime.Stdout)
	command.SetErr(runtime.Stderr)

	return command
}

func worktreeAddPrepareRuntime(deps WorktreeAddDependencies) worktreeAddRuntime {
	deps.Stdout = commandStdout(deps.Stdout)
	deps.Stderr = commandStderr(deps.Stderr)
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.Git == nil {
		deps.Git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     io.Discard,
			Stderr:     deps.Stderr,
		}
	}
	if deps.Gh == nil {
		deps.Gh = &ghcmd.Runner{Stdout: io.Discard, Stderr: deps.Stderr}
	}
	if deps.API == nil {
		deps.API = ghapi.NewClient(deps.Stderr)
	}
	if deps.Discover == nil {
		deps.Discover = local.DiscoverRepositories
	}
	if deps.Current == nil {
		deps.Current = local.DiscoverCurrent
	}
	if deps.Enumerate == nil {
		deps.Enumerate = local.EnumerateWorktrees
	}
	if deps.ValidateDestination == nil {
		deps.ValidateDestination = local.ValidateWorktreeDestination
	}
	if deps.ValidateAssociation == nil {
		deps.ValidateAssociation = local.ValidateWorktreeAssociation
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.Mkdir == nil {
		deps.Mkdir = os.Mkdir
	}
	if deps.Remove == nil {
		deps.Remove = os.Remove
	}
	return deps
}

func worktreeAddRun(
	ctx context.Context,
	runtime worktreeAddRuntime,
	request worktreeAddRequest,
) error {
	roots, err := runtime.Resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve roots: %w", err)
	}

	discovery, err := runtime.Discover(
		ctx,
		roots.RepositoryRoots,
		local.DiscoveryOptions{
			Git:        runtime.Git,
			Filesystem: runtime.Filesystem,
		},
	)
	if writeErr := commandWriteWarnings(runtime.Stderr, discovery.Warnings); writeErr != nil {
		return fmt.Errorf("write discovery warning: %w", writeErr)
	}
	if err != nil {
		return fmt.Errorf("discover repositories: %w", err)
	}

	repository, err := worktreeAddSelectRepository(
		ctx,
		runtime,
		roots.WorktreeRoot,
		discovery.Repositories,
		request.selector,
		request.selectorSet,
	)
	if err != nil {
		return err
	}

	worktrees, err := runtime.Enumerate(
		ctx,
		repository,
		roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     runtime.Git,
			Filesystem: runtime.Filesystem,
		},
	)
	if err != nil {
		return fmt.Errorf("enumerate worktrees for %q: %w", repository.Identity, err)
	}

	expectedDestination, err := local.WorktreePath(
		ctx,
		roots.WorktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		request.branch,
	)
	if err != nil {
		return fmt.Errorf("derive worktree destination: %w", err)
	}
	destination, err := runtime.ValidateDestination(
		ctx,
		roots.WorktreeRoot,
		repository,
		request.branch,
		worktrees,
		local.DestinationOptions{Filesystem: runtime.Filesystem},
	)
	if err != nil {
		return fmt.Errorf("validate worktree destination: %w", err)
	}
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("validate worktree destination: path %q is not absolute", destination)
	}
	if destination != filepath.Clean(expectedDestination) {
		return fmt.Errorf(
			"validate worktree destination: derived path %q does not match deterministic path %q",
			destination,
			expectedDestination,
		)
	}
	if !worktreeAddPathWithin(roots.WorktreeRoot, destination) {
		return fmt.Errorf(
			"validate worktree destination: %w: path %q is outside worktree root %q",
			rootpkg.ErrUnsafeTarget,
			destination,
			roots.WorktreeRoot,
		)
	}

	planner := &worktreeAddPlanner{
		ctx:        ctx,
		git:        runtime.Git,
		gh:         runtime.Gh,
		api:        runtime.API,
		repository: repository,
	}
	options, syncBranch, err := planner.worktreeAddPlan(request, destination)
	if err != nil {
		return err
	}

	createdParents, err := worktreeAddCreateParents(destination, runtime)
	if err != nil {
		return err
	}
	worktreeAddFailure := func(operationErr error) error {
		cleanupErr := worktreeAddCleanupParents(createdParents, runtime)
		if cleanupErr == nil {
			return operationErr
		}
		return errors.Join(operationErr, cleanupErr)
	}

	lstat := runtime.Filesystem.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	if _, err := lstat(destination); err == nil {
		return worktreeAddFailure(fmt.Errorf(
			"validate worktree destination: %w",
			&local.SlotCollisionError{
				Slot:         request.branch,
				ExistingSlot: request.branch,
			},
		))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return worktreeAddFailure(fmt.Errorf(
			"inspect worktree destination %q: %w",
			destination,
			err,
		))
	}

	// Only explicit -b/-B requests without a start point use the GitHub
	// default branch. Perform synchronization after destination validation
	// and re-observe the resulting local branch using Git.
	if syncBranch != "" {
		if err := runtime.Gh.RepoSync(
			ctx, repository.Path, ghcmd.SyncOptions{
				Host:   repository.Host,
				Source: repository.Owner + "/" + repository.Repo,
				Branch: syncBranch,
			},
		); err != nil {
			return worktreeAddFailure(fmt.Errorf("sync API default branch %q: %w", syncBranch, err))
		}
		exists, err := runtime.Git.LocalBranchExists(ctx, repository.Path, syncBranch)
		if err != nil {
			return worktreeAddFailure(fmt.Errorf("inspect synchronized default branch %q: %w", syncBranch, err))
		}
		if !exists {
			return worktreeAddFailure(fmt.Errorf("gh repo sync did not materialize local default branch %q", syncBranch))
		}
	}

	if err := runtime.Git.WorktreeAdd(ctx, repository.Path, options); err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"add worktree for %q at %q: %w",
			repository.Identity,
			destination,
			err,
		))
	}

	registered, err := runtime.Enumerate(
		ctx,
		repository,
		roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     runtime.Git,
			Filesystem: runtime.Filesystem,
		},
	)
	if err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}
	added, err := local.FindRegisteredLinkedWorktree(ctx, registered, request.branch)
	if err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}
	registeredPath := filepath.Clean(added.Path)
	if registeredPath != destination &&
		!(runtimepkg.GOOS == "windows" && strings.EqualFold(registeredPath, destination)) {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but Git registered path %q",
			destination,
			added.Path,
		))
	}
	if err := worktreeAddValidateMode(request, added); err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}
	if err := runtime.ValidateAssociation(
		ctx,
		repository,
		added,
		roots.WorktreeRoot,
		local.AssociationOptions{
			Git:        runtime.Git,
			Filesystem: runtime.Filesystem,
		},
	); err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}

	if _, err := fmt.Fprintln(runtime.Stdout, local.NormalizePathForOutput(destination)); err != nil {
		return worktreeAddFailure(fmt.Errorf("write worktree path: %w", err))
	}

	return nil
}

func worktreeAddSelectRepository(
	ctx context.Context,
	runtime worktreeAddRuntime,
	worktreeRoot string,
	repositories []local.Repository,
	selector string,
	selectorSet bool,
) (local.Repository, error) {
	if selectorSet {
		repository, err := local.ResolveRepositoryForMutation(repositories, selector)
		if err != nil {
			return local.Repository{}, worktreeAddNewUsageError(
				fmt.Errorf("resolve repository %q: %w", selector, err),
			)
		}
		return repository, nil
	}

	cwd, err := runtime.Getwd()
	if err != nil {
		return local.Repository{}, fmt.Errorf("get current directory: %w", err)
	}
	current, err := runtime.Current(
		ctx,
		cwd,
		worktreeRoot,
		repositories,
		local.CurrentOptions{
			Git:        runtime.Git,
			Filesystem: runtime.Filesystem,
		},
	)
	if err != nil {
		return local.Repository{}, worktreeAddNewUsageError(
			fmt.Errorf(
				"resolve repository from current directory (use -R outside a discovered repository): %w",
				err,
			),
		)
	}

	if err := local.ValidateRepository(current.Repository); err != nil {
		return local.Repository{}, worktreeAddNewUsageError(
			fmt.Errorf(
				"validate current repository for mutation: %w",
				err,
			),
		)
	}
	return current.Repository, nil
}

// worktreeAddPlan performs only read-only observations and default-branch API
// lookup. Git itself owns branch creation, remote guessing, tracking, and
// revision/conflict validation. The returned syncBranch is executed later,
// after the destination has been revalidated.
func (planner *worktreeAddPlanner) worktreeAddPlan(
	request worktreeAddRequest,
	destination string,
) (gitcmd.WorktreeAddOptions, string, error) {
	options := gitcmd.WorktreeAddOptions{Path: destination, Force: request.force}

	switch request.mode {
	case worktreeAddModeOrphan:
		options.Orphan = true
		options.NewBranch = request.branch
		return options, "", nil
	case worktreeAddModeDetach:
		options.Detach = true
		options.Commitish = request.commitish
		if options.Commitish == "" {
			options.Commitish = request.branch
		}
		return options, "", nil
	case worktreeAddModeNew, worktreeAddModeReset:
		target, syncBranch, err := planner.worktreeAddCreationTarget(request.commitish)
		if err != nil {
			return gitcmd.WorktreeAddOptions{}, "", err
		}
		options.Commitish = target
		if request.mode == worktreeAddModeNew {
			options.NewBranch = request.branch
		} else {
			options.ResetBranch = request.branch
		}
		return options, syncBranch, nil
	case worktreeAddModeAutomatic:
		// Passing the requested branch to native worktree add lets Git
		// apply checkout.defaultRemote and reject ambiguous candidates.
		// Unlike the removed gh-qw fallback, an unknown branch without a
		// start point is rejected by Git rather than querying GitHub.
		options.Commitish = request.branch
		if request.commitish == "" {
			return options, "", nil
		}
		exists, err := planner.git.LocalBranchExists(planner.ctx, planner.repository.Path, request.branch)
		if err != nil {
			return gitcmd.WorktreeAddOptions{}, "", fmt.Errorf("inspect local branch %q: %w", request.branch, err)
		}
		if !exists {
			options.NewBranch = request.branch
			options.Commitish = request.commitish
		}
		return options, "", nil
	default:
		return gitcmd.WorktreeAddOptions{}, "", errors.New("unsupported worktree add mode")
	}
}

// worktreeAddCreationTarget returns the explicit start point unchanged, or the
// GitHub default branch for -b/-B without a start point. If the default is not
// a local branch, the caller must synchronize it after checking the destination.
func (planner *worktreeAddPlanner) worktreeAddCreationTarget(explicit string) (string, string, error) {
	if explicit != "" {
		return explicit, "", nil
	}
	defaultBranch, err := planner.api.DefaultBranch(
		planner.ctx, planner.repository.Host, planner.repository.Owner, planner.repository.Repo,
	)
	if err != nil {
		return "", "", fmt.Errorf("determine a start point for the new branch: %w; provide an explicit commit-ish", err)
	}
	if err := local.ValidateBranch(planner.ctx, defaultBranch); err != nil {
		return "", "", fmt.Errorf("GitHub API returned an invalid default branch %q: %w", defaultBranch, err)
	}
	exists, err := planner.git.LocalBranchExists(planner.ctx, planner.repository.Path, defaultBranch)
	if err != nil {
		return "", "", fmt.Errorf("inspect default branch %q: %w", defaultBranch, err)
	}
	if exists {
		return "refs/heads/" + defaultBranch, "", nil
	}
	return "refs/heads/" + defaultBranch, defaultBranch, nil
}

func worktreeAddCreateParents(
	destination string,
	runtime worktreeAddRuntime,
) ([]string, error) {
	lstat := runtime.Filesystem.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}

	current := filepath.Dir(destination)
	missing := make([]string, 0)
	for {
		info, err := lstat(current)
		switch {
		case err == nil:
			if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
				return nil, fmt.Errorf(
					"create worktree parent directories: existing path %q is not a real directory",
					current,
				)
			}
			goto create
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, current)
		default:
			return nil, fmt.Errorf(
				"inspect worktree parent directory %q: %w",
				current,
				err,
			)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf(
				"create worktree parent directories: no existing ancestor for %q",
				destination,
			)
		}
		current = parent
	}

create:
	created := make([]string, 0, len(missing))
	for index := len(missing) - 1; index >= 0; index-- {
		path := missing[index]
		if err := runtime.Mkdir(path, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) {
				info, lstatErr := lstat(path)
				if lstatErr == nil &&
					info.Mode()&fs.ModeSymlink == 0 &&
					info.IsDir() {
					continue
				}
				if lstatErr != nil {
					err = errors.Join(err, lstatErr)
				}
			}
			cleanupErr := worktreeAddCleanupParents(created, runtime)
			return nil, errors.Join(
				fmt.Errorf("create worktree parent directory %q: %w", path, err),
				cleanupErr,
			)
		}
		created = append(created, path)
	}
	return created, nil
}

func worktreeAddCleanupParents(
	created []string,
	runtime worktreeAddRuntime,
) error {
	var cleanupErr error
	for index := len(created) - 1; index >= 0; index-- {
		path := created[index]
		err := runtime.Remove(path)
		if err == nil ||
			errors.Is(err, fs.ErrNotExist) ||
			errors.Is(err, syscall.ENOTEMPTY) ||
			errors.Is(err, syscall.EEXIST) {
			continue
		}
		cleanupErr = errors.Join(
			cleanupErr,
			fmt.Errorf("clean newly-created parent directory %q: %w", path, err),
		)
	}
	return cleanupErr
}

func worktreeAddValidateMode(
	request worktreeAddRequest,
	worktree local.Worktree,
) error {
	if request.mode == worktreeAddModeDetach {
		if !worktree.Detached || worktree.Branch != "" {
			return errors.New("Git did not register the new worktree as detached")
		}
		return nil
	}
	if worktree.Detached || worktree.Branch != request.branch {
		return fmt.Errorf(
			"Git registered branch %q instead of %q",
			worktree.Branch,
			request.branch,
		)
	}
	return nil
}

func worktreeAddPathWithin(rootPath, destination string) bool {
	rootPath = filepath.Clean(rootPath)
	destination = filepath.Clean(destination)
	relative, err := filepath.Rel(rootPath, destination)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func worktreeAddNewUsageError(err error) error {
	return &worktreeAddUsageError{err: err}
}

