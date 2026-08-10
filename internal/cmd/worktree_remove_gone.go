package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

type worktreeRemoveGoneEntry struct {
	worktree local.Worktree
	upstream string
	remove   bool
	reason   string
	dirty    bool
	target   removeLinkedTarget
}

type worktreeRemoveGonePlan struct {
	roots      rootpkg.Result
	repository local.Repository
	common     removePlan
	current    string
	entries    []worktreeRemoveGoneEntry
}

type worktreeRemoveGoneSharedError struct {
	err error
}

func (err *worktreeRemoveGoneSharedError) Error() string {
	if err == nil || err.err == nil {
		return "gone-worktree shared state changed"
	}
	return err.err.Error()
}

func (err *worktreeRemoveGoneSharedError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func worktreeRemoveGoneShared(err error) error {
	if err == nil {
		return nil
	}
	return &worktreeRemoveGoneSharedError{err: err}
}

func worktreeRemoveGoneRun(
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
	plan, err := worktreeRemoveGonePreflightWithRoots(
		ctx,
		commandRuntime,
		request,
		roots,
		true,
	)
	if err != nil {
		return err
	}

	if err := worktreeRemoveGoneWritePlan(commandRuntime.stderr, plan); err != nil {
		return fmt.Errorf("write gone-worktree removal plan: %w", err)
	}
	if len(plan.entries) == 0 {
		return nil
	}

	hasKept := worktreeRemoveGoneHasKept(plan)
	if request.dryRun {
		if hasKept {
			return newSilentStatusError(1)
		}
		return nil
	}
	if !worktreeRemoveGoneHasRemoval(plan) {
		return newSilentStatusError(1)
	}

	if !request.yes {
		confirmed, confirmErr := commandRuntime.prompt(
			ctx,
			commandRuntime.stderr,
			"Proceed with gone-worktree removal? [y/N] ",
		)
		if confirmErr != nil {
			return fmt.Errorf("confirm gone-worktree removal: %w", confirmErr)
		}
		if !confirmed {
			if writeErr := removeWriteAll(commandRuntime.stderr, []byte("removal declined\n")); writeErr != nil {
				return fmt.Errorf("write removal-declined diagnostic: %w", writeErr)
			}
			return newSilentStatusError(1)
		}
	}

	revalidated, err := worktreeRemoveGonePreflight(ctx, commandRuntime, request, false)
	if err != nil {
		return fmt.Errorf("revalidate gone-worktree removal plan: %w", err)
	}
	if err := worktreeRemoveGoneComparePlans(commandRuntime.fs, plan, revalidated); err != nil {
		return fmt.Errorf("revalidate gone-worktree removal plan: %w", err)
	}

	failed, err := worktreeRemoveGoneExecute(
		ctx,
		commandRuntime,
		revalidated,
		request,
		herdrEnabled,
	)
	if err != nil {
		return err
	}
	if hasKept || failed {
		return newSilentStatusError(1)
	}
	return nil
}

func worktreeRemoveGonePreflight(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	request worktreeRemoveRequest,
	emitWarnings bool,
) (worktreeRemoveGonePlan, error) {
	roots, err := commandRuntime.resolver.Resolve()
	if err != nil {
		return worktreeRemoveGonePlan{}, fmt.Errorf("resolve roots: %w", err)
	}

	return worktreeRemoveGonePreflightWithRoots(
		ctx,
		commandRuntime,
		request,
		roots,
		emitWarnings,
	)
}

func worktreeRemoveGonePreflightWithRoots(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	request worktreeRemoveRequest,
	roots rootpkg.Result,
	emitWarnings bool,
) (worktreeRemoveGonePlan, error) {
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
		warningErr = worktreeRemoveWriteWarnings(commandRuntime.stderr, discovery.Warnings)
	}
	if discoveryErr != nil || warningErr != nil {
		return worktreeRemoveGonePlan{}, errors.Join(
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
		return worktreeRemoveGonePlan{}, err
	}
	return worktreeRemoveGoneBuildPlan(ctx, commandRuntime, roots, repository, request.force)
}

func worktreeRemoveGoneBuildPlan(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	roots rootpkg.Result,
	repository local.Repository,
	force bool,
) (worktreeRemoveGonePlan, error) {
	removeRuntime := worktreeRemoveGoneSafetyRuntime(commandRuntime)
	common, err := removePrepareCommon(
		ctx,
		removeRuntime,
		removeSelection{raw: repository.Identity, selector: repository.Identity},
		roots,
		repository,
	)
	if err != nil {
		return worktreeRemoveGonePlan{}, err
	}

	worktrees, err := commandRuntime.enumerate(
		ctx,
		repository,
		roots.WorktreeRoot,
		local.WorktreeOptions{
			Lister:     commandRuntime.git,
			Filesystem: commandRuntime.filesystem,
		},
	)
	if err != nil {
		return worktreeRemoveGonePlan{}, fmt.Errorf(
			"enumerate worktrees for %q: %w",
			repository.Identity,
			err,
		)
	}
	attachedBranches := make(map[string]struct{}, len(worktrees))
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Detached || worktree.Branch == "" {
			continue
		}
		attachedBranches[worktree.Branch] = struct{}{}
	}

	upstreams, err := commandRuntime.git.BranchUpstreams(ctx, repository.Path)
	if err != nil {
		return worktreeRemoveGonePlan{}, fmt.Errorf(
			"inspect branch upstreams for %q: %w",
			repository.Identity,
			err,
		)
	}
	upstreamByBranch := make(map[string]string, len(upstreams))
	for _, upstream := range upstreams {
		if _, attached := attachedBranches[upstream.Branch]; !attached {
			continue
		}
		if err := local.ValidateBranch(upstream.Branch); err != nil {
			return worktreeRemoveGonePlan{}, fmt.Errorf(
				"inspect branch upstreams for %q: invalid branch %q: %w",
				repository.Identity,
				upstream.Branch,
				err,
			)
		}
		if _, exists := upstreamByBranch[upstream.Branch]; exists {
			return worktreeRemoveGonePlan{}, fmt.Errorf(
				"inspect branch upstreams for %q: duplicate branch %q",
				repository.Identity,
				upstream.Branch,
			)
		}
		upstreamByBranch[upstream.Branch] = upstream.Upstream
	}

	plan := worktreeRemoveGonePlan{
		roots:      roots,
		repository: repository,
		common:     common,
	}
	type goneCandidate struct {
		worktree local.Worktree
		upstream string
	}
	var candidates []goneCandidate
	refExists := make(map[string]bool)
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Detached || worktree.Branch == "" {
			continue
		}
		upstream := upstreamByBranch[worktree.Branch]
		if upstream == "" {
			continue
		}
		exists, inspected := refExists[upstream]
		if !inspected {
			exists, err = commandRuntime.git.RefExists(ctx, repository.Path, upstream)
			if err != nil {
				return worktreeRemoveGonePlan{}, fmt.Errorf(
					"verify upstream ref %q for branch %q: %w",
					upstream,
					worktree.Branch,
					err,
				)
			}
			refExists[upstream] = exists
		}
		if exists {
			continue
		}
		candidates = append(candidates, goneCandidate{worktree: worktree, upstream: upstream})
	}
	if len(candidates) == 0 {
		return plan, nil
	}

	current, err := worktreeRemoveGoneCurrentPath(commandRuntime)
	if err != nil {
		return worktreeRemoveGonePlan{}, err
	}
	plan.current = current
	for _, candidate := range candidates {
		worktree := candidate.worktree
		upstream := candidate.upstream

		entry := worktreeRemoveGoneEntry{
			worktree: worktree,
			upstream: upstream,
		}
		if worktree.Locked {
			entry.reason = "locked" + removeReasonSuffix(worktree.LockedReason)
			plan.entries = append(plan.entries, entry)
			continue
		}
		if worktree.Prunable {
			entry.reason = "prunable" + removeReasonSuffix(worktree.PrunableReason)
			plan.entries = append(plan.entries, entry)
			continue
		}
		if worktreeRemoveGonePathContains(worktree.Path, current) {
			entry.reason = "current shell is inside this worktree"
			plan.entries = append(plan.entries, entry)
			continue
		}
		if contained, found := worktreeRemoveGoneContainedWorktree(worktrees, worktree); found {
			entry.reason = fmt.Sprintf(
				"contains registered worktree at %q",
				removeOutputPath(contained.Path),
			)
			plan.entries = append(plan.entries, entry)
			continue
		}

		target, base, targetErr := removePrepareLinkedTargetWithCleanliness(
			ctx,
			removeRuntime,
			common,
			worktree,
			false,
		)
		if targetErr != nil {
			if errors.Is(targetErr, context.Canceled) ||
				errors.Is(targetErr, context.DeadlineExceeded) {
				return worktreeRemoveGonePlan{}, targetErr
			}
			entry.reason = targetErr.Error()
			plan.entries = append(plan.entries, entry)
			continue
		}
		dirty, dirtyErr := removeInspectDirty(
			ctx,
			removeRuntime.git,
			target.directory.path,
			"linked worktree "+worktree.Slot,
		)
		if dirtyErr != nil {
			if errors.Is(dirtyErr, context.Canceled) ||
				errors.Is(dirtyErr, context.DeadlineExceeded) {
				return worktreeRemoveGonePlan{}, dirtyErr
			}
			entry.reason = dirtyErr.Error()
			plan.entries = append(plan.entries, entry)
			continue
		}
		entry.dirty = dirty
		if dirty && !force {
			entry.reason = "dirty; use --force to allow Git's single force level"
			plan.entries = append(plan.entries, entry)
			continue
		}

		if common.worktreeBaseDir == nil {
			baseCopy := base
			common.worktreeBaseDir = &baseCopy
		} else if err := removeCompareNode(
			commandRuntime.fs,
			*common.worktreeBaseDir,
			base,
			"worktree base",
		); err != nil {
			return worktreeRemoveGonePlan{}, err
		}
		entry.remove = true
		entry.target = target
		common.linked = append(common.linked, target)
		plan.entries = append(plan.entries, entry)
	}

	sort.Slice(plan.entries, func(left, right int) bool {
		if plan.entries[left].worktree.Slot != plan.entries[right].worktree.Slot {
			return plan.entries[left].worktree.Slot < plan.entries[right].worktree.Slot
		}
		return removeOutputPath(plan.entries[left].worktree.Path) <
			removeOutputPath(plan.entries[right].worktree.Path)
	})
	plan.common = common
	return plan, nil
}

func worktreeRemoveGoneSafetyRuntime(commandRuntime worktreeRemoveRuntime) removeRuntime {
	return removeRuntime{
		resolver:            commandRuntime.resolver,
		discover:            commandRuntime.discover,
		resolveManaged:      commandRuntime.resolveManaged,
		enumerate:           commandRuntime.enumerate,
		validateAssociation: commandRuntime.validateAssociation,
		git:                 commandRuntime.git,
		filesystem:          commandRuntime.filesystem,
		fs:                  commandRuntime.fs,
		prompt:              commandRuntime.prompt,
		herdr:               commandRuntime.herdr,
		lookupEnv:           commandRuntime.lookupEnv,
		stdout:              commandRuntime.stdout,
		stderr:              commandRuntime.stderr,
	}
}

func worktreeRemoveGoneCurrentPath(commandRuntime worktreeRemoveRuntime) (string, error) {
	current, err := commandRuntime.getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	if !filepath.IsAbs(current) {
		current, err = filepath.Abs(current)
		if err != nil {
			return "", fmt.Errorf("make current directory absolute: %w", err)
		}
	}
	current, err = commandRuntime.fs.evalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	if !filepath.IsAbs(current) {
		return "", fmt.Errorf("resolved current directory %q is not absolute", current)
	}
	return filepath.Clean(current), nil
}

func worktreeRemoveGonePathContains(directory, path string) bool {
	return removeSamePath(directory, path) || removePathStrictlyWithin(directory, path)
}

func worktreeRemoveGoneContainedWorktree(
	worktrees []local.Worktree,
	parent local.Worktree,
) (local.Worktree, bool) {
	var contained local.Worktree
	found := false
	for _, worktree := range worktrees {
		if !removePathStrictlyWithin(parent.Path, worktree.Path) {
			continue
		}
		if !found || removePathKey(worktree.Path) < removePathKey(contained.Path) ||
			(removePathKey(worktree.Path) == removePathKey(contained.Path) &&
				worktree.Slot < contained.Slot) {
			contained = worktree
			found = true
		}
	}
	return contained, found
}

func worktreeRemoveGoneHasKept(plan worktreeRemoveGonePlan) bool {
	for _, entry := range plan.entries {
		if !entry.remove {
			return true
		}
	}
	return false
}

func worktreeRemoveGoneHasRemoval(plan worktreeRemoveGonePlan) bool {
	for _, entry := range plan.entries {
		if entry.remove {
			return true
		}
	}
	return false
}

func worktreeRemoveGoneWritePlan(writer io.Writer, plan worktreeRemoveGonePlan) error {
	var output bytes.Buffer
	if len(plan.entries) == 0 {
		_, _ = fmt.Fprintf(
			&output,
			"No linked worktrees with missing upstream refs for %s.\n",
			plan.repository.Identity,
		)
		return removeWriteAll(writer, output.Bytes())
	}

	_, _ = fmt.Fprintf(&output, "Gone-worktree removal plan for %s:\n", plan.repository.Identity)
	for _, entry := range plan.entries {
		action := "remove"
		if !entry.remove {
			action = "keep"
		}
		_, _ = fmt.Fprintf(
			&output,
			"  %s slot=%q branch=%q upstream=%q path=%q",
			action,
			entry.worktree.Slot,
			entry.worktree.Branch,
			entry.upstream,
			removeOutputPath(entry.worktree.Path),
		)
		if entry.reason != "" {
			_, _ = fmt.Fprintf(&output, " reason=%q", entry.reason)
		}
		_, _ = fmt.Fprintln(&output)
	}
	return removeWriteAll(writer, output.Bytes())
}

func worktreeRemoveGoneComparePlans(
	filesystem removeFilesystem,
	before, after worktreeRemoveGonePlan,
) error {
	if before.roots.Herdr != after.roots.Herdr ||
		before.repository != after.repository ||
		!removeSamePath(before.current, after.current) {
		return fmt.Errorf("%w: repository, current directory, or configuration changed", ErrRemoveSafety)
	}
	if err := removeComparePlans(filesystem, before.common, after.common); err != nil {
		return err
	}
	if len(before.entries) != len(after.entries) {
		return fmt.Errorf("%w: gone-worktree candidate count changed", ErrRemoveSafety)
	}
	for index := range before.entries {
		left := before.entries[index]
		right := after.entries[index]
		if !removeSameWorktree(left.worktree, right.worktree) ||
			left.upstream != right.upstream ||
			left.remove != right.remove ||
			left.reason != right.reason ||
			left.dirty != right.dirty {
			return fmt.Errorf(
				"%w: gone-worktree plan changed for slot %q",
				ErrRemoveSafety,
				left.worktree.Slot,
			)
		}
	}
	return nil
}

func worktreeRemoveGoneExecute(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	plan worktreeRemoveGonePlan,
	request worktreeRemoveRequest,
	herdrEnabled bool,
) (bool, error) {
	failed := false
	for _, entry := range plan.entries {
		if !entry.remove {
			continue
		}
		if err := ctx.Err(); err != nil {
			return failed, err
		}
		var workspaceID string
		var workspaceFound bool
		var herdrFindErr error
		if herdrEnabled {
			workspaceID, workspaceFound, herdrFindErr = commandRuntime.herdr.FindWorkspaceForPath(
				ctx,
				plan.repository.Path,
				entry.target.directory.path,
			)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return failed, ctxErr
			}
			if errors.Is(herdrFindErr, context.Canceled) ||
				errors.Is(herdrFindErr, context.DeadlineExceeded) {
				return failed, herdrFindErr
			}
		}

		if err := worktreeRemoveGoneRevalidateShared(ctx, commandRuntime, plan); err != nil {
			return failed, fmt.Errorf("revalidate shared removal boundary: %w", err)
		}
		if err := worktreeRemoveGoneRevalidateEntry(
			ctx,
			commandRuntime,
			plan,
			entry,
			request.force,
		); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return failed, err
			}
			var sharedErr *worktreeRemoveGoneSharedError
			if errors.As(err, &sharedErr) {
				return failed, fmt.Errorf(
					"revalidate registered state for slot %q: %w",
					entry.worktree.Slot,
					err,
				)
			}
			failed = true
			if writeErr := worktreeRemoveGoneWriteFailure(
				commandRuntime.stderr,
				"kept",
				entry.worktree.Slot,
				err,
			); writeErr != nil {
				return failed, fmt.Errorf("write gone-worktree revalidation failure: %w", writeErr)
			}
			continue
		}

		removeErr := commandRuntime.git.WorktreeRemove(
			ctx,
			plan.repository.Path,
			gitcmd.WorktreeRemoveOptions{
				Path:  entry.target.directory.path,
				Force: request.force,
			},
		)
		if removeErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return failed, ctxErr
			}
			if errors.Is(removeErr, context.Canceled) ||
				errors.Is(removeErr, context.DeadlineExceeded) {
				return failed, removeErr
			}
			failed = true
			if writeErr := worktreeRemoveGoneWriteFailure(
				commandRuntime.stderr,
				"failed",
				entry.worktree.Slot,
				fmt.Errorf("git worktree remove: %w", removeErr),
			); writeErr != nil {
				return failed, fmt.Errorf("write gone-worktree Git failure: %w", writeErr)
			}
			continue
		}

		pathMissing, err := removePathMissing(commandRuntime.fs, entry.target.directory.path)
		if err != nil || !pathMissing {
			if err == nil {
				err = errors.New("Git returned success but the exact worktree path still exists")
			}
			failed = true
			if writeErr := worktreeRemoveGoneWriteFailure(
				commandRuntime.stderr,
				"failed",
				entry.worktree.Slot,
				fmt.Errorf("verify removed path: %w", err),
			); writeErr != nil {
				return failed, fmt.Errorf("write gone-worktree path verification failure: %w", writeErr)
			}
			continue
		}

		registered, err := commandRuntime.git.WorktreeList(ctx, plan.repository.Path)
		if err != nil {
			return failed, fmt.Errorf(
				"verify registration removal for slot %q: %w",
				entry.worktree.Slot,
				err,
			)
		}
		stillRegistered := false
		for _, worktree := range registered {
			if removeSamePath(worktree.Path, entry.target.directory.path) {
				stillRegistered = true
				break
			}
		}
		if stillRegistered {
			failed = true
			if writeErr := worktreeRemoveGoneWriteFailure(
				commandRuntime.stderr,
				"failed",
				entry.worktree.Slot,
				errors.New("Git still registers the removed worktree path"),
			); writeErr != nil {
				return failed, fmt.Errorf("write gone-worktree registration failure: %w", writeErr)
			}
			continue
		}

		var candidateErrors []error
		if herdrFindErr != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("find Herdr workspace: %w", herdrFindErr))
		}
		if err := removeCleanupEmptyChain(
			commandRuntime.fs,
			*plan.common.worktreeBaseDir,
			entry.target.cleanup,
		); err != nil {
			candidateErrors = append(candidateErrors, fmt.Errorf("cleanup empty parents: %w", err))
		}
		if herdrEnabled && herdrFindErr == nil && workspaceFound {
			if err := commandRuntime.herdr.CloseWorkspace(ctx, workspaceID); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return failed, ctxErr
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return failed, err
				}
				candidateErrors = append(candidateErrors, fmt.Errorf("close Herdr workspace: %w", err))
			}
		}
		if err := removeWriteProgress(
			commandRuntime.stderr,
			"removed worktree",
			entry.target.directory.path,
		); err != nil {
			return failed, fmt.Errorf("write gone-worktree removal progress: %w", err)
		}
		if joined := errors.Join(candidateErrors...); joined != nil {
			failed = true
			if writeErr := worktreeRemoveGoneWriteFailure(
				commandRuntime.stderr,
				"warning",
				entry.worktree.Slot,
				joined,
			); writeErr != nil {
				return failed, fmt.Errorf("write gone-worktree post-removal failure: %w", writeErr)
			}
		}
	}
	return failed, nil
}

func worktreeRemoveGoneRevalidateShared(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	plan worktreeRemoveGonePlan,
) error {
	for _, item := range []struct {
		name string
		node removeNode
	}{
		{"repository root", plan.common.repositoryRoot},
		{"main repository", plan.common.mainDirectory},
		{"main .git", plan.common.mainGit},
		{"worktree root", plan.common.worktreeRoot},
		{"worktree base", *plan.common.worktreeBaseDir},
	} {
		if err := removeRevalidateNode(commandRuntime.fs, item.node, item.name); err != nil {
			return err
		}
	}
	if err := removeValidateMainGitAssociation(
		ctx,
		commandRuntime.git,
		plan.common.mainDirectory.path,
		plan.common.mainGit.path,
	); err != nil {
		return fmt.Errorf("%w: %v", ErrRemoveSafety, err)
	}
	return nil
}

func worktreeRemoveGoneRevalidateEntry(
	ctx context.Context,
	commandRuntime worktreeRemoveRuntime,
	plan worktreeRemoveGonePlan,
	planned worktreeRemoveGoneEntry,
	force bool,
) error {
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
		return worktreeRemoveGoneShared(fmt.Errorf("enumerate registered worktrees: %w", err))
	}
	worktree, err := local.FindRegisteredLinkedWorktree(worktrees, planned.worktree.Slot)
	if err != nil {
		return err
	}
	if !removeSameWorktree(planned.worktree, worktree) {
		return fmt.Errorf("%w: slot, path, branch, HEAD, lock, or prune state changed", ErrRemoveSafety)
	}
	if contained, found := worktreeRemoveGoneContainedWorktree(worktrees, worktree); found {
		return fmt.Errorf(
			"%w: contains registered worktree at %q",
			ErrRemoveSafety,
			removeOutputPath(contained.Path),
		)
	}

	upstreams, err := commandRuntime.git.BranchUpstreams(ctx, plan.repository.Path)
	if err != nil {
		return worktreeRemoveGoneShared(fmt.Errorf("inspect branch upstreams: %w", err))
	}
	upstream := ""
	found := false
	for _, item := range upstreams {
		if item.Branch == worktree.Branch {
			if found {
				return fmt.Errorf("branch %q appears more than once", worktree.Branch)
			}
			found = true
			upstream = item.Upstream
		}
	}
	if upstream != planned.upstream {
		return fmt.Errorf(
			"%w: upstream for branch %q changed from %q to %q",
			ErrRemoveSafety,
			worktree.Branch,
			planned.upstream,
			upstream,
		)
	}
	exists, err := commandRuntime.git.RefExists(ctx, plan.repository.Path, upstream)
	if err != nil {
		return worktreeRemoveGoneShared(fmt.Errorf("verify upstream ref %q: %w", upstream, err))
	}
	if exists {
		return fmt.Errorf("%w: upstream ref %q reappeared", ErrRemoveSafety, upstream)
	}
	current, err := worktreeRemoveGoneCurrentPath(commandRuntime)
	if err != nil {
		return worktreeRemoveGoneShared(err)
	}
	if worktreeRemoveGonePathContains(worktree.Path, current) {
		return fmt.Errorf("%w: current shell is inside this worktree", ErrRemoveSafety)
	}

	removeRuntime := worktreeRemoveGoneSafetyRuntime(commandRuntime)
	target, base, err := removePrepareLinkedTargetWithCleanliness(
		ctx,
		removeRuntime,
		plan.common,
		worktree,
		false,
	)
	if err != nil {
		return err
	}
	if err := removeCompareNode(
		commandRuntime.fs,
		*plan.common.worktreeBaseDir,
		base,
		"worktree base",
	); err != nil {
		return err
	}
	if err := removeCompareLinkedTarget(commandRuntime.fs, planned.target, target); err != nil {
		return err
	}
	dirty, err := removeInspectDirty(
		ctx,
		removeRuntime.git,
		target.directory.path,
		"linked worktree "+worktree.Slot,
	)
	if err != nil {
		return err
	}
	if dirty != planned.dirty {
		return fmt.Errorf("%w: dirty state changed", ErrRemoveSafety)
	}
	if dirty && !force {
		return fmt.Errorf("%w: worktree is dirty", ErrRemoveSafety)
	}
	return nil
}

func worktreeRemoveGoneWriteFailure(
	writer io.Writer,
	operation, slot string,
	err error,
) error {
	return removeWriteAll(
		writer,
		[]byte(fmt.Sprintf("%s worktree %q: %v\n", operation, slot, err)),
	)
}
