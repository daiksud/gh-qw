package migrate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestRealGitWorktreeRepairAfterMainMove(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
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

	runner := &gitcmd.Runner{Executable: "git"}
	plan, err := PlanWorktreeRepair(context.Background(), source, destination, runner, Filesystem{})
	if err != nil {
		t.Fatalf("PlanWorktreeRepair() error = %v", err)
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

func migrateTestPhysical(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func TestWorktreeRepairPlanRevalidatesMissingPaths(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name                                   string
		absentBefore, createAfter, removeAfter bool
		wantError                              bool
	}{
		{name: "unchanged missing", absentBefore: true},
		{name: "missing becomes present", absentBefore: true, createAfter: true, wantError: true},
		{name: "present becomes missing", removeAfter: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := migrateTestPhysical(t, t.TempDir())
			source, external := filepath.Join(base, "source"), filepath.Join(base, "external")
			migrateRunGit(t, "", "init", "-q", source)
			migrateRunGit(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
			migrateRunGit(t, source, "worktree", "add", "-qb", "external", external)
			if scenario.absentBefore {
				if err := os.RemoveAll(external); err != nil {
					t.Fatal(err)
				}
			}
			runner := &gitcmd.Runner{Executable: "git"}
			plan, err := PlanWorktreeRepair(context.Background(), source, filepath.Join(base, "destination"), runner, Filesystem{})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if got := plan.RepairPaths(); len(got) != 1 || got[0] != external {
				t.Fatalf("repair paths=%q, want missing registration retained", got)
			}
			if scenario.createAfter {
				if err := os.Mkdir(external, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.removeAfter {
				if err := os.RemoveAll(external); err != nil {
					t.Fatal(err)
				}
			}
			err = plan.Revalidate(context.Background(), runner, Filesystem{})
			if (err != nil) != scenario.wantError {
				t.Fatalf("revalidate error=%v, want error=%v", err, scenario.wantError)
			}
		})
	}
}
