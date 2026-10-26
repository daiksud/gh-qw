package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

func TestNewRemoveCommandWorktreeSelectsExplicitRepository(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-explicit")
	fixture.current = func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error) {
		t.Fatal("Current() called with explicit -R")
		return local.Current{}, nil
	}

	if err := fixture.worktreeRemoveExecute("-R", "acme/widget", fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.resolvedRepository.Identity != fixture.repository.Identity {
		t.Fatalf(
			"resolved repository = %q, want %q",
			fixture.resolvedRepository.Identity,
			fixture.repository.Identity,
		)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewRemoveCommandWorktreeSelectsCurrentMainOrLinked(t *testing.T) {
	for _, main := range []bool{true, false} {
		name := "linked"
		if main {
			name = "main"
		}
		t.Run(name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature-current-"+name)
			fixture.currentWorktree.Main = main
			fixture.currentWorktree.Slot = fixture.branch
			if main {
				fixture.currentWorktree.Slot = ""
			}

			if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if fixture.currentCalls != 1 {
				t.Fatalf("Current() calls = %d, want 1", fixture.currentCalls)
			}
			if fixture.resolvedRepository.Identity != fixture.repository.Identity {
				t.Fatalf(
					"resolved repository = %q, want %q",
					fixture.resolvedRepository.Identity,
					fixture.repository.Identity,
				)
			}
			worktreeRemoveAssertSuccess(t, fixture)
		})
	}
}

func TestNewRemoveCommandWorktreeUsesCurrentRepositoryWithDuplicateIdentity(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-current-duplicate")
	duplicate := fixture.repository
	duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
	duplicate.RootIndex = 1
	fixture.repositories = append(fixture.repositories, duplicate)

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.resolvedRepository.Path != fixture.repository.Path {
		t.Fatalf(
			"resolved repository path = %q, want current %q",
			fixture.resolvedRepository.Path,
			fixture.repository.Path,
		)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewRemoveCommandWorktreePassesOneForceLevel(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-forced")

	if err := fixture.worktreeRemoveExecute("--force", fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.git.removals) != 1 {
		t.Fatalf("WorktreeRemove() calls = %d, want 1", len(fixture.git.removals))
	}
	if got := fixture.git.removals[0].options; !got.Force {
		t.Fatalf("WorktreeRemove options = %#v, want Force true", got)
	}
}

func TestNewRemoveCommandWorktreeRejectsUsageAndAmbiguity(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		configure func(*worktreeRemoveFixture)
		wantIs    error
	}{
		{
			name:   "missing branch",
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "too many branches",
			args:   []string{"one", "two"},
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "invalid branch",
			args:   []string{"HEAD"},
			wantIs: local.ErrInvalidBranch,
		},
		{
			name:   "gone with branch",
			args:   []string{"--gone", "feature-test"},
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "dry run without gone",
			args:   []string{"--dry-run", "feature-test"},
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "yes without gone",
			args:   []string{"--yes", "feature-test"},
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "missing repository",
			args:   []string{"-R", "missing", "feature-test"},
			wantIs: local.ErrRepositoryNotFound,
		},
		{
			name: "duplicate explicit identity",
			args: []string{"-R", "github.com/acme/widget", "feature-test"},
			configure: func(fixture *worktreeRemoveFixture) {
				duplicate := fixture.repository
				duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
				duplicate.RootIndex = 1
				fixture.repositories = append(fixture.repositories, duplicate)
			},
			wantIs: local.ErrRepositoryAmbiguous,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature-test")
			if test.configure != nil {
				test.configure(fixture)
			}

			err := fixture.worktreeRemoveExecute(test.args...)
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, test.wantIs)
			}
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want usage classification", err)
			}
			if len(fixture.git.removals) != 0 {
				t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
			}
		})
	}
}

func TestNewRemoveCommandWorktreeRejectsUnsafeManagedResults(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreeRemoveFixture)
		wantIs    error
	}{
		{
			name: "main worktree",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Main = true
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "bare worktree",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Bare = true
			},
			wantIs: local.ErrBareWorktree,
		},
		{
			name: "external path",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Path = filepath.Join(fixture.root, "external")
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "non-deterministic path",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Path = filepath.Join(fixture.base, "other")
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "foreign repository",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Repository = local.Repository{
					Identity: "github.com/other/foreign",
				}
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "missing registration",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.resolveManagedErr = &local.WorktreeError{
					Kind:   local.ErrWorktreeNotFound,
					Slot:   fixture.branch,
					Reason: "slot is not registered",
				}
			},
			wantIs: local.ErrWorktreeNotFound,
		},
		{
			name: "foreign Git association",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.resolveManagedErr = &local.WorktreeError{
					Kind:       local.ErrUnsafeWorktree,
					Repository: fixture.repository.Identity,
					Slot:       fixture.branch,
					Path:       fixture.target,
					Reason:     "Git association does not match the selected main repository",
				}
			},
			wantIs: local.ErrUnsafeWorktree,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature-unsafe")
			test.configure(fixture)

			err := fixture.worktreeRemoveExecute("-R", fixture.repository.Identity, fixture.branch)
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, test.wantIs)
			}
			if len(fixture.git.removals) != 0 {
				t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
			}
			if _, statErr := os.Lstat(fixture.target); statErr != nil {
				t.Fatalf("unsafe target changed: %v", statErr)
			}
		})
	}
}

func TestNewRemoveCommandWorktreeRejectsNestedRegisteredRepository(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-nested-repository")
	nestedPath := filepath.Join(fixture.target, "nested")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.repositories = append(fixture.repositories, local.Repository{
		Identity: "github.com/other/nested",
		Host:     "github.com",
		Owner:    "other",
		Repo:     "nested",
		Path:     nestedPath,
		Root:     fixture.target,
	})

	err := fixture.worktreeRemoveExecute("-R", fixture.repository.Identity, fixture.branch)
	if !errors.Is(err, ErrRemoveSafety) || !strings.Contains(err.Error(), "registered repository") {
		t.Fatalf("Execute() error = %v, want nested registered repository safety error", err)
	}
	if len(fixture.git.removals) != 0 {
		t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
	}
	if info, statErr := os.Stat(nestedPath); statErr != nil || !info.IsDir() {
		t.Fatalf("nested repository changed: info=%v err=%v", info, statErr)
	}
}

func TestNewRemoveCommandWorktreeRevalidatesNestedRegisteredRepository(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-late-nested-repository")
	nestedPath := filepath.Join(fixture.target, "nested")
	fixture.onDiscover = func(call int) {
		if call != 2 {
			return
		}
		if err := os.MkdirAll(nestedPath, 0o755); err != nil {
			t.Fatal(err)
		}
		fixture.repositories = append(fixture.repositories, local.Repository{
			Identity: "github.com/other/nested",
			Host:     "github.com",
			Owner:    "other",
			Repo:     "nested",
			Path:     nestedPath,
			Root:     fixture.target,
		})
	}

	err := fixture.worktreeRemoveExecute("-R", fixture.repository.Identity, fixture.branch)
	if !errors.Is(err, ErrRemoveSafety) || !strings.Contains(err.Error(), "registered repository") {
		t.Fatalf("Execute() error = %v, want late nested registration safety error", err)
	}
	if fixture.discoveryCalls != 2 || len(fixture.git.removals) != 0 {
		t.Fatalf("discovery calls = %d, removals = %d; want 2, 0", fixture.discoveryCalls, len(fixture.git.removals))
	}
	if info, statErr := os.Stat(nestedPath); statErr != nil || !info.IsDir() {
		t.Fatalf("late nested repository changed: info=%v err=%v", info, statErr)
	}
}

func TestNewRemoveCommandWorktreePropagatesGitSafetyFailures(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{name: "dirty", err: errors.New("dirty worktree")},
		{name: "locked", err: errors.New("locked worktree")},
	} {
		t.Run(failure.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature-"+failure.name)
			fixture.git.removeErr = failure.err

			err := fixture.worktreeRemoveExecute(fixture.branch)
			if !errors.Is(err, failure.err) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, failure.err)
			}
			if _, statErr := os.Lstat(fixture.target); statErr != nil {
				t.Fatalf("target changed after Git failure: %v", statErr)
			}
			if fixture.stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want no success diagnostic", fixture.stderr.String())
			}
		})
	}
}

func TestNewRemoveCommandWorktreeCleansEmptySlotParentToBoundary(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-team-login")
	slotDirectory := filepath.Dir(fixture.target)

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Lstat(slotDirectory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want not exist", slotDirectory, err)
	}
	if info, err := os.Lstat(fixture.base); err != nil || !info.IsDir() {
		t.Fatalf("shared worktree root was removed or changed: info=%v err=%v", info, err)
	}
	wantRemoved := []string{slotDirectory}
	if !reflect.DeepEqual(fixture.cleanupRemovals, wantRemoved) {
		t.Fatalf("cleanup removals = %#v, want %#v", fixture.cleanupRemovals, wantRemoved)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewRemoveCommandWorktreeStopsAtNonemptyParent(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-nonempty-leaf")
	parent := filepath.Dir(fixture.target)
	sentinel := filepath.Join(parent, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel changed: %v", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
}

func TestNewRemoveCommandWorktreeContinuesPastMissingSlotDirectory(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-missing-parent-leaf")
	slotDirectory := filepath.Dir(fixture.target)
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Remove(slotDirectory)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if info, err := os.Stat(fixture.base); err != nil || !info.IsDir() {
		t.Fatalf("shared worktree root changed: info=%v err=%v", info, err)
	}
}

func TestNewRemoveCommandWorktreeStopsAtSymlinkParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link setup is not portable on Windows")
	}

	fixture := worktreeRemoveNewFixture(t, "feature-symlink-leaf")
	parent := filepath.Dir(fixture.target)
	outside := filepath.Join(fixture.root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Remove(parent); err != nil {
			return err
		}
		return os.Symlink(outside, parent)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	info, err := os.Lstat(parent)
	if err != nil {
		t.Fatalf("Lstat(symlink parent) error = %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("parent mode = %v, want symlink", info.Mode())
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if info, err := os.Stat(outside); err != nil || !info.IsDir() {
		t.Fatalf("outside directory changed: info=%v err=%v", info, err)
	}
}

func TestNewRemoveCommandWorktreeErrorsWhenGitLeavesDirectory(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-remains")
	fixture.git.leavePath = true

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "still exists") {
		t.Fatalf("Execute() error = %v, want remaining-directory error", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if fixture.stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no success diagnostic", fixture.stderr.String())
	}
	if _, statErr := os.Lstat(fixture.target); statErr != nil {
		t.Fatalf("remaining target changed: %v", statErr)
	}
}

func TestNewRemoveCommandWorktreeRevalidatesCleanupBoundary(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-replaced-leaf")
	originalBase := fixture.base + "-original"
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Rename(fixture.base, originalBase); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Dir(fixture.target), 0o755)
	}

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "worktree base") ||
		!strings.Contains(err.Error(), "changed") {
		t.Fatalf("Execute() error = %v, want changed-boundary error", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if info, statErr := os.Stat(filepath.Dir(fixture.target)); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement parent changed: info=%v err=%v", info, statErr)
	}
}

func TestNewRemoveCommandWorktreeWarningsAndWriterFailures(t *testing.T) {
	t.Run("warning prefix", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature-warning")
		warning := local.Warning{
			Kind:      local.WarningInspection,
			Root:      fixture.repositoryRoot,
			RootIndex: 0,
			Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
			Operation: "inspect",
			Err:       errors.New("unreadable"),
		}
		fixture.warnings = []local.Warning{warning}

		if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "gh-qw: warning: " + warning.Error() + "\n" +
			"removed worktree " + local.NormalizePathForOutput(fixture.target) + "\n"
		if got := fixture.stderr.String(); got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("warning write failure prevents mutation", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature-warning-write")
		fixture.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      "entry",
			Operation: "inspect",
		}}
		writeErr := errors.New("warning writer failed")
		fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

		err := fixture.worktreeRemoveExecute(fixture.branch)
		if !errors.Is(err, writeErr) {
			t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, writeErr)
		}
		if len(fixture.git.removals) != 0 {
			t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
		}
	})

	t.Run("diagnostic write failure reports completed mutation", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature-diagnostic-write")
		writeErr := errors.New("diagnostic writer failed")
		fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

		err := fixture.worktreeRemoveExecute(fixture.branch)
		if !errors.Is(err, writeErr) {
			t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, writeErr)
		}
		if err == nil || !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("Execute() error = %v, want explicit completed-mutation context", err)
		}
		if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("target remains after reported mutation: %v", statErr)
		}
	})
}

func TestNewRemoveCommandWorktreeReportsCleanupFailureAfterMutation(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-cleanup-failure")
	cleanupErr := errors.New("cleanup denied")
	fixture.removeErr = cleanupErr

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, cleanupErr)
	}
	if err == nil || !strings.Contains(err.Error(), "removed by Git") {
		t.Fatalf("Execute() error = %v, want completed-mutation context", err)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("target remains after Git removal: %v", statErr)
	}
}

func TestNewRemoveCommandWorktreeJoinsPostMutationFailures(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature-joined-failures")
	cleanupErr := errors.New("cleanup failed")
	writeErr := errors.New("diagnostic failed")
	fixture.removeErr = cleanupErr
	fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if !errors.Is(err, cleanupErr) || !errors.Is(err, writeErr) {
		t.Fatalf(
			"Execute() error = %v, want joined cleanup %v and writer %v",
			err,
			cleanupErr,
			writeErr,
		)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("target remains after Git removal: %v", statErr)
	}
}

func TestNewRemoveCommandWorktreeRealGitLifecycle(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")

	branch := "feature-real"
	target := filepath.Join(worktreeRoot, "feature-real", "widget")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", branch, target, "HEAD")

	var stdout, stderr bytes.Buffer
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", branch})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(),
		"removed worktree "+local.NormalizePathForOutput(target)+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed worktree Lstat error = %v, want not exist", err)
	}
	if _, err := os.Lstat(filepath.Dir(target)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty branch parent Lstat error = %v, want not exist", err)
	}
	if info, err := os.Stat(worktreeRoot); err != nil ||
		!info.IsDir() {
		t.Fatalf("per-repository boundary changed: info=%v err=%v", info, err)
	}
	list := worktreeRemoveGitOutput(t, repositoryPath, "worktree", "list", "--porcelain")
	if strings.Contains(list, target) {
		t.Fatalf("git worktree list still contains %q:\n%s", target, list)
	}
}

func TestNewRemoveCommandWorktreeGoneRealGitLifecycle(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")

	type trackedWorktree struct {
		branch string
		path   string
		ref    string
	}
	worktrees := []trackedWorktree{
		{
			branch: "feature-alive",
			path: filepath.Join(
				worktreeRoot, "feature-alive", "widget",
			),
			ref: "refs/remotes/custom/review/alive",
		},
		{
			branch: "feature-gone",
			path: filepath.Join(
				worktreeRoot, "slot-gone", "widget",
			),
			ref: "refs/remotes/custom/review/gone",
		},
	}
	worktreeRemoveRunGit(
		t,
		repositoryPath,
		"config",
		"remote.custom.fetch",
		"+refs/heads/review/*:refs/remotes/custom/review/*",
	)
	for _, item := range worktrees {
		if err := os.MkdirAll(filepath.Dir(item.path), 0o755); err != nil {
			t.Fatal(err)
		}
		worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", item.branch, item.path, "HEAD")
		worktreeRemoveRunGit(t, repositoryPath, "update-ref", item.ref, "HEAD")
		worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+item.branch+".remote", "custom")
		worktreeRemoveRunGit(
			t,
			repositoryPath,
			"config",
			"branch."+item.branch+".merge",
			"refs/heads/review/"+strings.TrimPrefix(item.branch, "feature-"),
		)
	}
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", worktrees[1].ref)
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "refs/heads/HEAD", "HEAD")

	var stdout, stderr bytes.Buffer
	prompts := 0
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd: func() (string, error) { return root, nil },
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			prompts++
			return true, nil
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if prompts != 1 {
		t.Fatalf("bulk confirmation calls = %d, want 1", prompts)
	}
	if !strings.Contains(stderr.String(), "remove slot=\"slot-gone\" branch=\"feature-gone\"") ||
		strings.Contains(stderr.String(), "feature-alive\"") {
		t.Fatalf("stderr plan = %q, want only gone candidate", stderr.String())
	}
	if _, err := os.Lstat(worktrees[1].path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("gone worktree Lstat error = %v, want not exist", err)
	}
	if info, err := os.Stat(worktrees[0].path); err != nil || !info.IsDir() {
		t.Fatalf("alive worktree changed: info=%v err=%v", info, err)
	}
	for _, item := range worktrees {
		worktreeRemoveRunGit(t, repositoryPath, "show-ref", "--verify", "refs/heads/"+item.branch)
	}

	invalidAttachedPath := filepath.Join(
		worktreeRoot, "slot-invalid-attached", "widget",
	)
	if err := os.MkdirAll(filepath.Dir(invalidAttachedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(
		t,
		repositoryPath,
		"worktree",
		"add",
		"-b",
		"@",
		invalidAttachedPath,
		"refs/heads/main",
	)
	stdout.Reset()
	stderr.Reset()
	command = newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd:  func() (string, error) { return root, nil },
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid branch \"@\"") {
		t.Fatalf("attached-invalid Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if info, statErr := os.Stat(invalidAttachedPath); statErr != nil || !info.IsDir() {
		t.Fatalf("attached-invalid worktree changed: info=%v err=%v", info, statErr)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "remove", invalidAttachedPath)
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", "refs/heads/@")

	stdout.Reset()
	stderr.Reset()
	command = newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd: func() (string, error) {
			return "", errors.New("must not inspect cwd without candidates")
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes"})
	if err := command.Execute(); err != nil {
		t.Fatalf("no-candidate Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "No linked worktrees with missing upstream refs") {
		t.Fatalf("no-candidate stderr = %q", stderr.String())
	}
}

func TestNewRemoveCommandWorktreeGoneDryRunAndForceDirty(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")

	branch := "feature-dirty"
	target := filepath.Join(worktreeRoot, "feature-dirty", "widget")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", branch, target, "HEAD")
	upstream := "refs/remotes/origin/feature-dirty"
	worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", upstream, "HEAD")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".remote", "origin")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".merge", "refs/heads/feature-dirty")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", upstream)
	if err := os.WriteFile(filepath.Join(target, "untracked"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prompt := func(context.Context, io.Writer, string) (bool, error) {
		return false, errors.New("dry-run unexpectedly prompted")
	}
	newCommand := func(stdout, stderr *bytes.Buffer) *cobra.Command {
		return newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
			Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
				RepositoryRoots: []string{repositoryRoot},
				WorktreeRoot:    worktreeRoot,
			}},
			Getwd:  func() (string, error) { return root, nil },
			Prompt: prompt,
			Stdout: stdout,
			Stderr: stderr,
		})
	}

	var stdout, stderr bytes.Buffer
	command := newCommand(&stdout, &stderr)
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--dry-run", "--yes"})
	err := command.Execute()
	if got := ExitCode(err); got != 1 {
		t.Fatalf("dry-run ExitCode(error=%v) = %d, want 1", err, got)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "keep slot=\"feature-dirty\"") ||
		!strings.Contains(stderr.String(), "dirty") {
		t.Fatalf("dry-run output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dry-run changed dirty worktree: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	command = newCommand(&stdout, &stderr)
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--dry-run", "--yes", "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced dry-run error = %v; stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "remove slot=\"feature-dirty\"") ||
		!strings.Contains(stderr.String(), "reason=\"dirty; --force enabled\"") {
		t.Fatalf("forced dry-run stderr = %q, want annotated remove plan", stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("forced dry-run changed worktree: %v", err)
	}

	prompt = func(context.Context, io.Writer, string) (bool, error) {
		worktreeRemoveRunGit(t, repositoryPath, "update-ref", upstream, "HEAD")
		return true, nil
	}
	stdout.Reset()
	stderr.Reset()
	command = newCommand(&stdout, &stderr)
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--force"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "revalidate gone-worktree removal plan") ||
		!errors.Is(err, ErrRemoveSafety) {
		t.Fatalf("changed-plan removal error = %v, want pre-mutation plan change", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("changed-plan removal mutated worktree: %v", err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", upstream)
	prompt = func(context.Context, io.Writer, string) (bool, error) {
		return false, nil
	}
	stdout.Reset()
	stderr.Reset()
	command = newCommand(&stdout, &stderr)
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--force"})
	err = command.Execute()
	if got := ExitCode(err); got != 1 || !strings.Contains(stderr.String(), "removal declined") {
		t.Fatalf("declined removal = status %d error %v stderr %q", got, err, stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("declined removal changed worktree: %v", err)
	}
	prompt = func(context.Context, io.Writer, string) (bool, error) {
		return false, errors.New("--yes unexpectedly prompted")
	}

	stdout.Reset()
	stderr.Reset()
	command = newCommand(&stdout, &stderr)
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes", "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("forced removal error = %v; stderr = %q", err, stderr.String())
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("forced removal target Lstat error = %v, want not exist", err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "show-ref", "--verify", "refs/heads/"+branch)
}

func TestNewRemoveCommandWorktreeGoneKeepsParentOfRegisteredWorktree(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")
	worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")

	parentBranch := "gone-parent"
	parentPath := filepath.Join(worktreeRoot, "gone", "widget")
	if err := os.MkdirAll(filepath.Dir(parentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", parentBranch, parentPath, "HEAD")
	parentUpstream := "refs/remotes/origin/gone-parent"
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", parentUpstream, "HEAD")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".remote", "origin")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".merge", "refs/heads/gone-parent")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", parentUpstream)

	childBranch := "live-child"
	childPath := filepath.Join(parentPath, "child")
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", childBranch, childPath, "HEAD")
	childUpstream := "refs/remotes/origin/live-child"
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", childUpstream, "HEAD")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+childBranch+".remote", "origin")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+childBranch+".merge", "refs/heads/live-child")

	var stdout, stderr bytes.Buffer
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd:  func() (string, error) { return root, nil },
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--dry-run", "--yes", "--force"})
	err := command.Execute()
	if got := ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(error=%v) = %d, want 1; stderr=%q", err, got, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "keep slot=\"gone\"") ||
		!strings.Contains(stderr.String(), "contains registered worktree") {
		t.Fatalf("output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, path := range []string{parentPath, childPath} {
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("registered worktree %q changed: info=%v err=%v", path, info, statErr)
		}
	}
	list := worktreeRemoveGitOutput(t, repositoryPath, "worktree", "list", "--porcelain")
	if !strings.Contains(list, local.NormalizePathForOutput(parentPath)) ||
		!strings.Contains(list, local.NormalizePathForOutput(childPath)) {
		t.Fatalf("registered worktree missing after dry-run:\n%s", list)
	}
}

func TestNewRemoveCommandWorktreeGoneKeepsParentOfOtherRepositoryWorktree(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	otherRepositoryPath := filepath.Join(repositoryRoot, "github.com", "other", "tool")
	for _, path := range []string{repositoryPath, otherRepositoryPath, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, ".gitignore"), []byte("child/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", ".gitignore")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")
	worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")

	parentBranch := "gone-parent"
	parentPath := filepath.Join(worktreeRoot, "gone", "widget")
	if err := os.MkdirAll(filepath.Dir(parentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", parentBranch, parentPath, "HEAD")
	parentUpstream := "refs/remotes/origin/gone-parent"
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", parentUpstream, "HEAD")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".remote", "origin")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".merge", "refs/heads/gone-parent")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", parentUpstream)

	worktreeRemoveRunGit(t, otherRepositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, otherRepositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, otherRepositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(otherRepositoryPath, "README"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, otherRepositoryPath, "add", "README")
	worktreeRemoveRunGit(t, otherRepositoryPath, "commit", "-m", "initial")
	childPath := filepath.Join(parentPath, "child")
	worktreeRemoveRunGit(t, otherRepositoryPath, "worktree", "add", "-b", "nested", childPath, "HEAD")
	if status := worktreeRemoveGitOutput(t, parentPath, "status", "--porcelain"); status != "" {
		t.Fatalf("parent worktree is dirty despite ignored child: %q", status)
	}

	var stdout, stderr bytes.Buffer
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd:  func() (string, error) { return root, nil },
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--dry-run", "--yes"})
	err := command.Execute()
	if got := ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(error=%v) = %d, want 1; stderr=%q", err, got, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "keep slot=\"gone\"") ||
		!strings.Contains(stderr.String(), "contains registered worktree") {
		t.Fatalf("output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, path := range []string{parentPath, childPath} {
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("registered worktree %q changed: info=%v err=%v", path, info, statErr)
		}
	}
	otherList := worktreeRemoveGitOutput(t, otherRepositoryPath, "worktree", "list", "--porcelain")
	if !strings.Contains(otherList, local.NormalizePathForOutput(childPath)) {
		t.Fatalf("other repository registration missing after dry-run:\n%s", otherList)
	}

	if err := os.RemoveAll(childPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	command = newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd:  func() (string, error) { return root, nil },
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--dry-run", "--yes"})
	err = command.Execute()
	if got := ExitCode(err); got != 1 {
		t.Fatalf("missing-child ExitCode(error=%v) = %d, want 1; stderr=%q", err, got, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "keep slot=\"gone\"") ||
		!strings.Contains(stderr.String(), "contains registered worktree") {
		t.Fatalf("missing-child output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if info, statErr := os.Stat(parentPath); statErr != nil || !info.IsDir() {
		t.Fatalf("parent worktree changed: info=%v err=%v", info, statErr)
	}
	if _, statErr := os.Lstat(childPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("missing child unexpectedly materialized: %v", statErr)
	}
	otherList = worktreeRemoveGitOutput(t, otherRepositoryPath, "worktree", "list", "--porcelain")
	if !strings.Contains(otherList, local.NormalizePathForOutput(childPath)) {
		t.Fatalf("missing child registration was pruned:\n%s", otherList)
	}
}

func TestNewRemoveCommandWorktreeGoneKeepsNewNestedWorktreeBeforeRemoval(t *testing.T) {
	for _, sameRepository := range []bool{false, true} {
		name := "other repository"
		if sameRepository {
			name = "selected repository"
		}
		t.Run(name, func(t *testing.T) {
			root := worktreeRemovePhysicalPath(t, t.TempDir())
			repositoryRoot := filepath.Join(root, "repositories")
			worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
			repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
			otherRepositoryPath := filepath.Join(repositoryRoot, "github.com", "other", "tool")
			for _, path := range []string{repositoryPath, otherRepositoryPath, worktreeRoot} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
			worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
			worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
			if err := os.WriteFile(filepath.Join(repositoryPath, ".gitignore"), []byte("child/\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			worktreeRemoveRunGit(t, repositoryPath, "add", ".gitignore")
			worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")
			worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
			worktreeRemoveRunGit(t, otherRepositoryPath, "init", "-b", "main")
			worktreeRemoveRunGit(t, otherRepositoryPath, "config", "user.name", "gh-qw test")
			worktreeRemoveRunGit(t, otherRepositoryPath, "config", "user.email", "gh-qw@example.invalid")
			if err := os.WriteFile(filepath.Join(otherRepositoryPath, "README"), []byte("other\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			worktreeRemoveRunGit(t, otherRepositoryPath, "add", "README")
			worktreeRemoveRunGit(t, otherRepositoryPath, "commit", "-m", "initial")

			parentBranch := "gone-parent"
			parentPath := filepath.Join(worktreeRoot, "gone", "widget")
			if err := os.MkdirAll(filepath.Dir(parentPath), 0o755); err != nil {
				t.Fatal(err)
			}
			worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", parentBranch, parentPath, "HEAD")
			parentUpstream := "refs/remotes/origin/gone-parent"
			worktreeRemoveRunGit(t, repositoryPath, "update-ref", parentUpstream, "HEAD")
			worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".remote", "origin")
			worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+parentBranch+".merge", "refs/heads/gone-parent")
			worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", parentUpstream)

			childRepository, childIdentity := otherRepositoryPath, "github.com/other/tool"
			if sameRepository {
				childRepository, childIdentity = repositoryPath, "github.com/acme/widget"
			}
			childPath := filepath.Join(parentPath, "child")
			initialReads := map[string]int{}
			initialPhase := true
			git := &worktreeRemoveObservedRunner{worktreeRemoveFailingRunner: &worktreeRemoveFailingRunner{
				Runner:   &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: io.Discard},
				failPath: parentPath,
			}, onList: func(path string) {
				if initialPhase {
					initialReads[path]++
				}
			}}
			var stdout, stderr bytes.Buffer
			prompts := 0
			discoveryCalls := 0
			injections := 0
			discover := func(ctx context.Context, roots []string, options ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
				discoveryCalls++
				if discoveryCalls == 3 {
					if prompts != 1 {
						t.Fatalf("late inventory check ran after %d confirmations, want 1", prompts)
					}
					worktreeRemoveRunGit(t, childRepository, "worktree", "add", "-b", "live-child", childPath, "HEAD")
					injections++
				}
				return local.DiscoverRepositories(ctx, roots, options...)
			}
			command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
				Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
					RepositoryRoots: []string{repositoryRoot},
					WorktreeRoot:    worktreeRoot,
				}},
				Git:      git,
				Discover: discover,
				Getwd:    func() (string, error) { return root, nil },
				Prompt: func(context.Context, io.Writer, string) (bool, error) {
					prompts++
					initialPhase = false
					return true, nil
				},
				Stdout: &stdout,
				Stderr: &stderr,
			})
			command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--force"})
			err := command.Execute()
			if got := ExitCode(err); got != 1 {
				t.Fatalf("ExitCode(error=%v) = %d, want 1; stderr=%q", err, got, stderr.String())
			}
			for _, repository := range []string{repositoryPath, otherRepositoryPath} {
				if initialReads[repository] != 1 {
					t.Errorf("initial inventory reads for %q = %d, want 1", repository, initialReads[repository])
				}
			}

			if prompts != 1 || discoveryCalls != 3 || injections != 1 || len(git.removals) != 0 {
				t.Fatalf("prompts=%d discovery=%d injections=%d removals=%#v", prompts, discoveryCalls, injections, git.removals)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "kept worktree \"gone\"") ||
				!strings.Contains(stderr.String(), "contains registered worktree from \""+childIdentity+"\"") {
				t.Fatalf("output stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			for _, path := range []string{parentPath, childPath} {
				if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
					t.Fatalf("registered worktree %q changed: info=%v err=%v", path, info, statErr)
				}
			}

		})
	}
}

func TestNewRemoveCommandWorktreeGoneStopsWhenDiscoveredRepositoryDisappears(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	otherRepositoryPath := filepath.Join(repositoryRoot, "github.com", "other", "tool")
	for _, path := range []string{repositoryPath, otherRepositoryPath, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{repositoryPath, otherRepositoryPath} {
		worktreeRemoveRunGit(t, path, "init", "-b", "main")
		worktreeRemoveRunGit(t, path, "config", "user.name", "gh-qw test")
		worktreeRemoveRunGit(t, path, "config", "user.email", "gh-qw@example.invalid")
		if err := os.WriteFile(filepath.Join(path, "README"), []byte("initial\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		worktreeRemoveRunGit(t, path, "add", "README")
		worktreeRemoveRunGit(t, path, "commit", "-m", "initial")
	}

	branch := "gone-parent"
	parentPath := filepath.Join(worktreeRoot, "gone", "widget")
	if err := os.MkdirAll(filepath.Dir(parentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", branch, parentPath, "HEAD")
	upstream := "refs/remotes/origin/gone-parent"
	worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", upstream, "HEAD")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".remote", "origin")
	worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".merge", "refs/heads/gone-parent")
	worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", upstream)

	discoveryCalls := 0
	discover := func(
		ctx context.Context,
		roots []string,
		options ...local.DiscoveryOptions,
	) (local.DiscoveryResult, error) {
		discoveryCalls++
		result, err := local.DiscoverRepositories(ctx, roots, options...)
		if err == nil && discoveryCalls >= 3 {
			result.Repositories = result.Repositories[:1]
			result.Warnings = append(result.Warnings, local.Warning{
				Kind:      local.WarningInspection,
				Root:      repositoryRoot,
				Path:      otherRepositoryPath,
				Operation: "simulated repository inspection",
				Err:       errors.New("simulated warning"),
			})
		}
		return result, err
	}
	git := &worktreeRemoveFailingRunner{
		Runner:   &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: io.Discard},
		failPath: parentPath,
	}

	var stdout, stderr bytes.Buffer
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Discover: discover,
		Git:      git,
		Getwd:    func() (string, error) { return root, nil },
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes"})
	err := command.Execute()
	if err == nil || !errors.Is(err, ErrRemoveSafety) ||
		!strings.Contains(err.Error(), "revalidate registered state") {
		t.Fatalf("Execute() error = %v, want shared repository-inventory failure", err)
	}
	if discoveryCalls != 3 || len(git.removals) != 0 {
		t.Fatalf("discovery calls=%d removals=%#v, want 3 and none", discoveryCalls, git.removals)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if info, statErr := os.Stat(parentPath); statErr != nil || !info.IsDir() {
		t.Fatalf("parent worktree changed: info=%v err=%v", info, statErr)
	}
}

func TestNewRemoveCommandWorktreeGoneContinuesAfterGitFailure(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")
	worktreeRemoveRunGit(t, repositoryPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")

	branches := []string{"feature-a-fails", "feature-b-succeeds"}
	paths := make([]string, 0, len(branches))
	for _, branch := range branches {
		path := filepath.Join(
			worktreeRoot,
			branch,
			"widget",
		)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", branch, path, "HEAD")
		upstream := "refs/remotes/origin/" + branch
		worktreeRemoveRunGit(t, repositoryPath, "update-ref", upstream, "HEAD")
		worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".remote", "origin")
		worktreeRemoveRunGit(t, repositoryPath, "config", "branch."+branch+".merge", "refs/heads/"+branch)
		worktreeRemoveRunGit(t, repositoryPath, "update-ref", "-d", upstream)
		paths = append(paths, path)
	}

	var stdout, stderr bytes.Buffer
	git := &worktreeRemoveFailingRunner{
		Runner:   &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: &stderr},
		failPath: paths[0],
	}
	command := newWorktreeRemoveTestCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Git:    git,
		Getwd:  func() (string, error) { return root, nil },
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes"})
	err := command.Execute()
	if got := ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(error=%v) = %d, want 1; stderr=%q", err, got, stderr.String())
	}
	if len(git.removals) != 2 || !physicalpath.SamePath(git.removals[0], paths[0]) ||
		!physicalpath.SamePath(git.removals[1], paths[1]) {
		t.Fatalf("WorktreeRemove paths = %#v, want %#v", git.removals, paths)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("failed candidate changed: %v", err)
	}
	if _, err := os.Lstat(paths[1]); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("independent candidate Lstat error = %v, want not exist", err)
	}
	if !strings.Contains(stderr.String(), "failed worktree \"feature-a-fails\"") ||
		!strings.Contains(stderr.String(), "removed worktree "+local.NormalizePathForOutput(paths[1])) {
		t.Fatalf("stderr = %q, want failure and continued success", stderr.String())
	}
	for _, branch := range branches {
		worktreeRemoveRunGit(t, repositoryPath, "show-ref", "--verify", "refs/heads/"+branch)
	}
}

type worktreeRemoveStaticResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver worktreeRemoveStaticResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

type worktreeRemoveRemoval struct {
	dir     string
	options gitcmd.WorktreeRemoveOptions
}

type worktreeRemoveFailingRunner struct {
	*gitcmd.Runner
	failPath string
	removals []string
}

func (git *worktreeRemoveFailingRunner) WorktreeRemove(
	ctx context.Context,
	dir string,
	options gitcmd.WorktreeRemoveOptions,
) error {
	git.removals = append(git.removals, options.Path)
	if physicalpath.SamePath(options.Path, git.failPath) {
		return errors.New("simulated removal failure")
	}
	return git.Runner.WorktreeRemove(ctx, dir, options)
}

type worktreeRemoveFakeGit struct {
	removals           []worktreeRemoveRemoval
	removeErr          error
	removeAction       func(string) error
	leavePath          bool
	postRemovalRecords []gitcmd.Worktree
	postRemovalListErr error
}

func (git *worktreeRemoveFakeGit) OutputDir(
	context.Context,
	string,
	...string,
) ([]byte, error) {
	return nil, errors.New("unexpected OutputDir call")
}

func (git *worktreeRemoveFakeGit) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	if len(git.removals) == 0 {
		return nil, errors.New("unexpected WorktreeList before removal")
	}
	return append([]gitcmd.Worktree(nil), git.postRemovalRecords...), git.postRemovalListErr
}

func (git *worktreeRemoveFakeGit) BranchUpstreams(
	context.Context,
	string,
) ([]gitcmd.BranchUpstream, error) {
	return nil, errors.New("unexpected BranchUpstreams call")
}

func (git *worktreeRemoveFakeGit) RefExists(
	context.Context,
	string,
	string,
) (bool, error) {
	return false, errors.New("unexpected RefExists call")
}

func (git *worktreeRemoveFakeGit) WorktreeRemove(
	_ context.Context,
	dir string,
	options gitcmd.WorktreeRemoveOptions,
) error {
	git.removals = append(git.removals, worktreeRemoveRemoval{
		dir:     dir,
		options: options,
	})
	if git.removeErr != nil {
		return git.removeErr
	}
	if git.removeAction != nil {
		return git.removeAction(options.Path)
	}
	if git.leavePath {
		return nil
	}
	return os.Remove(options.Path)
}

type worktreeRemoveFixture struct {
	root               string
	repositoryRoot     string
	worktreeRoot       string
	base               string
	target             string
	branch             string
	repository         local.Repository
	repositories       []local.Repository
	currentWorktree    local.Worktree
	managed            local.Worktree
	resolveManagedErr  error
	resolvedRepository local.Repository
	currentCalls       int
	warnings           []local.Warning
	discoveryErr       error
	discoveryCalls     int
	onDiscover         func(int)
	current            func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)
	git             *worktreeRemoveFakeGit
	cleanupRemovals []string
	removeErr       error
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stderrWriter    io.Writer
}

func worktreeRemoveNewFixture(t *testing.T, branch string) *worktreeRemoveFixture {
	t.Helper()

	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	base := worktreeRoot
	target := filepath.Join(base, branch, "widget")
	for _, path := range []string{repositoryPath, target} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repository := local.Repository{
		Identity:  "github.com/acme/widget",
		Host:      "github.com",
		Owner:     "acme",
		Repo:      "widget",
		Path:      repositoryPath,
		Root:      repositoryRoot,
		RootIndex: 0,
	}
	managed := local.Worktree{
		Repository: repository,
		Identity:   repository.Identity + "@" + branch,
		Slot:       branch,
		Path:       target,
		Branch:     branch,
	}
	return &worktreeRemoveFixture{
		root:            root,
		repositoryRoot:  repositoryRoot,
		worktreeRoot:    worktreeRoot,
		base:            base,
		target:          target,
		branch:          branch,
		repository:      repository,
		repositories:    []local.Repository{repository},
		currentWorktree: local.Worktree{Repository: repository, Main: true},
		managed:         managed,
		git:             &worktreeRemoveFakeGit{},
	}
}

func (fixture *worktreeRemoveFixture) worktreeRemoveDependencies() WorktreeRemoveDependencies {
	stderr := fixture.stderrWriter
	if stderr == nil {
		stderr = &fixture.stderr
	}
	current := fixture.current
	if current == nil {
		current = func(
			context.Context,
			string,
			string,
			[]local.Repository,
			...local.CurrentOptions,
		) (local.Current, error) {
			fixture.currentCalls++
			return local.Current{
				Repository: fixture.repository,
				Worktree:   fixture.currentWorktree,
			}, nil
		}
	}
	return WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{fixture.repositoryRoot},
			WorktreeRoot:    fixture.worktreeRoot,
		}},
		Discover: func(
			context.Context,
			[]string,
			...local.DiscoveryOptions,
		) (local.DiscoveryResult, error) {
			fixture.discoveryCalls++
			if fixture.onDiscover != nil {
				fixture.onDiscover(fixture.discoveryCalls)
			}
			return local.DiscoveryResult{
				Repositories: append([]local.Repository(nil), fixture.repositories...),
				Warnings:     append([]local.Warning(nil), fixture.warnings...),
			}, fixture.discoveryErr
		},
		Current: current,
		ResolveManaged: func(
			_ context.Context,
			repository local.Repository,
			_ string,
			_ string,
			_ ...local.ManagedWorktreeOptions,
		) (local.Worktree, error) {
			fixture.resolvedRepository = repository
			return fixture.managed, fixture.resolveManagedErr
		},
		Git:    fixture.git,
		Getwd:  func() (string, error) { return fixture.repository.Path, nil },
		Stdout: &fixture.stdout,
		Stderr: stderr,
		Remove: func(path string) error {
			fixture.cleanupRemovals = append(fixture.cleanupRemovals, path)
			if fixture.removeErr != nil {
				return fixture.removeErr
			}
			return os.Remove(path)
		},
	}
}

func (fixture *worktreeRemoveFixture) worktreeRemoveExecute(args ...string) error {
	command := newWorktreeRemoveTestCommand(fixture.worktreeRemoveDependencies())
	command.SetArgs(append([]string{"--worktree"}, args...))
	return command.Execute()
}

func newWorktreeRemoveTestCommand(dependencies WorktreeRemoveDependencies) *cobra.Command {
	return NewRemoveCommand(RemoveDependencies{Worktree: dependencies})
}

func worktreeRemoveAssertSuccess(t *testing.T, fixture *worktreeRemoveFixture) {
	t.Helper()
	if fixture.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", fixture.stdout.String())
	}
	if got, want := fixture.stderr.String(),
		"removed worktree "+local.NormalizePathForOutput(fixture.target)+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := os.Lstat(fixture.target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target Lstat error = %v, want not exist", err)
	}
	if len(fixture.git.removals) != 1 {
		t.Fatalf("WorktreeRemove() calls = %d, want 1", len(fixture.git.removals))
	}
	if got := fixture.git.removals[0]; got.dir != fixture.repository.Path ||
		got.options.Path != fixture.target {
		t.Fatalf("WorktreeRemove() call = %#v, want repository %q path %q",
			got, fixture.repository.Path, fixture.target)
	}
}

type worktreeRemoveErrorWriter struct {
	err error
}

func (writer *worktreeRemoveErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func worktreeRemovePhysicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func worktreeRemoveRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
}

func worktreeRemoveGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

type worktreeRemoveObservedRunner struct {
	*worktreeRemoveFailingRunner
	onList func(string)
}

func (git *worktreeRemoveObservedRunner) WorktreeList(ctx context.Context, path string) ([]gitcmd.Worktree, error) {
	git.onList(path)
	return git.Runner.WorktreeList(ctx, path)
}
