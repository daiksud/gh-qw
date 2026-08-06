package local

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
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
	if err := ValidateSlotAvailable(worktrees, slot); err != nil {
		return "", err
	}

	destination, err := WorktreePath(
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

	base, err := WorktreeBasePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return "", err
	}
	components := strings.Split(slot, "/")
	current := base
	for index, component := range components[:len(components)-1] {
		current = filepath.Join(current, filepath.FromSlash(component))
		info, statErr := filesystem.lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Slot:       slot,
				Path:       current,
				Reason:     "cannot inspect proposed slot prefix",
				Err:        statErr,
			}
		}
		if !info.IsDir() {
			return "", &SlotCollisionError{
				Slot:         slot,
				ExistingSlot: strings.Join(components[:index+1], "/"),
			}
		}

		gitMarker := filepath.Join(current, ".git")
		if _, markerErr := filesystem.lstat(gitMarker); markerErr == nil {
			return "", &SlotCollisionError{
				Slot:         slot,
				ExistingSlot: strings.Join(components[:index+1], "/"),
			}
		} else if !errors.Is(markerErr, fs.ErrNotExist) {
			return "", &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Slot:       slot,
				Path:       gitMarker,
				Reason:     "cannot inspect proposed slot prefix Git marker",
				Err:        markerErr,
			}
		}
	}

	return destination, nil
}
