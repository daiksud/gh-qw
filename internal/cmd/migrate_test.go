package cmd_test

import (
	"bytes"
	"context"
	"errors"
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
	migratepkg "github.com/daiksud/gh-qw/internal/migrate"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewMigrateCommandBulkFiltersAndMovesExactRepositories(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	legacy := filepath.Join(base, "legacy")
	primary := filepath.Join(base, "primary")
	good := filepath.Join(legacy, "github.com", "acme", "good")
	collision := filepath.Join(legacy, "github.com", "acme", "collision")
	nonGit := filepath.Join(legacy, "github.com", "acme", "not-git")
	bare := filepath.Join(legacy, "github.com", "acme", "bare")
	linked := filepath.Join(legacy, "github.com", "acme", "linked")
	deep := filepath.Join(legacy, "github.com", "acme", "container", "deep")
	for _, path := range []string{primary, nonGit, linked} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	migrateTestInitRepository(t, good, "")
	migrateTestInitRepository(t, collision, "")
	migrateTestRunGit(t, "", "init", "--bare", "-q", bare)
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, deep, "")
	collisionDestination := filepath.Join(primary, "github.com", "acme", "collision")
	if err := os.MkdirAll(collisionDestination, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	git := migrateTestGitRunner(&stderr)
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git: git,
		LookupEnv: func(name string) (string, bool) {
			if name == "GHQ_ROOT" {
				return legacy, true
			}
			return "", false
		},
		Stdout: &stdout,
		Stderr: &stderr,
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			t.Fatal("prompt called with -y")
			return false, nil
		},
	})
	command.SetArgs([]string{"-y"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr.String())
	}
	goodDestination := filepath.Join(primary, "github.com", "acme", "good")
	if got, want := stdout.String(), filepath.ToSlash(goodDestination)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(goodDestination); err != nil {
		t.Fatalf("moved repository missing: %v", err)
	}
	if _, err := os.Stat(good); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source error = %v, want not exist", err)
	}
	for _, path := range []string{collision, nonGit, bare, linked, deep} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("skipped path %q changed: %v", path, err)
		}
	}
	if !strings.Contains(stderr.String(), "destination") ||
		!strings.Contains(stderr.String(), "collision") {
		t.Fatalf("stderr = %q, want collision warning", stderr.String())
	}
}

func TestNewMigrateCommandDryRunPlansWithoutPromptOrMutation(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/Acme/Widget.git")

	var stdout, stderr bytes.Buffer
	promptCalls := 0
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    migrateTestGitRunner(&stderr),
		Stdout: &stdout,
		Stderr: &stderr,
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			promptCalls++
			return true, nil
		},
	})
	command.SetArgs([]string{"--dry-run", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	destination := filepath.Join(primary, "github.com", "Acme", "Widget")
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination error = %v, want not exist", err)
	}
	if !strings.Contains(stderr.String(), filepath.ToSlash(source)) ||
		!strings.Contains(stderr.String(), filepath.ToSlash(destination)) {
		t.Fatalf("stderr = %q, want complete source/destination plan", stderr.String())
	}
}

func TestNewMigrateCommandDeclineLeavesRepositoryUntouched(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget.git")

	var stdout, stderr bytes.Buffer
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    migrateTestGitRunner(&stderr),
		Stdout: &stdout,
		Stderr: &stderr,
		Prompt: func(_ context.Context, output io.Writer, prompt string) (bool, error) {
			if output != &stderr || !strings.Contains(prompt, "[y/N]") {
				t.Fatalf("prompt output/message = %T %q", output, prompt)
			}
			return false, nil
		},
	})
	command.SetArgs([]string{source})

	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("Execute() error = %v, want declined", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}

func TestNewMigrateCommandCrossDeviceFallback(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "git@github.com:acme/widget.git")
	if err := os.Symlink("README", filepath.Join(source, "README-link")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git: migrateTestGitRunner(&stderr),
		Filesystem: migratepkg.Filesystem{
			Rename: func(oldPath, newPath string) error {
				return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
			},
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"-y", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr:\n%s", err, stderr.String())
	}
	destination := filepath.Join(primary, "github.com", "acme", "widget")
	if got, want := stdout.String(), filepath.ToSlash(destination)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if target, err := os.Readlink(filepath.Join(destination, "README-link")); err != nil || target != "README" {
		t.Fatalf("copied symlink = %q, %v", target, err)
	}
}

func TestNewMigrateCommandSingleCollisionIsSafetyError(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	destination := filepath.Join(primary, "github.com", "acme", "widget")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget")

	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    migrateTestGitRunner(io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			t.Fatal("prompt called for collision")
			return false, nil
		},
	})
	command.SetArgs([]string{"-y", source})

	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Execute() error = %v, want destination collision", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}

func TestNewMigrateCommandBulkTOCTOUCollisionSkips(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	legacy := filepath.Join(base, "legacy")
	primary := filepath.Join(base, "primary")
	source := filepath.Join(legacy, "github.com", "acme", "widget")
	destination := filepath.Join(primary, "github.com", "acme", "widget")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "")

	var stdout, stderr bytes.Buffer
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git: migrateTestGitRunner(&stderr),
		LookupEnv: func(string) (string, bool) {
			return legacy, true
		},
		Stdout: &stdout,
		Stderr: &stderr,
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return false, err
			}
			return true, nil
		},
	})
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr = %q, want collision warning", stderr.String())
	}
}

func TestNewMigrateCommandRepairFailureLeavesMovedDestination(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	external := filepath.Join(base, "external")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget")
	migrateTestRunGit(t, source, "worktree", "add", "-qb", "external", external)

	wantErr := errors.New("repair failed")
	var stdout, stderr bytes.Buffer
	git := migrateTestGitRunner(&stderr)
	git.repairErr = wantErr
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    git,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"-y", source})

	err := command.Execute()
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "leave the repository there") {
		t.Fatalf("Execute() error = %v, want recovery error", err)
	}
	destination := filepath.Join(primary, "github.com", "acme", "widget")
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("destination missing after repair failure: %v", err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source error = %v, want not exist", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on failed repair", stdout.String())
	}
	if len(git.repairs) != 1 || git.repairs[0].dir != destination {
		t.Fatalf("repairs = %#v, want one from destination", git.repairs)
	}
}

func TestNewMigrateCommandRejectsDeepRemoteAsUsage(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget/extra")

	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    migrateTestGitRunner(io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	command.SetArgs([]string{"--dry-run", source})

	err := command.Execute()
	if !errors.Is(err, repospec.ErrUsage) {
		t.Fatalf("Execute() error = %v, want repospec.ErrUsage", err)
	}
}

func TestNewMigrateCommandUsesFirstRemoteWhenOriginIsAbsent(t *testing.T) {
	t.Parallel()

	base := migrateTestPhysical(t, t.TempDir())
	primary := filepath.Join(base, "primary")
	source := filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "")
	migrateTestRunGit(t, source, "remote", "add", "zeta", "https://github.com/zeta/widget")
	migrateTestRunGit(t, source, "remote", "add", "alpha", "https://github.com/alpha/widget")

	var stderr bytes.Buffer
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary},
		}},
		Git:    migrateTestGitRunner(&stderr),
		Getwd:  func() (string, error) { return base, nil },
		Stdout: io.Discard,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--dry-run", "source"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantDestination := filepath.Join(primary, "github.com", "alpha", "widget")
	if !strings.Contains(stderr.String(), filepath.ToSlash(wantDestination)) {
		t.Fatalf("stderr = %q, want first-remote destination %q", stderr.String(), wantDestination)
	}
}

type migrateRootResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver *migrateRootResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

type migrateRepairCall struct {
	dir   string
	paths []string
}

type migrateTestGit struct {
	runner       *gitcmd.Runner
	repairErr    error
	beforeRepair func(string, []string)
	repairs      []migrateRepairCall
}

func (git *migrateTestGit) Output(ctx context.Context, arguments ...string) ([]byte, error) {
	return git.runner.Output(ctx, arguments...)
}

func (git *migrateTestGit) OutputDir(
	ctx context.Context,
	dir string,
	arguments ...string,
) ([]byte, error) {
	return git.runner.OutputDir(ctx, dir, arguments...)
}

func (git *migrateTestGit) WorktreeRepair(
	ctx context.Context,
	dir string,
	paths ...string,
) error {
	if git.beforeRepair != nil {
		git.beforeRepair(dir, paths)
	}
	git.repairs = append(git.repairs, migrateRepairCall{
		dir:   dir,
		paths: append([]string(nil), paths...),
	})
	if git.repairErr != nil {
		return git.repairErr
	}
	return git.runner.WorktreeRepair(ctx, dir, paths...)
}

type migrateLockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (writer *migrateLockedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(data)
}

func migrateTestGitRunner(stderr io.Writer) *migrateTestGit {
	output := &migrateLockedWriter{writer: stderr}
	return &migrateTestGit{
		runner: &gitcmd.Runner{
			Executable: "git",
			Stdout:     output,
			Stderr:     output,
		},
	}
}

func migrateTestInitRepository(t *testing.T, path, remote string) {
	t.Helper()
	migrateTestRunGit(t, "", "init", "-q", path)
	migrateTestRunGit(t, path, "config", "user.email", "test@example.com")
	migrateTestRunGit(t, path, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateTestRunGit(t, path, "add", "README")
	migrateTestRunGit(t, path, "commit", "-qm", "initial")
	if remote != "" {
		migrateTestRunGit(t, path, "remote", "add", "origin", remote)
	}
}

func migrateTestRunGit(t *testing.T, dir string, arguments ...string) string {
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

func TestMigrateDelegatesWorktreeRepairToGit(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name                            string
		internal, external, crossDevice bool
	}{
		{name: "main only"}, {name: "external", external: true}, {name: "nested internal", internal: true},
		{name: "mixed", internal: true, external: true}, {name: "mixed cross device", internal: true, external: true, crossDevice: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := migrateTestPhysical(t, t.TempDir())
			source, primary := filepath.Join(base, "source"), filepath.Join(base, "primary")
			destination := filepath.Join(primary, "github.com", "acme", "widget")
			if err := os.Mkdir(primary, 0755); err != nil {
				t.Fatal(err)
			}
			migrateTestInitRepository(t, source, "https://github.com/acme/widget")
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
				migrateTestRunGit(t, source, "worktree", "add", "-qb", branch, path)
				admin := strings.TrimSpace(migrateTestRunGit(t, path, "rev-parse", "--absolute-git-dir"))
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
			git := migrateTestGitRunner(&stderr)
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
			filesystem := migratepkg.Filesystem{}
			if scenario.crossDevice {
				filesystem.Rename = func(old, new string) error {
					return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
				}
			}
			command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
				Resolver: &migrateRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
				Git:      git, Filesystem: filesystem, Stdout: &stdout, Stderr: &stderr,
			})
			command.SetArgs([]string{"-y", source})
			if err := command.Execute(); err != nil {
				t.Fatalf("migration: %v; stderr=%s", err, stderr.String())
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
				common := strings.TrimSpace(migrateTestRunGit(t, path, "rev-parse", "--path-format=absolute", "--git-common-dir"))
				if migrateTestPhysical(t, common) != filepath.Join(destination, ".git") {
					t.Fatalf("common dir=%q, want destination .git", common)
				}
				migrateTestRunGit(t, path, "status", "--porcelain")
			}
		})
	}
}

func TestMigrateRejectsWorktreeRegistrationChangeAfterConfirmation(t *testing.T) {
	t.Parallel()
	base := migrateTestPhysical(t, t.TempDir())
	source, primary := filepath.Join(base, "source"), filepath.Join(base, "primary")
	if err := os.Mkdir(primary, 0755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget")
	git := migrateTestGitRunner(io.Discard)
	var stdout bytes.Buffer
	prompts := 0
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
		Git:      git, Stdout: &stdout, Stderr: io.Discard,
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			prompts++
			migrateTestRunGit(t, source, "worktree", "add", "-qb", "new", filepath.Join(base, "new"))
			return true, nil
		},
	})
	command.SetArgs([]string{source})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "revalidate linked worktrees") {
		t.Fatalf("error=%v, want revalidation failure", err)
	}
	if prompts != 1 || len(git.repairs) != 0 || stdout.Len() != 0 {
		t.Fatalf("prompts=%d repairs=%v stdout=%q", prompts, git.repairs, stdout.String())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, "github.com", "acme", "widget")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists: %v", err)
	}
}

func (git *migrateTestGit) WorktreeList(ctx context.Context, dir string) ([]gitcmd.Worktree, error) {
	return git.runner.WorktreeList(ctx, dir)
}

func TestMigrateRejectsLinkedGitPointerCrossingSourceBoundary(t *testing.T) {
	t.Parallel()
	base := migrateTestPhysical(t, t.TempDir())
	source, primary, external := filepath.Join(base, "source"), filepath.Join(base, "primary"), filepath.Join(base, "external")
	if err := os.Mkdir(primary, 0755); err != nil {
		t.Fatal(err)
	}
	migrateTestInitRepository(t, source, "https://github.com/acme/widget")
	migrateTestRunGit(t, source, "worktree", "add", "-qb", "external", external)
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
	git := migrateTestGitRunner(io.Discard)
	command := cmd.NewMigrateCommand(cmd.MigrateDependencies{
		Resolver: &migrateRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}}, Git: git, Stdout: io.Discard, Stderr: io.Discard,
	})
	command.SetArgs([]string{"-y", source})
	if err := command.Execute(); err == nil {
		t.Fatal("migration succeeded despite crossing Git pointer")
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
