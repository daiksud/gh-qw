package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

// WorktreeLister is the Git worktree-list seam. gitcmd.Runner satisfies it.
type WorktreeLister interface {
	WorktreeList(context.Context, string) ([]gitcmd.Worktree, error)
}

// Git combines the read-only Git capabilities used by local discovery.
type Git interface {
	GitOutputter
	WorktreeLister
}

// WorktreeOptions supplies enumeration seams.
type WorktreeOptions struct {
	Lister     WorktreeLister
	Filesystem FilesystemOptions
}

// EnumerateWorktrees returns the selected main first and registered linked
// worktrees sorted by identity.
func EnumerateWorktrees(
	ctx context.Context,
	repository Repository,
	worktreeRoot string,
	options ...WorktreeOptions,
) ([]Worktree, error) {
	if len(options) > 1 {
		return nil, errors.New("enumerate worktrees: more than one options value")
	}
	var option WorktreeOptions
	if len(options) == 1 {
		option = options[0]
	}
	if err := ValidateRepository(repository); err != nil {
		return nil, err
	}

	lister := option.Lister
	if lister == nil {
		lister = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}
	filesystem := newFilesystem(option.Filesystem)

	base, err := WorktreeBasePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return nil, err
	}

	registered, err := lister.WorktreeList(ctx, repository.Path)
	if err != nil {
		return nil, fmt.Errorf("list worktrees for %q: %w", repository.Identity, err)
	}

	var main *Worktree
	linked := make([]Worktree, 0, len(registered))
	slots := make(map[string]string, len(registered))
	slotNames := make(map[string]string, len(registered))
	registeredPaths := make([]string, 0, len(registered))
	for _, item := range registered {
		if item.Bare {
			return nil, &WorktreeError{
				Kind:       ErrBareWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "Git reported a bare worktree record",
			}
		}
		if item.Detached && item.Branch != "" {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "Git reported both a branch and detached state",
			}
		}
		if item.HEAD == "" {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "Git reported a worktree without HEAD",
			}
		}
		if !item.Detached && item.Branch == "" {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "Git reported a non-bare worktree without a branch or detached state",
			}
		}
		if !filepath.IsAbs(item.Path) {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "registered worktree path is not absolute",
			}
		}
		pathInfo, statErr := filesystem.stat(item.Path)
		switch {
		case statErr == nil && !pathInfo.IsDir():
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "registered worktree path exists and is not a directory",
			}
		case statErr != nil && !errors.Is(statErr, os.ErrNotExist):
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "cannot inspect registered worktree path",
				Err:        statErr,
			}
		case errors.Is(statErr, os.ErrNotExist) && !item.Prunable:
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "registered worktree path is missing but Git did not mark it prunable",
			}
		}

		physicalPath, err := filesystem.physicalizeAbsolute(item.Path)
		if err != nil {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "cannot physicalize registered path",
				Err:        err,
			}
		}
		for _, previousPath := range registeredPaths {
			samePath, compareErr := filesystem.samePhysicalPath(previousPath, physicalPath)
			if compareErr != nil {
				return nil, &WorktreeError{
					Kind:       ErrUnsafeWorktree,
					Repository: repository.Identity,
					Path:       item.Path,
					OtherPath:  previousPath,
					Reason:     "cannot compare duplicate registered paths",
					Err:        compareErr,
				}
			}
			if samePath {
				return nil, &WorktreeError{
					Kind:       ErrWorktreeAmbiguous,
					Repository: repository.Identity,
					Path:       previousPath,
					OtherPath:  physicalPath,
					Reason:     "Git reported the same physical worktree path more than once",
				}
			}
		}
		registeredPaths = append(registeredPaths, physicalPath)
		isMain, err := filesystem.samePhysicalPath(physicalPath, repository.Path)
		if err != nil {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "cannot compare registered path with the main worktree",
				Err:        err,
			}
		}

		record := Worktree{
			Repository:     repository,
			Path:           physicalPath,
			HEAD:           item.HEAD,
			Branch:         item.Branch,
			Detached:       item.Detached,
			Bare:           item.Bare,
			Locked:         item.Locked,
			LockedReason:   item.LockedReason,
			Prunable:       item.Prunable,
			PrunableReason: item.PrunableReason,
		}
		if isMain {
			if main != nil {
				return nil, &WorktreeError{
					Kind:       ErrWorktreeAmbiguous,
					Repository: repository.Identity,
					Path:       main.Path,
					OtherPath:  physicalPath,
					Reason:     "Git reported the main worktree more than once",
				}
			}
			record.Identity = repository.Identity
			record.Main = true
			main = &record
			continue
		}

		slot, err := deriveWorktreeSlot(base, item, physicalPath)
		if err != nil {
			return nil, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       item.Path,
				Reason:     "cannot derive a safe worktree slot",
				Err:        err,
			}
		}
		slotKey := slotComparisonKey(slot)
		if previousPath, exists := slots[slotKey]; exists {
			return nil, &WorktreeError{
				Kind:       ErrWorktreeAmbiguous,
				Repository: repository.Identity,
				Slot:       slot,
				Path:       previousPath,
				OtherPath:  physicalPath,
				Reason: fmt.Sprintf(
					"multiple registered worktrees use equivalent derived slots %q and %q",
					slotNames[slotKey],
					slot,
				),
			}
		}
		slots[slotKey] = physicalPath
		slotNames[slotKey] = slot
		record.Identity = repository.Identity + "@" + slot
		record.Slot = slot
		linked = append(linked, record)
	}

	if main == nil {
		return nil, &WorktreeError{
			Kind:       ErrUnsafeWorktree,
			Repository: repository.Identity,
			Reason:     "Git did not report the discovered main worktree",
		}
	}

	sort.Slice(linked, func(left, right int) bool {
		if linked[left].Identity == linked[right].Identity {
			return linked[left].Path < linked[right].Path
		}
		return linked[left].Identity < linked[right].Identity
	})

	result := make([]Worktree, 0, len(linked)+1)
	result = append(result, *main)
	result = append(result, linked...)
	return result, nil
}

// ValidateRepository checks the internal consistency needed by local Git
// operations.
func ValidateRepository(repository Repository) error {
	parts, err := ParseIdentity(repository.Identity)
	if err != nil {
		return err
	}
	if repository.Host != parts.Host ||
		repository.Owner != parts.Owner ||
		repository.Repo != parts.Repo {
		return invalidIdentity(
			repository.Identity,
			"record components do not match its canonical identity",
			nil,
		)
	}
	if !filepath.IsAbs(repository.Path) {
		return fmt.Errorf("repository %q path %q is not absolute", repository.Identity, repository.Path)
	}
	if repository.Root != "" && !filepath.IsAbs(repository.Root) {
		return fmt.Errorf("repository %q root %q is not absolute", repository.Identity, repository.Root)
	}
	if repository.RootIndex < 0 {
		return fmt.Errorf("repository %q has a negative root index", repository.Identity)
	}
	return nil
}

// ValidateSlotAvailable rejects exact and component-prefix collisions.
func ValidateSlotAvailable(worktrees []Worktree, slot string) error {
	if err := ValidateBranch(slot); err != nil {
		return err
	}
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Slot == "" {
			continue
		}
		if slotsCollide(slot, worktree.Slot) {
			return &SlotCollisionError{
				Slot:         slot,
				ExistingSlot: worktree.Slot,
			}
		}
	}
	return nil
}

// FindLinkedWorktree returns the one registered linked worktree with an exact
// slot.
func FindLinkedWorktree(worktrees []Worktree, slot string) (Worktree, error) {
	if err := ValidateBranch(slot); err != nil {
		return Worktree{}, err
	}
	matches := make([]Worktree, 0, 1)
	for _, worktree := range worktrees {
		if !worktree.Main && worktree.Slot == slot {
			matches = append(matches, worktree)
		}
	}
	switch len(matches) {
	case 0:
		return Worktree{}, &WorktreeError{
			Kind:   ErrWorktreeNotFound,
			Slot:   slot,
			Reason: "slot is not registered",
		}
	case 1:
		return matches[0], nil
	default:
		return Worktree{}, &WorktreeError{
			Kind:   ErrWorktreeAmbiguous,
			Slot:   slot,
			Path:   matches[0].Path,
			Reason: "slot is registered more than once",
		}
	}
}

// FindRegisteredLinkedWorktree is an explicit alias for FindLinkedWorktree.
func FindRegisteredLinkedWorktree(worktrees []Worktree, slot string) (Worktree, error) {
	return FindLinkedWorktree(worktrees, slot)
}

func deriveWorktreeSlot(
	base string,
	item gitcmd.Worktree,
	physicalPath string,
) (string, error) {
	if filepath.Clean(base) == filepath.Clean(physicalPath) ||
		(runtime.GOOS == "windows" && equalFoldPath(filepath.Clean(base), filepath.Clean(physicalPath))) {
		return "", errors.New("registered linked worktree occupies the per-repository container without a slot")
	}
	lexicalInside := pathStrictlyWithin(base, filepath.Clean(item.Path))
	physicalInside := pathStrictlyWithin(base, physicalPath)
	if lexicalInside && !physicalInside {
		return "", errors.New("registered path escapes its deterministic repository directory through a symbolic link")
	}

	var slot string
	if physicalInside {
		relative, err := filepath.Rel(base, physicalPath)
		if err != nil {
			return "", fmt.Errorf("make deterministic path relative: %w", err)
		}
		slot = filepath.ToSlash(relative)
	} else if item.Detached {
		slot = filepath.Base(physicalPath)
	} else {
		slot = item.Branch
	}
	if err := ValidateBranch(slot); err != nil {
		return "", err
	}
	return slot, nil
}

func slotsCollide(first, second string) bool {
	if runtime.GOOS == "windows" {
		first = strings.ToLower(first)
		second = strings.ToLower(second)
	}
	return first == second ||
		strings.HasPrefix(first, second+"/") ||
		strings.HasPrefix(second, first+"/")
}

func slotComparisonKey(slot string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(slot)
	}
	return slot
}
