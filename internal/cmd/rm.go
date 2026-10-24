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

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/removal"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

var (
	// ErrRemoveDeclined reports a safe negative response to the removal prompt.
	ErrRemoveDeclined = errors.New("removal declined")
	// ErrRemoveSafety reports a runtime safety refusal.
	ErrRemoveSafety = removal.ErrSafety
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
	Forget       func(string, string) error
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

type removeNode = removal.Node

type removeLinkedTarget = removal.LinkedObservation

type removePlan struct {
	removal.RepositoryObservation
	selection removeSelection

	whole bool

	mainWorktree local.Worktree
	mainCleanup  []removeNode

	worktreeBaseDir *removeNode
	worktreeCleanup []removeNode

	linked []removeLinkedTarget
}

type removeUsageError = repositoryUsageError

// NewRemoveCommand returns the command that safely removes one managed linked
// worktree or a complete main repository and all of its linked worktrees.
func NewRemoveCommand(dependencies RemoveDependencies) *cobra.Command {
	commandRuntime := removePrepareRuntime(dependencies)

	var dryRun bool
	command := &cobra.Command{
		Use: "rm [--dry-run] " +
			"<repo>|<owner>/<repo>|<host>/<owner>/<repo>[@<branch>]",
		Short:         "Remove a managed repository or linked worktree",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return removeNewUsageError(err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			selection, err := removeParseSelection(command.Context(), args[0])
			if err != nil {
				if errors.Is(err, local.ErrSelector) || errors.Is(err, local.ErrInvalidBranch) {
					return removeNewUsageError(err)
				}
				return err
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
				removed, removeErr := removeExecuteWhole(command.Context(), commandRuntime, revalidated)
				var forgetErr error
				if removed && dependencies.Forget != nil {
					if err := dependencies.Forget(revalidated.Repository.Identity, revalidated.Repository.Path); err != nil {
						forgetErr = fmt.Errorf("repository was removed at %q but its registration could not be forgotten: %w", revalidated.Repository.Path, err)
					}
				}
				return errors.Join(removeErr, forgetErr)
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

func removeParseSelection(ctx context.Context, argument string) (removeSelection, error) {
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
		if err := local.ValidateSlot(ctx, selection.slot); err != nil {
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
		plan, err = removePreflightLinked(ctx, commandRuntime, plan)
	} else {
		plan, err = removePreflightWhole(ctx, commandRuntime, plan)
	}
	if err != nil {
		return removePlan{}, err
	}
	if err := removeRejectNestedRegistrations(plan, discovery.Repositories); err != nil {
		return removePlan{}, err
	}
	return plan, nil
}

func removeRejectNestedRegistrations(plan removePlan, repositories []local.Repository) error {
	targets := make([]string, 0, len(plan.linked)+1)
	for _, linked := range plan.linked {
		targets = append(targets, linked.Directory.Path)
	}
	if plan.whole {
		targets = append(targets, plan.MainDirectory.Path)
	}
	return removeRejectNestedRegisteredPaths(plan.Repository.Identity, targets, repositories)
}

func removeRejectNestedRegisteredPaths(
	selectedIdentity string,
	targets []string,
	repositories []local.Repository,
) error {
	physicalTargets := make([]string, len(targets))
	for index, target := range targets {
		physical, err := physicalRegisteredLocation(target)
		if err != nil {
			return err
		}
		physicalTargets[index] = physical
	}
	for _, repository := range repositories {
		if repository.Identity == selectedIdentity {
			continue
		}
		physical, err := physicalRegisteredLocation(repository.Path)
		if err != nil {
			return err
		}
		for index, target := range physicalTargets {
			within, withinErr := physicalRegisteredLocationStrictlyWithin(target, physical)
			if withinErr != nil {
				return withinErr
			}
			if within {
				return fmt.Errorf(
					"%w: registered repository %q at %q is nested inside removal target %q",
					ErrRemoveSafety,
					repository.Identity,
					repository.Path,
					targets[index],
				)
			}
		}
	}
	return nil
}

func removePrepareCommon(ctx context.Context, commandRuntime removeRuntime, selection removeSelection, roots rootpkg.Result, repository local.Repository) (removePlan, error) {
	observation, err := commandRuntime.observer().ObserveRepository(ctx, roots, repository)
	return removePlan{RepositoryObservation: observation, selection: selection}, err
}

func removePreflightLinked(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) (removePlan, error) {
	worktree, err := commandRuntime.observer().ResolveWorktree(
		ctx,
		plan.Repository,
		plan.Roots.WorktreeRoot,
		plan.selection.slot,
	)
	if err != nil {
		return removePlan{}, fmt.Errorf(
			"resolve managed worktree %q for %q: %w",
			plan.selection.slot,
			plan.Repository.Identity,
			err,
		)
	}
	target, base, err := removePrepareResolvedLinkedTarget(
		ctx,
		commandRuntime.observer(),
		plan.RepositoryObservation,
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
		plan.Repository,
		plan.Roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return removePlan{}, fmt.Errorf(
			"enumerate worktrees for %q: %w",
			plan.Repository.Identity,
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
			plan.Repository.Identity,
		)
	}
	if err := removeValidateMainRecord(ctx, plan.Repository, mainRecords[0]); err != nil {
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
		slotKey := physicalpath.Key(filepath.FromSlash(worktree.Slot))
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

		pathKey := physicalpath.Key(worktree.Path)
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
			commandRuntime.observer(),
			plan.RepositoryObservation,
			worktree,
		)
		if err != nil {
			return removePlan{}, err
		}
		if plan.worktreeBaseDir == nil {
			plan.worktreeBaseDir = &base
		} else if err := removal.CompareNode(commandRuntime.fs.observations(),
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
		plan.MainDirectory.Path,
		"main repository",
	); err != nil {
		return removePlan{}, err
	}

	plan.mainCleanup, err = removal.CaptureCleanupChain(commandRuntime.fs.observations(),
		plan.RepositoryRoot.Path,
		filepath.Dir(plan.MainDirectory.Path),
	)
	if err != nil {
		return removePlan{}, fmt.Errorf("plan main parent cleanup: %w", err)
	}

	if plan.worktreeBaseDir == nil {
		base, exists, captureErr := removal.CaptureOptionalDirectory(commandRuntime.fs.observations(),
			plan.WorktreeRoot.Path,
			plan.WorktreeBase,
		)
		if captureErr != nil {
			return removePlan{}, fmt.Errorf("inspect worktree base: %w", captureErr)
		}
		if exists {
			plan.worktreeBaseDir = &base
		}
	}
	if plan.worktreeBaseDir != nil {
		plan.worktreeCleanup, err = removal.CaptureCleanupChain(commandRuntime.fs.observations(),
			plan.WorktreeRoot.Path,
			plan.WorktreeBase,
		)
		if err != nil {
			return removePlan{}, fmt.Errorf("plan worktree-base cleanup: %w", err)
		}
	}

	plan.whole = true
	return plan, nil
}

func removeValidateMainRecord(
	ctx context.Context,
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
	case !physicalpath.SamePath(worktree.Path, repository.Path):
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
	if err := local.ValidateBranch(ctx, worktree.Branch); err != nil {
		return fmt.Errorf("%w: validate main worktree branch: %w", ErrRemoveSafety, err)
	}
	return nil
}

func removeValidateLinkedRecord(
	ctx context.Context,
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
	if err := local.ValidateSlot(ctx, worktree.Slot); err != nil {
		return fmt.Errorf("%w: validate linked slot: %w", ErrRemoveSafety, err)
	}
	if err := local.ValidateBranch(ctx, worktree.Branch); err != nil {
		return fmt.Errorf("%w: validate linked branch: %w", ErrRemoveSafety, err)
	}
	return nil
}

func removeRequireClean(
	ctx context.Context,
	git local.GitOutputter,
	path, description string,
) error {
	dirty, err := removal.InspectDirty(ctx, git, path, description)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf(
			"%w: %s %q has tracked or untracked changes",
			ErrRemoveSafety,
			description,
			removeOutputPath(path),
		)
	}
	return nil
}

func removeComparePlans(
	filesystem removeFilesystem,
	before, after removePlan,
) error {
	if before.selection != after.selection || before.whole != after.whole {
		return fmt.Errorf("%w: selected repository or removal mode changed", ErrRemoveSafety)
	}
	if err := removal.CompareRepository(filesystem.observations(), before.RepositoryObservation, after.RepositoryObservation); err != nil {
		return err
	}
	if (before.worktreeBaseDir == nil) != (after.worktreeBaseDir == nil) {
		return fmt.Errorf("%w: worktree base changed", ErrRemoveSafety)
	}
	if before.worktreeBaseDir != nil {
		if err := removal.CompareNode(filesystem.observations(),
			*before.worktreeBaseDir,
			*after.worktreeBaseDir,
			"worktree base",
		); err != nil {
			return err
		}
	}
	if before.whole && !removal.SameWorktree(before.mainWorktree, after.mainWorktree) {
		return fmt.Errorf("%w: main worktree registration changed", ErrRemoveSafety)
	}
	if err := removal.CompareNodeSlices(filesystem.observations(),
		before.mainCleanup,
		after.mainCleanup,
		"main cleanup",
	); err != nil {
		return err
	}
	if err := removal.CompareNodeSlices(filesystem.observations(),
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
		if err := removal.CompareLinked(filesystem.observations(),
			before.linked[index],
			after.linked[index],
		); err != nil {
			return err
		}
	}
	return nil
}

func removeExecuteLinked(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) error {
	return removeExecuteLinkedTarget(ctx, commandRuntime, plan, plan.linked[0], removeNewMutationState(plan))
}

func removeExecuteLinkedTarget(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
	target removeLinkedTarget,
	state *removeMutationState,
) error {
	result := commandRuntime.fs.executor(commandRuntime.git).RemoveLinked(
		ctx, plan.Repository.Path,
		gitcmd.WorktreeRemoveOptions{Path: target.Directory.Path},
		*plan.worktreeBaseDir, target.Cleanup,
	)
	if result.PathRemoved {
		state.markRemoved(result.Path)
	}
	if result.Err != nil {
		operation := fmt.Sprintf("remove linked worktree %q", removeOutputPath(result.Path))
		switch result.FailedStep {
		case removal.VerifyPath:
			operation = "verify linked worktree removal"
		case removal.ReadRegistrations, removal.VerifyRegistration:
			operation = "verify linked worktree registration removal"
		case removal.CleanupParents:
			operation = "cleanup linked worktree parents"
		}
		return state.failure(operation, result.Err)
	}
	if err := removeWriteProgress(commandRuntime.stderr, "removed linked worktree", result.Path); err != nil {
		return state.failure("write linked removal progress", err)
	}
	return nil
}

func removeExecuteWhole(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) (bool, error) {
	state := removeNewMutationState(plan)

	for _, target := range plan.linked {
		if err := removeRevalidateLinkedForMutation(
			ctx,
			commandRuntime,
			plan,
			target,
		); err != nil {
			return state.isRemoved(plan.MainDirectory.Path), state.failure(
				"revalidate linked worktree "+target.Worktree.Slot,
				err,
			)
		}

		if err := removeExecuteLinkedTarget(ctx, commandRuntime, plan, target, state); err != nil {
			return state.isRemoved(plan.MainDirectory.Path), err
		}
	}

	if err := removeRevalidateMainForDeletion(ctx, commandRuntime, plan); err != nil {
		return state.isRemoved(plan.MainDirectory.Path), state.failure("revalidate main repository before exact deletion", err)
	}
	if err := ctx.Err(); err != nil {
		return state.isRemoved(plan.MainDirectory.Path), state.failure("remove exact main repository directory", err)
	}
	if err := commandRuntime.fs.removeAll(plan.MainDirectory.Path); err != nil {
		observationErr := state.observePath(commandRuntime.fs, plan.MainDirectory.Path)
		return state.isRemoved(plan.MainDirectory.Path), state.failure(
			fmt.Sprintf(
				"remove exact main repository directory %q",
				removeOutputPath(plan.MainDirectory.Path),
			),
			errors.Join(err, observationErr),
		)
	}
	missing, err := removal.PathMissing(commandRuntime.fs.observations(), plan.MainDirectory.Path)
	if err != nil {
		return state.isRemoved(plan.MainDirectory.Path), state.failure("verify main repository deletion", err)
	}
	if !missing {
		return state.isRemoved(plan.MainDirectory.Path), state.failure(
			"verify main repository deletion",
			fmt.Errorf(
				"exact main repository path %q still exists",
				removeOutputPath(plan.MainDirectory.Path),
			),
		)
	}
	state.markRemoved(plan.MainDirectory.Path)

	if err := commandRuntime.fs.executor(commandRuntime.git).CleanupEmptyChain(
		ctx, plan.RepositoryRoot,
		plan.mainCleanup,
	); err != nil {
		return true, state.failure("cleanup empty repository owner and host directories", err)
	}
	if plan.worktreeBaseDir != nil {
		if err := commandRuntime.fs.executor(commandRuntime.git).CleanupEmptyChain(
			ctx, plan.WorktreeRoot,
			plan.worktreeCleanup,
		); err != nil {
			return true, state.failure("cleanup empty worktree-base directories", err)
		}
	}
	if err := removeWriteProgress(
		commandRuntime.stderr,
		"removed main repository",
		plan.MainDirectory.Path,
	); err != nil {
		return true, state.failure("write main removal progress", err)
	}
	return true, nil
}

func removeRevalidateLinkedForMutation(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
	planned removeLinkedTarget,
) error {
	if err := commandRuntime.observer().RevalidateRepository(ctx, plan.RepositoryObservation, plan.worktreeBaseDir); err != nil {
		return err
	}

	worktree, err := commandRuntime.observer().ResolveWorktree(
		ctx,
		plan.Repository,
		plan.Roots.WorktreeRoot,
		planned.Worktree.Slot,
	)
	if err != nil {
		return err
	}
	current, base, err := removePrepareResolvedLinkedTarget(
		ctx,
		commandRuntime.observer(),
		plan.RepositoryObservation,
		worktree,
	)
	if err != nil {
		return err
	}
	if err := removal.CompareNode(commandRuntime.fs.observations(),
		*plan.worktreeBaseDir,
		base,
		"worktree base",
	); err != nil {
		return err
	}
	return removal.CompareLinked(commandRuntime.fs.observations(), planned, current)
}

func removeRevalidateMainForDeletion(
	ctx context.Context,
	commandRuntime removeRuntime,
	plan removePlan,
) error {
	if err := commandRuntime.observer().RevalidateRepository(ctx, plan.RepositoryObservation, plan.worktreeBaseDir); err != nil {
		return err
	}

	worktrees, err := commandRuntime.enumerate(
		ctx,
		plan.Repository,
		plan.Roots.WorktreeRoot,
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
	if err := removeValidateMainRecord(ctx, plan.Repository, worktrees[0]); err != nil {
		return err
	}
	if !removal.SameWorktree(plan.mainWorktree, worktrees[0]) {
		return fmt.Errorf("%w: main worktree registration changed", ErrRemoveSafety)
	}
	if err := removeRequireClean(
		ctx,
		commandRuntime.git,
		plan.MainDirectory.Path,
		"main repository",
	); err != nil {
		return err
	}

	for _, node := range append(
		append([]removeNode(nil), plan.mainCleanup...),
		plan.worktreeCleanup...,
	) {
		if err := removal.RevalidateNode(commandRuntime.fs.observations(), node, "cleanup directory"); err != nil {
			return err
		}
	}
	if err := removal.RevalidateNode(commandRuntime.fs.observations(),
		plan.MainDirectory,
		"main repository",
	); err != nil {
		return err
	}
	return removal.RevalidateNode(commandRuntime.fs.observations(), plan.MainGit, "main .git")
}

type removeMutationState struct {
	planned []string
	removed map[string]bool
}

func removeNewMutationState(plan removePlan) *removeMutationState {
	planned := make([]string, 0, len(plan.linked)+1)
	for _, target := range plan.linked {
		planned = append(planned, target.Directory.Path)
	}
	if plan.whole {
		planned = append(planned, plan.MainDirectory.Path)
	}
	return &removeMutationState{
		planned: planned,
		removed: make(map[string]bool, len(planned)),
	}
}

func (state *removeMutationState) markRemoved(path string) {
	state.removed[physicalpath.Key(path)] = true
}

func (state *removeMutationState) isRemoved(path string) bool {
	return state.removed[physicalpath.Key(path)]
}

func (state *removeMutationState) observePath(
	filesystem removeFilesystem,
	path string,
) error {
	missing, err := removal.PathMissing(filesystem.observations(), path)
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
		if state.removed[physicalpath.Key(path)] {
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
	return commandWriteAll(writer, output.Bytes())
}

func removeWritePlan(writer io.Writer, plan removePlan) error {
	var output bytes.Buffer
	_, _ = fmt.Fprintln(&output, "Removal plan:")
	for _, target := range plan.linked {
		_, _ = fmt.Fprintf(
			&output,
			"  linked worktree: %s\n",
			removeOutputPath(target.Directory.Path),
		)
	}
	if plan.whole {
		_, _ = fmt.Fprintf(
			&output,
			"  main repository: %s\n",
			removeOutputPath(plan.MainDirectory.Path),
		)
	}
	return commandWriteAll(writer, output.Bytes())
}

func removeWriteProgress(writer io.Writer, operation, path string) error {
	return commandWriteAll(
		writer,
		[]byte(fmt.Sprintf("%s %s\n", operation, removeOutputPath(path))),
	)
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

	if err := commandWriteAll(output, []byte(prompt)); err != nil {
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
	return physicalpath.StrictlyWithin(boundary, candidate)
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

func (filesystem removeFilesystem) observations() removal.Filesystem {
	return removal.Filesystem{Lstat: filesystem.lstat, EvalSymlinks: filesystem.evalSymlinks, SameFile: filesystem.sameFile}
}

func (filesystem removeFilesystem) executor(git removal.LinkedGit) removal.Executor {
	return removal.Executor{Git: git, Filesystem: filesystem.observations(), ReadDir: filesystem.readDir, Remove: filesystem.remove}
}

func (commandRuntime removeRuntime) observer() removal.Observer {
	return removal.Observer{Git: commandRuntime.git, Filesystem: commandRuntime.fs.observations(), LocalFilesystem: commandRuntime.filesystem, ResolveManaged: commandRuntime.resolveManaged, ValidateAssociation: commandRuntime.validateAssociation}
}

func removeObserveEligibleLinked(ctx context.Context, observer removal.Observer, observation removal.RepositoryObservation, worktree local.Worktree) (removeLinkedTarget, removeNode, error) {
	if err := removeValidateLinkedRecord(ctx, observation.Repository, worktree); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}
	return observer.ObserveListedLinked(ctx, observation, worktree)
}

func removePrepareLinkedTarget(ctx context.Context, observer removal.Observer, observation removal.RepositoryObservation, worktree local.Worktree) (removeLinkedTarget, removeNode, error) {
	target, base, err := removeObserveEligibleLinked(ctx, observer, observation, worktree)
	if err != nil {
		return target, base, err
	}
	if err := removeRequireClean(ctx, observer.Git, target.Directory.Path, "linked worktree "+worktree.Slot); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}
	return target, base, nil
}

func removePrepareResolvedLinkedTarget(ctx context.Context, observer removal.Observer, observation removal.RepositoryObservation, worktree local.Worktree) (removeLinkedTarget, removeNode, error) {
	if err := removeValidateLinkedRecord(ctx, observation.Repository, worktree); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}
	target, base, err := observer.ObserveResolvedLinked(ctx, observation, worktree)
	if err != nil {
		return target, base, err
	}
	if err := removeRequireClean(ctx, observer.Git, target.Directory.Path, "linked worktree "+worktree.Slot); err != nil {
		return removeLinkedTarget{}, removeNode{}, err
	}
	return target, base, nil
}
