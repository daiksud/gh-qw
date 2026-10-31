package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/daiksud/gh-qw/internal/fzfcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/removal"
)

// removeCandidate keeps display text separate from the identity and filesystem
// observations that must survive the interactive selection boundary.
type removeCandidate struct {
	row        string
	repository local.Repository
	worktree   local.Worktree
	nodes      []removal.Node
}

func removeSelectTarget(ctx context.Context, commandRuntime removeRuntime) (*removeCandidate, error) {
	candidates, err := removeCandidates(ctx, commandRuntime)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, commandWriteAll(commandRuntime.stderr, []byte("No removal candidates.\n"))
	}
	rows := make([]string, len(candidates))
	for index, candidate := range candidates {
		rows[index] = candidate.row
	}
	selected, err := commandRuntime.selectTarget(ctx, rows)
	if err != nil {
		switch {
		case errors.Is(err, fzfcmd.ErrCanceled):
			return nil, newSilentStatusError(130)
		case errors.Is(err, fzfcmd.ErrNoMatch):
			return nil, newSilentStatusError(1)
		default:
			return nil, fmt.Errorf("select removal target: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range candidates {
		if candidates[index].row == selected {
			return &candidates[index], nil
		}
	}
	return nil, fmt.Errorf("%w: selected removal target was not offered", ErrRemoveSafety)
}

func removeCandidates(ctx context.Context, commandRuntime removeRuntime) ([]removeCandidate, error) {
	roots, err := commandRuntime.resolver.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve roots: %w", err)
	}
	discovery, discoveryErr := commandRuntime.discover(ctx, roots.RepositoryRoots, local.DiscoveryOptions{
		Git: commandRuntime.git, Filesystem: commandRuntime.filesystem,
	})
	warningErr := commandWriteWarnings(commandRuntime.stderr, discovery.Warnings)
	if discoveryErr != nil || warningErr != nil {
		return nil, errors.Join(
			removeWrapError("discover repositories", discoveryErr),
			removeWrapError("write discovery warnings", warningErr),
		)
	}

	var candidates []removeCandidate
	var entries []listEntry
	for _, repository := range discovery.Repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A canonical identity must still resolve uniquely when removal starts.
		if _, err := local.ResolveRepositoryForMutation(discovery.Repositories, repository.Identity); err != nil {
			return nil, err
		}
		mainNodes, err := removeCaptureCandidateNodes(commandRuntime.fs.observations(), repository.Path, removal.Directory)
		if err != nil {
			return nil, fmt.Errorf("observe removal candidate %q: %w", repository.Identity, err)
		}
		worktrees, err := commandRuntime.enumerate(ctx, repository, roots.WorktreeRoot, local.WorktreeOptions{
			Lister: commandRuntime.git, Filesystem: commandRuntime.filesystem,
		})
		if err != nil {
			return nil, fmt.Errorf("enumerate worktrees for %q: %w", repository.Identity, err)
		}
		mainCount := 0
		for _, worktree := range worktrees {
			if worktree.Main {
				mainCount++
				if worktree.Slot != "" || !physicalpath.SamePath(worktree.Path, repository.Path) {
					return nil, fmt.Errorf("%w: main worktree does not match repository %q", ErrRemoveSafety, repository.Identity)
				}
			} else {
				if worktree.Prunable {
					continue
				}
				expected, err := local.WorktreePath(ctx, roots.WorktreeRoot, repository.Host, repository.Owner, repository.Repo, worktree.Slot)
				if err != nil {
					return nil, err
				}
				// Git can also report external worktrees; this picker only offers
				// targets accepted by the existing managed-worktree removal path.
				if !physicalpath.SamePath(worktree.Path, expected) {
					continue
				}
			}
			entry, _, err := listWorktreeEntry(ctx, repository, worktree)
			if err != nil {
				return nil, err
			}
			nodes := append([]removal.Node(nil), mainNodes...)
			if !worktree.Main {
				linkedNodes, err := removeCaptureCandidateNodes(commandRuntime.fs.observations(), worktree.Path, removal.RegularFile)
				if err != nil {
					return nil, fmt.Errorf("observe removal candidate %q: %w", worktree.Identity, err)
				}
				nodes = append(nodes, linkedNodes...)
			}
			entries = append(entries, entry)
			candidates = append(candidates, removeCandidate{repository: repository, worktree: worktree, nodes: nodes})
		}
		if mainCount != 1 {
			return nil, fmt.Errorf("%w: Git reported %d main worktrees for %q", ErrRemoveSafety, mainCount, repository.Identity)
		}
	}
	listDisambiguateLabels(entries)
	for index := range candidates {
		kind := "worktree"
		if candidates[index].worktree.Main {
			kind = "repository"
		}
		// Quote paths so embedded tabs, newlines, and terminal controls remain
		// visible text within one candidate rather than changing the display.
		candidates[index].row = fmt.Sprintf("%s\t%s\t%q", kind, entries[index].displayIdentity(), removeOutputPath(entries[index].path))
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].worktree.Identity < candidates[right].worktree.Identity
	})
	return candidates, nil
}

func removeCaptureCandidateNodes(filesystem removal.Filesystem, path string, gitKind removal.Kind) ([]removal.Node, error) {
	directory, err := removal.CaptureNode(filesystem, "", path, removal.Directory, false)
	if err != nil {
		return nil, err
	}
	git, err := removal.CaptureNode(filesystem, path, filepath.Join(path, ".git"), gitKind, true)
	if err != nil {
		return nil, err
	}
	return []removal.Node{directory, git}, nil
}

func (candidate removeCandidate) revalidate(filesystem removal.Filesystem, repository local.Repository, worktree local.Worktree) error {
	if candidate.repository != repository || !removal.SameWorktree(candidate.worktree, worktree) {
		return fmt.Errorf("%w: selected removal target changed while fzf was open", ErrRemoveSafety)
	}
	for _, node := range candidate.nodes {
		if err := removal.RevalidateNode(filesystem, node, "selected removal target"); err != nil {
			return err
		}
	}
	return nil
}
