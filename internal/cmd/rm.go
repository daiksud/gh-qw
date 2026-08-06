package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

var (
	// ErrRemoveDeclined reports a safe negative response to the removal prompt.
	ErrRemoveDeclined = errors.New("removal declined")
	// ErrRemoveSafety reports a runtime safety refusal.
	ErrRemoveSafety = errors.New("remove safety refusal")
)

// RemoveGit combines the Git capabilities required by rm.
type RemoveGit interface {
	local.Git
	WorktreeRemove(context.Context, string, gitcmd.WorktreeRemoveOptions) error
}

// RemovePrompt asks for confirmation without reading command stdin.
type RemovePrompt func(context.Context, io.Writer, string) (bool, error)

// RemoveDependencies supplies command integration and test seams.
type RemoveDependencies struct {
	Resolver RootResolver
	Discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
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
	Git          RemoveGit
	Filesystem   local.FilesystemOptions
	Prompt       RemovePrompt
	OpenTerminal func() (io.ReadCloser, error)
	Remove       func(string) error
	RemoveAll    func(string) error
	Stdout       io.Writer
	Stderr       io.Writer
}

type removeRuntime struct {
	resolver RootResolver
	discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
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
	git        RemoveGit
	filesystem local.FilesystemOptions
	fs         removeFilesystem
	prompt     RemovePrompt
	stdout     io.Writer
	stderr     io.Writer
}

type removeFilesystem struct {
	readDir      func(string) ([]os.DirEntry, error)
	lstat        func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool
	remove       func(string) error
	removeAll    func(string) error
}

type removeSelection struct {
	raw      string
	selector string
	slot     string
	linked   bool
}

type removeNodeKind uint8

const (
	removeDirectory removeNodeKind = iota + 1
	removeRegularFile
)

type removeNode struct {
	path string
	info fs.FileInfo
	kind removeNodeKind
}

type removeLinkedTarget struct {
	worktree  local.Worktree
	directory removeNode
	gitFile   removeNode
	cleanup   []removeNode
}

type removePlan struct {
	selection  removeSelection
	roots      rootpkg.Result
	repository local.Repository
	whole      bool

	repositoryRoot removeNode
	mainDirectory  removeNode
	mainGit        removeNode
	mainWorktree   local.Worktree
	mainCleanup    []removeNode

	worktreeRoot    removeNode
	worktreeBase    string
	worktreeBaseDir *removeNode
	worktreeCleanup []removeNode

	linked []removeLinkedTarget
}

type removeUsageError struct {
	err error
}

func (err *removeUsageError) Error() string {
	if err == nil || err.err == nil {
		return repospec.ErrUsage.Error()
	}
	return err.err.Error()
}

func (err *removeUsageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *removeUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

// NewRemoveCommand returns the command that safely removes one managed linked
// worktree or a complete main repository and all of its linked worktrees.
func NewRemoveCommand(dependencies RemoveDependencies) *cobra.Command {
	commandRuntime := removePrepareRuntime(dependencies)

	var dryRun bool
	command := &cobra.Command{
		Use:           "rm [--dry-run] <repo>|<owner>/<repo>|<host>/<owner>/<repo>[@<branch>]",
		Short:         "Remove a managed repository or linked worktree",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return removeNewUsageError(err)
			}
			if _, err := removeParseSelection(args[0]); err != nil {
				return removeNewUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			selection, err := removeParseSelection(args[0])
			if err != nil {
				return removeNewUsageError(err)
			}

			plan, err := removePreflight(
				command.Context(),
				commandRuntime,
				selection,
				true,
			)
			if err != nil {
				return err
			}
			if err := removeWritePlan(command.ErrOrStderr(), plan); err != nil {
				return fmt.Errorf("write removal plan: %w", err)
			}
			if dryRun {
				return nil
			}

			confirmed, err := commandRuntime.prompt(
				command.Context(),
				command.ErrOrStderr(),
				"Proceed with removal? [y/N] ",
			)
			if err != nil {
				return fmt.Errorf("confirm removal: %w", err)
			}
			if !confirmed {
				return ErrRemoveDeclined
			}

			revalidated, err := removePreflight(
				command.Context(),
				commandRuntime,
				selection,
				false,
			)
			if err != nil {
				return fmt.Errorf("revalidate removal plan: %w", err)
			}
			if err := removeComparePlans(commandRuntime.fs, plan, revalidated); err != nil {
				return fmt.Errorf("revalidate removal plan: %w", err)
			}

			if revalidated.whole {
				return removeExecuteWhole(command.Context(), commandRuntime, revalidated)
			}
			return removeExecuteLinked(command.Context(), commandRuntime, revalidated)
		},
	}
	command.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Print the complete removal plan without changing files",
	)
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return removeNewUsageError(err)
	})
	command.SetOut(commandRuntime.stdout)
	command.SetErr(commandRuntime.stderr)
	return command
}

func removePrepareRuntime(dependencies RemoveDependencies) removeRuntime {
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
	if dependencies.Discover == nil {
		dependencies.Discover = local.DiscoverRepositories
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

	git := dependencies.Git
	if git == nil {
		git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     io.Discard,
			Stderr:     stderr,
		}
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
	remove := dependencies.Remove
	if remove == nil {
		remove = os.Remove
	}
	removeAll := dependencies.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
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

	return removeRuntime{
		resolver:            resolver,
		discover:            dependencies.Discover,
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
			removeAll:    removeAll,
		},
		prompt: prompt,
		stdout: stdout,
		stderr: stderr,
	}
}

func removeParseSelection(argument string) (removeSelection, error) {
	selection := removeSelection{
		raw:      argument,
		selector: argument,
	}
	if index := strings.IndexByte(argument, '@'); index >= 0 {
		selection.selector = argument[:index]
		selection.slot = argument[index+1:]
		selection.linked = true
	}

	if _, err := local.ParseSelector(selection.selector); err != nil {
		return removeSelection{}, err
	}
	if selection.linked {
		if err := local.ValidateBranch(selection.slot); err != nil {
			return removeSelection{}, err
		}
	}
	return selection, nil
}

func removePreflight(
	ctx context.Context,
	commandRuntime removeRuntime,
	selection removeSelection,
	emitWarnings bool,
) (removePlan, error) {
	roots, err := commandRuntime.resolver.Resolve()
	if err != nil {
		return removePlan{}, fmt.Errorf("resolve roots: %w", err)
	}
	if len(roots.RepositoryRoots) == 0 {
		return removePlan{}, fmt.Errorf("%w: no repository root is configured", ErrRemoveSafety)
	}

	discovery, discoveryErr := commandRuntime.discover(
		ctx,
		roots.RepositoryRoots,
		local.DiscoveryOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	var warningErr error
	if emitWarnings {
		warningErr = removeWriteWarnings(commandRuntime.stderr, discovery.Warnings)
	}
	if discoveryErr != nil || warningErr != nil {
		return removePlan{}, errors.Join(
			removeWrapError("discover repositories", discoveryErr),
			removeWrapError("write discovery warnings", warningErr),
		)
	}

	repository, err := local.ResolveRepositoryForMutation(
		discovery.Repositories,
		selection.selector,
	)
	if err != nil {
		return removePlan{}, removeNewUsageError(
			fmt.Errorf("resolve repository %q: %w", selection.selector, err),
		)
	}

	plan, err := removePrepareCommon(
		ctx,
		commandRuntime,
		selection,
		roots,
		repository,
	)
	if err != nil {
		return removePlan{}, err
	}
	if selection.linked {
		return removePreflightLinked(ctx, commandRuntime, plan)
	}
	return removePreflightWhole(ctx, commandRuntime, plan)
}

func removePrepareCommon(
	ctx context.Context,
	commandRuntime removeRuntime,
	selection removeSelection,
	roots rootpkg.Result,
	repository local.Repository,
) (removePlan, error) {
	if err := local.ValidateRepository(repository); err != nil {
		return removePlan{}, fmt.Errorf("%w: validate selected repository: %v", ErrRemoveSafety, err)
	}
	if repository.RootIndex >= len(roots.RepositoryRoots) {
		return removePlan{}, fmt.Errorf(
			"%w: repository %q has root index %d outside %d configured roots",
			ErrRemoveSafety,
			repository.Identity,
			repository.RootIndex,
			len(roots.RepositoryRoots),
		)
	}
	configuredRoot := roots.RepositoryRoots[repository.RootIndex]
	if !removeSamePath(repository.Root, configuredRoot) {
		return removePlan{}, fmt.Errorf(
			"%w: repository root changed from %q to %q",
			ErrRemoveSafety,
			repository.Root,
			configuredRoot,
		)
	}
	if roots.WorktreeRoot == "" || !filepath.IsAbs(roots.WorktreeRoot) {
		return removePlan{}, fmt.Errorf(
			"%w: worktree root %q is not an absolute path",
			ErrRemoveSafety,
			roots.WorktreeRoot,
		)
	}
	for _, repositoryRoot := range roots.RepositoryRoots {
		if removePathsOverlap(repositoryRoot, roots.WorktreeRoot) {
			return removePlan{}, fmt.Errorf(
				"%w: repository root %q overlaps worktree root %q",
				ErrRemoveSafety,
				repositoryRoot,
				roots.WorktreeRoot,
			)
		}
	}

	repositoryRoot, err := removeCaptureNode(
		commandRuntime.fs,
		"",
		configuredRoot,
		removeDirectory,
		false,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("inspect repository root: %w", err)
	}
	worktreeRoot, err := removeCaptureNode(
		commandRuntime.fs,
		"",
		roots.WorktreeRoot,
		removeDirectory,
		false,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("inspect worktree root: %w", err)
	}

	expectedMain := filepath.Join(
		configuredRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if !removeSamePath(repository.Path, expectedMain) {
		return removePlan{}, fmt.Errorf(
			"%w: repository path %q is not the exact expected path %q",
			ErrRemoveSafety,
			repository.Path,
			expectedMain,
		)
	}
	derivedMain, err := local.MainPath(
		configuredRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("derive main repository path: %w", err)
	}
	if !removeSamePath(derivedMain, expectedMain) {
		return removePlan{}, fmt.Errorf(
			"%w: expected repository path %q resolves to %q",
			ErrRemoveSafety,
			expectedMain,
			derivedMain,
		)
	}

	mainDirectory, err := removeCaptureNode(
		commandRuntime.fs,
		configuredRoot,
		expectedMain,
		removeDirectory,
		true,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("inspect main repository: %w", err)
	}
	mainGit, err := removeCaptureNode(
		commandRuntime.fs,
		expectedMain,
		filepath.Join(expectedMain, ".git"),
		removeDirectory,
		true,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf(
			"%w: main repository .git is not a real contained directory: %v",
			ErrRemoveSafety,
			err,
		)
	}
	if err := removeValidateMainGitAssociation(
		ctx,
		commandRuntime.git,
		expectedMain,
		mainGit.path,
	); err != nil {
		return removePlan{}, fmt.Errorf("%w: %v", ErrRemoveSafety, err)
	}

	worktreeBase, err := removeExpectedWorktreeBase(roots.WorktreeRoot, repository)
	if err != nil {
		return removePlan{}, err
	}

	return removePlan{
		selection:      selection,
		roots:          roots,
		repository:     repository,
		repositoryRoot: repositoryRoot,
		mainDirectory:  mainDirectory,
		mainGit:        mainGit,
		worktreeRoot:   worktreeRoot,
		worktreeBase:   worktreeBase,
	}, nil
}

func removePreflightLinked(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) (removePlan, error) {
	worktree, err := commandRuntime.resolveManaged(
		ctx,
		plan.repository,
		plan.roots.WorktreeRoot,
		plan.selection.slot,
		local.ManagedWorktreeOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return removePlan{}, fmt.Errorf(
			"resolve managed worktree %q for %q: %w",
			plan.selection.slot,
			plan.repository.Identity,
			err,
		)
	}
	target, base, err := removePrepareLinkedTarget(
		ctx,
		commandRuntime,
		plan,
		worktree,
	)
	if err != nil {
		return removePlan{}, err
	}
	plan.worktreeBaseDir = &base
	plan.linked = []removeLinkedTarget{target}
	return plan, nil
}

func removePreflightWhole(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) (removePlan, error) {
	worktrees, err := commandRuntime.enumerate(
		ctx,
		plan.repository,
		plan.roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return removePlan{}, fmt.Errorf(
			"enumerate worktrees for %q: %w",
			plan.repository.Identity,
			err,
		)
	}

	var (
		mainRecords []local.Worktree
		linked      []local.Worktree
	)
	for _, worktree := range worktrees {
		if worktree.Main {
			mainRecords = append(mainRecords, worktree)
			continue
		}
		linked = append(linked, worktree)
	}
	if len(mainRecords) != 1 {
		return removePlan{}, fmt.Errorf(
			"%w: Git reported %d main worktrees for %q",
			ErrRemoveSafety,
			len(mainRecords),
			plan.repository.Identity,
		)
	}
	if err := removeValidateMainRecord(plan.repository, mainRecords[0]); err != nil {
		return removePlan{}, err
	}
	plan.mainWorktree = mainRecords[0]

	sort.Slice(linked, func(left, right int) bool {
		if linked[left].Identity != linked[right].Identity {
			return linked[left].Identity < linked[right].Identity
		}
		return removeOutputPath(linked[left].Path) < removeOutputPath(linked[right].Path)
	})

	seenSlots := make(map[string]string, len(linked))
	seenPaths := make(map[string]string, len(linked))
	for _, worktree := range linked {
		slotKey := removePathKey(filepath.FromSlash(worktree.Slot))
		if previous, exists := seenSlots[slotKey]; exists {
			return removePlan{}, fmt.Errorf(
				"%w: linked slot %q is duplicated by %q and %q",
				ErrRemoveSafety,
				worktree.Slot,
				previous,
				worktree.Path,
			)
		}
		seenSlots[slotKey] = worktree.Path

		pathKey := removePathKey(worktree.Path)
		if previous, exists := seenPaths[pathKey]; exists {
			return removePlan{}, fmt.Errorf(
				"%w: linked path %q is duplicated by %q",
				ErrRemoveSafety,
				worktree.Path,
				previous,
			)
		}
		seenPaths[pathKey] = worktree.Path
	}

	for _, worktree := range linked {
		target, base, err := removePrepareLinkedTarget(
			ctx,
			commandRuntime,
			plan,
			worktree,
		)
		if err != nil {
			return removePlan{}, err
		}
		if plan.worktreeBaseDir == nil {
			plan.worktreeBaseDir = &base
		} else if err := removeCompareNode(
			commandRuntime.fs,
			*plan.worktreeBaseDir,
			base,
			"worktree base",
		); err != nil {
			return removePlan{}, err
		}
		plan.linked = append(plan.linked, target)
	}

	if err := removeRequireClean(
		ctx,
		commandRuntime.git,
		plan.mainDirectory.path,
		"main repository",
	); err != nil {
		return removePlan{}, err
	}

	plan.mainCleanup, err = removeCaptureCleanupChain(
		commandRuntime.fs,
		plan.repositoryRoot.path,
		filepath.Dir(plan.mainDirectory.path),
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("plan main parent cleanup: %w", err)
	}

	if plan.worktreeBaseDir == nil {
		base, exists, captureErr := removeCaptureOptionalDirectory(
			commandRuntime.fs,
			plan.worktreeRoot.path,
			plan.worktreeBase,
		)
		if captureErr != nil {
			return removePlan{}, fmt.Errorf("inspect worktree base: %w", captureErr)
		}
		if exists {
			plan.worktreeBaseDir = &base
		}
	}
	if plan.worktreeBaseDir != nil {
		plan.worktreeCleanup, err = removeCaptureCleanupChain(
			commandRuntime.fs,
			plan.worktreeRoot.path,
			plan.worktreeBase,
		)
		if err != nil {
			return removePlan{}, fmt.Errorf("plan worktree-base cleanup: %w", err)
		}
	}

	plan.whole = true
	return plan, nil
}

func removePrepareLinkedTarget(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
	worktree local.Worktree,
) (removeLinkedTarget, removeNode, error) {
	if err := removeValidateLinkedRecord(plan.repository, worktree); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}
	if err := commandRuntime.validateAssociation(
		ctx,
		plan.repository,
		worktree,
		plan.roots.WorktreeRoot,
		local.AssociationOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	); err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"validate linked worktree %q association: %w",
			worktree.Slot,
			err,
		)
	}

	expectedPath, err := local.WorktreePath(
		plan.roots.WorktreeRoot,
		plan.repository.Host,
		plan.repository.Owner,
		plan.repository.Repo,
		worktree.Slot,
	)
	if err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"derive linked worktree path %q: %w",
			worktree.Slot,
			err,
		)
	}
	expectedLexical := filepath.Join(
		plan.worktreeBase,
		filepath.FromSlash(worktree.Slot),
	)
	if !removeSamePath(expectedPath, expectedLexical) ||
		!removeSamePath(worktree.Path, expectedLexical) {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"%w: linked worktree %q path %q is not the exact deterministic path %q",
			ErrRemoveSafety,
			worktree.Slot,
			worktree.Path,
			expectedLexical,
		)
	}

	base, err := removeCaptureNode(
		commandRuntime.fs,
		plan.worktreeRoot.path,
		plan.worktreeBase,
		removeDirectory,
		true,
	)
	if err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"inspect worktree base for %q: %w",
			worktree.Slot,
			err,
		)
	}
	directory, err := removeCaptureNode(
		commandRuntime.fs,
		base.path,
		expectedLexical,
		removeDirectory,
		true,
	)
	if err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"inspect linked worktree %q: %w",
			worktree.Slot,
			err,
		)
	}
	gitFile, err := removeCaptureNode(
		commandRuntime.fs,
		directory.path,
		filepath.Join(directory.path, ".git"),
		removeRegularFile,
		true,
	)
	if err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"%w: linked worktree %q .git is not a real contained file: %v",
			ErrRemoveSafety,
			worktree.Slot,
			err,
		)
	}
	cleanup, err := removeCaptureCleanupChain(
		commandRuntime.fs,
		base.path,
		filepath.Dir(directory.path),
	)
	if err != nil {
		return removeLinkedTarget{}, removeNode{}, fmt.Errorf(
			"plan linked parent cleanup for %q: %w",
			worktree.Slot,
			err,
		)
	}
	if err := removeRequireClean(
		ctx,
		commandRuntime.git,
		directory.path,
		"linked worktree "+worktree.Slot,
	); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}

	return removeLinkedTarget{
		worktree:  worktree,
		directory: directory,
		gitFile:   gitFile,
		cleanup:   cleanup,
	}, base, nil
}

func removeValidateMainRecord(
	repository local.Repository,
	worktree local.Worktree,
) error {
	switch {
	case !worktree.Main:
		return fmt.Errorf("%w: expected a main worktree record", ErrRemoveSafety)
	case worktree.Bare:
		return fmt.Errorf("%w: main worktree is bare", ErrRemoveSafety)
	case worktree.Detached:
		return fmt.Errorf(
			"%w: detached main worktree for %q is ambiguous",
			ErrRemoveSafety,
			repository.Identity,
		)
	case worktree.Locked:
		return fmt.Errorf(
			"%w: main worktree %q is locked%s",
			ErrRemoveSafety,
			removeOutputPath(worktree.Path),
			removeReasonSuffix(worktree.LockedReason),
		)
	case worktree.Prunable:
		return fmt.Errorf(
			"%w: main worktree %q is prunable%s",
			ErrRemoveSafety,
			removeOutputPath(worktree.Path),
			removeReasonSuffix(worktree.PrunableReason),
		)
	case worktree.Repository.Identity != repository.Identity:
		return fmt.Errorf(
			"%w: main worktree belongs to %q instead of %q",
			ErrRemoveSafety,
			worktree.Repository.Identity,
			repository.Identity,
		)
	case worktree.Identity != repository.Identity:
		return fmt.Errorf(
			"%w: main worktree identity %q does not match %q",
			ErrRemoveSafety,
			worktree.Identity,
			repository.Identity,
		)
	case !removeSamePath(worktree.Path, repository.Path):
		return fmt.Errorf(
			"%w: main worktree path %q does not match %q",
			ErrRemoveSafety,
			worktree.Path,
			repository.Path,
		)
	case worktree.HEAD == "":
		return fmt.Errorf("%w: main worktree has no HEAD", ErrRemoveSafety)
	case worktree.Branch == "":
		return fmt.Errorf("%w: main worktree has no branch", ErrRemoveSafety)
	}
	if err := local.ValidateBranch(worktree.Branch); err != nil {
		return fmt.Errorf("%w: invalid main worktree branch: %v", ErrRemoveSafety, err)
	}
	return nil
}

func removeValidateLinkedRecord(
	repository local.Repository,
	worktree local.Worktree,
) error {
	switch {
	case worktree.Main:
		return fmt.Errorf(
			"%w: slot %q resolved to the main worktree",
			ErrRemoveSafety,
			worktree.Slot,
		)
	case worktree.Bare:
		return fmt.Errorf(
			"%w: linked worktree %q is bare",
			ErrRemoveSafety,
			worktree.Slot,
		)
	case worktree.Detached:
		return fmt.Errorf(
			"%w: detached linked worktree %q is ambiguous",
			ErrRemoveSafety,
			worktree.Slot,
		)
	case worktree.Locked:
		return fmt.Errorf(
			"%w: linked worktree %q is locked%s",
			ErrRemoveSafety,
			worktree.Slot,
			removeReasonSuffix(worktree.LockedReason),
		)
	case worktree.Prunable:
		return fmt.Errorf(
			"%w: linked worktree %q is prunable%s",
			ErrRemoveSafety,
			worktree.Slot,
			removeReasonSuffix(worktree.PrunableReason),
		)
	case worktree.Repository.Identity != repository.Identity:
		return fmt.Errorf(
			"%w: linked worktree %q belongs to %q instead of %q",
			ErrRemoveSafety,
			worktree.Slot,
			worktree.Repository.Identity,
			repository.Identity,
		)
	case worktree.Slot == "":
		return fmt.Errorf("%w: linked worktree has an empty slot", ErrRemoveSafety)
	case worktree.Identity != repository.Identity+"@"+worktree.Slot:
		return fmt.Errorf(
			"%w: linked worktree identity %q is inconsistent",
			ErrRemoveSafety,
			worktree.Identity,
		)
	case worktree.HEAD == "":
		return fmt.Errorf(
			"%w: linked worktree %q has no HEAD",
			ErrRemoveSafety,
			worktree.Slot,
		)
	case worktree.Branch == "":
		return fmt.Errorf(
			"%w: linked worktree %q has no branch",
			ErrRemoveSafety,
			worktree.Slot,
		)
	}
	if err := local.ValidateBranch(worktree.Slot); err != nil {
		return fmt.Errorf("%w: invalid linked slot: %v", ErrRemoveSafety, err)
	}
	if err := local.ValidateBranch(worktree.Branch); err != nil {
		return fmt.Errorf("%w: invalid linked branch: %v", ErrRemoveSafety, err)
	}
	return nil
}

func removeValidateMainGitAssociation(
	ctx context.Context,
	git RemoveGit,
	repositoryPath, gitPath string,
) error {
	topOutput, err := git.OutputDir(ctx, repositoryPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("inspect main Git top-level: %w", err)
	}
	topLevel, err := removeParseGitPath(topOutput, repositoryPath, "top-level")
	if err != nil {
		return err
	}
	commonOutput, err := git.OutputDir(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("inspect main Git common directory: %w", err)
	}
	commonDir, err := removeParseGitPath(
		commonOutput,
		repositoryPath,
		"common Git directory",
	)
	if err != nil {
		return err
	}
	if !removeSamePath(topLevel, repositoryPath) ||
		!removeSamePath(commonDir, gitPath) {
		return fmt.Errorf(
			"main Git association changed: top-level %q, common directory %q",
			topLevel,
			commonDir,
		)
	}
	return nil
}

func removeParseGitPath(
	output []byte,
	relativeTo, description string,
) (string, error) {
	output = bytes.TrimSuffix(output, []byte{'\n'})
	output = bytes.TrimSuffix(output, []byte{'\r'})
	if len(output) == 0 {
		return "", fmt.Errorf("Git returned an empty %s", description)
	}
	if bytes.IndexByte(output, 0) >= 0 ||
		bytes.IndexByte(output, '\n') >= 0 ||
		bytes.IndexByte(output, '\r') >= 0 {
		return "", fmt.Errorf("Git returned a non-single-line %s", description)
	}
	path := string(output)
	if !filepath.IsAbs(path) {
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path), nil
}

func removeExpectedWorktreeBase(
	worktreeRoot string,
	repository local.Repository,
) (string, error) {
	expected := filepath.Join(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	derived, err := local.WorktreeBasePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return "", fmt.Errorf("derive worktree base: %w", err)
	}
	if !removeSamePath(derived, expected) {
		return "", fmt.Errorf(
			"%w: expected worktree base %q resolves to %q",
			ErrRemoveSafety,
			expected,
			derived,
		)
	}
	return expected, nil
}

func removeRequireClean(
	ctx context.Context,
	git RemoveGit,
	path, description string,
) error {
	output, err := git.OutputDir(
		ctx,
		path,
		"status",
		"--porcelain=v1",
		"-z",
		"--untracked-files=all",
		"--ignore-submodules=none",
	)
	if err != nil {
		return fmt.Errorf("inspect %s cleanliness at %q: %w", description, path, err)
	}
	if len(output) != 0 {
		return fmt.Errorf(
			"%w: %s %q has tracked or untracked changes",
			ErrRemoveSafety,
			description,
			removeOutputPath(path),
		)
	}
	return nil
}

func removeCaptureNode(
	filesystem removeFilesystem,
	boundary, path string,
	kind removeNodeKind,
	strictlyWithin bool,
) (removeNode, error) {
	if path == "" || !filepath.IsAbs(path) {
		return removeNode{}, fmt.Errorf("path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	if strictlyWithin && !removePathStrictlyWithin(boundary, path) {
		return removeNode{}, fmt.Errorf("path %q is outside boundary %q", path, boundary)
	}

	info, err := filesystem.lstat(path)
	if err != nil {
		return removeNode{}, err
	}
	if err := fsidentity.Prime(info, filesystem.sameFile); err != nil {
		return removeNode{}, fmt.Errorf("capture filesystem identity: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return removeNode{}, errors.New("path is a symbolic link")
	}
	switch kind {
	case removeDirectory:
		if !info.IsDir() {
			return removeNode{}, errors.New("path is not a directory")
		}
	case removeRegularFile:
		if !info.Mode().IsRegular() {
			return removeNode{}, errors.New("path is not a regular file")
		}
	default:
		return removeNode{}, errors.New("unknown path kind")
	}

	physical, err := filesystem.evalSymlinks(path)
	if err != nil {
		return removeNode{}, err
	}
	if !filepath.IsAbs(physical) {
		return removeNode{}, fmt.Errorf("resolved path %q is not absolute", physical)
	}
	physical = filepath.Clean(physical)
	if !removeSamePath(physical, path) {
		return removeNode{}, fmt.Errorf(
			"path resolves through a symbolic link to %q",
			physical,
		)
	}
	if strictlyWithin && !removePathStrictlyWithin(boundary, physical) {
		return removeNode{}, fmt.Errorf(
			"resolved path %q is outside boundary %q",
			physical,
			boundary,
		)
	}
	return removeNode{path: path, info: info, kind: kind}, nil
}

func removeCaptureOptionalDirectory(
	filesystem removeFilesystem,
	boundary, path string,
) (removeNode, bool, error) {
	node, err := removeCaptureNode(
		filesystem,
		boundary,
		path,
		removeDirectory,
		true,
	)
	if errors.Is(err, fs.ErrNotExist) {
		return removeNode{}, false, nil
	}
	return node, err == nil, err
}

func removeCaptureCleanupChain(
	filesystem removeFilesystem,
	boundary, start string,
) ([]removeNode, error) {
	boundary = filepath.Clean(boundary)
	start = filepath.Clean(start)
	if removeSamePath(start, boundary) {
		return nil, nil
	}
	if !removePathStrictlyWithin(boundary, start) {
		return nil, fmt.Errorf("cleanup start %q is outside boundary %q", start, boundary)
	}

	var result []removeNode
	for current := start; !removeSamePath(current, boundary); current = filepath.Dir(current) {
		if !removePathStrictlyWithin(boundary, current) {
			return nil, fmt.Errorf(
				"cleanup directory %q escaped boundary %q",
				current,
				boundary,
			)
		}
		node, err := removeCaptureNode(
			filesystem,
			boundary,
			current,
			removeDirectory,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("inspect cleanup directory %q: %w", current, err)
		}
		result = append(result, node)
	}
	return result, nil
}

func removeComparePlans(
	filesystem removeFilesystem,
	before, after removePlan,
) error {
	if before.selection != after.selection ||
		before.whole != after.whole ||
		before.repository.Identity != after.repository.Identity ||
		before.repository.RootIndex != after.repository.RootIndex ||
		!removeSamePath(before.repository.Path, after.repository.Path) ||
		!removeSamePath(before.repository.Root, after.repository.Root) {
		return fmt.Errorf("%w: selected repository or removal mode changed", ErrRemoveSafety)
	}
	if len(before.roots.RepositoryRoots) != len(after.roots.RepositoryRoots) ||
		!removeSamePath(before.roots.WorktreeRoot, after.roots.WorktreeRoot) {
		return fmt.Errorf("%w: configured roots changed", ErrRemoveSafety)
	}
	for index := range before.roots.RepositoryRoots {
		if !removeSamePath(
			before.roots.RepositoryRoots[index],
			after.roots.RepositoryRoots[index],
		) {
			return fmt.Errorf("%w: configured roots changed", ErrRemoveSafety)
		}
	}

	for _, comparison := range []struct {
		name   string
		before removeNode
		after  removeNode
	}{
		{"repository root", before.repositoryRoot, after.repositoryRoot},
		{"main repository", before.mainDirectory, after.mainDirectory},
		{"main .git", before.mainGit, after.mainGit},
		{"worktree root", before.worktreeRoot, after.worktreeRoot},
	} {
		if err := removeCompareNode(
			filesystem,
			comparison.before,
			comparison.after,
			comparison.name,
		); err != nil {
			return err
		}
	}

	if !removeSamePath(before.worktreeBase, after.worktreeBase) ||
		(before.worktreeBaseDir == nil) != (after.worktreeBaseDir == nil) {
		return fmt.Errorf("%w: worktree base changed", ErrRemoveSafety)
	}
	if before.worktreeBaseDir != nil {
		if err := removeCompareNode(
			filesystem,
			*before.worktreeBaseDir,
			*after.worktreeBaseDir,
			"worktree base",
		); err != nil {
			return err
		}
	}
	if before.whole && !removeSameWorktree(before.mainWorktree, after.mainWorktree) {
		return fmt.Errorf("%w: main worktree registration changed", ErrRemoveSafety)
	}
	if err := removeCompareNodeSlices(
		filesystem,
		before.mainCleanup,
		after.mainCleanup,
		"main cleanup",
	); err != nil {
		return err
	}
	if err := removeCompareNodeSlices(
		filesystem,
		before.worktreeCleanup,
		after.worktreeCleanup,
		"worktree cleanup",
	); err != nil {
		return err
	}

	if len(before.linked) != len(after.linked) {
		return fmt.Errorf(
			"%w: linked worktree count changed from %d to %d",
			ErrRemoveSafety,
			len(before.linked),
			len(after.linked),
		)
	}
	for index := range before.linked {
		if err := removeCompareLinkedTarget(
			filesystem,
			before.linked[index],
			after.linked[index],
		); err != nil {
			return err
		}
	}
	return nil
}

func removeCompareLinkedTarget(
	filesystem removeFilesystem,
	before, after removeLinkedTarget,
) error {
	if !removeSameWorktree(before.worktree, after.worktree) {
		return fmt.Errorf(
			"%w: linked worktree registration changed for %q",
			ErrRemoveSafety,
			before.worktree.Slot,
		)
	}
	if err := removeCompareNode(
		filesystem,
		before.directory,
		after.directory,
		"linked worktree "+before.worktree.Slot,
	); err != nil {
		return err
	}
	if err := removeCompareNode(
		filesystem,
		before.gitFile,
		after.gitFile,
		"linked .git "+before.worktree.Slot,
	); err != nil {
		return err
	}
	return removeCompareNodeSlices(
		filesystem,
		before.cleanup,
		after.cleanup,
		"linked cleanup "+before.worktree.Slot,
	)
}

func removeCompareNodeSlices(
	filesystem removeFilesystem,
	before, after []removeNode,
	description string,
) error {
	if len(before) != len(after) {
		return fmt.Errorf("%w: %s path set changed", ErrRemoveSafety, description)
	}
	for index := range before {
		if err := removeCompareNode(
			filesystem,
			before[index],
			after[index],
			description,
		); err != nil {
			return err
		}
	}
	return nil
}

func removeCompareNode(
	filesystem removeFilesystem,
	before, after removeNode,
	description string,
) error {
	if before.kind != after.kind || !removeSamePath(before.path, after.path) {
		return fmt.Errorf("%w: %s path changed", ErrRemoveSafety, description)
	}
	if before.info == nil || after.info == nil ||
		!filesystem.sameFile(before.info, after.info) {
		return fmt.Errorf(
			"%w: %s %q was replaced",
			ErrRemoveSafety,
			description,
			removeOutputPath(before.path),
		)
	}
	return nil
}

func removeSameWorktree(first, second local.Worktree) bool {
	return first.Repository.Identity == second.Repository.Identity &&
		first.Identity == second.Identity &&
		first.Slot == second.Slot &&
		removeSamePath(first.Path, second.Path) &&
		first.HEAD == second.HEAD &&
		first.Branch == second.Branch &&
		first.Main == second.Main &&
		first.Detached == second.Detached &&
		first.Bare == second.Bare &&
		first.Locked == second.Locked &&
		first.LockedReason == second.LockedReason &&
		first.Prunable == second.Prunable &&
		first.PrunableReason == second.PrunableReason
}

func removeExecuteLinked(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) error {
	state := removeNewMutationState(plan)
	target := plan.linked[0]

	if err := commandRuntime.git.WorktreeRemove(
		ctx,
		plan.repository.Path,
		gitcmd.WorktreeRemoveOptions{Path: target.directory.path},
	); err != nil {
		observationErr := state.observePath(commandRuntime.fs, target.directory.path)
		return state.failure(
			fmt.Sprintf("remove linked worktree %q", removeOutputPath(target.directory.path)),
			errors.Join(err, observationErr),
		)
	}
	missing, err := removePathMissing(commandRuntime.fs, target.directory.path)
	if err != nil {
		return state.failure("verify linked worktree removal", err)
	}
	if !missing {
		return state.failure(
			"verify linked worktree removal",
			fmt.Errorf(
				"Git returned success but exact path %q still exists",
				removeOutputPath(target.directory.path),
			),
		)
	}
	state.markRemoved(target.directory.path)

	if err := removeVerifyWorktreeUnregistered(
		ctx,
		commandRuntime.git,
		plan.repository.Path,
		target.directory.path,
	); err != nil {
		return state.failure("verify linked worktree registration removal", err)
	}
	if err := removeCleanupEmptyChain(
		commandRuntime.fs,
		*plan.worktreeBaseDir,
		target.cleanup,
	); err != nil {
		return state.failure("cleanup linked worktree parents", err)
	}
	if err := removeWriteProgress(
		commandRuntime.stderr,
		"removed linked worktree",
		target.directory.path,
	); err != nil {
		return state.failure("write linked removal progress", err)
	}
	return nil
}

func removeExecuteWhole(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) error {
	state := removeNewMutationState(plan)

	for _, target := range plan.linked {
		if err := removeRevalidateLinkedForMutation(
			ctx,
			commandRuntime,
			plan,
			target,
		); err != nil {
			return state.failure(
				"revalidate linked worktree "+target.worktree.Slot,
				err,
			)
		}

		err := commandRuntime.git.WorktreeRemove(
			ctx,
			plan.repository.Path,
			gitcmd.WorktreeRemoveOptions{Path: target.directory.path},
		)
		if err != nil {
			observationErr := state.observePath(commandRuntime.fs, target.directory.path)
			return state.failure(
				fmt.Sprintf(
					"remove linked worktree %q",
					removeOutputPath(target.directory.path),
				),
				errors.Join(err, observationErr),
			)
		}
		missing, err := removePathMissing(commandRuntime.fs, target.directory.path)
		if err != nil {
			return state.failure("verify linked worktree removal", err)
		}
		if !missing {
			return state.failure(
				"verify linked worktree removal",
				fmt.Errorf(
					"Git returned success but exact path %q still exists",
					removeOutputPath(target.directory.path),
				),
			)
		}
		state.markRemoved(target.directory.path)

		if err := removeVerifyWorktreeUnregistered(
			ctx,
			commandRuntime.git,
			plan.repository.Path,
			target.directory.path,
		); err != nil {
			return state.failure("verify linked worktree registration removal", err)
		}
		if err := removeCleanupEmptyChain(
			commandRuntime.fs,
			*plan.worktreeBaseDir,
			target.cleanup,
		); err != nil {
			return state.failure("cleanup linked worktree parents", err)
		}
		if err := removeWriteProgress(
			commandRuntime.stderr,
			"removed linked worktree",
			target.directory.path,
		); err != nil {
			return state.failure("write linked removal progress", err)
		}
	}

	if err := removeRevalidateMainForDeletion(ctx, commandRuntime, plan); err != nil {
		return state.failure("revalidate main repository before exact deletion", err)
	}
	if err := commandRuntime.fs.removeAll(plan.mainDirectory.path); err != nil {
		observationErr := state.observePath(commandRuntime.fs, plan.mainDirectory.path)
		return state.failure(
			fmt.Sprintf(
				"remove exact main repository directory %q",
				removeOutputPath(plan.mainDirectory.path),
			),
			errors.Join(err, observationErr),
		)
	}
	missing, err := removePathMissing(commandRuntime.fs, plan.mainDirectory.path)
	if err != nil {
		return state.failure("verify main repository deletion", err)
	}
	if !missing {
		return state.failure(
			"verify main repository deletion",
			fmt.Errorf(
				"exact main repository path %q still exists",
				removeOutputPath(plan.mainDirectory.path),
			),
		)
	}
	state.markRemoved(plan.mainDirectory.path)

	if err := removeCleanupEmptyChain(
		commandRuntime.fs,
		plan.repositoryRoot,
		plan.mainCleanup,
	); err != nil {
		return state.failure("cleanup empty repository owner and host directories", err)
	}
	if plan.worktreeBaseDir != nil {
		if err := removeCleanupEmptyChain(
			commandRuntime.fs,
			plan.worktreeRoot,
			plan.worktreeCleanup,
		); err != nil {
			return state.failure("cleanup empty worktree-base directories", err)
		}
	}
	if err := removeWriteProgress(
		commandRuntime.stderr,
		"removed main repository",
		plan.mainDirectory.path,
	); err != nil {
		return state.failure("write main removal progress", err)
	}
	return nil
}

func removeRevalidateLinkedForMutation(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
	planned removeLinkedTarget,
) error {
	for _, node := range []struct {
		name string
		node removeNode
	}{
		{"repository root", plan.repositoryRoot},
		{"main repository", plan.mainDirectory},
		{"main .git", plan.mainGit},
		{"worktree root", plan.worktreeRoot},
		{"worktree base", *plan.worktreeBaseDir},
	} {
		if err := removeRevalidateNode(commandRuntime.fs, node.node, node.name); err != nil {
			return err
		}
	}
	if err := removeValidateMainGitAssociation(
		ctx,
		commandRuntime.git,
		plan.mainDirectory.path,
		plan.mainGit.path,
	); err != nil {
		return fmt.Errorf("%w: %v", ErrRemoveSafety, err)
	}

	worktree, err := commandRuntime.resolveManaged(
		ctx,
		plan.repository,
		plan.roots.WorktreeRoot,
		planned.worktree.Slot,
		local.ManagedWorktreeOptions{
			Git:        commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return err
	}
	current, base, err := removePrepareLinkedTarget(
		ctx,
		commandRuntime,
		plan,
		worktree,
	)
	if err != nil {
		return err
	}
	if err := removeCompareNode(
		commandRuntime.fs,
		*plan.worktreeBaseDir,
		base,
		"worktree base",
	); err != nil {
		return err
	}
	return removeCompareLinkedTarget(commandRuntime.fs, planned, current)
}

func removeRevalidateMainForDeletion(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) error {
	for _, node := range []struct {
		name string
		node removeNode
	}{
		{"repository root", plan.repositoryRoot},
		{"main repository", plan.mainDirectory},
		{"main .git", plan.mainGit},
		{"worktree root", plan.worktreeRoot},
	} {
		if err := removeRevalidateNode(commandRuntime.fs, node.node, node.name); err != nil {
			return err
		}
	}
	if plan.worktreeBaseDir != nil {
		if err := removeRevalidateNode(
			commandRuntime.fs,
			*plan.worktreeBaseDir,
			"worktree base",
		); err != nil {
			return err
		}
	}
	if err := removeValidateMainGitAssociation(
		ctx,
		commandRuntime.git,
		plan.mainDirectory.path,
		plan.mainGit.path,
	); err != nil {
		return fmt.Errorf("%w: %v", ErrRemoveSafety, err)
	}

	worktrees, err := commandRuntime.enumerate(
		ctx,
		plan.repository,
		plan.roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return err
	}
	if len(worktrees) != 1 || !worktrees[0].Main {
		return fmt.Errorf(
			"%w: Git reports %d worktrees immediately before main deletion",
			ErrRemoveSafety,
			len(worktrees),
		)
	}
	if err := removeValidateMainRecord(plan.repository, worktrees[0]); err != nil {
		return err
	}
	if !removeSameWorktree(plan.mainWorktree, worktrees[0]) {
		return fmt.Errorf("%w: main worktree registration changed", ErrRemoveSafety)
	}
	if err := removeRequireClean(
		ctx,
		commandRuntime.git,
		plan.mainDirectory.path,
		"main repository",
	); err != nil {
		return err
	}

	for _, node := range append(
		append([]removeNode(nil), plan.mainCleanup...),
		plan.worktreeCleanup...,
	) {
		if err := removeRevalidateNode(commandRuntime.fs, node, "cleanup directory"); err != nil {
			return err
		}
	}
	if err := removeRevalidateNode(
		commandRuntime.fs,
		plan.mainDirectory,
		"main repository",
	); err != nil {
		return err
	}
	return removeRevalidateNode(commandRuntime.fs, plan.mainGit, "main .git")
}

func removeRevalidateNode(
	filesystem removeFilesystem,
	snapshot removeNode,
	description string,
) error {
	current, err := removeCaptureNode(
		filesystem,
		"",
		snapshot.path,
		snapshot.kind,
		false,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: revalidate %s %q: %v",
			ErrRemoveSafety,
			description,
			removeOutputPath(snapshot.path),
			err,
		)
	}
	return removeCompareNode(filesystem, snapshot, current, description)
}

func removeVerifyWorktreeUnregistered(
	ctx context.Context,
	git RemoveGit,
	repositoryPath, removedPath string,
) error {
	worktrees, err := git.WorktreeList(ctx, repositoryPath)
	if err != nil {
		return err
	}
	for _, worktree := range worktrees {
		if removeSamePath(worktree.Path, removedPath) {
			return fmt.Errorf(
				"Git still registers exact path %q",
				removeOutputPath(removedPath),
			)
		}
	}
	return nil
}

func removeCleanupEmptyChain(
	filesystem removeFilesystem,
	boundary removeNode,
	chain []removeNode,
) error {
	if err := removeRevalidateNode(filesystem, boundary, "cleanup boundary"); err != nil {
		return err
	}
	for _, planned := range chain {
		if !removePathStrictlyWithin(boundary.path, planned.path) {
			return fmt.Errorf(
				"cleanup directory %q escaped boundary %q",
				planned.path,
				boundary.path,
			)
		}

		current, err := removeCaptureNode(
			filesystem,
			boundary.path,
			planned.path,
			removeDirectory,
			true,
		)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect cleanup directory %q: %w", planned.path, err)
		}
		if err := removeCompareNode(
			filesystem,
			planned,
			current,
			"cleanup directory",
		); err != nil {
			return err
		}

		entries, err := filesystem.readDir(planned.path)
		if err != nil {
			return fmt.Errorf("read cleanup directory %q: %w", planned.path, err)
		}
		if len(entries) != 0 {
			return nil
		}

		revalidated, err := removeCaptureNode(
			filesystem,
			boundary.path,
			planned.path,
			removeDirectory,
			true,
		)
		if err != nil {
			return fmt.Errorf("reinspect cleanup directory %q: %w", planned.path, err)
		}
		if err := removeCompareNode(
			filesystem,
			current,
			revalidated,
			"cleanup directory",
		); err != nil {
			return err
		}
		if err := filesystem.remove(planned.path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
				return nil
			}
			return fmt.Errorf("remove empty cleanup directory %q: %w", planned.path, err)
		}
	}
	return nil
}

func removePathMissing(filesystem removeFilesystem, path string) (bool, error) {
	_, err := filesystem.lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

type removeMutationState struct {
	planned []string
	removed map[string]bool
}

func removeNewMutationState(plan removePlan) *removeMutationState {
	planned := make([]string, 0, len(plan.linked)+1)
	for _, target := range plan.linked {
		planned = append(planned, target.directory.path)
	}
	if plan.whole {
		planned = append(planned, plan.mainDirectory.path)
	}
	return &removeMutationState{
		planned: planned,
		removed: make(map[string]bool, len(planned)),
	}
}

func (state *removeMutationState) markRemoved(path string) {
	state.removed[removePathKey(path)] = true
}

func (state *removeMutationState) observePath(
	filesystem removeFilesystem,
	path string,
) error {
	missing, err := removePathMissing(filesystem, path)
	if err != nil {
		return fmt.Errorf("observe exact target state at %q: %w", path, err)
	}
	if missing {
		state.markRemoved(path)
	}
	return nil
}

func (state *removeMutationState) failure(operation string, err error) error {
	removed := make([]string, 0, len(state.planned))
	remaining := make([]string, 0, len(state.planned))
	for _, path := range state.planned {
		if state.removed[removePathKey(path)] {
			removed = append(removed, removeOutputPath(path))
		} else {
			remaining = append(remaining, removeOutputPath(path))
		}
	}
	return fmt.Errorf(
		"removal incomplete; partial state: removed %s; remaining %s; %s: %w",
		removePathList(removed),
		removePathList(remaining),
		operation,
		err,
	)
}

func removePathList(paths []string) string {
	if len(paths) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(paths, ", ") + "]"
}

func removeWriteWarnings(writer io.Writer, warnings []local.Warning) error {
	if len(warnings) == 0 {
		return nil
	}
	var output bytes.Buffer
	for _, warning := range warnings {
		path := removeOutputPath(warning.Path)
		if warning.Err == nil {
			_, _ = fmt.Fprintf(
				&output,
				"gh-qw: warning: %s warning at %q during %s\n",
				warning.Kind,
				path,
				warning.Operation,
			)
			continue
		}
		_, _ = fmt.Fprintf(
			&output,
			"gh-qw: warning: %s warning at %q during %s: %v\n",
			warning.Kind,
			path,
			warning.Operation,
			warning.Err,
		)
	}
	return removeWriteAll(writer, output.Bytes())
}

func removeWritePlan(writer io.Writer, plan removePlan) error {
	var output bytes.Buffer
	_, _ = fmt.Fprintln(&output, "Removal plan:")
	for _, target := range plan.linked {
		_, _ = fmt.Fprintf(
			&output,
			"  linked worktree: %s\n",
			removeOutputPath(target.directory.path),
		)
	}
	if plan.whole {
		_, _ = fmt.Fprintf(
			&output,
			"  main repository: %s\n",
			removeOutputPath(plan.mainDirectory.path),
		)
	}
	return removeWriteAll(writer, output.Bytes())
}

func removeWriteProgress(writer io.Writer, operation, path string) error {
	return removeWriteAll(
		writer,
		[]byte(fmt.Sprintf("%s %s\n", operation, removeOutputPath(path))),
	)
}

func removeWriteAll(writer io.Writer, data []byte) error {
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

func removeConfirm(
	ctx context.Context,
	output io.Writer,
	prompt string,
	openTerminal func() (io.ReadCloser, error),
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	terminal, err := openTerminal()
	if err != nil {
		return false, fmt.Errorf("open controlling terminal: %w", err)
	}
	defer terminal.Close()

	if err := removeWriteAll(output, []byte(prompt)); err != nil {
		return false, err
	}
	response, err := bufio.NewReader(terminal).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read controlling terminal: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	response = strings.TrimSpace(response)
	return strings.EqualFold(response, "y") ||
		strings.EqualFold(response, "yes"), nil
}

func removeOpenControllingTerminal() (io.ReadCloser, error) {
	path := "/dev/tty"
	if runtime.GOOS == "windows" {
		path = "CONIN$"
	}
	return os.Open(path)
}

func removePathStrictlyWithin(boundary, candidate string) bool {
	if boundary == "" || candidate == "" {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(boundary), filepath.Clean(candidate))
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func removePathsOverlap(first, second string) bool {
	return removeSamePath(first, second) ||
		removePathStrictlyWithin(first, second) ||
		removePathStrictlyWithin(second, first)
}

func removeSamePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if first == second {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(first, second)
}

func removePathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func removeOutputPath(path string) string {
	return local.NormalizePathForOutput(filepath.Clean(path))
}

func removeReasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func removeNewUsageError(err error) error {
	if err == nil {
		return nil
	}
	var usage *removeUsageError
	if errors.As(err, &usage) {
		return err
	}
	return &removeUsageError{err: err}
}

func removeWrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
