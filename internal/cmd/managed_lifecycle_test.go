package cmd_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/registry"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/daiksud/gh-qw/internal/testenv"
	"github.com/daiksud/gh-qw/internal/testregistry"
)

func TestRemovePreservesArbitraryRegistrationParent(t *testing.T) {
	for _, location := range []string{"inside placement root", "outside placement root"} {
		t.Run(location, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			placement := filepath.Join(base, "repositories")
			worktrees := filepath.Join(placement, "worktrees")
			parent := filepath.Join(placement, "personal", "config")
			if location == "outside placement root" {
				parent = filepath.Join(base, "home", "config")
			}
			repository := filepath.Join(parent, ".dotfiles")
			for _, path := range []string{repository, worktrees} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			sibling := filepath.Join(filepath.Dir(parent), "keep")
			if err := os.WriteFile(sibling, []byte("unrelated data"), 0o600); err != nil {
				t.Fatal(err)
			}
			testenv.Git(t, repository, "init", "--initial-branch=main")
			testenv.Git(t, repository, "commit", "--allow-empty", "-m", "initial")
			store, err := registry.NewStoreAt(filepath.Join(base, "data", "repositories.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Register("github.com/acme/dotfiles", repository); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr lockedBuffer
			git := gitcmd.NewRunner()
			git.Env = testenv.Environment(t.TempDir())
			git.Stdout, git.Stderr = &stdout, &stderr
			prompts := 0
			command := cmd.NewRemoveCommand(cmd.RemoveDependencies{
				Resolver: managedRootResolver{result: rootpkg.Result{RepositoryRoot: placement, WorktreeRoot: worktrees}},
				Registry: store,
				Git:      git,
				Prompt: func(context.Context, io.Writer, string) (bool, error) {
					prompts++
					return true, nil
				},
				Stdout: &stdout,
				Stderr: &stderr,
			})
			command.SetArgs([]string{"acme/dotfiles"})
			if err := command.Execute(); err != nil {
				t.Fatalf("remove: %v\n%s", err, stderr.String())
			}
			if _, err := os.Lstat(repository); !os.IsNotExist(err) {
				t.Fatalf("removed repository still exists: %v", err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("empty immediate parent was not preserved: %v, %v", entries, err)
			}
			if data, err := os.ReadFile(sibling); err != nil || string(data) != "unrelated data" {
				t.Fatalf("unrelated sibling changed: %q, %v", data, err)
			}
			registered, err := store.List()
			if err != nil || len(registered) != 0 {
				t.Fatalf("removed repository remains registered: %v, %v", registered, err)
			}
			if prompts != 1 || stdout.String() != "" {
				t.Fatalf("remove prompts = %d, stdout = %q", prompts, stdout.String())
			}
		})
	}
}

func TestManagedRepositoryLifecycle(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "configured")
	repo := filepath.Join(root, "api")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	testenv.Git(t, "", "init", repo)
	testenv.Git(t, "", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	testenv.Git(t, "", "-C", repo, "remote", "add", "origin", "https://github.com/acme/api.git")

	store, err := registry.NewStoreAt(filepath.Join(base, "data", "ghqw", "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	roots := rootpkg.Result{RepositoryRoot: root, WorktreeRoot: filepath.Join(root, "worktrees")}

	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr lockedBuffer
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

	worktree := filepath.Join(root, "worktrees", "github-com-acme-api-review", "api")
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
	movedRepo := filepath.Join(root, "github.com", "acme", "api")
	if code, output, errText := run("add", "--move", repo); code != 0 || strings.TrimSpace(output) != filepath.ToSlash(movedRepo) {
		t.Fatalf("move registered repository = %d, %q, %q", code, output, errText)
	}
	registered, err = store.List()
	if err != nil || registered["github.com/acme/api"] != movedRepo {
		t.Fatalf("registry after move = %v, %v; want %q", registered, err, movedRepo)
	}

	if code, _, errText := run("forget", "acme/api"); code != 0 {
		t.Fatalf("forget = %d, %q", code, errText)
	}
	if code, output, errText := run("list", "--all"); code != 0 || output != "" {
		t.Fatalf("list after forget = %d, %q, %q", code, output, errText)
	}
	if _, err := os.Stat(filepath.Join(movedRepo, ".git")); err != nil {
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

func TestFlatAddRejectsInvalidWorktreeSlotBeforeRegistryRead(t *testing.T) {
	registryReads := 0
	code := cmd.Execute(context.Background(), []string{"add", "--worktree", "../escape"}, cmd.ApplicationDependencies{
		Registry: testregistry.From(func() (map[string]string, error) {
			registryReads++
			return map[string]string{}, nil
		}),
		Stdout: &lockedBuffer{},
		Stderr: &lockedBuffer{},
	})
	if code != 2 {
		t.Fatalf("exit status = %d, want 2", code)
	}
	if registryReads != 0 {
		t.Fatalf("registry calls = %d, want 0", registryReads)
	}
}

func TestCloneRejectsRegisteredLocationWithoutReuse(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := registry.NewStoreAt(filepath.Join(base, "data", "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "missing")
	if err := store.Register("github.com/acme/api", missing); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr lockedBuffer
	code := cmd.Execute(context.Background(), []string{"clone", "acme/api"}, cmd.ApplicationDependencies{
		Resolver: managedRootResolver{result: rootpkg.Result{
			RepositoryRoot: filepath.Join(base, "root"),
			WorktreeRoot:   filepath.Join(base, "root", "worktrees"),
		}},
		Registry: store,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if code != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "already registered") {
		t.Fatalf("registered clone = %d, %q, %q", code, stdout.String(), stderr.String())
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type managedRootResolver struct{ result rootpkg.Result }

func (m managedRootResolver) Resolve() (rootpkg.Result, error) { return m.result, nil }
