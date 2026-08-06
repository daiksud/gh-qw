package local

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidIdentity identifies a non-canonical repository identity.
	ErrInvalidIdentity = errors.New("invalid canonical repository identity")
	// ErrInvalidBranch identifies a branch or slot that cannot safely map to a
	// deterministic worktree path.
	ErrInvalidBranch = errors.New("invalid worktree branch")

	// ErrSelector identifies repository-selector usage errors.
	ErrSelector = errors.New("repository selector error")
	// ErrInvalidSelector identifies a selector outside the exact local forms.
	ErrInvalidSelector = errors.New("invalid repository selector")
	// ErrRepositoryNotFound identifies a selector with no match.
	ErrRepositoryNotFound = errors.New("repository selector matched no repositories")
	// ErrRepositoryAmbiguous identifies a selector with more than one match.
	ErrRepositoryAmbiguous = errors.New("repository selector is ambiguous")

	// ErrWorktreeNotFound identifies a requested linked slot with no match.
	ErrWorktreeNotFound = errors.New("linked worktree not found")
	// ErrWorktreeAmbiguous identifies duplicate or otherwise ambiguous slots.
	ErrWorktreeAmbiguous = errors.New("linked worktree is ambiguous")
	// ErrUnsafeWorktree identifies a worktree that fails path or Git-association
	// validation.
	ErrUnsafeWorktree = errors.New("unsafe linked worktree")
	// ErrBareWorktree identifies bare Git state, which gh-qw never manages.
	ErrBareWorktree = errors.New("bare worktree is not managed")
	// ErrCurrentUnmanaged identifies a current directory outside every
	// discovered main repository and its registered linked worktrees.
	ErrCurrentUnmanaged = errors.New("current directory is not in a managed repository")
	// ErrSlotCollision identifies exact and component-prefix slot collisions.
	ErrSlotCollision = errors.New("worktree slot collides with an existing slot")
)

// ValidationError describes an invalid canonical identity or branch.
type ValidationError struct {
	Kind   error
	Value  string
	Reason string
	Err    error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid local value"
	}
	kind := e.Kind
	if kind == nil {
		kind = errors.New("invalid local value")
	}
	if e.Reason == "" {
		return fmt.Sprintf("%s %q", kind, e.Value)
	}
	return fmt.Sprintf("%s %q: %s", kind, e.Value, e.Reason)
}

// Unwrap preserves the underlying parser or filesystem error.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is makes validation failures discoverable through their sentinel.
func (e *ValidationError) Is(target error) bool {
	return e != nil && target == e.Kind
}

// SelectorErrorKind classifies an exact-selector failure.
type SelectorErrorKind uint8

const (
	SelectorInvalid SelectorErrorKind = iota + 1
	SelectorNotFound
	SelectorAmbiguous
)

// SelectorError is a typed usage error for local repository selection.
type SelectorError struct {
	Selector string
	Kind     SelectorErrorKind
	Reason   string
	Matches  []Repository
}

func (e *SelectorError) Error() string {
	if e == nil {
		return ErrSelector.Error()
	}
	switch e.Kind {
	case SelectorInvalid:
		if e.Reason == "" {
			return fmt.Sprintf("%s %q", ErrInvalidSelector, e.Selector)
		}
		return fmt.Sprintf("%s %q: %s", ErrInvalidSelector, e.Selector, e.Reason)
	case SelectorNotFound:
		return fmt.Sprintf("%s: %q", ErrRepositoryNotFound, e.Selector)
	case SelectorAmbiguous:
		identities := make([]string, 0, len(e.Matches))
		for _, match := range e.Matches {
			identities = append(identities, match.Identity)
		}
		return fmt.Sprintf(
			"%s: %q matched %d repositories (%s)",
			ErrRepositoryAmbiguous,
			e.Selector,
			len(e.Matches),
			strings.Join(identities, ", "),
		)
	default:
		return fmt.Sprintf("%s: %q", ErrSelector, e.Selector)
	}
}

// Is exposes both the selector umbrella and its specific failure.
func (e *SelectorError) Is(target error) bool {
	if target == ErrSelector {
		return true
	}
	if e == nil {
		return false
	}
	switch e.Kind {
	case SelectorInvalid:
		return target == ErrInvalidSelector
	case SelectorNotFound:
		return target == ErrRepositoryNotFound
	case SelectorAmbiguous:
		return target == ErrRepositoryAmbiguous
	default:
		return false
	}
}

// WorktreeError describes a runtime worktree discovery or validation failure.
type WorktreeError struct {
	Kind       error
	Repository string
	Slot       string
	Path       string
	OtherPath  string
	Reason     string
	Err        error
}

func (e *WorktreeError) Error() string {
	if e == nil {
		return ErrUnsafeWorktree.Error()
	}
	kind := e.Kind
	if kind == nil {
		kind = ErrUnsafeWorktree
	}

	var subject string
	switch {
	case e.Slot != "":
		subject = fmt.Sprintf(" slot %q", e.Slot)
	case e.Path != "":
		subject = fmt.Sprintf(" path %q", e.Path)
	case e.Repository != "":
		subject = fmt.Sprintf(" for %q", e.Repository)
	}
	if e.Reason == "" {
		return kind.Error() + subject
	}
	return fmt.Sprintf("%s%s: %s", kind, subject, e.Reason)
}

// Unwrap preserves a Git or filesystem cause.
func (e *WorktreeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is exposes the worktree error's sentinel.
func (e *WorktreeError) Is(target error) bool {
	return e != nil && target == e.Kind
}

// SlotCollisionError describes an exact or component-prefix slot collision.
type SlotCollisionError struct {
	Slot         string
	ExistingSlot string
}

func (e *SlotCollisionError) Error() string {
	if e == nil {
		return ErrSlotCollision.Error()
	}
	return fmt.Sprintf(
		"%s: %q conflicts with %q",
		ErrSlotCollision,
		e.Slot,
		e.ExistingSlot,
	)
}

// Is makes every slot collision discoverable with errors.Is.
func (e *SlotCollisionError) Is(target error) bool {
	return target == ErrSlotCollision
}
