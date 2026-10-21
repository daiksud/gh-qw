package local

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/daiksud/gh-qw/internal/root"
)

// IdentityParts is a validated canonical repository identity.
type IdentityParts = repospec.Identity

// ParseIdentity validates an already-canonical <host>/<owner>/<repo>
// identity. It does not accept URLs, ports, branch suffixes, or .git suffixes.
func ParseIdentity(identity string) (IdentityParts, error) {
	parts, err := repospec.ParseIdentity(identity)
	if err != nil {
		return IdentityParts{}, invalidIdentity(identity, err.Error(), err)
	}
	return parts, nil
}

// CanonicalIdentity validates components and returns their slash-separated
// canonical identity.
func CanonicalIdentity(host, owner, repo string) (string, error) {
	parts, err := ParseIdentity((repospec.Identity{Host: host, Owner: owner, Repo: repo}).String())
	if err != nil {
		return "", err
	}
	return parts.String(), nil
}

// ValidateBranch delegates literal ref validity to Git while preserving the
// command-line exclusion of HEAD and surrounding whitespace/control characters.
func ValidateBranch(ctx context.Context, branch string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fail := func(reason string, err error) error {
		return &ValidationError{Kind: ErrInvalidBranch, Value: branch, Reason: reason, Err: err}
	}
	if branch == "HEAD" {
		return fail("branch must not be HEAD", nil)
	}
	if strings.TrimSpace(branch) != branch {
		return fail("branch has leading or trailing whitespace", nil)
	}
	for _, r := range branch {
		if unicode.IsControl(r) {
			return fail("branch contains a control character", nil)
		}
	}
	if err := (*gitcmd.Runner)(nil).CheckRefFormat(ctx, branch); err != nil {
		if errors.Is(err, gitcmd.ErrInvalidRef) {
			return fail(err.Error(), err)
		}
		return fmt.Errorf("validate worktree branch %q: %w", branch, err)
	}
	return nil
}

// ValidateSlot applies branch validation and gh-qw's portable directory layout
// restriction. A storage slot need not match the worktree's checked-out branch.
func ValidateSlot(ctx context.Context, slot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, component := range strings.Split(slot, "/") {
		if strings.HasSuffix(component, ".") {
			return &ValidationError{
				Kind:   ErrInvalidBranch,
				Value:  slot,
				Reason: "slot components must not end with a dot",
			}
		}
	}
	return ValidateBranch(ctx, slot)
}

// MainPath returns the contained physical destination for one canonical main
// repository. It neither creates nor mutates the destination.
func MainPath(rootPath, host, owner, repo string) (string, error) {
	if _, err := CanonicalIdentity(host, owner, repo); err != nil {
		return "", err
	}

	target := filepath.Join(rootPath, host, owner, repo)
	physical, err := root.PhysicalizeTarget(rootPath, target, root.StrictlyUnder)
	if err != nil {
		return "", fmt.Errorf("derive main repository path: %w", err)
	}
	return physical, nil
}

// WorktreeBasePath returns the contained per-repository directory below the
// worktree root.
func WorktreeBasePath(worktreeRoot, host, owner, repo string) (string, error) {
	return MainPath(worktreeRoot, host, owner, repo)
}

// WorktreePath returns the contained physical destination for a branch slot.
// Slashes in branch form nested directories.
func WorktreePath(ctx context.Context, worktreeRoot, host, owner, repo, branch string) (string, error) {
	if err := ValidateSlot(ctx, branch); err != nil {
		return "", err
	}

	base, err := WorktreeBasePath(worktreeRoot, host, owner, repo)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, filepath.FromSlash(branch))
	physical, err := root.PhysicalizeTarget(base, target, root.StrictlyUnder)
	if err != nil {
		return "", fmt.Errorf("derive linked worktree path: %w", err)
	}
	return physical, nil
}

// NormalizePathForOutput converts a native absolute path to the slash form
// used by the CLI, including Windows paths in platform-independent tests.
func NormalizePathForOutput(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
}

// NormalizeIdentityForOutput validates identity and returns its canonical
// slash form.
func NormalizeIdentityForOutput(identity string) (string, error) {
	parts, err := ParseIdentity(identity)
	if err != nil {
		return "", err
	}
	return parts.String(), nil
}

func invalidIdentity(identity, reason string, err error) error {
	return &ValidationError{
		Kind:   ErrInvalidIdentity,
		Value:  identity,
		Reason: reason,
		Err:    err,
	}
}
