package local

import (
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

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/gitcmd"
)

// WorktreeLister is the Git worktree-list seam. gitcmd.Runner satisfies it.
type WorktreeLister interface {
	WorktreeList(context.Context, string) ([]gitcmd.Worktree, error)
}

// Git combines the read-only Git capabilities used by local discovery.
type Git interface {
	gitcmd.Outputter
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
	// Each path and object identity is observed only once in this enumeration.
	// A new enumeration creates fresh observations after any interaction or mutation.
	observed := make(map[string]worktreePathObservation, len(registered))
	observe := func(path string) (worktreePathObservation, error) {
		if observation, exists := observed[path]; exists {
			return observation, nil
		}
		observation, err := filesystem.observeWorktreePath(path)
		if err == nil {
			observed[path] = observation
		}
		return observation, err
	}
	mainPath, err := observe(repository.Path)
	if err != nil {
		return nil, &WorktreeError{
			Kind: ErrUnsafeWorktree, Repository: repository.Identity, Path: repository.Path,
			Reason: "cannot inspect main worktree path", Err: err,
		}
	}
	registeredPaths := make([]worktreePathObservation, 0, len(registered))
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
		observation, err := observe(item.Path)
		if err != nil {
			return nil, &WorktreeError{
				Kind: ErrUnsafeWorktree, Repository: repository.Identity, Path: item.Path,
				Reason: "cannot inspect registered worktree path", Err: err,
			}
		}
		switch {
		case observation.info != nil && !observation.info.IsDir():
			return nil, &WorktreeError{
				Kind: ErrUnsafeWorktree, Repository: repository.Identity, Path: item.Path,
				Reason: "registered worktree path exists and is not a directory",
			}
		case observation.info == nil && !item.Prunable:
			return nil, &WorktreeError{
				Kind: ErrUnsafeWorktree, Repository: repository.Identity, Path: item.Path,
				Reason: "registered worktree path is missing but Git did not mark it prunable",
			}
		}

		physicalPath := observation.path
		for _, previous := range registeredPaths {
			if filesystem.sameWorktreePath(previous, observation) {
				return nil, &WorktreeError{
					Kind:       ErrWorktreeAmbiguous,
					Repository: repository.Identity,
					Path:       previous.path,
					OtherPath:  physicalPath,
					Reason:     "Git reported the same physical worktree path more than once",
				}
			}
		}
		registeredPaths = append(registeredPaths, observation)
		isMain := filesystem.sameWorktreePath(observation, mainPath)

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

		slot, err := deriveWorktreeSlot(ctx, base, repository, item, physicalPath)
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
			Reason:     "Git did not report the registered main worktree",
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

type worktreePathObservation struct {
	path string
	info fs.FileInfo
}

func (f filesystem) observeWorktreePath(path string) (worktreePathObservation, error) {
	physical, err := f.physicalizeAbsolute(path)
	if err != nil {
		return worktreePathObservation{}, err
	}
	info, err := f.stat(physical)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return worktreePathObservation{}, err
	}
	if err == nil {
		if err := fsidentity.Prime(info, f.sameFile); err != nil {
			return worktreePathObservation{}, err
		}
	}
	return worktreePathObservation{path: physical, info: info}, nil
}

func (f filesystem) sameWorktreePath(first, second worktreePathObservation) bool {
	if first.path == second.path || (runtime.GOOS == "windows" && equalFoldPath(first.path, second.path)) {
		return true
	}
	return first.info != nil && second.info != nil && f.sameFile(first.info, second.info)
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
	return nil
}

// ValidateSlotAvailable rejects an exact slot collision in the repository inventory.
func ValidateSlotAvailable(ctx context.Context, worktrees []Worktree, slot string) error {
	if err := ValidateSlot(ctx, slot); err != nil {
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

// FindLinkedWorktree resolves a registered directory slot independently of
// the checked-out branch, including any auto-assigned numeric suffix.
func FindLinkedWorktree(ctx context.Context, worktrees []Worktree, slot string) (Worktree, error) {
	if err := ValidateSlot(ctx, slot); err != nil {
		return Worktree{}, err
	}
	matches := make([]Worktree, 0, 1)
	for _, worktree := range worktrees {
		if !worktree.Main && slotsCollide(slot, worktree.Slot) {
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

func deriveWorktreeSlot(
	ctx context.Context,
	base string,
	repository Repository,
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
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) != 2 || parts[1] != repository.Repo {
			return "", fmt.Errorf("registered linked worktree is outside <slot>/<repo> layout: %q", relative)
		}
		prefix := WorktreeSlotDirectoryName(repository.Identity + "-")
		if WorktreeSlotDirectoryName(parts[0]) != parts[0] ||
			!strings.HasPrefix(parts[0], prefix) || len(parts[0]) == len(prefix) {
			return "", fmt.Errorf("registered linked worktree has an unexpected slot directory %q", parts[0])
		}
		slot = parts[0]
	} else if item.Detached {
		slot = filepath.Base(physicalPath)
	} else {
		slot = item.Branch
	}
	if err := ValidateSlot(ctx, slot); err != nil {
		return "", err
	}
	return slot, nil
}

func slotsCollide(first, second string) bool {
	return slotComparisonKey(first) == slotComparisonKey(second)
}

func slotComparisonKey(slot string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(slot)
	}
	return slot
}
