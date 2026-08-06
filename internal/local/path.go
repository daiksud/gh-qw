package local

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/daiksud/gh-qw/internal/root"
)

// IdentityParts is a validated canonical repository identity.
type IdentityParts struct {
	Identity string
	Host     string
	Owner    string
	Repo     string
}

// ParseIdentity validates an already-canonical <host>/<owner>/<repo>
// identity. It does not accept URLs, ports, branch suffixes, or .git suffixes.
func ParseIdentity(identity string) (IdentityParts, error) {
	parts := strings.Split(identity, "/")
	if len(parts) != 3 {
		return IdentityParts{}, invalidIdentity(identity, "must contain exactly host, owner, and repository", nil)
	}

	spec, err := repospec.Parse(identity, repospec.Options{})
	if err != nil {
		return IdentityParts{}, invalidIdentity(identity, parserReason(err), err)
	}
	if spec.Identity != identity ||
		spec.Host != parts[0] ||
		spec.Owner != parts[1] ||
		spec.Repo != parts[2] ||
		spec.Branch != "" {
		return IdentityParts{}, invalidIdentity(
			identity,
			"must already be canonical and contain no port, .git suffix, or branch",
			nil,
		)
	}

	return IdentityParts{
		Identity: spec.Identity,
		Host:     spec.Host,
		Owner:    spec.Owner,
		Repo:     spec.Repo,
	}, nil
}

// CanonicalIdentity validates components and returns their slash-separated
// canonical identity.
func CanonicalIdentity(host, owner, repo string) (string, error) {
	identity := strings.Join([]string{host, owner, repo}, "/")
	parts, err := ParseIdentity(identity)
	if err != nil {
		return "", err
	}
	return parts.Identity, nil
}

// ValidateBranch validates a branch or slot against gh-qw's Git-ref and path
// safety rules.
func ValidateBranch(branch string) error {
	const prefix = "github.com/gh-qw/validation@"

	if branch == "HEAD" {
		return &ValidationError{
			Kind:   ErrInvalidBranch,
			Value:  branch,
			Reason: "branch must not be HEAD",
		}
	}
	spec, err := repospec.Parse(prefix+branch, repospec.Options{})
	if err != nil {
		return &ValidationError{
			Kind:   ErrInvalidBranch,
			Value:  branch,
			Reason: parserReason(err),
			Err:    err,
		}
	}
	if spec.Branch != branch {
		return &ValidationError{
			Kind:   ErrInvalidBranch,
			Value:  branch,
			Reason: "branch is not preserved by canonical parsing",
		}
	}
	return nil
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
func WorktreePath(worktreeRoot, host, owner, repo, branch string) (string, error) {
	if err := ValidateBranch(branch); err != nil {
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
	return parts.Identity, nil
}

func invalidIdentity(identity, reason string, err error) error {
	return &ValidationError{
		Kind:   ErrInvalidIdentity,
		Value:  identity,
		Reason: reason,
		Err:    err,
	}
}

func parserReason(err error) string {
	var usageErr *repospec.UsageError
	if errors.As(err, &usageErr) && usageErr.Reason != "" {
		return usageErr.Reason
	}
	return err.Error()
}
