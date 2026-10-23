package cmd_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
)

func managedRunGit(t *testing.T, path string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func managedRepositoryFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "data")
	root := filepath.Join(base, "managed")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("GHQW_ROOT", root)
	t.Setenv("GHQW_WORKTREE_ROOT", filepath.Join(data, "ghqw", "worktrees"))
	path := filepath.Join(base, "outside", "api")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	managedRunGit(t, path, "init", "-b", "main")
	managedRunGit(t, path, "config", "user.name", "Test")
	managedRunGit(t, path, "config", "user.email", "test@example.com")
	managedRunGit(t, path, "commit", "--allow-empty", "-m", "initial")
	managedRunGit(t, path, "remote", "add", "origin", "https://github.com/acme/api.git")
	return path, root
}

func managedExecute(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	status := cmd.Execute(context.Background(), args, cmd.ApplicationDependencies{
		Stdin: strings.NewReader(""),
		Stdout: &out,
		Stderr: &diag,
	})
	return status, out.String(), diag.String()
}

func TestRegisteredRepositoryIsTheOnlyManagedSet(t *testing.T) {
	path, _ := managedRepositoryFixture(t)
	status, output, diag := managedExecute(t, "list")
	if status != 0 || output != "" || diag != "" {
		t.Fatalf("unregistered list: %d, %q, %q", status, output, diag)
	}
	status, output, diag = managedExecute(t, "add", path)
	if status != 0 || strings.TrimSpace(output) != filepath.ToSlash(path) || diag != "" {
		t.Fatalf("add: %d, %q, %q", status, output, diag)
	}
	status, output, diag = managedExecute(t, "list")
	if status != 0 || output != "github.com/acme/api\n" || diag != "" {
		t.Fatalf("list registered: %d, %q, %q", status, output, diag)
	}
	status, output, diag = managedExecute(t, "list", "--worktree")
	if status != 0 || output != "" || diag != "" {
		t.Fatalf("list worktree-only: %d, %q, %q", status, output, diag)
	}
	status, output, diag = managedExecute(t, "list", "--all")
	if status != 0 || output != "github.com/acme/api\n" || diag != "" {
		t.Fatalf("list all: %d, %q, %q", status, output, diag)
	}
	if status, _, _ := managedExecute(t, "list", "--all", "--worktree"); status != 2 {
		t.Fatal("--all and --worktree must conflict")
	}
	status, output, diag = managedExecute(t, "forget", "api")
	if status != 0 || output != "" || diag != "" {
		t.Fatalf("forget: %d, %q, %q", status, output, diag)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("forget mutated repository: %v", err)
	}
	status, output, diag = managedExecute(t, "list", "--all")
	if status != 0 || output != "" || diag != "" {
		t.Fatalf("forgotten location reappeared: %d, %q, %q", status, output, diag)
	}
}

func TestAddMoveRegistersNewLocation(t *testing.T) {
	path, root := managedRepositoryFixture(t)
	status, output, diag := managedExecute(t, "add", "--move", path)
	destination := filepath.Join(root, "github.com", "acme", "api")
	if status != 0 || strings.TrimSpace(output) != filepath.ToSlash(destination) || diag != "" {
		t.Fatalf("add --move: %d, %q, %q", status, output, diag)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old path was not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("moved destination: %v", err)
	}
	status, output, diag = managedExecute(t, "list", "--full-path")
	if status != 0 || strings.TrimSpace(output) != filepath.ToSlash(destination) || diag != "" {
		t.Fatalf("list moved path: %d, %q, %q", status, output, diag)
	}
}
