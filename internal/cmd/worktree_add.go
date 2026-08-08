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
	runtimepkg "runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghauth"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/herdr"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// WorktreeAddGit is the Git capability required by worktree add.
type WorktreeAddGit interface {
	local.Git
	WorktreeAdd(context.Context, string, gitcmd.WorktreeAddOptions) error
	LocalBranchExists(context.Context, string, string) (bool, error)
	RemoteBranchExists(context.Context, string, string, string) (bool, error)
	RevisionExists(context.Context, string, string) (bool, error)
}

// WorktreeAddGh is the gh capability required by worktree add to bring the
// API-selected default branch into the local repository when it is missing,
// so GitHub authentication and host resolution follow gh's own rules.
type WorktreeAddGh interface {
	RepoSync(context.Context, string, ghcmd.SyncOptions) error
}

// WorktreeAddAPI is the GitHub API capability required by worktree add.
type WorktreeAddAPI interface {
	DefaultBranch(ctx context.Context, host, owner, repo, tokenOverride string) (string, error)
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

	Git             WorktreeAddGit
	Gh              WorktreeAddGh
	API             WorktreeAddAPI
	AccountResolver AccountResolver
	Filesystem      local.FilesystemOptions
	Getwd           func() (string, error)
	Mkdir           func(string, fs.FileMode) error
	Remove          func(string) error
	// Herdr opens and focuses a Herdr workspace for the new linked
	// worktree when --herdr (or GHQW_HERDR/configuration outside a
	// Herdr-managed pane) enables the integration. Nil uses
	// herdr.NewRunner().
	Herdr HerdrCreator
	// LookupEnv resolves HERDR_ENV to decide whether this process is
	// running inside a Herdr-managed pane. Nil uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
	Stdout    io.Writer
	Stderr    io.Writer
}

type worktreeAddMode uint8

const (
	worktreeAddModeAutomatic worktreeAddMode = iota
	worktreeAddModeNew
	worktreeAddModeReset
	worktreeAddModeDetach
	worktreeAddModeOrphan
)

type worktreeAddRuntime struct {
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
	enumerate func(
		context.Context,
		local.Repository,
		string,
		...local.WorktreeOptions,
	) ([]local.Worktree, error)
	validateDestination func(
		string,
		local.Repository,
		string,
		[]local.Worktree,
		...local.DestinationOptions,
	) (string, error)
	validateAssociation func(
		context.Context,
		local.Repository,
		local.Worktree,
		string,
		...local.AssociationOptions,
	) error
	git             WorktreeAddGit
	gh              WorktreeAddGh
	api             WorktreeAddAPI
	accountResolver AccountResolver
	filesystem      local.FilesystemOptions
	getwd           func() (string, error)
	mkdir           func(string, fs.FileMode) error
	remove          func(string) error
	herdr           HerdrCreator
	lookupEnv       func(string) (string, bool)
	stdout          io.Writer
	stderr          io.Writer
}

type worktreeAddRequest struct {
	selector    string
	selectorSet bool
	branch      string
	commitish   string
	mode        worktreeAddMode
	force       bool
	herdr       herdrIntent
}

type worktreeAddPlanner struct {
	ctx             context.Context
	git             WorktreeAddGit
	gh              WorktreeAddGh
	api             WorktreeAddAPI
	accountResolver AccountResolver
	repository      local.Repository

	remotes       []string
	remotesLoaded bool
}

type worktreeAddUsageError struct {
	err error
}

func (err *worktreeAddUsageError) Error() string {
	if err == nil || err.err == nil {
		return repospec.ErrUsage.Error()
	}
	return err.err.Error()
}

func (err *worktreeAddUsageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *worktreeAddUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

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
		herdrFlags herdrFlagValues
	)

	command := &cobra.Command{
		Use: "add [-R|--repo selector] [-b] [-B] [--detach] [--orphan] [-f] " +
			"[--herdr|--no-herdr] <branch> [commit-ish]",
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
			if err := local.ValidateBranch(args[0]); err != nil {
				return worktreeAddNewUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
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
				herdr:       newHerdrIntent(command),
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
	registerHerdrFlags(command, &herdrFlags, "Open")
	command.SetOut(runtime.stdout)
	command.SetErr(runtime.stderr)

	return command
}

func worktreeAddPrepareRuntime(dependencies WorktreeAddDependencies) worktreeAddRuntime {
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
	gh := dependencies.Gh
	if gh == nil {
		gh = &ghcmd.Runner{
			Stdout: io.Discard,
			Stderr: stderr,
		}
	}
	apiClient := dependencies.API
	if apiClient == nil {
		apiClient = ghapi.NewClient()
	}
	accountResolver := dependencies.AccountResolver
	if accountResolver == nil {
		accountResolver = ghauth.NewResolver(ghauth.ResolverOptions{
			Runner:     ghcmd.NewRunner(),
			Cache:      ghauth.NewCache(),
			Stdin:      os.Stdin,
			Stderr:     stderr,
			IsTerminal: getIsTerminal,
		})
	}
	if dependencies.Discover == nil {
		dependencies.Discover = local.DiscoverRepositories
	}
	if dependencies.Current == nil {
		dependencies.Current = local.DiscoverCurrent
	}
	if dependencies.Enumerate == nil {
		dependencies.Enumerate = local.EnumerateWorktrees
	}
	if dependencies.ValidateDestination == nil {
		dependencies.ValidateDestination = local.ValidateWorktreeDestination
	}
	if dependencies.ValidateAssociation == nil {
		dependencies.ValidateAssociation = local.ValidateWorktreeAssociation
	}

	getwd := dependencies.Getwd
	if getwd == nil {
		getwd = os.Getwd
	}
	mkdir := dependencies.Mkdir
	if mkdir == nil {
		mkdir = os.Mkdir
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

	return worktreeAddRuntime{
		resolver:            resolver,
		discover:            dependencies.Discover,
		current:             dependencies.Current,
		enumerate:           dependencies.Enumerate,
		git:                 git,
		gh:                  gh,
		api:                 apiClient,
		accountResolver:     accountResolver,
		filesystem:          dependencies.Filesystem,
		getwd:               getwd,
		mkdir:               mkdir,
		remove:              remove,
		herdr:               herdrRunner,
		lookupEnv:           lookupEnv,
		stdout:              stdout,
		stderr:              stderr,
		validateDestination: dependencies.ValidateDestination,
		validateAssociation: dependencies.ValidateAssociation,
	}
}

func worktreeAddRun(
	ctx context.Context,
	runtime worktreeAddRuntime,
	request worktreeAddRequest,
) error {
	roots, err := runtime.resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve roots: %w", err)
	}

	herdrEnabled, err := resolveHerdrIntegration(request.herdr, roots.Herdr, runtime.lookupEnv, runtime.stderr)
	if err != nil {
		return worktreeAddNewUsageError(err)
	}

	discovery, err := runtime.discover(
		ctx,
		roots.RepositoryRoots,
		local.DiscoveryOptions{
			Git:        runtime.git,
			Filesystem: runtime.filesystem,
		},
	)
	if writeErr := worktreeAddWriteWarnings(runtime.stderr, discovery.Warnings); writeErr != nil {
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

	worktrees, err := runtime.enumerate(
		ctx,
		repository,
		roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     runtime.git,
			Filesystem: runtime.filesystem,
		},
	)
	if err != nil {
		return fmt.Errorf("enumerate worktrees for %q: %w", repository.Identity, err)
	}

	expectedDestination, err := local.WorktreePath(
		roots.WorktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		request.branch,
	)
	if err != nil {
		return fmt.Errorf("derive worktree destination: %w", err)
	}
	destination, err := runtime.validateDestination(
		roots.WorktreeRoot,
		repository,
		request.branch,
		worktrees,
		local.DestinationOptions{Filesystem: runtime.filesystem},
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
		ctx:             ctx,
		git:             runtime.git,
		gh:              runtime.gh,
		api:             runtime.api,
		accountResolver: runtime.accountResolver,
		repository:      repository,
	}
	options, err := planner.worktreeAddPlan(request, destination)
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

	lstat := runtime.filesystem.Lstat
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

	if err := runtime.git.WorktreeAdd(ctx, repository.Path, options); err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"add worktree for %q at %q: %w",
			repository.Identity,
			destination,
			err,
		))
	}

	registered, err := runtime.enumerate(
		ctx,
		repository,
		roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     runtime.git,
			Filesystem: runtime.filesystem,
		},
	)
	if err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}
	added, err := local.FindRegisteredLinkedWorktree(registered, request.branch)
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
	if err := runtime.validateAssociation(
		ctx,
		repository,
		added,
		roots.WorktreeRoot,
		local.AssociationOptions{
			Git:        runtime.git,
			Filesystem: runtime.filesystem,
		},
	); err != nil {
		return worktreeAddFailure(fmt.Errorf(
			"worktree was added at %q but registration validation failed: %w",
			destination,
			err,
		))
	}

	if _, err := fmt.Fprintln(runtime.stdout, local.NormalizePathForOutput(destination)); err != nil {
		return worktreeAddFailure(fmt.Errorf("write worktree path: %w", err))
	}

	if herdrEnabled {
		if _, err := runtime.herdr.CreateWorkspace(ctx, herdr.CreateOptions{
			Cwd:   destination,
			Label: herdrWorkspaceLabel(repository.Repo, request.branch),
			Focus: true,
		}); err != nil {
			// The worktree itself and its stdout path are already
			// committed and correct; only the additional Herdr workspace
			// failed, so this is reported as an ordinary error rather
			// than unwound through worktreeAddFailure.
			return fmt.Errorf("open Herdr workspace for %q: %w", destination, err)
		}
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

	cwd, err := runtime.getwd()
	if err != nil {
		return local.Repository{}, fmt.Errorf("get current directory: %w", err)
	}
	current, err := runtime.current(
		ctx,
		cwd,
		worktreeRoot,
		repositories,
		local.CurrentOptions{
			Git:        runtime.git,
			Filesystem: runtime.filesystem,
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

func (planner *worktreeAddPlanner) worktreeAddPlan(
	request worktreeAddRequest,
	destination string,
) (gitcmd.WorktreeAddOptions, error) {
	options := gitcmd.WorktreeAddOptions{
		Path:  destination,
		Force: request.force,
	}

	switch request.mode {
	case worktreeAddModeOrphan:
		options.Orphan = true
		options.NewBranch = request.branch
		return options, nil
	case worktreeAddModeDetach:
		target := request.commitish
		if target == "" {
			target = request.branch
		}
		if err := planner.worktreeAddRequireRevision(target); err != nil {
			return gitcmd.WorktreeAddOptions{}, err
		}
		options.Detach = true
		options.Commitish = target
		return options, nil
	case worktreeAddModeNew:
		exists, err := planner.git.LocalBranchExists(
			planner.ctx,
			planner.repository.Path,
			request.branch,
		)
		if err != nil {
			return gitcmd.WorktreeAddOptions{}, fmt.Errorf(
				"inspect local branch %q: %w",
				request.branch,
				err,
			)
		}
		if exists {
			return gitcmd.WorktreeAddOptions{}, fmt.Errorf(
				"cannot create branch %q with -b: local branch already exists",
				request.branch,
			)
		}
		target, err := planner.worktreeAddCreationTarget(request.commitish)
		if err != nil {
			return gitcmd.WorktreeAddOptions{}, err
		}
		options.NewBranch = request.branch
		options.Commitish = target
		return options, nil
	case worktreeAddModeReset:
		target, err := planner.worktreeAddCreationTarget(request.commitish)
		if err != nil {
			return gitcmd.WorktreeAddOptions{}, err
		}
		options.ResetBranch = request.branch
		options.Commitish = target
		return options, nil
	case worktreeAddModeAutomatic:
	default:
		return gitcmd.WorktreeAddOptions{}, errors.New("unsupported worktree add mode")
	}

	localExists, err := planner.git.LocalBranchExists(
		planner.ctx,
		planner.repository.Path,
		request.branch,
	)
	if err != nil {
		return gitcmd.WorktreeAddOptions{}, fmt.Errorf(
			"inspect local branch %q: %w",
			request.branch,
			err,
		)
	}
	if localExists {
		options.Commitish = request.branch
		return options, nil
	}

	remote, found, err := planner.worktreeAddFindRemoteBranch(request.branch)
	if err != nil {
		return gitcmd.WorktreeAddOptions{}, err
	}
	if found {
		options.Commitish = remote + "/" + request.branch
		options.NewBranch = request.branch
		options.Tracking = gitcmd.TrackingEnabled
		return options, nil
	}

	target, err := planner.worktreeAddCreationTarget(request.commitish)
	if err != nil {
		return gitcmd.WorktreeAddOptions{}, err
	}
	options.Commitish = target
	options.NewBranch = request.branch
	return options, nil
}

func (planner *worktreeAddPlanner) worktreeAddCreationTarget(
	explicit string,
) (string, error) {
	if explicit != "" {
		if err := planner.worktreeAddRequireRevision(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}

	// Resolution only happens here, once this fallback path is actually
	// reached: worktreeAddPlan's earlier local- and remote-branch matches
	// (steps 1 and 2) never call the GitHub API, so the common case of
	// attaching to or tracking an existing branch never triggers gh account
	// resolution or an interactive prompt (see internal/ghauth).
	resolution, err := getResolveAccount(
		planner.ctx,
		planner.accountResolver,
		planner.repository.Host,
		planner.repository.Owner,
	)
	if err != nil {
		return "", err
	}

	defaultBranch, err := planner.api.DefaultBranch(
		planner.ctx,
		planner.repository.Host,
		planner.repository.Owner,
		planner.repository.Repo,
		resolution.Token,
	)
	if err != nil {
		return "", fmt.Errorf(
			"determine a start point for the new branch: %w; provide an explicit commit-ish",
			wrapAccountFailureHint(err, resolution),
		)
	}
	if err := local.ValidateBranch(defaultBranch); err != nil {
		return "", fmt.Errorf(
			"GitHub API returned an invalid default branch %q: %w",
			defaultBranch,
			err,
		)
	}

	localExists, err := planner.git.LocalBranchExists(
		planner.ctx,
		planner.repository.Path,
		defaultBranch,
	)
	if err != nil {
		return "", fmt.Errorf("inspect default branch %q: %w", defaultBranch, err)
	}
	if localExists {
		return defaultBranch, nil
	}

	remote, found, err := planner.worktreeAddFindRemoteBranch(defaultBranch)
	if err != nil {
		return "", err
	}
	if found {
		return remote + "/" + defaultBranch, nil
	}

	remote, err = planner.worktreeAddSyncRemote()
	if err != nil {
		return "", fmt.Errorf(
			"sync API default branch %q: %w; provide an explicit commit-ish",
			defaultBranch,
			err,
		)
	}
	// gh repo sync brings the API-selected default branch into the local
	// repository (and its remote-tracking ref) using gh's own GitHub
	// authentication, rather than a targeted `git fetch` that would need an
	// ambient Git credential helper for a private repository.
	if err := planner.gh.RepoSync(
		planner.ctx,
		planner.repository.Path,
		ghcmd.SyncOptions{
			Source: planner.repository.Owner + "/" + planner.repository.Repo,
			Branch: defaultBranch,
			Token:  resolution.Token,
		},
	); err != nil {
		return "", fmt.Errorf(
			"sync API default branch %q from remote %q: %w",
			defaultBranch,
			remote,
			wrapAccountFailureHint(err, resolution),
		)
	}
	exists, err := planner.git.RemoteBranchExists(
		planner.ctx,
		planner.repository.Path,
		remote,
		defaultBranch,
	)
	if err != nil {
		return "", fmt.Errorf(
			"verify synced default branch %q from remote %q: %w",
			defaultBranch,
			remote,
			err,
		)
	}
	if !exists {
		return "", fmt.Errorf(
			"sync API default branch %q from remote %q completed without creating the remote-tracking ref",
			defaultBranch,
			remote,
		)
	}
	return remote + "/" + defaultBranch, nil
}

func (planner *worktreeAddPlanner) worktreeAddRequireRevision(revision string) error {
	exists, err := planner.git.RevisionExists(
		planner.ctx,
		planner.repository.Path,
		revision,
	)
	if err != nil {
		return fmt.Errorf("resolve commit-ish %q: %w", revision, err)
	}
	if !exists {
		return fmt.Errorf("commit-ish %q does not exist locally", revision)
	}
	return nil
}

func (planner *worktreeAddPlanner) worktreeAddFindRemoteBranch(
	branch string,
) (string, bool, error) {
	originExists, err := planner.git.RemoteBranchExists(
		planner.ctx,
		planner.repository.Path,
		"origin",
		branch,
	)
	if err != nil {
		return "", false, fmt.Errorf(
			"inspect remote branch %q: %w",
			"origin/"+branch,
			err,
		)
	}
	if originExists {
		return "origin", true, nil
	}

	remotes, err := planner.worktreeAddRemotes()
	if err != nil {
		return "", false, err
	}
	matches := make([]string, 0, 1)
	for _, remote := range remotes {
		if remote == "origin" {
			continue
		}
		exists, err := planner.git.RemoteBranchExists(
			planner.ctx,
			planner.repository.Path,
			remote,
			branch,
		)
		if err != nil {
			return "", false, fmt.Errorf(
				"inspect remote branch %q: %w",
				remote+"/"+branch,
				err,
			)
		}
		if exists {
			matches = append(matches, remote)
		}
	}

	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf(
			"remote branch %q is ambiguous across remotes: %s",
			branch,
			strings.Join(matches, ", "),
		)
	}
}

// worktreeAddSyncRemote resolves which local remote name should have a
// tracking ref after gh repo sync brings the API default branch locally.
func (planner *worktreeAddPlanner) worktreeAddSyncRemote() (string, error) {
	remotes, err := planner.worktreeAddRemotes()
	if err != nil {
		return "", err
	}
	for _, remote := range remotes {
		if remote == "origin" {
			return remote, nil
		}
	}
	switch len(remotes) {
	case 0:
		return "", errors.New("repository has no configured remote")
	case 1:
		return remotes[0], nil
	default:
		return "", fmt.Errorf(
			"repository has multiple remotes and no origin (%s)",
			strings.Join(remotes, ", "),
		)
	}
}

func (planner *worktreeAddPlanner) worktreeAddRemotes() ([]string, error) {
	if planner.remotesLoaded {
		return append([]string(nil), planner.remotes...), nil
	}

	output, err := planner.git.OutputDir(
		planner.ctx,
		planner.repository.Path,
		"remote",
	)
	if err != nil {
		return nil, fmt.Errorf("list repository remotes: %w", err)
	}
	output = bytes.TrimRight(output, "\r\n")
	if len(output) == 0 {
		planner.remotesLoaded = true
		return nil, nil
	}

	rawRemotes := bytes.Split(output, []byte{'\n'})
	remotes := make([]string, 0, len(rawRemotes))
	seen := make(map[string]struct{}, len(rawRemotes))
	for _, rawRemote := range rawRemotes {
		rawRemote = bytes.TrimSuffix(rawRemote, []byte{'\r'})
		if len(rawRemote) == 0 || bytes.IndexByte(rawRemote, 0) >= 0 {
			return nil, errors.New("Git returned an invalid remote name")
		}
		remote := string(rawRemote)
		if _, duplicate := seen[remote]; duplicate {
			return nil, fmt.Errorf("Git returned duplicate remote %q", remote)
		}
		seen[remote] = struct{}{}
		remotes = append(remotes, remote)
	}
	sort.Strings(remotes)
	planner.remotes = remotes
	planner.remotesLoaded = true
	return append([]string(nil), remotes...), nil
}

func worktreeAddCreateParents(
	destination string,
	runtime worktreeAddRuntime,
) ([]string, error) {
	lstat := runtime.filesystem.Lstat
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
		if err := runtime.mkdir(path, 0o755); err != nil {
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
		err := runtime.remove(path)
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

func worktreeAddWriteWarnings(writer io.Writer, warnings []local.Warning) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(writer, "gh-qw: warning: %v\n", warning); err != nil {
			return err
		}
	}
	return nil
}
