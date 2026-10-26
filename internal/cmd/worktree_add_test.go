package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

func TestAddWorktreeAutomaticResolution(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		configure    func(*worktreeAddFixture)
		wantOptions  gitcmd.WorktreeAddOptions
		wantAPICalls int
		wantSyncs    []ghcmd.SyncOptions
	}{
		{
			name:        "attaches existing local branch and ignores explicit commit-ish",
			args:        []string{"feature-local", "ignored"},
			configure:   func(f *worktreeAddFixture) { f.git.localBranches["feature-local"] = true },
			wantOptions: gitcmd.WorktreeAddOptions{Commitish: "feature-local"},
		},
		{
			name:        "delegates branch selection to Git without speculative API",
			args:        []string{"feature-requested"},
			wantOptions: gitcmd.WorktreeAddOptions{Commitish: "feature-requested"},
		},
		{
			name:        "creates branch from explicit commit-ish through Git",
			args:        []string{"feature-explicit", "release-base"},
			wantOptions: gitcmd.WorktreeAddOptions{Commitish: "release-base", NewBranch: "feature-explicit"},
		},
		{
			name:         "explicit new branch starts at local GitHub default",
			args:         []string{"-b", "feature-api-local"},
			configure:    func(f *worktreeAddFixture) { f.git.localBranches["main"] = true },
			wantOptions:  gitcmd.WorktreeAddOptions{Commitish: "refs/heads/main", NewBranch: "feature-api-local"},
			wantAPICalls: 1,
		},
		{
			name:         "explicit new branch synchronizes missing local default",
			args:         []string{"-b", "feature-api-sync"},
			configure:    func(f *worktreeAddFixture) { f.gh.syncCreatesLocalBranch = true },
			wantOptions:  gitcmd.WorktreeAddOptions{Commitish: "refs/heads/main", NewBranch: "feature-api-sync"},
			wantAPICalls: 1,
			wantSyncs:    []ghcmd.SyncOptions{{Host: "github.com", Source: "acme/widget", Branch: "main"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			branch := test.args[len(test.args)-1]
			if len(test.args) == 2 && test.args[0] != "-b" {
				branch = test.args[0]
			}
			fixture := worktreeAddNewFixture(t, branch)
			if test.configure != nil {
				test.configure(fixture)
			}
			if err := fixture.worktreeAddExecute(test.args...); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(fixture.git.adds) != 1 {
				t.Fatalf("WorktreeAdd() calls = %d, want 1", len(fixture.git.adds))
			}
			want := test.wantOptions
			want.Path = fixture.destination
			if got := fixture.git.adds[0].options; !reflect.DeepEqual(got, want) {
				t.Fatalf("WorktreeAdd options = %#v, want %#v", got, want)
			}
			if fixture.api.calls != test.wantAPICalls {
				t.Fatalf("DefaultBranch calls = %d, want %d", fixture.api.calls, test.wantAPICalls)
			}
			if !reflect.DeepEqual(fixture.gh.syncs, test.wantSyncs) {
				t.Fatalf("RepoSync calls = %#v, want %#v", fixture.gh.syncs, test.wantSyncs)
			}
		})
	}
}

func TestAddWorktreeFetchesDefaultBranchThroughAPIAndSync(t *testing.T) {
	t.Setenv("GH_HOST", "ambient.example.com")
	fixture := worktreeAddNewFixture(t, "feature-api-fetch")
	fixture.api.branch = "main"
	fixture.gh.syncCreatesLocalBranch = true
	if err := fixture.worktreeAddExecute("-b", "feature-api-fetch"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.api.calls != 1 || len(fixture.gh.syncs) != 1 {
		t.Fatalf("API calls=%d, syncs=%v; want one of each", fixture.api.calls, fixture.gh.syncs)
	}
	if fixture.gh.syncs[0].Host != "github.com" {
		t.Fatalf("sync host = %q, want github.com", fixture.gh.syncs[0].Host)
	}
}

func TestAddWorktreeModesAndForce(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		args        []string
		wantOptions gitcmd.WorktreeAddOptions
	}{
		{
			name:   "new branch",
			branch: "experiment-new",
			args:   []string{"-b", "-f", "experiment-new", "base"},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "base",
				NewBranch: "experiment-new",
				Force:     true,
			},
		},
		{
			name:   "reset branch",
			branch: "experiment-reset",
			args:   []string{"-B", "-f", "experiment-reset", "base"},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish:   "base",
				ResetBranch: "experiment-reset",
				Force:       true,
			},
		},
		{
			name:   "detached explicit target",
			branch: "review-123",
			args:   []string{"--detach", "-f", "review-123", "refs/pull/123/head"},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "refs/pull/123/head",
				Detach:    true,
				Force:     true,
			},
		},
		{
			name:   "detached positional target",
			branch: "release",
			args:   []string{"--detach", "-f", "release"},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "release",
				Detach:    true,
				Force:     true,
			},
		},
		{
			name:   "orphan branch",
			branch: "scratch-orphan",
			args:   []string{"--orphan", "-f", "scratch-orphan"},
			wantOptions: gitcmd.WorktreeAddOptions{
				NewBranch: "scratch-orphan",
				Orphan:    true,
				Force:     true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, test.branch)
			if err := fixture.worktreeAddExecute(test.args...); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			want := test.wantOptions
			want.Path = fixture.destination
			if got := fixture.git.adds[0].options; !reflect.DeepEqual(got, want) {
				t.Fatalf("WorktreeAdd options = %#v, want %#v", got, want)
			}
			if fixture.api.calls != 0 || len(fixture.gh.syncs) != 0 {
				t.Fatal("explicit creation modes must not query or sync a default branch")
			}
		})
	}
}

func TestAddWorktreeModeValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "new and reset",
			args: []string{"-b", "-B", "feature-x"},
			want: "mutually exclusive",
		},
		{
			name: "new and detached",
			args: []string{"-b", "--detach", "feature-x"},
			want: "mutually exclusive",
		},
		{
			name: "reset and orphan",
			args: []string{"-B", "--orphan", "feature-x"},
			want: "mutually exclusive",
		},
		{
			name: "orphan commit-ish",
			args: []string{"--orphan", "feature-x", "main"},
			want: "cannot have a start point",
		},
		{
			name: "invalid slot",
			args: []string{"feature-../x"},
			want: "invalid worktree branch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature-x")
			err := fixture.worktreeAddExecute(test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.want)
			}
			if !errors.Is(err, ErrCommandUsage) {
				t.Fatalf("Execute() error = %v, want ErrCommandUsage", err)
			}
			if fixture.resolver.calls != 0 {
				t.Fatalf("Resolve() calls = %d, want 0", fixture.resolver.calls)
			}
			if len(fixture.git.adds) != 0 {
				t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
			}
		})
	}
}

func TestAddWorktreeAPIAndSyncFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreeAddFixture)
		want      string
	}{
		{
			name: "API unavailable",
			configure: func(fixture *worktreeAddFixture) {
				fixture.api.err = errors.New("offline")
			},
			want: "provide an explicit commit-ish",
		},
		{
			name: "invalid API branch",
			configure: func(fixture *worktreeAddFixture) {
				fixture.api.branch = "HEAD"
			},
			want: "invalid default branch",
		},
		{
			name: "sync failure",
			configure: func(fixture *worktreeAddFixture) {
				fixture.gh.syncErr = errors.New("sync failed")
			},
			want: "sync failed",
		},
		{
			name:      "sync did not create ref",
			configure: func(fixture *worktreeAddFixture) {},
			want:      "did not materialize local default branch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature-api-failure")
			fixture.api.branch = "main"
			test.configure(fixture)

			err := fixture.worktreeAddExecute("-b", "feature-api-failure")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.want)
			}
			if len(fixture.git.adds) != 0 {
				t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
			}
		})
	}
}

func TestAddWorktreeRepositorySelection(t *testing.T) {
	t.Run("current descendant", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-current")
		fixture.git.localBranches["feature-current"] = true
		fixture.cwd = filepath.Join(fixture.repository.Path, "nested", "directory")

		if err := fixture.worktreeAddExecute("feature-current"); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if fixture.currentCalls != 1 || fixture.currentCwd != fixture.cwd {
			t.Fatalf(
				"Current() = (%d calls, cwd %q), want (1, %q)",
				fixture.currentCalls,
				fixture.currentCwd,
				fixture.cwd,
			)
		}
	})

	t.Run("explicit selector wins", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-selected")
		second := worktreeAddRepository(
			filepath.Join(fixture.base, "repositories-two"),
			"github.com/other/second",
			1,
		)
		if err := os.MkdirAll(second.Path, 0o755); err != nil {
			t.Fatalf("MkdirAll(second) error = %v", err)
		}
		fixture.repositories = append(fixture.repositories, second)
		fixture.repository = second
		fixture.destination = worktreeAddDestination(fixture.worktreeRoot, second, fixture.branch)
		fixture.git.localBranches["feature-selected"] = true
		fixture.currentErr = errors.New("must not be called")

		if err := fixture.worktreeAddExecute("-R", "other/second", "feature-selected"); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if fixture.currentCalls != 0 {
			t.Fatalf("Current() calls = %d, want 0", fixture.currentCalls)
		}
		if got, want := fixture.git.adds[0].dir, second.Path; got != want {
			t.Fatalf("WorktreeAdd dir = %q, want %q", got, want)
		}
	})

	t.Run("outside requires repo", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-outside")
		fixture.currentErr = local.ErrCurrentUnmanaged

		err := fixture.worktreeAddExecute("feature-outside")
		if err == nil || !strings.Contains(err.Error(), "use -R") {
			t.Fatalf("Execute() error = %v, want -R guidance", err)
		}
		if len(fixture.git.adds) != 0 {
			t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
		}
	})

	t.Run("explicit empty selector does not fall back to current", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-empty-selector")
		fixture.currentErr = errors.New("must not be called")

		err := fixture.worktreeAddExecute("--repo=", "feature-empty-selector")
		if !errors.Is(err, local.ErrInvalidSelector) {
			t.Fatalf("Execute() error = %v, want ErrInvalidSelector", err)
		}
		if fixture.currentCalls != 0 {
			t.Fatalf("Current() calls = %d, want 0", fixture.currentCalls)
		}
	})
}

func TestAddWorktreeHandlesDuplicateRepositoryIdentity(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "current"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature-duplicate")
			duplicate := fixture.repository
			duplicate.Root = filepath.Join(fixture.base, "repositories-two")
			duplicate.Path = filepath.Join(
				duplicate.Root,
				duplicate.Host,
				duplicate.Owner,
				duplicate.Repo,
			)
			duplicate.RootIndex = 1
			if err := os.MkdirAll(duplicate.Path, 0o755); err != nil {
				t.Fatalf("MkdirAll(duplicate) error = %v", err)
			}
			fixture.repositories = append(fixture.repositories, duplicate)
			fixture.git.localBranches["feature-duplicate"] = true

			args := []string{"feature-duplicate"}
			if explicit {
				args = append([]string{"-R", fixture.repository.Identity}, args...)
			}
			err := fixture.worktreeAddExecute(args...)
			if explicit {
				if !errors.Is(err, local.ErrRepositoryAmbiguous) {
					t.Fatalf("Execute() error = %v, want ErrRepositoryAmbiguous", err)
				}
				if len(fixture.git.adds) != 0 {
					t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(fixture.git.adds) != 1 {
				t.Fatalf("WorktreeAdd() calls = %d, want 1", len(fixture.git.adds))
			}
			if got := fixture.git.adds[0].dir; got != fixture.repository.Path {
				t.Fatalf("WorktreeAdd() dir = %q, want current %q", got, fixture.repository.Path)
			}
		})
	}
}

func TestAddWorktreePathSafety(t *testing.T) {
	t.Run("registered parent slot", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feat-x")
		fixture.initialWorktrees = append(fixture.initialWorktrees, local.Worktree{
			Repository: fixture.repository,
			Identity:   fixture.repository.Identity + "@feat-x",
			Slot:       "feat-x",
			Path:       filepath.Join(fixture.worktreeRoot, "existing"),
			Branch:     "feat",
		})
		fixture.git.localBranches["feat-x"] = true

		err := fixture.worktreeAddExecute("-f", "feat-x")
		if !errors.Is(err, local.ErrSlotCollision) {
			t.Fatalf("Execute() error = %v, want ErrSlotCollision", err)
		}
		if len(fixture.git.adds) != 0 {
			t.Fatalf("force bypassed registered collision")
		}
	})

	t.Run("existing exact destination", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-existing-path")
		if err := os.MkdirAll(fixture.destination, 0o755); err != nil {
			t.Fatalf("MkdirAll(destination) error = %v", err)
		}
		fixture.git.localBranches[fixture.branch] = true

		err := fixture.worktreeAddExecute("-f", fixture.branch)
		if !errors.Is(err, local.ErrSlotCollision) {
			t.Fatalf("Execute() error = %v, want ErrSlotCollision", err)
		}
	})

	t.Run("filesystem Git marker prefix", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature-child")
		prefix := filepath.Dir(fixture.destination)
		if err := os.MkdirAll(prefix, 0o755); err != nil {
			t.Fatalf("MkdirAll(prefix) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(prefix, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(.git) error = %v", err)
		}
		fixture.git.localBranches[fixture.branch] = true

		err := fixture.worktreeAddExecute(fixture.branch)
		if !errors.Is(err, local.ErrSlotCollision) {
			t.Fatalf("Execute() error = %v, want ErrSlotCollision", err)
		}
	})

	t.Run("symlink escape", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation requires platform privileges")
		}
		fixture := worktreeAddNewFixture(t, "feature-symlink")
		outside := filepath.Join(fixture.base, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatalf("MkdirAll(outside) error = %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(fixture.worktreeRoot, fixture.branch)); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}
		fixture.git.localBranches[fixture.branch] = true

		err := fixture.worktreeAddExecute(fixture.branch)
		if !errors.Is(err, rootpkg.ErrUnsafeTarget) {
			t.Fatalf("Execute() error = %v, want root.ErrUnsafeTarget", err)
		}
		if len(fixture.git.adds) != 0 {
			t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
		}
	})
}

func TestAddWorktreeCleansOnlyNewEmptyParents(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-cleanup")
	basePath := filepath.Dir(filepath.Dir(fixture.destination))
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(basePath) error = %v", err)
	}
	sentinel := filepath.Join(basePath, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile(sentinel) error = %v", err)
	}
	fixture.git.localBranches[fixture.branch] = true
	fixture.git.addErr = errors.New("git refused")

	err := fixture.worktreeAddExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "git refused") {
		t.Fatalf("Execute() error = %v, want Git failure", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("existing data was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(fixture.destination)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("new empty branch parent remains or Lstat error = %v", err)
	}
	if _, err := os.Stat(basePath); err != nil {
		t.Fatalf("existing base was removed: %v", err)
	}
}

func TestAddWorktreeCleansAfterMkdirFailure(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-deep-cleanup")
	fixture.git.localBranches[fixture.branch] = true
	mkdirCalls := 0
	realMkdir := os.Mkdir
	fixture.mkdir = func(path string, mode fs.FileMode) error {
		mkdirCalls++
		if mkdirCalls == 1 {
			return errors.New("mkdir failed")
		}
		return realMkdir(path, mode)
	}

	err := fixture.worktreeAddExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "mkdir failed") {
		t.Fatalf("Execute() error = %v, want mkdir failure", err)
	}
	if len(fixture.git.adds) != 0 {
		t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
	}
	if entries, readErr := os.ReadDir(fixture.worktreeRoot); readErr != nil {
		t.Fatalf("ReadDir(worktree root) error = %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("new parent directories remain: %#v", entries)
	}
}

func TestAddWorktreePostValidationFailurePreservesGitState(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreeAddFixture)
		want      string
	}{
		{
			name: "missing registration",
			configure: func(fixture *worktreeAddFixture) {
				fixture.omitPostWorktree = true
			},
			want: "registration validation failed",
		},
		{
			name: "association rejection",
			configure: func(fixture *worktreeAddFixture) {
				fixture.associationErr = errors.New("association mismatch")
			},
			want: "association mismatch",
		},
		{
			name: "wrong registered branch",
			configure: func(fixture *worktreeAddFixture) {
				fixture.postBranch = "wrong"
			},
			want: "registered branch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature-post")
			fixture.git.localBranches[fixture.branch] = true
			test.configure(fixture)

			err := fixture.worktreeAddExecute(fixture.branch)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.want)
			}
			if _, statErr := os.Stat(fixture.destination); statErr != nil {
				t.Fatalf("successful Git worktree state was deleted: %v", statErr)
			}
			if fixture.stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", fixture.stdout.String())
			}
		})
	}
}

func TestAddWorktreePropagatesWriteFailureWithoutDeletingWorktree(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-write")
	fixture.git.localBranches[fixture.branch] = true
	wantErr := errors.New("write failed")
	fixture.stdoutWriter = worktreeAddFailingWriter{err: wantErr}

	err := fixture.worktreeAddExecute(fixture.branch)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	if _, statErr := os.Stat(fixture.destination); statErr != nil {
		t.Fatalf("worktree was deleted after output failure: %v", statErr)
	}
}

func TestAddWorktreePropagatesDiagnosticWriteFailure(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-diagnostic-write")
	wantErr := errors.New("diagnostic write failed")
	fixture.stderrWriter = worktreeAddFailingWriter{err: wantErr}
	fixture.discoveryWarning = &local.Warning{
		Kind:      local.WarningInspection,
		Root:      fixture.repositoryRoot,
		RootIndex: 0,
		Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
		Operation: "inspect",
		Err:       errors.New("unreadable"),
	}

	err := fixture.worktreeAddExecute(fixture.branch)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	if len(fixture.git.adds) != 0 {
		t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
	}
}

func TestAddWorktreeWritesDiscoveryWarningsToStderr(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-warning")
	fixture.git.localBranches[fixture.branch] = true
	fixture.discoveryWarning = &local.Warning{
		Kind:      local.WarningInspection,
		Root:      fixture.repositoryRoot,
		RootIndex: 0,
		Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
		Operation: "inspect",
		Err:       errors.New("unreadable"),
	}

	if err := fixture.worktreeAddExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := fixture.stderr.String(); !strings.Contains(got, "warning:") ||
		!strings.Contains(got, "unreadable") {
		t.Fatalf("stderr = %q, want discovery warning", got)
	}
	if strings.Contains(fixture.stdout.String(), "warning:") {
		t.Fatalf("stdout contains diagnostics: %q", fixture.stdout.String())
	}
}

func TestAddWorktreeRealGitLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	base := worktreeAddPhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repository := worktreeAddRepository(
		repositoryRoot,
		"github.com/acme/widget",
		0,
	)
	for _, path := range []string{repository.Path, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}
	worktreeAddRunGit(t, repository.Path, "init", "-b", "main")
	worktreeAddRunGit(t, repository.Path, "config", "user.name", "gh-qw test")
	worktreeAddRunGit(t, repository.Path, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repository.Path, "README"), []byte("main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(README) error = %v", err)
	}
	worktreeAddRunGit(t, repository.Path, "add", "README")
	worktreeAddRunGit(t, repository.Path, "commit", "-m", "initial")
	branch := "feature-lifecycle"
	worktreeAddRunGit(t, repository.Path, "branch", branch)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := newWorktreeAddTestCommand(WorktreeAddDependencies{
		Resolver: &worktreeAddResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Getwd: func() (string, error) {
			return repository.Path, nil
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"--worktree", branch})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, stderr.String())
	}

	destination := worktreeAddDestination(worktreeRoot, repository, branch)
	if got, want := stdout.String(), filepath.ToSlash(destination)+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := strings.TrimSpace(worktreeAddGitOutput(t, destination, "branch", "--show-current")); got != branch {
		t.Fatalf("checked-out branch = %q, want %q", got, branch)
	}
	if info, err := os.Lstat(filepath.Join(destination, ".git")); err != nil {
		t.Fatalf("Lstat(linked .git) error = %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("linked .git mode = %v, want regular file", info.Mode())
	}
}

type worktreeAddFixture struct {
	t              *testing.T
	base           string
	repositoryRoot string
	worktreeRoot   string
	repository     local.Repository
	repositories   []local.Repository
	branch         string
	destination    string

	resolver         *worktreeAddResolver
	git              *worktreeAddGit
	gh               *worktreeAddGh
	api              *worktreeAddAPI
	initialWorktrees []local.Worktree
	enumerateCalls   int
	omitPostWorktree bool
	postBranch       string
	postPath         string
	postDetached     bool
	associationErr   error
	associationCalls int
	discoveryWarning *local.Warning
	discoverCalls    int
	currentCalls     int
	currentCwd       string
	currentErr       error
	cwd              string
	mkdir            func(string, fs.FileMode) error
	remove           func(string) error
	stdout           bytes.Buffer
	stdoutWriter     io.Writer
	stderr           bytes.Buffer
	stderrWriter     io.Writer
}

func worktreeAddNewFixture(t *testing.T, branch string) *worktreeAddFixture {
	t.Helper()
	base := worktreeAddPhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	repository := worktreeAddRepository(
		repositoryRoot,
		"github.com/acme/widget",
		0,
	)
	for _, path := range []string{repository.Path, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}

	git := &worktreeAddGit{localBranches: make(map[string]bool)}
	fixture := &worktreeAddFixture{
		t:              t,
		base:           base,
		repositoryRoot: repositoryRoot,
		worktreeRoot:   worktreeRoot,
		repository:     repository,
		repositories:   []local.Repository{repository},
		branch:         branch,
		resolver: &worktreeAddResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		git: git,
		gh:  &worktreeAddGh{git: git},
		api: &worktreeAddAPI{branch: "main"},
		cwd: repository.Path,
	}
	fixture.destination = worktreeAddDestination(worktreeRoot, repository, branch)
	fixture.initialWorktrees = []local.Worktree{{
		Repository: repository,
		Identity:   repository.Identity,
		Path:       repository.Path,
		HEAD:       strings.Repeat("1", 40),
		Branch:     "main",
		Main:       true,
	}}
	fixture.git.add = func(options gitcmd.WorktreeAddOptions) error {
		return os.Mkdir(options.Path, 0o755)
	}
	return fixture
}

func (fixture *worktreeAddFixture) worktreeAddExecute(args ...string) error {
	fixture.t.Helper()
	stdout := fixture.stdoutWriter
	if stdout == nil {
		stdout = &fixture.stdout
	}
	mkdir := fixture.mkdir
	if mkdir == nil {
		mkdir = os.Mkdir
	}
	remove := fixture.remove
	if remove == nil {
		remove = os.Remove
	}
	stderr := fixture.stderrWriter
	if stderr == nil {
		stderr = &fixture.stderr
	}

	command := newWorktreeAddTestCommand(WorktreeAddDependencies{
		Resolver: fixture.resolver,
		Discover: func(
			_ context.Context,
			_ []string,
			_ ...local.DiscoveryOptions,
		) (local.DiscoveryResult, error) {
			fixture.discoverCalls++
			result := local.DiscoveryResult{
				Repositories: append([]local.Repository(nil), fixture.repositories...),
			}
			if fixture.discoveryWarning != nil {
				result.Warnings = []local.Warning{*fixture.discoveryWarning}
			}
			return result, nil
		},
		Current: func(
			_ context.Context,
			cwd string,
			_ string,
			_ []local.Repository,
			_ ...local.CurrentOptions,
		) (local.Current, error) {
			fixture.currentCalls++
			fixture.currentCwd = cwd
			if fixture.currentErr != nil {
				return local.Current{}, fixture.currentErr
			}
			return local.Current{
				Repository: fixture.repository,
				Worktree:   fixture.initialWorktrees[0],
			}, nil
		},
		Enumerate: func(
			_ context.Context,
			repository local.Repository,
			_ string,
			_ ...local.WorktreeOptions,
		) ([]local.Worktree, error) {
			fixture.enumerateCalls++
			if fixture.enumerateCalls == 1 {
				return append([]local.Worktree(nil), fixture.initialWorktrees...), nil
			}
			result := append([]local.Worktree(nil), fixture.initialWorktrees...)
			if fixture.omitPostWorktree {
				return result, nil
			}
			path := fixture.postPath
			if path == "" {
				path = fixture.destination
			}
			branch := fixture.postBranch
			detached := fixture.postDetached
			if len(fixture.git.adds) != 0 && fixture.git.adds[0].options.Detach {
				detached = true
			}
			if branch == "" && !detached {
				branch = fixture.branch
			}
			result = append(result, local.Worktree{
				Repository: repository,
				Identity:   repository.Identity + "@" + fixture.branch,
				Slot:       fixture.branch,
				Path:       path,
				HEAD:       strings.Repeat("2", 40),
				Branch:     branch,
				Detached:   detached,
			})
			return result, nil
		},
		ValidateAssociation: func(
			_ context.Context,
			_ local.Repository,
			_ local.Worktree,
			_ string,
			_ ...local.AssociationOptions,
		) error {
			fixture.associationCalls++
			return fixture.associationErr
		},
		Git:    fixture.git,
		Gh:     fixture.gh,
		API:    fixture.api,
		Getwd:  func() (string, error) { return fixture.cwd, nil },
		Mkdir:  mkdir,
		Remove: remove,
		Stdout: stdout,
		Stderr: stderr,
	})
	command.SetArgs(append([]string{"--worktree"}, args...))
	return command.Execute()
}

func newWorktreeAddTestCommand(deps WorktreeAddDependencies) *cobra.Command {
	return NewAddCommand(AddDependencies{
		Resolver: deps.Resolver,
		Worktree: deps,
		Stdout:   deps.Stdout,
		Stderr:   deps.Stderr,
	})
}

type worktreeAddResolver struct {
	result rootpkg.Result
	err    error
	calls  int
}

func (resolver *worktreeAddResolver) Resolve() (rootpkg.Result, error) {
	resolver.calls++
	return resolver.result, resolver.err
}

type worktreeAddGitCall struct {
	dir     string
	options gitcmd.WorktreeAddOptions
}

type worktreeAddGit struct {
	localBranches map[string]bool

	add            func(gitcmd.WorktreeAddOptions) error
	addErr         error
	outputErr      error
	localBranchErr error

	adds []worktreeAddGitCall
}

func (git *worktreeAddGit) OutputDir(
	_ context.Context,
	_ string,
	args ...string,
) ([]byte, error) {
	if git.outputErr != nil {
		return nil, git.outputErr
	}
	return nil, fmt.Errorf("unexpected OutputDir arguments: %#v", args)
}

func (git *worktreeAddGit) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	return nil, nil
}

func (git *worktreeAddGit) WorktreeAdd(
	_ context.Context,
	dir string,
	options gitcmd.WorktreeAddOptions,
) error {
	git.adds = append(git.adds, worktreeAddGitCall{dir: dir, options: options})
	if git.addErr != nil {
		return git.addErr
	}
	if git.add != nil {
		return git.add(options)
	}
	return nil
}

func (git *worktreeAddGit) LocalBranchExists(
	_ context.Context,
	_ string,
	branch string,
) (bool, error) {
	if git.localBranchErr != nil {
		return false, git.localBranchErr
	}
	return git.localBranches[branch], nil
}

// worktreeAddGh simulates synchronization materializing the local default branch.
type worktreeAddGh struct {
	git *worktreeAddGit

	syncErr                error
	syncCreatesLocalBranch bool

	syncs []ghcmd.SyncOptions
}

func (gh *worktreeAddGh) RepoSync(
	_ context.Context,
	_ string,
	options ghcmd.SyncOptions,
) error {
	gh.syncs = append(gh.syncs, options)
	if gh.syncErr != nil {
		return gh.syncErr
	}
	if gh.syncCreatesLocalBranch {
		gh.git.localBranches[options.Branch] = true
	}
	return nil
}

type worktreeAddAPI struct {
	branch string
	err    error
	calls  int
	host   string
	owner  string
	repo   string
}

func (api *worktreeAddAPI) DefaultBranch(
	_ context.Context,
	host string,
	owner string,
	repo string,
) (string, error) {
	api.calls++
	api.host = host
	api.owner = owner
	api.repo = repo
	return api.branch, api.err
}

type worktreeAddFailingWriter struct {
	err error
}

func (writer worktreeAddFailingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func worktreeAddRepository(rootPath, identity string, rootIndex int) local.Repository {
	parts := strings.Split(identity, "/")
	return local.Repository{
		Identity:  identity,
		Host:      parts[0],
		Owner:     parts[1],
		Repo:      parts[2],
		Path:      filepath.Join(rootPath, parts[0], parts[1], parts[2]),
		Root:      rootPath,
		RootIndex: rootIndex,
	}
}

func worktreeAddDestination(
	worktreeRoot string,
	repository local.Repository,
	branch string,
) string {
	return filepath.Join(worktreeRoot, branch, repository.Repo)
}

func worktreeAddRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
}

func worktreeAddGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func worktreeAddPhysicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return physical
}

var _ io.Writer = worktreeAddFailingWriter{}
var _ WorktreeAddGit = (*worktreeAddGit)(nil)
var _ WorktreeAddAPI = (*worktreeAddAPI)(nil)
