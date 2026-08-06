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

	"github.com/daiksud/gh-qw/internal/ghauth"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewWorktreeAddCommandAutomaticResolution(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		configure     func(*worktreeAddFixture)
		wantOptions   gitcmd.WorktreeAddOptions
		wantAPICalls  int
		wantSyncs     []ghcmd.SyncOptions
		wantRevisions []string
	}{
		{
			name: "attaches existing local branch and ignores commit-ish",
			args: []string{"feature/local", "missing-ignored"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.localBranches["feature/local"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "feature/local",
			},
		},
		{
			name: "creates tracking branch from origin",
			args: []string{"feature/origin"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.remoteBranches["origin/feature/origin"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "origin/feature/origin",
				NewBranch: "feature/origin",
				Tracking:  gitcmd.TrackingEnabled,
			},
		},
		{
			name: "creates tracking branch from sole other remote",
			args: []string{"feature/upstream"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.remotes = []string{"upstream"}
				fixture.git.remoteBranches["upstream/feature/upstream"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "upstream/feature/upstream",
				NewBranch: "feature/upstream",
				Tracking:  gitcmd.TrackingEnabled,
			},
		},
		{
			name: "creates branch from explicit commit-ish",
			args: []string{"feature/explicit", "release-base"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.revisions["release-base"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "release-base",
				NewBranch: "feature/explicit",
			},
			wantRevisions: []string{"release-base"},
		},
		{
			name: "creates branch from local API default branch",
			args: []string{"feature/api-local"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.api.branch = "main"
				fixture.git.localBranches["main"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "main",
				NewBranch: "feature/api-local",
			},
			wantAPICalls: 1,
		},
		{
			name: "creates branch from existing API default remote ref",
			args: []string{"feature/api-remote"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.api.branch = "main"
				fixture.git.remoteBranches["origin/main"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "origin/main",
				NewBranch: "feature/api-remote",
			},
			wantAPICalls: 1,
		},
		{
			name: "syncs missing API default branch",
			args: []string{"feature/api-fetch"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.api.branch = "main"
				fixture.gh.syncCreatesRemoteBranch = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "origin/main",
				NewBranch: "feature/api-fetch",
			},
			wantAPICalls: 1,
			wantSyncs: []ghcmd.SyncOptions{{
				Source: "acme/widget",
				Branch: "main",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			branch := test.args[0]
			fixture := worktreeAddNewFixture(t, branch)
			if test.configure != nil {
				test.configure(fixture)
			}

			err := fixture.worktreeAddExecute(test.args...)
			if err != nil {
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
				t.Fatalf("DefaultBranch() calls = %d, want %d", fixture.api.calls, test.wantAPICalls)
			}
			if got := fixture.gh.syncs; !reflect.DeepEqual(got, test.wantSyncs) {
				t.Fatalf("Sync options = %#v, want %#v", got, test.wantSyncs)
			}
			if got := fixture.git.revisionQueries; !reflect.DeepEqual(got, test.wantRevisions) {
				t.Fatalf("RevisionExists queries = %#v, want %#v", got, test.wantRevisions)
			}
			if got, want := fixture.stdout.String(), filepath.ToSlash(fixture.destination)+"\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestNewWorktreeAddCommandPassesResolvedTokenToAPIAndSync(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/api-fetch")
	fixture.api.branch = "main"
	fixture.gh.syncCreatesRemoteBranch = true
	fixture.accountResolver.resolution = ghauth.Resolution{
		Source: ghauth.SourceSelected,
		Login:  "acme-bot",
		Token:  "gho_scoped",
	}

	if err := fixture.worktreeAddExecute("feature/api-fetch"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.api.token != "gho_scoped" {
		t.Fatalf("DefaultBranch() token = %q, want %q", fixture.api.token, "gho_scoped")
	}
	if len(fixture.gh.syncs) != 1 || fixture.gh.syncs[0].Token != "gho_scoped" {
		t.Fatalf("RepoSync() options = %#v, want Token %q", fixture.gh.syncs, "gho_scoped")
	}
	if calls := fixture.accountResolver.calls; len(calls) != 1 ||
		calls[0].host != "github.com" || calls[0].owner != "acme" {
		t.Fatalf("Resolve() calls = %#v, want one call for (github.com, acme)", calls)
	}
}

func TestNewWorktreeAddCommandStopsBeforeAPIWhenAccountResolutionFails(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/api-fetch")
	wantErr := errors.New("account cache is unreadable")
	fixture.accountResolver.err = wantErr

	err := fixture.worktreeAddExecute("feature/api-fetch")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if fixture.api.calls != 0 {
		t.Fatalf("DefaultBranch() calls = %d, want 0 after resolution failure", fixture.api.calls)
	}
	if len(fixture.gh.syncs) != 0 {
		t.Fatalf("RepoSync() calls = %#v, want none after resolution failure", fixture.gh.syncs)
	}
}

func TestNewWorktreeAddCommandSkipsAccountResolutionForTheCommonCase(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreeAddFixture)
	}{
		{
			name: "existing local branch",
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.localBranches["feature/local"] = true
			},
		},
		{
			name: "existing origin branch",
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.remoteBranches["origin/feature/local"] = true
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature/local")
			test.configure(fixture)

			if err := fixture.worktreeAddExecute("feature/local"); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(fixture.accountResolver.calls) != 0 {
				t.Fatalf(
					"Resolve() calls = %#v, want none: attaching to an existing branch must never trigger gh account resolution or a prompt",
					fixture.accountResolver.calls,
				)
			}
		})
	}
}

func TestNewWorktreeAddCommandSkipsAccountResolutionForExplicitCommitish(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/explicit")
	fixture.git.revisions["release-base"] = true

	if err := fixture.worktreeAddExecute("feature/explicit", "release-base"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.accountResolver.calls) != 0 {
		t.Fatalf("Resolve() calls = %#v, want none for an explicit commit-ish", fixture.accountResolver.calls)
	}
}

func TestNewWorktreeAddCommandSyncFailureHintsTheSelectedAccount(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/api-fetch")
	fixture.api.branch = "main"
	fixture.gh.syncErr = errors.New("sync failed")
	fixture.accountResolver.resolution = ghauth.Resolution{
		Source: ghauth.SourceSelected,
		Login:  "TE-DaikiSudo",
		Token:  "gho_scoped",
	}

	err := fixture.worktreeAddExecute("feature/api-fetch")
	if err == nil || !strings.Contains(err.Error(), "sync failed") {
		t.Fatalf("Execute() error = %v, want it to wrap the sync failure", err)
	}
	if !strings.Contains(err.Error(), `"TE-DaikiSudo"`) {
		t.Fatalf("Execute() error = %q, want it to name the selected account", err.Error())
	}
}

func TestNewWorktreeAddCommandModesAndForce(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		args        []string
		configure   func(*worktreeAddFixture)
		wantOptions gitcmd.WorktreeAddOptions
	}{
		{
			name:   "new branch",
			branch: "experiment/new",
			args:   []string{"-b", "-f", "experiment/new", "base"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.revisions["base"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "base",
				NewBranch: "experiment/new",
				Force:     true,
			},
		},
		{
			name:   "reset branch",
			branch: "experiment/reset",
			args:   []string{"-B", "-f", "experiment/reset", "base"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.localBranches["experiment/reset"] = true
				fixture.git.revisions["base"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish:   "base",
				ResetBranch: "experiment/reset",
				Force:       true,
			},
		},
		{
			name:   "detached explicit target",
			branch: "review/123",
			args:   []string{"--detach", "-f", "review/123", "refs/pull/123/head"},
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.revisions["refs/pull/123/head"] = true
			},
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
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.revisions["release"] = true
			},
			wantOptions: gitcmd.WorktreeAddOptions{
				Commitish: "release",
				Detach:    true,
				Force:     true,
			},
		},
		{
			name:   "orphan branch",
			branch: "scratch/orphan",
			args:   []string{"--orphan", "-f", "scratch/orphan"},
			wantOptions: gitcmd.WorktreeAddOptions{
				NewBranch: "scratch/orphan",
				Orphan:    true,
				Force:     true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, test.branch)
			if test.configure != nil {
				test.configure(fixture)
			}

			if err := fixture.worktreeAddExecute(test.args...); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			want := test.wantOptions
			want.Path = fixture.destination
			if got := fixture.git.adds[0].options; !reflect.DeepEqual(got, want) {
				t.Fatalf("WorktreeAdd options = %#v, want %#v", got, want)
			}
		})
	}
}

func TestNewWorktreeAddCommandModeValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "new and reset",
			args: []string{"-b", "-B", "feature/x"},
			want: "mutually exclusive",
		},
		{
			name: "new and detached",
			args: []string{"-b", "--detach", "feature/x"},
			want: "mutually exclusive",
		},
		{
			name: "reset and orphan",
			args: []string{"-B", "--orphan", "feature/x"},
			want: "mutually exclusive",
		},
		{
			name: "orphan commit-ish",
			args: []string{"--orphan", "feature/x", "main"},
			want: "does not accept",
		},
		{
			name: "invalid slot",
			args: []string{"feature/../x"},
			want: "invalid worktree branch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature/x")
			err := fixture.worktreeAddExecute(test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.want)
			}
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want repospec.ErrUsage", err)
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

func TestNewWorktreeAddCommandNewBranchFailsWhenLocalExists(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/existing")
	fixture.git.localBranches["feature/existing"] = true
	fixture.git.revisions["base"] = true

	err := fixture.worktreeAddExecute("-b", "feature/existing", "base")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Execute() error = %v, want existing branch error", err)
	}
	if len(fixture.git.adds) != 0 {
		t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
	}
	if len(fixture.git.revisionQueries) != 0 {
		t.Fatalf("commit-ish was consulted after existing -b branch: %#v", fixture.git.revisionQueries)
	}
}

func TestNewWorktreeAddCommandRejectsRemoteAmbiguity(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/shared")
	fixture.git.remotes = []string{"upstream", "fork"}
	fixture.git.remoteBranches["upstream/feature/shared"] = true
	fixture.git.remoteBranches["fork/feature/shared"] = true
	fixture.git.revisions["explicit-ignored"] = true

	err := fixture.worktreeAddExecute("feature/shared", "explicit-ignored")
	if err == nil || !strings.Contains(err.Error(), "ambiguous across remotes: fork, upstream") {
		t.Fatalf("Execute() error = %v, want sorted remote ambiguity", err)
	}
	if fixture.api.calls != 0 {
		t.Fatalf("DefaultBranch() calls = %d, want 0", fixture.api.calls)
	}
	if len(fixture.git.revisionQueries) != 0 {
		t.Fatalf("explicit commit-ish was consulted: %#v", fixture.git.revisionQueries)
	}
	if len(fixture.git.adds) != 0 {
		t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
	}
}

func TestNewWorktreeAddCommandOriginWinsRemoteAmbiguity(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/shared")
	fixture.git.remotes = []string{"origin", "upstream", "fork"}
	fixture.git.remoteBranches["origin/feature/shared"] = true
	fixture.git.remoteBranches["upstream/feature/shared"] = true
	fixture.git.remoteBranches["fork/feature/shared"] = true

	if err := fixture.worktreeAddExecute("feature/shared"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := fixture.git.adds[0].options.Commitish, "origin/feature/shared"; got != want {
		t.Fatalf("Commitish = %q, want %q", got, want)
	}
}

func TestNewWorktreeAddCommandAPIAndSyncFailures(t *testing.T) {
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
			name: "multiple fetch remotes without origin",
			configure: func(fixture *worktreeAddFixture) {
				fixture.git.remotes = []string{"fork", "upstream"}
			},
			want: "multiple remotes and no origin",
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
			want:      "without creating the remote-tracking ref",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature/api-failure")
			fixture.api.branch = "main"
			test.configure(fixture)

			err := fixture.worktreeAddExecute("feature/api-failure")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute() error = %v, want containing %q", err, test.want)
			}
			if len(fixture.git.adds) != 0 {
				t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
			}
		})
	}
}

func TestNewWorktreeAddCommandRepositorySelection(t *testing.T) {
	t.Run("current descendant", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature/current")
		fixture.git.localBranches["feature/current"] = true
		fixture.cwd = filepath.Join(fixture.repository.Path, "nested", "directory")

		if err := fixture.worktreeAddExecute("feature/current"); err != nil {
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
		fixture := worktreeAddNewFixture(t, "feature/selected")
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
		fixture.git.localBranches["feature/selected"] = true
		fixture.currentErr = errors.New("must not be called")

		if err := fixture.worktreeAddExecute("-R", "other/second", "feature/selected"); err != nil {
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
		fixture := worktreeAddNewFixture(t, "feature/outside")
		fixture.currentErr = local.ErrCurrentUnmanaged

		err := fixture.worktreeAddExecute("feature/outside")
		if err == nil || !strings.Contains(err.Error(), "use -R") {
			t.Fatalf("Execute() error = %v, want -R guidance", err)
		}
		if len(fixture.git.adds) != 0 {
			t.Fatalf("WorktreeAdd() calls = %d, want 0", len(fixture.git.adds))
		}
	})

	t.Run("explicit empty selector does not fall back to current", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature/empty-selector")
		fixture.currentErr = errors.New("must not be called")

		err := fixture.worktreeAddExecute("--repo=", "feature/empty-selector")
		if !errors.Is(err, local.ErrInvalidSelector) {
			t.Fatalf("Execute() error = %v, want ErrInvalidSelector", err)
		}
		if fixture.currentCalls != 0 {
			t.Fatalf("Current() calls = %d, want 0", fixture.currentCalls)
		}
	})
}

func TestNewWorktreeAddCommandHandlesDuplicateRepositoryIdentity(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "current"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			fixture := worktreeAddNewFixture(t, "feature/duplicate")
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
			fixture.git.localBranches["feature/duplicate"] = true

			args := []string{"feature/duplicate"}
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

func TestNewWorktreeAddCommandPathSafety(t *testing.T) {
	t.Run("registered parent slot", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feat/x")
		fixture.initialWorktrees = append(fixture.initialWorktrees, local.Worktree{
			Repository: fixture.repository,
			Identity:   fixture.repository.Identity + "@feat",
			Slot:       "feat",
			Path:       filepath.Join(fixture.worktreeRoot, "existing"),
			Branch:     "feat",
		})
		fixture.git.localBranches["feat/x"] = true

		err := fixture.worktreeAddExecute("-f", "feat/x")
		if !errors.Is(err, local.ErrSlotCollision) {
			t.Fatalf("Execute() error = %v, want ErrSlotCollision", err)
		}
		if len(fixture.git.adds) != 0 {
			t.Fatalf("force bypassed registered collision")
		}
	})

	t.Run("existing exact destination", func(t *testing.T) {
		fixture := worktreeAddNewFixture(t, "feature/existing-path")
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
		fixture := worktreeAddNewFixture(t, "feature/child")
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
		fixture := worktreeAddNewFixture(t, "feature/symlink")
		outside := filepath.Join(fixture.base, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatalf("MkdirAll(outside) error = %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(fixture.worktreeRoot, "github.com")); err != nil {
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

func TestNewWorktreeAddCommandCleansOnlyNewEmptyParents(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/cleanup")
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

func TestNewWorktreeAddCommandCleansAfterMkdirFailure(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/deep/cleanup")
	fixture.git.localBranches[fixture.branch] = true
	mkdirCalls := 0
	realMkdir := os.Mkdir
	fixture.mkdir = func(path string, mode fs.FileMode) error {
		mkdirCalls++
		if mkdirCalls == 3 {
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

func TestNewWorktreeAddCommandPostValidationFailurePreservesGitState(t *testing.T) {
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
			fixture := worktreeAddNewFixture(t, "feature/post")
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

func TestNewWorktreeAddCommandPropagatesWriteFailureWithoutDeletingWorktree(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/write")
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

func TestNewWorktreeAddCommandPropagatesDiagnosticWriteFailure(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/diagnostic-write")
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

func TestNewWorktreeAddCommandWritesDiscoveryWarningsToStderr(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature/warning")
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

func TestNewWorktreeAddCommandRealGitLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	base := worktreeAddPhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
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
	branch := "feature/lifecycle"
	worktreeAddRunGit(t, repository.Path, "branch", branch)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := NewWorktreeAddCommand(WorktreeAddDependencies{
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
	command.SetArgs([]string{branch})
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

	resolver        *worktreeAddResolver
	git             *worktreeAddGit
	gh              *worktreeAddGh
	api             *worktreeAddAPI
	accountResolver *worktreeAddAccountResolver

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
	worktreeRoot := filepath.Join(base, "worktrees")
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

	git := &worktreeAddGit{
		localBranches:  make(map[string]bool),
		remoteBranches: make(map[string]bool),
		revisions:      make(map[string]bool),
		remotes:        []string{"origin"},
	}
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
		accountResolver: &worktreeAddAccountResolver{
			resolution: ghauth.Resolution{Source: ghauth.SourceExplicitEnv},
		},
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

	command := NewWorktreeAddCommand(WorktreeAddDependencies{
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
		Git:             fixture.git,
		Gh:              fixture.gh,
		API:             fixture.api,
		AccountResolver: fixture.accountResolver,
		Getwd:           func() (string, error) { return fixture.cwd, nil },
		Mkdir:           mkdir,
		Remove:          remove,
		Stdout:          stdout,
		Stderr:          stderr,
	})
	command.SetArgs(args)
	return command.Execute()
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
	localBranches  map[string]bool
	remoteBranches map[string]bool
	revisions      map[string]bool
	remotes        []string

	add             func(gitcmd.WorktreeAddOptions) error
	addErr          error
	outputErr       error
	localBranchErr  error
	remoteBranchErr error
	revisionErr     error

	adds                []worktreeAddGitCall
	revisionQueries     []string
	remoteBranchQueries []string
}

func (git *worktreeAddGit) OutputDir(
	_ context.Context,
	_ string,
	args ...string,
) ([]byte, error) {
	if git.outputErr != nil {
		return nil, git.outputErr
	}
	if reflect.DeepEqual(args, []string{"remote"}) {
		if len(git.remotes) == 0 {
			return nil, nil
		}
		return []byte(strings.Join(git.remotes, "\n") + "\n"), nil
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

func (git *worktreeAddGit) RemoteBranchExists(
	_ context.Context,
	_ string,
	remote string,
	branch string,
) (bool, error) {
	query := remote + "/" + branch
	git.remoteBranchQueries = append(git.remoteBranchQueries, query)
	if git.remoteBranchErr != nil {
		return false, git.remoteBranchErr
	}
	return git.remoteBranches[query], nil
}

func (git *worktreeAddGit) RevisionExists(
	_ context.Context,
	_ string,
	revision string,
) (bool, error) {
	git.revisionQueries = append(git.revisionQueries, revision)
	if git.revisionErr != nil {
		return false, git.revisionErr
	}
	return git.revisions[revision], nil
}

// worktreeAddGh is the gh test double for worktree add. It references the
// paired worktreeAddGit double so RepoSync can simulate gh bringing a new
// remote-tracking ref into the local repository.
type worktreeAddGh struct {
	git *worktreeAddGit

	syncErr                 error
	syncCreatesRemoteBranch bool

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
	if gh.syncCreatesRemoteBranch {
		// Every exercised scenario resolves to the "origin" remote; keep the
		// simulation simple rather than reimplementing worktreeAddSyncRemote.
		gh.git.remoteBranches["origin/"+options.Branch] = true
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
	token  string
}

func (api *worktreeAddAPI) DefaultBranch(
	_ context.Context,
	host string,
	owner string,
	repo string,
	tokenOverride string,
) (string, error) {
	api.calls++
	api.host = host
	api.owner = owner
	api.repo = repo
	api.token = tokenOverride
	return api.branch, api.err
}

type worktreeAddFailingWriter struct {
	err error
}

func (writer worktreeAddFailingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

// worktreeAddAccountResolver is an AccountResolver test double that records
// every host/owner it was asked to resolve and always answers with a fixed
// resolution.
type worktreeAddAccountResolver struct {
	resolution ghauth.Resolution
	err        error
	calls      []worktreeAddAccountResolverCall
}

type worktreeAddAccountResolverCall struct {
	host, owner string
}

func (r *worktreeAddAccountResolver) Resolve(
	_ context.Context,
	host, owner string,
) (ghauth.Resolution, error) {
	r.calls = append(r.calls, worktreeAddAccountResolverCall{host: host, owner: owner})
	return r.resolution, r.err
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
	return filepath.Join(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		filepath.FromSlash(branch),
	)
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
