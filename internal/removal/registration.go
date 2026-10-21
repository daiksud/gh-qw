package removal

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
)

// Registration records a Git-reported path, including lexical and physical aliases.
type Registration struct {
	Repository string
	Path       string
}

// ContainedRegistration selects a nested Git registration in deterministic path order.
func ContainedRegistration(
	registrations []Registration,
	parent local.Worktree,
) (Registration, bool) {
	var contained Registration
	found := false
	for _, registration := range registrations {
		if !pathStrictlyWithin(parent.Path, registration.Path) {
			continue
		}
		if !found || physicalpath.Key(registration.Path) < physicalpath.Key(contained.Path) ||
			(physicalpath.Key(registration.Path) == physicalpath.Key(contained.Path) &&
				registration.Repository < contained.Repository) {
			contained = registration
			found = true
		}
	}
	return contained, found
}

// Registrations observes all Git registrations, retaining missing paths and observation errors.
func (observer Observer) Registrations(
	ctx context.Context,
	repositories []local.Repository,
) ([]Registration, error) {
	var registrations []Registration
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		worktrees, err := observer.Git.WorktreeList(ctx, repository.Path)
		if err != nil {
			return nil, fmt.Errorf(
				"list registered worktrees for %q: %w",
				repository.Identity,
				err,
			)
		}
		for _, worktree := range worktrees {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			paths, err := RegistrationPaths(observer.Filesystem, worktree.Path)
			if err != nil {
				return nil, fmt.Errorf(
					"inspect registered path %q for %q: %w",
					worktree.Path,
					repository.Identity,
					err,
				)
			}
			for _, path := range paths {
				registrations = append(registrations, Registration{
					Repository: repository.Identity,
					Path:       path,
				})
			}
		}
	}
	return registrations, nil
}

// RegistrationPaths retains lexical and physical aliases, including missing trailing components.
func RegistrationPaths(
	filesystem Filesystem,
	path string,
) ([]string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("registered path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	physical, err := physicalizeRegistration(filesystem, path)
	if err != nil {
		return nil, err
	}
	paths := []string{path}
	if !physicalpath.SamePath(path, physical) {
		paths = append(paths, physical)
	}
	return paths, nil
}

func physicalizeRegistration(filesystem Filesystem, path string) (string, error) {
	observer := physicalpath.Filesystem{Lstat: filesystem.Lstat, EvalSymlinks: filesystem.EvalSymlinks}
	physical, err := observer.ResolveAbsolute(path)
	if err != nil {
		return "", fmt.Errorf("resolve registered path %q: %w", path, err)
	}
	return physical, nil
}

// CompareRepositories rejects changes to the discovered repository inventory.
func CompareRepositories(
	before, after []local.Repository,
) error {
	if len(before) != len(after) {
		return fmt.Errorf("%w: discovered repository inventory changed", ErrSafety)
	}
	for index := range before {
		if before[index] != after[index] {
			return fmt.Errorf("%w: discovered repository inventory changed", ErrSafety)
		}
	}
	return nil
}
