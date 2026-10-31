package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/daiksud/gh-qw/internal/fzfcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
)

// clonePreparation resolves inputs in order before handing any work to parallel
// workers. This keeps interactive selectors from sharing a terminal and keeps
// listing-only inputs independent of local configuration and registration.
type clonePreparation struct {
	deps           CloneDependencies
	ssh            bool
	warn           local.WarningSink
	stdout, stderr io.Writer
	roots          []string
	states         map[string]*cloneTarget
	fatalError     error
}

func (p *clonePreparation) prepare(ctx context.Context, input string) (*cloneParsedItem, *cloneTarget, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	original := input
	ownerOnly := input != "." && input != ".." && !strings.ContainsAny(input, "/:@\\")
	if ownerOnly {
		if err := repospec.ValidateOwner(input); err != nil {
			return nil, nil, cloneUsageError(&repospec.UsageError{Input: input, Err: err})
		}
		selected, err := cloneSelectRepository(ctx, cloneDefaultHost(), input, "", p.deps, p.stdout, p.stderr)
		if err != nil || selected == "" {
			return nil, nil, err
		}
		input = selected
	}

	parsed, err := repospec.Parse(input)
	if err != nil {
		return nil, nil, cloneUsageError(err)
	}
	if p.roots == nil {
		p.roots, err = cloneResolveRoots(p.deps.RootResolver)
		if err != nil {
			p.fatalError = err
			return nil, nil, err
		}
	}
	spec, err := cloneResolveRepository(ctx, parsed, p.roots, p.ssh, p.deps)
	if err != nil {
		if errors.Is(err, repospec.ErrUsage) {
			err = cloneUsageError(err)
		}
		return nil, nil, err
	}
	if p.states == nil {
		discovery, err := p.deps.Discover(ctx, p.roots)
		if err != nil {
			p.fatalError = fmt.Errorf("discover existing repositories: %w", err)
			return nil, nil, p.fatalError
		}
		if err := cloneEmitDiscoveryWarnings(discovery.Warnings, p.warn, p.stderr); err != nil {
			p.fatalError = fmt.Errorf("write discovery warning: %w", err)
			return nil, nil, p.fatalError
		}
		p.states = cloneInitialTargetStates(discovery.Repositories)
	}
	state, err := cloneResolveTarget(p.states, spec, p.roots[0])
	if err != nil {
		return nil, nil, err
	}
	if err := cloneCheckTarget(state, p.deps); err != nil {
		return nil, nil, err
	}

	// Explicit transports and local identity forms retain their direct clone
	// behavior. Only owner/repo shorthand asks for discovery on a missing name.
	base, _, _ := strings.Cut(original, "@")
	shorthand := !ownerOnly && strings.Count(base, "/") == 1 &&
		!strings.ContainsAny(base, ":\\") && !strings.HasPrefix(base, "./") && !strings.HasPrefix(base, "../")
	if shorthand {
		exists, err := p.deps.RepositoryAPI.RepositoryExists(ctx, cloneDefaultHost(), spec.Owner, spec.Repo)
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			selected, err := cloneSelectRepository(ctx, cloneDefaultHost(), spec.Owner, spec.Repo, p.deps, p.stdout, p.stderr)
			if err != nil || selected == "" {
				return nil, nil, err
			}
			selection, err := repospec.Parse(selected)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid selected repository: %w", err)
			}
			branch := spec.Branch
			spec, err = cloneResolveRepository(ctx, selection, p.roots, p.ssh, p.deps)
			if err != nil {
				return nil, nil, err
			}
			spec.Branch = branch
			state, err = cloneResolveTarget(p.states, spec, p.roots[0])
			if err != nil {
				return nil, nil, err
			}
			if err := cloneCheckTarget(state, p.deps); err != nil {
				return nil, nil, err
			}
		}
	}
	return &cloneParsedItem{input: original, spec: spec}, state, nil
}

func cloneDefaultHost() string {
	host := strings.ToLower(strings.TrimSpace(os.Getenv("GH_HOST")))
	if host == "" {
		return "github.com"
	}
	return host
}

func cloneSelectRepository(ctx context.Context, host, owner, query string, deps CloneDependencies, stdout, stderr io.Writer) (string, error) {
	candidates, err := deps.RepositoryAPI.OwnerRepositories(ctx, host, owner)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", commandWriteAll(stderr, []byte(fmt.Sprintf("No repositories found for %s.\n", owner)))
	}
	if _, err := deps.LookPath("fzf"); err != nil {
		if !errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("find fzf: %w", err)
		}
		for _, candidate := range candidates {
			_, repo, _ := strings.Cut(candidate, "/")
			if strings.Contains(repo, query) {
				if err := commandWriteAll(stdout, []byte(candidate+"\n")); err != nil {
					return "", fmt.Errorf("write repository candidate: %w", err)
				}
			}
		}
		return "", nil
	}
	selected, err := deps.Select(ctx, candidates, query)
	if err != nil {
		switch {
		case errors.Is(err, fzfcmd.ErrCanceled):
			return "", newSilentStatusError(130)
		case errors.Is(err, fzfcmd.ErrNoMatch):
			return "", newSilentStatusError(1)
		default:
			return "", fmt.Errorf("select repository to clone: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, candidate := range candidates {
		if selected == candidate {
			return selected, nil
		}
	}
	return "", errors.New("selected repository was not offered")
}
