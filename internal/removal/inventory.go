package removal

import (
	"context"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
)

// Inventory shares Git registrations within one sequential read-only phase.
// Create a new inventory after confirmation, discovery, or any mutation boundary.
// Other Git observations are always delegated directly.
type Inventory struct {
	local.Git
	registrations map[string]registrationResult
}

type registrationResult struct {
	records []gitcmd.Worktree
	err     error
}

// NewInventory starts a fresh registration observation phase.
func NewInventory(git local.Git) *Inventory {
	return &Inventory{Git: git, registrations: make(map[string]registrationResult)}
}

// WorktreeList preserves unknown states and returns an independent snapshot.
func (inventory *Inventory) WorktreeList(ctx context.Context, repository string) ([]gitcmd.Worktree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, found := inventory.registrations[repository]
	if !found {
		records, err := inventory.Git.WorktreeList(ctx, repository)
		result = registrationResult{records: append([]gitcmd.Worktree(nil), records...), err: err}
		inventory.registrations[repository] = result
	}
	return append([]gitcmd.Worktree(nil), result.records...), result.err
}
