package removal

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
)

// LinkedObservation holds captured state without a command's eligibility policy.
type LinkedObservation struct {
	Worktree  local.Worktree
	Directory Node
	GitFile   Node
	Cleanup   []Node
}

// ObserveListedLinked validates association for a record obtained from an inventory.
func (observer Observer) ObserveListedLinked(
	ctx context.Context,
	plan RepositoryObservation,
	worktree local.Worktree,
) (LinkedObservation, Node, error) {
	if err := observer.ValidateAssociation(
		ctx,
		plan.Repository,
		worktree,
		plan.Roots.WorktreeRoot,
		local.AssociationOptions{
			Git:        observer.Git,
			Filesystem: observer.LocalFilesystem,
		},
	); err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"validate linked worktree %q association: %w",
			worktree.Slot,
			err,
		)
	}

	return observer.ObserveResolvedLinked(ctx, plan, worktree)
}

// ObserveResolvedLinked captures a worktree whose association was validated by
// ResolveWorktree for the same repository and root in this observation phase.
// Callers must not cross a mutation or confirmation boundary between these calls.
func (observer Observer) ObserveResolvedLinked(ctx context.Context, plan RepositoryObservation, worktree local.Worktree) (LinkedObservation, Node, error) {

	expectedPath, err := local.WorktreePath(
		ctx,
		plan.Roots.WorktreeRoot,
		plan.Repository.Host,
		plan.Repository.Owner,
		plan.Repository.Repo,
		worktree.Slot,
	)
	if err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"derive linked worktree path %q: %w",
			worktree.Slot,
			err,
		)
	}
	expectedLexical := filepath.Join(
		plan.WorktreeBase,
		filepath.FromSlash(worktree.Slot),
	)
	if !physicalpath.SamePath(expectedPath, expectedLexical) ||
		!physicalpath.SamePath(worktree.Path, expectedLexical) {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"%w: linked worktree %q path %q is not the exact deterministic path %q",
			ErrSafety,
			worktree.Slot,
			worktree.Path,
			expectedLexical,
		)
	}

	base, err := CaptureNode(observer.Filesystem,
		plan.WorktreeRoot.Path,
		plan.WorktreeBase,
		Directory,
		true,
	)
	if err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"inspect worktree base for %q: %w",
			worktree.Slot,
			err,
		)
	}
	directory, err := CaptureNode(observer.Filesystem,
		base.Path,
		expectedLexical,
		Directory,
		true,
	)
	if err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"inspect linked worktree %q: %w",
			worktree.Slot,
			err,
		)
	}
	gitFile, err := CaptureNode(observer.Filesystem,
		directory.Path,
		filepath.Join(directory.Path, ".git"),
		RegularFile,
		true,
	)
	if err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"%w: linked worktree %q .git is not a real contained file: %v",
			ErrSafety,
			worktree.Slot,
			err,
		)
	}
	cleanup, err := CaptureCleanupChain(observer.Filesystem,
		base.Path,
		filepath.Dir(directory.Path),
	)
	if err != nil {
		return LinkedObservation{}, Node{}, fmt.Errorf(
			"plan linked parent cleanup for %q: %w",
			worktree.Slot,
			err,
		)
	}

	return LinkedObservation{
		Worktree:  worktree,
		Directory: directory,
		GitFile:   gitFile,
		Cleanup:   cleanup,
	}, base, nil
}

// CompareLinked compares registration and captured object identities.
func CompareLinked(
	filesystem Filesystem,
	before, after LinkedObservation,
) error {
	if !SameWorktree(before.Worktree, after.Worktree) {
		return fmt.Errorf(
			"%w: linked worktree registration changed for %q",
			ErrSafety,
			before.Worktree.Slot,
		)
	}
	if err := CompareNode(filesystem,
		before.Directory,
		after.Directory,
		"linked worktree "+before.Worktree.Slot,
	); err != nil {
		return err
	}
	if err := CompareNode(filesystem,
		before.GitFile,
		after.GitFile,
		"linked .git "+before.Worktree.Slot,
	); err != nil {
		return err
	}
	return CompareNodeSlices(filesystem,
		before.Cleanup,
		after.Cleanup,
		"linked cleanup "+before.Worktree.Slot,
	)
}

// SameWorktree compares observed Git registration fields, preserving slot and branch distinctions.
func SameWorktree(first, second local.Worktree) bool {
	return first.Repository.Identity == second.Repository.Identity &&
		first.Identity == second.Identity &&
		first.Slot == second.Slot &&
		physicalpath.SamePath(first.Path, second.Path) &&
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

// InspectDirty observes tracked, untracked, and submodule changes through Git.
func InspectDirty(
	ctx context.Context,
	git local.GitOutputter,
	path, description string,
) (bool, error) {
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
		return false, fmt.Errorf("inspect %s cleanliness at %q: %w", description, path, err)
	}
	return len(output) != 0, nil
}
