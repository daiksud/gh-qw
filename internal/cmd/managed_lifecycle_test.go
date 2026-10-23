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
	"github.com/daiksud/gh-qw/internal/registry"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func runGit(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestManagedRepositoryLifecycle(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "outside", "api")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, "init", repo)
	runGit(t, "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	runGit(t, "-C", repo, "remote", "add", "origin", "https://github.com/acme/api.git")

	store, err := registry.NewStoreAt(filepath.Join(base, "data", "ghqw", "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "configured")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	roots := rootpkg.Result{RepositoryRoots: []string{root}, WorktreeRoot: filepath.Join(base, "worktrees")}

	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := cmd.Execute(context.Background(), args, cmd.ApplicationDependencies{
			Resolver: managedRootResolver{result: roots},
			Registry: store,
			Stdout:   &stdout,
			Stderr:   &stderr,
			Stdin:    strings.NewReader(""),
		})
		return code, stdout.String(), stderr.String()
	}

	// Existing directories are not automatically imported.
	if code, output, errText := run("list"); code != 0 || output != "" {
		t.Fatalf("unregistered list = %d, %q, %q", code, output, errText)
	}
	if code, output, errText := run("add", repo); code != 0 {
		t.Fatalf("add = %d, %q, %q", code, output, errText)
	}
	registered, err := store.List()
	if err != nil || len(registered) != 1 || registered["github.com/acme/api"] == "" {
		t.Fatalf("registry = %v, %v", registered, err)
	}
	if code, output, errText := run("list"); code != 0 || strings.TrimSpace(output) != "github.com/acme/api" {
		t.Fatalf("list after add = %d, %q, %q", code, output, errText)
	}
	if code, output, errText := run("list", "other"); code != 0 || output != "" {
		t.Fatalf("filtered list = %d, %q, %q", code, output, errText)
	}
	if code, output, errText := run("list", "--worktree"); code != 0 || output != "" {
		t.Fatalf("worktree-only list = %d, %q, %q", code, output, errText)
	}
	if code, _, errText := run("list", "--worktree", "--all"); code != 2 || !strings.Contains(errText, "mutually exclusive") {
		t.Fatalf("incompatible list modes = %d, %q", code, errText)
	}

	worktree := filepath.Join(base, "worktrees", "github.com", "acme", "api", "review")
	if code, output, errText := run("add", "--worktree", "-R", "acme/api", "-b", "review", "HEAD"); code != 0 ||
		strings.TrimSpace(output) != filepath.ToSlash(worktree) {
		t.Fatalf("add linked worktree = %d, %q, %q", code, output, errText)
	}
	if code, output, errText := run("list", "--worktree"); code != 0 || strings.TrimSpace(output) != "github.com/acme/api@review" {
		t.Fatalf("linked list = %d, %q, %q", code, output, errText)
	}
	if code, output, errText := run("list", "--all"); code != 0 ||
		!strings.Contains(output, "github.com/acme/api\n") ||
		!strings.Contains(output, "github.com/acme/api@review\n") {
		t.Fatalf("combined list = %d, %q, %q", code, output, errText)
	}

	if code, _, errText := run("forget", "acme/api"); code != 0 {
		t.Fatalf("forget = %d, %q", code, errText)
	}
	if code, output, errText := run("list", "--all"); code != 0 || output != "" {
		t.Fatalf("list after forget = %d, %q, %q", code, output, errText)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("forget deleted repository: %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("forget deleted worktree: %v", err)
	}
	// Reconstructing an empty registry must not rediscover the checkout.
	other, err := registry.NewStoreAt(filepath.Join(base, "data", "ghqw", "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := other.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty registry was re-populated: %v, %v", entries, err)
	}
}

type managedRootResolver struct{ result rootpkg.Result }

func (m managedRootResolver) Resolve() (rootpkg.Result, error) { return m.result, nil }
