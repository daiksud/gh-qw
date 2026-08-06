package migrate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestPlanAndApplyBackPointers(t *testing.T) {
	t.Parallel()

	base := backPointerTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destination := filepath.Join(base, "destination")
	external := filepath.Join(base, "external")
	internalGit := filepath.Join(source, "inside", ".git")
	externalGit := filepath.Join(external, ".git")
	for _, path := range []string{
		filepath.Join(source, ".git", "worktrees", "a"),
		filepath.Join(source, ".git", "worktrees", "b"),
		filepath.Dir(internalGit),
		filepath.Dir(externalGit),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(internalGit, []byte("pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalGit, []byte("pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	internalPointer := filepath.Join(source, ".git", "worktrees", "a", "gitdir")
	externalPointer := filepath.Join(source, ".git", "worktrees", "b", "gitdir")
	if err := os.WriteFile(internalPointer, []byte(filepath.ToSlash(internalGit)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalPointer, []byte(filepath.ToSlash(externalGit)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanBackPointers(source, destination, Filesystem{})
	if err != nil {
		t.Fatalf("PlanBackPointers() error = %v", err)
	}
	wantPaths := []string{
		filepath.Join(destination, "inside"),
		external,
	}
	if got := plan.RepairPaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("repair paths = %#v, want %#v", got, wantPaths)
	}
	if err := plan.Revalidate(Filesystem{}); err != nil {
		t.Fatalf("Revalidate() error = %v", err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}
	if err := plan.ApplyBackPointers(Filesystem{}); err != nil {
		t.Fatalf("ApplyBackPointers() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destination, ".git", "worktrees", "a", "gitdir"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), filepath.ToSlash(filepath.Join(destination, "inside", ".git"))+"\n"; got != want {
		t.Fatalf("internal back-pointer = %q, want %q", got, want)
	}
	content, err = os.ReadFile(filepath.Join(destination, ".git", "worktrees", "b", "gitdir"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), filepath.ToSlash(externalGit)+"\n"; got != want {
		t.Fatalf("external back-pointer = %q, want %q", got, want)
	}
}

func TestBackPointerRevalidationDetectsChanges(t *testing.T) {
	t.Parallel()

	base := backPointerTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destination := filepath.Join(base, "destination")
	entry := filepath.Join(source, ".git", "worktrees", "one")
	worktreeGit := filepath.Join(base, "worktree", ".git")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreeGit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worktreeGit, []byte("pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(entry, "gitdir")
	if err := os.WriteFile(pointer, []byte(filepath.ToSlash(worktreeGit)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanBackPointers(source, destination, Filesystem{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pointer, []byte(filepath.ToSlash(filepath.Join(base, "other", ".git"))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plan.Revalidate(Filesystem{}); err == nil {
		t.Fatal("Revalidate() error = nil, want metadata change")
	}
}

func TestRealGitWorktreeRepairAfterMainMove(t *testing.T) {
	t.Parallel()

	base := backPointerTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination-root")
	destination := filepath.Join(destinationRoot, "github.com", "owner", "repo")
	external := filepath.Join(base, "external")
	internal := filepath.Join(source, "internal")

	migrateRunGit(t, "", "init", "-q", source)
	migrateRunGit(t, source, "config", "user.email", "test@example.com")
	migrateRunGit(t, source, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "README"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateRunGit(t, source, "add", "README")
	migrateRunGit(t, source, "commit", "-qm", "initial")
	migrateRunGit(t, source, "worktree", "add", "-qb", "internal", internal)
	migrateRunGit(t, source, "worktree", "add", "-qb", "external", external)
	if err := os.Mkdir(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanBackPointers(source, destination, Filesystem{})
	if err != nil {
		t.Fatalf("PlanBackPointers() error = %v", err)
	}
	if err := Move(source, destination, MoveOptions{
		DestinationRoot: destinationRoot,
		Filesystem: Filesystem{
			Rename: func(oldPath, newPath string) error {
				return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
			},
		},
	}); err != nil {
		t.Fatalf("Move() error = %v", err)
	}
	if err := plan.ApplyBackPointers(Filesystem{}); err != nil {
		t.Fatalf("ApplyBackPointers() error = %v", err)
	}
	runner := &gitcmd.Runner{Executable: "git"}
	if err := runner.WorktreeRepair(context.Background(), destination, plan.RepairPaths()...); err != nil {
		t.Fatalf("WorktreeRepair() error = %v", err)
	}

	movedInternal := filepath.Join(destination, "internal")
	for _, worktree := range []string{movedInternal, external} {
		output := migrateRunGit(t, worktree, "rev-parse", "--git-common-dir")
		common := strings.TrimSpace(output)
		if !filepath.IsAbs(common) {
			common = filepath.Join(worktree, common)
		}
		resolved, err := filepath.EvalSymlinks(common)
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(filepath.Join(destination, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		if resolved != want {
			t.Fatalf("common dir for %q = %q, want %q", worktree, resolved, want)
		}
	}
}

func migrateRunGit(t *testing.T, dir string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func backPointerTestPhysical(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
