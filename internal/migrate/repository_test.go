package migrate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestInspectRepositoryAcceptsOnlyOrdinaryMainWorktree(t *testing.T) {
	t.Parallel()

	base := backPointerTestPhysical(t, t.TempDir())
	main := filepath.Join(base, "main")
	linked := filepath.Join(base, "linked")
	bare := filepath.Join(base, "bare")
	nonGit := filepath.Join(base, "not-git")

	migrateRunGit(t, "", "init", "-q", main)
	migrateRunGit(t, main, "config", "user.email", "test@example.com")
	migrateRunGit(t, main, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(main, "README"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateRunGit(t, main, "add", "README")
	migrateRunGit(t, main, "commit", "-qm", "initial")
	migrateRunGit(t, main, "worktree", "add", "-qb", "linked", linked)
	migrateRunGit(t, "", "init", "--bare", "-q", bare)
	if err := os.Mkdir(nonGit, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	snapshot, err := InspectRepository(context.Background(), main, runner, Filesystem{})
	if err != nil {
		t.Fatalf("InspectRepository(main) error = %v", err)
	}
	if snapshot.Path != main || snapshot.GitDir != filepath.Join(main, ".git") {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	tests := []struct {
		name string
		path string
		want error
	}{
		{name: "linked", path: linked, want: ErrLinkedRepository},
		{name: "bare", path: bare, want: ErrBareRepository},
		{name: "non Git", path: nonGit, want: ErrNotRepository},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := InspectRepository(context.Background(), test.path, runner, Filesystem{})
			if !errors.Is(err, test.want) {
				t.Fatalf("InspectRepository() error = %v, want %v", err, test.want)
			}
			if _, statErr := os.Stat(test.path); statErr != nil {
				t.Fatalf("rejected path changed: %v", statErr)
			}
		})
	}
}
