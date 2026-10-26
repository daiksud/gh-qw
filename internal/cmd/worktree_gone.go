package cmd

import (
	"context"
	"fmt"

	"github.com/daiksud/gh-qw/internal/local"
)

type goneWorktreeCandidate struct {
	worktree local.Worktree
	upstream string
}

// goneWorktreeCandidates uses Git's upstream mapping and literal ref existence
// for both listing and removal. Removal applies its safety checks separately.
func goneWorktreeCandidates(ctx context.Context, repository local.Repository, worktrees []local.Worktree, git ListGit) ([]goneWorktreeCandidate, error) {
	attached := make(map[string]struct{}, len(worktrees))
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Detached || worktree.Branch == "" {
			continue
		}
		attached[worktree.Branch] = struct{}{}
	}

	upstreams, err := git.BranchUpstreams(ctx, repository.Path)
	if err != nil {
		return nil, fmt.Errorf("inspect branch upstreams for %q: %w", repository.Identity, err)
	}
	upstreamByBranch := make(map[string]string, len(attached))
	for _, upstream := range upstreams {
		if _, exists := attached[upstream.Branch]; !exists {
			continue
		}
		if err := local.ValidateBranch(ctx, upstream.Branch); err != nil {
			return nil, fmt.Errorf(
				"inspect branch upstreams for %q: invalid branch %q: %w",
				repository.Identity,
				upstream.Branch,
				err,
			)
		}
		if _, duplicate := upstreamByBranch[upstream.Branch]; duplicate {
			return nil, fmt.Errorf(
				"inspect branch upstreams for %q: duplicate branch %q",
				repository.Identity,
				upstream.Branch,
			)
		}
		upstreamByBranch[upstream.Branch] = upstream.Upstream
	}

	goneRefs := make(map[string]bool, len(upstreamByBranch))
	var candidates []goneWorktreeCandidate
	for _, worktree := range worktrees {
		if worktree.Main || worktree.Detached || worktree.Branch == "" {
			continue
		}
		upstream := upstreamByBranch[worktree.Branch]
		if upstream == "" {
			continue
		}
		gone, inspected := goneRefs[upstream]
		if !inspected {
			exists, refErr := git.RefExists(ctx, repository.Path, upstream)
			if refErr != nil {
				return nil, fmt.Errorf(
					"verify upstream ref %q for branch %q: %w",
					upstream,
					worktree.Branch,
					refErr,
				)
			}
			gone = !exists
			goneRefs[upstream] = gone
		}
		if gone {
			candidates = append(candidates, goneWorktreeCandidate{worktree: worktree, upstream: upstream})
		}
	}
	return candidates, nil
}
