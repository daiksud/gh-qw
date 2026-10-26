package local

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// DestinationOptions supplies the filesystem seam for add-time path
// validation.
type DestinationOptions struct {
	Filesystem FilesystemOptions
}

// ValidateWorktreeDestination validates a proposed deterministic slot against
// registered identities and existing filesystem prefix collisions. It returns
// the physical destination without creating it.
func ValidateWorktreeDestination(
	ctx context.Context,
	worktreeRoot string,
	repository Repository,
	slot string,
	worktrees []Worktree,
	options ...DestinationOptions,
) (string, error) {
	if len(options) > 1 {
		return "", errors.New("validate worktree destination: more than one options value")
	}
	var option DestinationOptions
	if len(options) == 1 {
		option = options[0]
	}
	if err := ValidateRepository(repository); err != nil {
		return "", err
	}
	if err := ValidateSlotAvailable(ctx, worktrees, slot); err != nil {
		return "", err
	}

	destination, err := WorktreePath(
		ctx,
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		slot,
	)
	if err != nil {
		return "", err
	}

	filesystem := newFilesystem(option.Filesystem)
	if _, err := filesystem.lstat(destination); err == nil {
		return "", &SlotCollisionError{Slot: slot, ExistingSlot: slot}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       slot,
			Path:       destination,
			Reason:     "cannot inspect proposed destination",
			Err:        err,
		}
	}

	// The slot namespace is shared by every repository. Reserve the slot
	// directory itself instead of allowing two repositories to share a name.
	slotDirectory := filepath.Join(worktreeRoot, slot)
	if _, err := filesystem.lstat(slotDirectory); err == nil {
		return "", &SlotCollisionError{Slot: slot, ExistingSlot: slot}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Slot:       slot,
			Path:       slotDirectory,
			Reason:     "cannot inspect proposed slot directory",
			Err:        err,
		}
	}

	return destination, nil
}
