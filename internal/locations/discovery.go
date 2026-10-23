package locations

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/repospec"
)

// Discover reads only explicitly registered repositories. It does not scan the
// filesystem to grow the managed set, even under a configured repository root.
func (s Store) Discover(ctx context.Context, roots []string, _ ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
	entries, err := s.Read()
	if err != nil {
		return local.DiscoveryResult{}, err
	}
	result := local.DiscoveryResult{Repositories: make([]local.Repository, 0, len(entries))}
	for _, identity := range Identities(entries) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		parts, err := repospec.ParseIdentity(identity)
		if err != nil {
			return result, fmt.Errorf("registered repository %q: %w", identity, err)
		}
		path := entries[identity]
		repository := local.Repository{
			Identity:  identity,
			Host:      parts.Host,
			Owner:     parts.Owner,
			Repo:      parts.Repo,
			Path:      path,
			Root:      filepath.Dir(path),
			RootIndex: len(roots),
		}
		for index, root := range roots {
			expected := filepath.Join(root, parts.Host, parts.Owner, parts.Repo)
			if physicalpath.SamePath(expected, path) {
				repository.Root, repository.RootIndex = root, index
				break
			}
		}
		result.Repositories = append(result.Repositories, repository)
	}
	return result, nil
}
