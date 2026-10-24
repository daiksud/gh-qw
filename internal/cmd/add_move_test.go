package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	movementpkg "github.com/daiksud/gh-qw/internal/movement"
	"github.com/daiksud/gh-qw/internal/registry"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func newAddMoveCommand(t *testing.T, move cmd.RepositoryMoveDependencies) interface {
	SetArgs([]string)
	Execute() error
} {
	t.Helper()
	store, err := registry.NewStoreAt(filepath.Join(t.TempDir(), "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cmd.NewAddCommand(cmd.AddDependencies{
		Resolver: move.Resolver,
		Git:      move.Git,
		Move:     move,
		Registry: store,
		Stdout:   move.Stdout,
		Stderr:   move.Stderr,
		Getwd:    move.Getwd,
	})
}

type moveRootResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver *moveRootResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

type moveRepairCall struct {
	dir   string
	paths []string
}

type moveTestGit struct {
	runner       *gitcmd.Runner
	repairErr    error
	beforeRepair func(string, []string)
	repairs      []moveRepairCall
}

func (git *moveTestGit) Output(ctx context.Context, arguments ...string) ([]byte, error) {
	return git.runner.Output(ctx, arguments...)
}

func (git *moveTestGit) OutputDir(
	ctx context.Context,
	dir string,
	arguments ...string,
) ([]byte, error) {
	return git.runner.OutputDir(ctx, dir, arguments...)
}

func (git *moveTestGit) WorktreeRepair(
	ctx context.Context,
	dir string,
	paths ...string,
) error {
	if git.beforeRepair != nil {
		git.beforeRepair(dir, paths)
	}
	git.repairs = append(git.repairs, moveRepairCall{
		dir:   dir,
		paths: append([]string(nil), paths...),
	})
	if git.repairErr != nil {
		return git.repairErr
	}
	return git.runner.WorktreeRepair(ctx, dir, paths...)
}

type moveLockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (writer *moveLockedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(data)
}

func moveTestGitRunner(stderr io.Writer) *moveTestGit {
	output := &moveLockedWriter{writer: stderr}
	return &moveTestGit{
		runner: &gitcmd.Runner{
			Executable: "git",
			Stdout:     output,
			Stderr:     output,
		},
	}
}

func moveTestInitRepository(t *testing.T, path, remote string) {
	t.Helper()
	moveTestRunGit(t, "", "init", "-q", path)
	moveTestRunGit(t, path, "config", "user.email", "test@example.com")
	moveTestRunGit(t, path, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	moveTestRunGit(t, path, "add", "README")
	moveTestRunGit(t, path, "commit", "-qm", "initial")
	if remote != "" {
		moveTestRunGit(t, path, "remote", "add", "origin", remote)
	}
}

func moveTestRunGit(t *testing.T, dir string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func moveTestPhysical(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func TestAddMoveDelegatesWorktreeRepairToGit(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name                            string
		internal, external, crossDevice bool
	}{
		{name: "main only"}, {name: "external", external: true}, {name: "nested internal", internal: true},
		{name: "mixed", internal: true, external: true}, {name: "mixed cross device", internal: true, external: true, crossDevice: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := moveTestPhysical(t, t.TempDir())
			source, primary := filepath.Join(base, "source"), filepath.Join(base, "primary")
			destination := filepath.Join(primary, "github.com", "acme", "widget")
			if err := os.Mkdir(primary, 0755); err != nil {
				t.Fatal(err)
			}
			moveTestInitRepository(t, source, "https://github.com/acme/widget")
			var before, after []string
			if scenario.internal {
				before = append(before, filepath.Join(source, "nested", "internal"))
				after = append(after, filepath.Join(destination, "nested", "internal"))
			}
			if scenario.external {
				before = append(before, filepath.Join(base, "external"))
				after = append(after, filepath.Join(base, "external"))
			}
			originalPointers := map[string][]byte{}
			for _, path := range before {
				branch := filepath.Base(path)
				moveTestRunGit(t, source, "worktree", "add", "-qb", branch, path)
				admin := strings.TrimSpace(moveTestRunGit(t, path, "rev-parse", "--absolute-git-dir"))
				rel, err := filepath.Rel(source, filepath.Join(admin, "gitdir"))
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(filepath.Join(source, rel))
				if err != nil {
					t.Fatal(err)
				}
				originalPointers[rel] = content
			}
			var stdout, stderr bytes.Buffer
			git := moveTestGitRunner(&stderr)
			git.beforeRepair = func(dir string, paths []string) {
				if dir != destination {
					t.Fatalf("repair dir=%q, want %q", dir, destination)
				}
				got, want := append([]string(nil), paths...), append([]string(nil), after...)
				sort.Strings(got)
				sort.Strings(want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("repair paths=%q, want %q", got, want)
				}
				for rel, want := range originalPointers {
					got, err := os.ReadFile(filepath.Join(destination, rel))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("gh-qw rewrote %s before Git repair: got %q, want original %q", rel, got, want)
					}
				}
			}
			filesystem := movementpkg.Filesystem{}
			if scenario.crossDevice {
				filesystem.Rename = func(old, new string) error {
					return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
				}
			}
			command := newAddMoveCommand(t, cmd.RepositoryMoveDependencies{
				Resolver: &moveRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
				Git:      git, Filesystem: filesystem, Stdout: &stdout, Stderr: &stderr,
			})
			command.SetArgs([]string{"--move", source})
			if err := command.Execute(); err != nil {
				t.Fatalf("repository move: %v; stderr=%s", err, stderr.String())
			}
			if stdout.String() != filepath.ToSlash(destination)+"\n" {
				t.Fatalf("stdout=%q", stdout.String())
			}
			wantCalls := 0
			if len(after) > 0 {
				wantCalls = 1
			}
			if len(git.repairs) != wantCalls {
				t.Fatalf("repair calls=%d, want %d", len(git.repairs), wantCalls)
			}
			wantPaths := append([]string{destination}, after...)
			sort.Strings(wantPaths)
			for _, path := range append([]string{destination}, after...) {
				entries, err := git.runner.WorktreeList(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, entry := range entries {
					got = append(got, filepath.FromSlash(entry.Path))
				}
				sort.Strings(got)
				if !reflect.DeepEqual(got, wantPaths) {
					t.Fatalf("registered paths from %q=%q, want %q", path, got, wantPaths)
				}
				common := strings.TrimSpace(moveTestRunGit(t, path, "rev-parse", "--path-format=absolute", "--git-common-dir"))
				if moveTestPhysical(t, common) != filepath.Join(destination, ".git") {
					t.Fatalf("common dir=%q, want destination .git", common)
				}
				moveTestRunGit(t, path, "status", "--porcelain")
			}
		})
	}
}

func (git *moveTestGit) WorktreeList(ctx context.Context, dir string) ([]gitcmd.Worktree, error) {
	return git.runner.WorktreeList(ctx, dir)
}

func TestAddMoveRejectsLinkedGitPointerCrossingSourceBoundary(t *testing.T) {
	t.Parallel()
	base := moveTestPhysical(t, t.TempDir())
	source, primary, external := filepath.Join(base, "source"), filepath.Join(base, "primary"), filepath.Join(base, "external")
	if err := os.Mkdir(primary, 0755); err != nil {
		t.Fatal(err)
	}
	moveTestInitRepository(t, source, "https://github.com/acme/widget")
	moveTestRunGit(t, source, "worktree", "add", "-qb", "external", external)
	pointer := filepath.Join(external, ".git")
	content, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(source, "external-pointer")
	if err := os.WriteFile(inside, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pointer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, pointer); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	git := moveTestGitRunner(io.Discard)
	command := newAddMoveCommand(t, cmd.RepositoryMoveDependencies{
		Resolver: &moveRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}}, Git: git, Stdout: io.Discard, Stderr: io.Discard,
	})
	command.SetArgs([]string{"--move", source})
	if err := command.Execute(); err == nil {
		t.Fatal("repository move succeeded despite crossing Git pointer")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was moved before rejecting crossing pointer: %v", err)
	}
	if len(git.repairs) != 0 {
		t.Fatalf("repair calls=%d, want zero", len(git.repairs))
	}
	if _, err := os.Stat(filepath.Join(primary, "github.com", "acme", "widget")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists: %v", err)
	}
}

func TestAddMoveRejectsExternalAdministrativeSymlinks(t *testing.T) {
	for _, target := range []string{"worktrees", "entry", "gitdir"} {
		t.Run(target, func(t *testing.T) {
			base := moveTestPhysical(t, t.TempDir())
			source, primary := filepath.Join(base, "source"), filepath.Join(base, "primary")
			if err := os.Mkdir(primary, 0755); err != nil {
				t.Fatal(err)
			}
			moveTestInitRepository(t, source, "https://github.com/acme/widget")
			moveTestRunGit(t, source, "worktree", "add", "-qb", "linked", filepath.Join(source, "linked"))
			admin := filepath.Join(source, ".git", "worktrees")
			path := admin
			if target != "worktrees" {
				path = filepath.Join(admin, "linked")
			}
			if target == "gitdir" {
				path = filepath.Join(path, "gitdir")
			}
			outside := filepath.Join(base, "outside")
			if err := os.Rename(path, outside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			pointer := filepath.Join(admin, "linked", "gitdir")
			before, err := os.ReadFile(pointer)
			if err != nil {
				t.Fatal(err)
			}
			git := moveTestGitRunner(io.Discard)
			command := newAddMoveCommand(t, cmd.RepositoryMoveDependencies{
				Resolver: &moveRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
				Git:      git, Stdout: io.Discard, Stderr: io.Discard,
			})
			command.SetArgs([]string{"--move", source})
			err = command.Execute()
			if err == nil {
				t.Fatal("repository move accepted external administrative symlink")
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("source moved before rejecting metadata: %v", err)
			}
			if len(git.repairs) != 0 {
				t.Fatalf("repair calls=%d, want none", len(git.repairs))
			}
			if _, err := os.Stat(filepath.Join(primary, "github.com", "acme", "widget")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination exists: %v", err)
			}
			after, err := os.ReadFile(pointer)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("external metadata changed: %v", err)
			}
		})
	}
}

func TestAddMoveRepairFailureIncludesMovedWorktreeArguments(t *testing.T) {
	base := moveTestPhysical(t, t.TempDir())
	source, primary := filepath.Join(base, "source"), filepath.Join(base, "primary")
	if err := os.Mkdir(primary, 0755); err != nil {
		t.Fatal(err)
	}
	moveTestInitRepository(t, source, "https://github.com/acme/widget")
	moveTestRunGit(t, source, "worktree", "add", "-qb", "linked", filepath.Join(source, "linked space"))
	git := moveTestGitRunner(io.Discard)
	git.repairErr = errors.New("repair failed before modifying metadata")
	command := newAddMoveCommand(t, cmd.RepositoryMoveDependencies{
		Resolver: &moveRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
		Git:      git, Stdout: io.Discard, Stderr: io.Discard,
	})
	command.SetArgs([]string{"--move", source})
	err := command.Execute()
	destination := filepath.Join(primary, "github.com", "acme", "widget")
	moved := filepath.Join(destination, "linked space")
	if !errors.Is(err, git.repairErr) || !strings.Contains(err.Error(), fmt.Sprintf("%q", moved)) {
		t.Fatalf("recovery error=%v, want mapped worktree argument %q", err, moved)
	}
	if len(git.repairs) != 1 || !reflect.DeepEqual(git.repairs[0].paths, []string{moved}) {
		t.Fatalf("repair calls=%v, want relocated argument", git.repairs)
	}
	if err := git.runner.WorktreeRepair(context.Background(), destination, git.repairs[0].paths...); err != nil {
		t.Fatal(err)
	}
	moveTestRunGit(t, moved, "status", "--porcelain")
}
