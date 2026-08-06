package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

// AssociationOptions supplies Git and filesystem seams for pre-mutation
// validation.
type AssociationOptions struct {
	Git        GitOutputter
	Filesystem FilesystemOptions
}

// ValidateWorktreeAssociation verifies that a linked record has its
// deterministic path and Git common-directory association.
func ValidateWorktreeAssociation(
	ctx context.Context,
	repository Repository,
	worktree Worktree,
	worktreeRoot string,
	options ...AssociationOptions,
) error {
	if len(options) > 1 {
		return errors.New("validate worktree association: more than one options value")
	}
	var option AssociationOptions
	if len(options) == 1 {
		option = options[0]
	}
	if err := ValidateRepository(repository); err != nil {
		return err
	}
	if worktree.Main {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Path:       worktree.Path,
			Reason:     "main worktree cannot be used as a linked mutation target",
		}
	}
	if worktree.Bare {
		return &WorktreeError{
			Kind:       ErrBareWorktree,
			Repository: repository.Identity,
			Path:       worktree.Path,
		}
	}
	if worktree.Repository.Identity != repository.Identity {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Path:       worktree.Path,
			Reason:     "worktree parent does not match the selected repository",
		}
	}
	if err := ValidateBranch(worktree.Slot); err != nil {
		return err
	}

	filesystem := newFilesystem(option.Filesystem)
	git := option.Git
	if git == nil {
		git = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}

	expectedPath, err := WorktreePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		worktree.Slot,
	)
	if err != nil {
		return err
	}
	pathMatches, err := filesystem.samePhysicalPath(expectedPath, worktree.Path)
	if err != nil || !pathMatches {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       worktree.Slot,
			Path:       worktree.Path,
			OtherPath:  expectedPath,
			Reason:     "registered path does not match the deterministic slot path",
			Err:        err,
		}
	}

	gitPointer := filepath.Join(worktree.Path, ".git")
	info, err := filesystem.lstat(gitPointer)
	if err != nil {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       worktree.Slot,
			Path:       worktree.Path,
			Reason:     "linked .git pointer is unavailable",
			Err:        err,
		}
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       worktree.Slot,
			Path:       gitPointer,
			Reason:     "linked .git must be a real, non-symlink file",
		}
	}

	topLevel, commonDir, err := gitTopLevelAndCommon(ctx, git, worktree.Path)
	if err != nil {
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       worktree.Slot,
			Path:       worktree.Path,
			Reason:     "cannot inspect linked Git association",
			Err:        err,
		}
	}
	topMatches, topErr := filesystem.samePhysicalPath(topLevel, worktree.Path)
	commonMatches, commonErr := filesystem.samePhysicalPath(
		commonDir,
		filepath.Join(repository.Path, ".git"),
	)
	if topErr != nil || commonErr != nil || !topMatches || !commonMatches {
		associationErr := errors.Join(topErr, commonErr)
		if associationErr == nil {
			associationErr = fmt.Errorf(
				"top-level or common directory does not match repository %q",
				repository.Identity,
			)
		}
		return &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       worktree.Slot,
			Path:       worktree.Path,
			Reason:     "Git association does not match the selected main repository",
			Err:        associationErr,
		}
	}
	return nil
}

// ManagedWorktreeOptions supplies the combined Git seam for registered lookup
// and association validation.
type ManagedWorktreeOptions struct {
	Git        Git
	Filesystem FilesystemOptions
}

// ResolveManagedWorktree enumerates, selects, and validates one deterministic
// linked worktree before mutation.
func ResolveManagedWorktree(
	ctx context.Context,
	repository Repository,
	worktreeRoot, slot string,
	options ...ManagedWorktreeOptions,
) (Worktree, error) {
	if len(options) > 1 {
		return Worktree{}, errors.New("resolve managed worktree: more than one options value")
	}
	var option ManagedWorktreeOptions
	if len(options) == 1 {
		option = options[0]
	}

	git := option.Git
	if git == nil {
		git = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}
	worktrees, err := EnumerateWorktrees(ctx, repository, worktreeRoot, WorktreeOptions{
		Lister:     git,
		Filesystem: option.Filesystem,
	})
	if err != nil {
		return Worktree{}, err
	}
	worktree, err := FindRegisteredLinkedWorktree(worktrees, slot)
	if err != nil {
		return Worktree{}, err
	}
	err = ValidateWorktreeAssociation(
		ctx,
		repository,
		worktree,
		worktreeRoot,
		AssociationOptions{
			Git:        git,
			Filesystem: option.Filesystem,
		},
	)
	if err != nil {
		return Worktree{}, err
	}
	return worktree, nil
}
