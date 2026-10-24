package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/daiksud/gh-qw/internal/local"
	movementpkg "github.com/daiksud/gh-qw/internal/movement"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/registry"
	"github.com/daiksud/gh-qw/internal/repospec"
)

// LocationRegistry owns only explicit repository identity-to-path registrations.
// Git worktree and branch metadata is never persisted here.
type LocationRegistry interface {
	List() (map[string]string, error)
	Register(identity, path string) error
	Relocate(identity, previous, next string) error
	Forget(identity string) error
	ForgetAt(identity, path string) error
}

func defaultLocationRegistry() (LocationRegistry, error) {
	return registry.NewStore()
}

func resolveLocationRegistry(provided LocationRegistry) (LocationRegistry, error) {
	if provided != nil {
		return provided, nil
	}
	return defaultLocationRegistry()
}

func registeredRepositories(ctx context.Context, store LocationRegistry, roots []string) ([]local.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("read managed repository locations: %w", err)
	}
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	result := make([]local.Repository, 0, len(entries))
	for _, identity := range identities {
		parts, err := repospec.ParseIdentity(identity)
		if err != nil {
			return nil, fmt.Errorf("invalid registered identity %q: %w", identity, err)
		}
		path := entries[identity]
		root := filepath.Dir(path)
		rootIndex := len(roots)
		for index, configured := range roots {
			expected := filepath.Join(configured, parts.Host, parts.Owner, parts.Repo)
			equal, compareErr := (physicalpath.Filesystem{}).Equivalent(expected, path)
			if compareErr != nil {
				return nil, fmt.Errorf("compare registered location for %q with configured layout: %w", identity, compareErr)
			}
			if equal {
				root, rootIndex = configured, index
				break
			}
		}
		result = append(result, local.Repository{
			Identity:  parts.String(),
			Host:      parts.Host,
			Owner:     parts.Owner,
			Repo:      parts.Repo,
			Path:      path,
			Root:      root,
			RootIndex: rootIndex,
		})
	}
	return result, nil
}

func registryDiscovery(store LocationRegistry) func(context.Context, []string, ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
	return func(ctx context.Context, roots []string, _ ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
		repositories, err := registeredRepositories(ctx, store, roots)
		if err != nil {
			return local.DiscoveryResult{}, err
		}
		return local.DiscoveryResult{Repositories: repositories}, nil
	}
}

func validateRegisteredRepository(ctx context.Context, git RepositoryMoveGit, repository local.Repository) error {
	snapshot, err := movementpkg.InspectRepository(ctx, repository.Path, git, movementpkg.Filesystem{})
	if err != nil {
		return err
	}
	equal, err := (physicalpath.Filesystem{}).Equivalent(snapshot.Path, repository.Path)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("Git top-level %q does not match the registered location", snapshot.Path)
	}
	return nil
}

func physicalRegisteredLocation(path string) (string, error) {
	physical, err := (physicalpath.Filesystem{}).ResolveAbsolute(path)
	if err != nil {
		return "", fmt.Errorf("resolve registered location %q: %w", path, err)
	}
	return physical, nil
}

func physicalRegisteredLocationStrictlyWithin(base, candidate string) (bool, error) {
	if physicalpath.StrictlyWithin(base, candidate) {
		return true, nil
	}
	filesystem := physicalpath.Filesystem{}
	for ancestor := filepath.Dir(candidate); ; ancestor = filepath.Dir(ancestor) {
		equal, err := filesystem.Equivalent(base, ancestor)
		if err != nil {
			return false, fmt.Errorf("compare registered location ancestry: %w", err)
		}
		if equal {
			return true, nil
		}
		if parent := filepath.Dir(ancestor); parent == ancestor {
			return false, nil
		}
	}
}

// dynamicLocationRegistry resolves the XDG data path on first use, rather than
// during help, version, or CLI argument parsing. Do not cache registrations.
type dynamicLocationRegistry struct{}

func (dynamicLocationRegistry) List() (map[string]string, error) {
	store, err := defaultLocationRegistry()
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (dynamicLocationRegistry) Register(identity, path string) error {
	store, err := defaultLocationRegistry()
	if err != nil {
		return err
	}
	return store.Register(identity, path)
}

func (dynamicLocationRegistry) Relocate(identity, previous, next string) error {
	store, err := defaultLocationRegistry()
	if err != nil {
		return err
	}
	return store.Relocate(identity, previous, next)
}

func (dynamicLocationRegistry) Forget(identity string) error {
	store, err := defaultLocationRegistry()
	if err != nil {
		return err
	}
	return store.Forget(identity)
}

func (dynamicLocationRegistry) ForgetAt(identity, path string) error {
	store, err := defaultLocationRegistry()
	if err != nil {
		return err
	}
	return store.ForgetAt(identity, path)
}
