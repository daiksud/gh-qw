package removal

import (
	"context"
	"errors"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestInventorySharesOnlyWithinPhaseAndProtectsSnapshots(t *testing.T) {
	source := &inventoryGit{records: []gitcmd.Worktree{{Path: "original"}}}
	first := NewInventory(source)
	records, err := first.WorktreeList(context.Background(), "repository")
	if err != nil {
		t.Fatal(err)
	}
	records[0].Path = "caller mutation"
	source.records[0].Path = "new registration"
	again, err := first.WorktreeList(context.Background(), "repository")
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || again[0].Path != "original" {
		t.Fatalf("calls=%d snapshot=%v, want one read and original", source.calls, again)
	}
	next, err := NewInventory(source).WorktreeList(context.Background(), "repository")
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 || next[0].Path != "new registration" {
		t.Fatalf("new phase calls=%d snapshot=%v", source.calls, next)
	}
}

func TestInventoryPreservesUnknownStateAndCancellation(t *testing.T) {
	want := errors.New("registration unavailable")
	source := &inventoryGit{err: want}
	phase := NewInventory(source)
	for range 2 {
		records, err := phase.WorktreeList(context.Background(), "repository")
		if !errors.Is(err, want) || len(records) != 0 {
			t.Fatalf("records=%v err=%v, want unknown state", records, err)
		}
	}
	if source.calls != 1 {
		t.Fatalf("calls=%d, want one failed observation", source.calls)
	}
	source.err = nil
	if _, err := NewInventory(source).WorktreeList(context.Background(), "repository"); err != nil {
		t.Fatalf("new phase retained old error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := phase.WorktreeList(ctx, "repository"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation=%v", err)
	}
}

type inventoryGit struct {
	records []gitcmd.Worktree
	err     error
	calls   int
}

func (git *inventoryGit) WorktreeList(ctx context.Context, _ string) ([]gitcmd.Worktree, error) {
	git.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]gitcmd.Worktree(nil), git.records...), git.err
}
func (*inventoryGit) OutputDir(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("unexpected Git output")
}
