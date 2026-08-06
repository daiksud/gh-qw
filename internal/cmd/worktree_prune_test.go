package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewWorktreePruneCommandSelectionAndArguments(t *testing.T) {
	t.Run("explicit repository and option passthrough", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		fixture.current = func(
			context.Context,
			string,
			string,
			[]local.Repository,
			...local.CurrentOptions,
		) (local.Current, error) {
			t.Fatal("Current() called with -R")
			return local.Current{}, nil
		}

		err := fixture.execute(
			"-R",
			"acme/widget",
			"--dry-run",
			"--verbose",
			"--expire",
			"2.weeks.ago",
		)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(fixture.git.prunes) != 1 {
			t.Fatalf("WorktreePrune() calls = %d, want 1", len(fixture.git.prunes))
		}
		want := gitcmd.WorktreePruneOptions{
			DryRun:  true,
			Verbose: true,
			Expire:  "2.weeks.ago",
		}
		if got := fixture.git.prunes[0].options; !reflect.DeepEqual(got, want) {
			t.Fatalf("WorktreePrune options = %#v, want %#v", got, want)
		}
		if got := fixture.git.prunes[0].dir; got != fixture.repository.Path {
			t.Fatalf("WorktreePrune dir = %q, want %q", got, fixture.repository.Path)
		}
		fixture.assertStdoutEmpty(t)
	})

	for _, main := range []bool{true, false} {
		name := "linked"
		if main {
			name = "main"
		}
		t.Run("current "+name, func(t *testing.T) {
			fixture := newWorktreePruneFixture(t)
			fixture.currentResult.Worktree.Main = main
			if err := fixture.execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if fixture.currentCalls != 1 {
				t.Fatalf("Current() calls = %d, want 1", fixture.currentCalls)
			}
			if len(fixture.git.prunes) != 1 {
				t.Fatalf("WorktreePrune() calls = %d, want 1", len(fixture.git.prunes))
			}
		})
	}

	t.Run("current duplicate identity", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		duplicate := fixture.repository
		duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
		duplicate.RootIndex = 1
		fixture.repositories = append(fixture.repositories, duplicate)

		if err := fixture.execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(fixture.git.prunes) != 1 {
			t.Fatalf("WorktreePrune() calls = %d, want 1", len(fixture.git.prunes))
		}
		if got := fixture.git.prunes[0].dir; got != fixture.repository.Path {
			t.Fatalf("WorktreePrune() dir = %q, want current %q", got, fixture.repository.Path)
		}
	})
}

func TestNewWorktreePruneCommandRejectsUsageAndDuplicateMutationSelection(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		configure func(*worktreePruneFixture)
		want      error
	}{
		{
			name: "positional argument",
			args: []string{"unexpected"},
			want: repospec.ErrUsage,
		},
		{
			name: "missing selector",
			args: []string{"-R", "missing"},
			want: local.ErrRepositoryNotFound,
		},
		{
			name: "duplicate explicit identity",
			args: []string{"-R", "github.com/acme/widget"},
			configure: func(fixture *worktreePruneFixture) {
				duplicate := fixture.repository
				duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
				duplicate.RootIndex = 1
				fixture.repositories = append(fixture.repositories, duplicate)
			},
			want: local.ErrRepositoryAmbiguous,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorktreePruneFixture(t)
			if test.configure != nil {
				test.configure(fixture)
			}
			err := fixture.execute(test.args...)
			if !errors.Is(err, test.want) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, test.want)
			}
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want usage classification", err)
			}
			if len(fixture.git.prunes) != 0 {
				t.Fatalf("WorktreePrune() calls = %d, want 0", len(fixture.git.prunes))
			}
		})
	}
}

func TestNewWorktreePruneCommandDryRunReportsWithoutMutation(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, target := fixture.makeCandidate("stale", "feature/nested", true)
	missing := filepath.Join(fixture.root, "missing-worktree")
	fixture.writeFile(filepath.Join(target, "gitdir"), []byte(filepath.Join(missing, ".git")+"\n"))
	fixture.git.lists = [][]gitcmd.Worktree{{
		fixture.mainRecord(),
		{
			Path:           missing,
			HEAD:           strings.Repeat("1", 40),
			Branch:         "feature/nested",
			Prunable:       true,
			PrunableReason: "gitdir file points to non-existent location",
		},
	}}

	if err := fixture.execute("-n", "--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertExists(t, candidate)
	fixture.assertExists(t, target)
	if len(fixture.removedAll) != 0 || len(fixture.removed) != 0 {
		t.Fatalf("removals = %v / %v, want none", fixture.removedAll, fixture.removed)
	}
	if got := fixture.stderr.String(); !strings.Contains(got, "would remove orphaned worktree") ||
		!strings.Contains(got, strconv.Quote(candidate)) {
		t.Fatalf("stderr = %q, want dry-run candidate report", got)
	}
	fixture.assertStdoutEmpty(t)
}

func TestNewWorktreePruneCommandRemovesOnlyAfterGitPrunesMetadata(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, target := fixture.makeCandidate("stale", "feature/nested", true)
	fixture.git.onPrune = func() error {
		return os.RemoveAll(target)
	}

	if err := fixture.execute("-v", "--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertMissing(t, candidate)
	fixture.assertMissing(t, filepath.Dir(candidate))
	fixture.assertExists(t, fixture.worktreeBase)
	if got, want := fixture.removedAll, []string{candidate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoveAll paths = %v, want %v", got, want)
	}
	if got := fixture.stderr.String(); !strings.Contains(got, "remove orphaned worktree") ||
		!strings.Contains(got, "remove empty directory") {
		t.Fatalf("stderr = %q, want verbose cleanup decisions", got)
	}
}

func TestNewWorktreePruneCommandAcceptsRelativePointer(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, target := fixture.makeCandidate("relative", "topic/relative", true)
	relative, err := filepath.Rel(candidate, target)
	if err != nil {
		t.Fatal(err)
	}
	fixture.writeFile(filepath.Join(candidate, ".git"), []byte("gitdir: "+relative+"\n"))
	fixture.git.onPrune = func() error {
		return os.RemoveAll(target)
	}

	if err := fixture.execute("--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertMissing(t, candidate)
}

func TestNewWorktreePruneCommandLeavesStaleRegisteredMetadataUntilGitExpiresIt(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, target := fixture.makeCandidate("young", "feature/young", true)
	missing := filepath.Join(fixture.root, "missing-young")
	fixture.writeFile(filepath.Join(target, "gitdir"), []byte(filepath.Join(missing, ".git")+"\n"))
	fixture.git.lists = [][]gitcmd.Worktree{{
		fixture.mainRecord(),
		{
			Path:           missing,
			HEAD:           strings.Repeat("2", 40),
			Branch:         "feature/young",
			Prunable:       true,
			PrunableReason: "expire threshold not met",
		},
	}}

	if err := fixture.execute("-v", "--expire", "never"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertExists(t, candidate)
	fixture.assertExists(t, target)
	if len(fixture.removedAll) != 0 {
		t.Fatalf("RemoveAll paths = %v, want none", fixture.removedAll)
	}
}

func TestNewWorktreePruneCommandAgeProofAndUnverifiableOrphans(t *testing.T) {
	t.Run("effective expiry proves orphan age", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		candidate, _ := fixture.makeCandidate("gone", "old/orphan", false)
		fixture.expiryThreshold = math.MaxUint64

		if err := fixture.execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		fixture.assertMissing(t, candidate)
	})

	t.Run("newer than effective expiry is retained", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		candidate, _ := fixture.makeCandidate("gone", "new/orphan", false)
		fixture.expiryThreshold = 1

		if err := fixture.execute("-v"); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		fixture.assertExists(t, candidate)
		if !strings.Contains(fixture.stderr.String(), "age does not satisfy") {
			t.Fatalf("stderr = %q, want age decision", fixture.stderr.String())
		}
	})

	t.Run("unverifiable effective expiry warns and retains", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		candidate, _ := fixture.makeCandidate("gone", "unknown/orphan", false)
		fixture.expiryErr = errors.New("bad expiry configuration")

		if err := fixture.execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		fixture.assertExists(t, candidate)
		if got := fixture.stderr.String(); !strings.Contains(got, "gh-qw: warning:") ||
			!strings.Contains(got, "effective expiry cannot be determined") {
			t.Fatalf("stderr = %q, want prefixed expiry warning", got)
		}
	})
}

func TestNewWorktreePruneCommandLeavesForeignMalformedAndSymlinkPointers(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreePruneFixture, string)
	}{
		{
			name: "malformed",
			configure: func(fixture *worktreePruneFixture, candidate string) {
				fixture.writeFile(filepath.Join(candidate, ".git"), []byte("not-a-pointer\n"))
			},
		},
		{
			name: "foreign",
			configure: func(fixture *worktreePruneFixture, candidate string) {
				outside := filepath.Join(fixture.root, "foreign", "metadata")
				fixture.mkdirAll(outside)
				fixture.writeFile(
					filepath.Join(candidate, ".git"),
					[]byte("gitdir: "+outside+"\n"),
				)
			},
		},
		{
			name: "symlink pointer",
			configure: func(fixture *worktreePruneFixture, candidate string) {
				if err := os.Remove(filepath.Join(candidate, ".git")); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(fixture.root, "pointer-target")
				fixture.writeFile(target, []byte("gitdir: ignored\n"))
				if err := os.Symlink(target, filepath.Join(candidate, ".git")); err != nil {
					t.Skipf("symlink unsupported: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorktreePruneFixture(t)
			candidate, _ := fixture.makeCandidate("unsafe", "unsafe/"+test.name, false)
			test.configure(fixture, candidate)

			if err := fixture.execute("--expire", "now"); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			fixture.assertExists(t, candidate)
			if len(fixture.removedAll) != 0 {
				t.Fatalf("RemoveAll paths = %v, want none", fixture.removedAll)
			}
			if got := fixture.stderr.String(); !strings.Contains(got, "gh-qw: warning:") ||
				!strings.Contains(got, strconv.Quote(candidate)) {
				t.Fatalf("stderr = %q, want candidate warning", got)
			}
		})
	}
}

func TestNewWorktreePruneCommandLeavesLiveRegisteredWorktree(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, _ := fixture.makeCandidate("live", "feature/live", true)
	fixture.git.lists = [][]gitcmd.Worktree{{
		fixture.mainRecord(),
		{
			Path:   candidate,
			HEAD:   strings.Repeat("3", 40),
			Branch: "feature/live",
		},
	}}

	if err := fixture.execute("-v", "--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertExists(t, candidate)
	if len(fixture.removedAll) != 0 {
		t.Fatalf("RemoveAll paths = %v, want none", fixture.removedAll)
	}
	if !strings.Contains(fixture.stderr.String(), "remains registered with Git") {
		t.Fatalf("stderr = %q, want live decision", fixture.stderr.String())
	}
}

func TestNewWorktreePruneCommandCleansEmptyDirectoriesButLeavesUnknownFiles(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	empty := filepath.Join(fixture.worktreeBase, "old", "empty")
	fixture.mkdirAll(empty)
	unknown := filepath.Join(fixture.worktreeBase, "keep.txt")
	fixture.writeFile(unknown, []byte("keep"))

	if err := fixture.execute("-v"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertMissing(t, empty)
	fixture.assertMissing(t, filepath.Dir(empty))
	fixture.assertExists(t, unknown)
	if got := fixture.stderr.String(); !strings.Contains(got, "unknown non-worktree file") ||
		!strings.Contains(got, "remove empty directory") {
		t.Fatalf("stderr = %q, want unknown warning and empty cleanup", got)
	}
}

func TestNewWorktreePruneCommandNeverFollowsDirectorySymlinks(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	outside := filepath.Join(fixture.root, "outside")
	fixture.mkdirAll(outside)
	marker := filepath.Join(outside, "marker")
	fixture.writeFile(marker, []byte("keep"))
	link := filepath.Join(fixture.worktreeBase, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := fixture.execute("--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertExists(t, link)
	fixture.assertExists(t, marker)
	if !strings.Contains(fixture.stderr.String(), "unknown symbolic link") {
		t.Fatalf("stderr = %q, want symlink warning", fixture.stderr.String())
	}
}

func TestNewWorktreePruneCommandNeverTreatsRepositoryContainerAsCandidate(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	target := filepath.Join(fixture.repository.Path, ".git", "worktrees", "container")
	fixture.mkdirAll(target)
	fixture.writeFile(
		filepath.Join(target, "gitdir"),
		[]byte(filepath.Join(fixture.worktreeBase, ".git")+"\n"),
	)
	fixture.writeFile(
		filepath.Join(fixture.worktreeBase, ".git"),
		[]byte("gitdir: "+target+"\n"),
	)
	fixture.git.onPrune = func() error {
		return os.RemoveAll(target)
	}

	if err := fixture.execute("--expire", "now"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	fixture.assertExists(t, fixture.worktreeBase)
	fixture.assertExists(t, filepath.Join(fixture.worktreeBase, ".git"))
	if len(fixture.removedAll) != 0 {
		t.Fatalf("RemoveAll paths = %v, want none", fixture.removedAll)
	}
	if !strings.Contains(fixture.stderr.String(), "unknown non-worktree") {
		t.Fatalf("stderr = %q, want container .git warning", fixture.stderr.String())
	}
}

func TestNewWorktreePruneCommandGitFailurePreventsFilesystemCleanup(t *testing.T) {
	fixture := newWorktreePruneFixture(t)
	candidate, _ := fixture.makeCandidate("orphan", "failure/orphan", false)
	fixture.git.pruneErr = errors.New("git prune failed")

	err := fixture.execute("--expire", "now")
	if err == nil || !strings.Contains(err.Error(), "git prune failed") {
		t.Fatalf("Execute() error = %v, want Git failure", err)
	}
	fixture.assertExists(t, candidate)
	if fixture.git.listCalls != 0 {
		t.Fatalf("WorktreeList() calls = %d, want 0 after prune failure", fixture.git.listCalls)
	}
	if len(fixture.removedAll) != 0 || len(fixture.removed) != 0 {
		t.Fatalf("removals = %v / %v, want none", fixture.removedAll, fixture.removed)
	}
}

func TestNewWorktreePruneCommandWarningAndWriteErrors(t *testing.T) {
	t.Run("discovery warning write failure prevents Git", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		fixture.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
			Operation: "inspect repository",
			Err:       errors.New("denied"),
		}}
		fixture.stderrWriter = worktreePruneErrorWriter{err: errors.New("write failed")}

		err := fixture.execute()
		if err == nil || !strings.Contains(err.Error(), "write failed") {
			t.Fatalf("Execute() error = %v, want write failure", err)
		}
		if len(fixture.git.prunes) != 0 {
			t.Fatalf("WorktreePrune() calls = %d, want 0", len(fixture.git.prunes))
		}
	})

	t.Run("cleanup diagnostics write failure prevents custom deletion", func(t *testing.T) {
		fixture := newWorktreePruneFixture(t)
		candidate, target := fixture.makeCandidate("stale", "write/failure", true)
		fixture.writeFile(filepath.Join(fixture.worktreeBase, "unknown"), []byte("keep"))
		fixture.git.onPrune = func() error {
			return os.RemoveAll(target)
		}
		fixture.stderrWriter = worktreePruneErrorWriter{err: errors.New("write failed")}

		err := fixture.execute()
		if err == nil || !strings.Contains(err.Error(), "write failed") {
			t.Fatalf("Execute() error = %v, want write failure", err)
		}
		fixture.assertExists(t, candidate)
		if len(fixture.removedAll) != 0 {
			t.Fatalf("RemoveAll paths = %v, want none", fixture.removedAll)
		}
	})
}

func TestNewWorktreePruneCommandRealGitDryRunAndCleanup(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		name := "cleanup"
		if dryRun {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			layout := newWorktreePruneRealLayout(t)
			args := []string{"-R", layout.repository.Identity, "--expire", "now", "-v"}
			if dryRun {
				args = append(args, "-n")
			}

			command := NewWorktreePruneCommand(WorktreePruneDependencies{
				Resolver: worktreePruneTestResolver{result: rootpkg.Result{
					RepositoryRoots: []string{layout.repositoryRoot},
					WorktreeRoot:    layout.worktreeRoot,
				}},
				Discover: func(
					context.Context,
					[]string,
					...local.DiscoveryOptions,
				) (local.DiscoveryResult, error) {
					return local.DiscoveryResult{
						Repositories: []local.Repository{layout.repository},
					}, nil
				},
				Git: &gitcmd.Runner{
					Executable: "git",
					Stdout:     io.Discard,
					Stderr:     &layout.stderr,
				},
				Stdout: &layout.stdout,
				Stderr: &layout.stderr,
			})
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v\nstderr:\n%s", err, layout.stderr.String())
			}
			if layout.stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", layout.stdout.String())
			}
			if !strings.Contains(layout.stderr.String(), "Removing worktrees/") {
				t.Fatalf("stderr = %q, want preserved Git diagnostic", layout.stderr.String())
			}
			if dryRun {
				assertWorktreePrunePathExists(t, layout.candidate)
				assertWorktreePrunePathExists(t, layout.adminTarget)
				if !strings.Contains(layout.stderr.String(), "would remove orphaned worktree") {
					t.Fatalf("stderr = %q, want dry-run directory report", layout.stderr.String())
				}
			} else {
				assertWorktreePrunePathMissing(t, layout.candidate)
				assertWorktreePrunePathMissing(t, layout.adminTarget)
				assertWorktreePrunePathMissing(t, filepath.Dir(layout.candidate))
				assertWorktreePrunePathExists(t, layout.worktreeBase)
			}
		})
	}
}

type worktreePruneFixture struct {
	t *testing.T

	root           string
	repositoryRoot string
	worktreeRoot   string
	worktreeBase   string
	repository     local.Repository
	repositories   []local.Repository
	currentResult  local.Current
	current        func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)
	currentCalls int
	warnings     []local.Warning

	git             *worktreePruneFakeGit
	expiryThreshold uint64
	expiryErr       error
	removedAll      []string
	removed         []string
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stderrWriter    io.Writer
}

func newWorktreePruneFixture(t *testing.T) *worktreePruneFixture {
	t.Helper()
	root := worktreePrunePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(root, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	worktreeBase := filepath.Join(worktreeRoot, "github.com", "acme", "widget")
	for _, path := range []string{
		filepath.Join(repositoryPath, ".git"),
		worktreeBase,
	} {
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
	fixture := &worktreePruneFixture{
		t:               t,
		root:            root,
		repositoryRoot:  repositoryRoot,
		worktreeRoot:    worktreeRoot,
		worktreeBase:    worktreeBase,
		repository:      repository,
		repositories:    []local.Repository{repository},
		expiryThreshold: math.MaxUint64,
	}
	fixture.currentResult = local.Current{
		Repository: repository,
		Worktree: local.Worktree{
			Repository: repository,
			Identity:   repository.Identity,
			Path:       repository.Path,
			Main:       true,
		},
	}
	fixture.current = func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error) {
		fixture.currentCalls++
		return fixture.currentResult, nil
	}
	fixture.git = &worktreePruneFakeGit{
		lists: [][]gitcmd.Worktree{{fixture.mainRecord()}},
	}
	return fixture
}

func (fixture *worktreePruneFixture) execute(args ...string) error {
	fixture.t.Helper()
	stderr := fixture.stderrWriter
	if stderr == nil {
		stderr = &fixture.stderr
	}
	command := NewWorktreePruneCommand(WorktreePruneDependencies{
		Resolver: worktreePruneTestResolver{result: rootpkg.Result{
			RepositoryRoots: []string{fixture.repositoryRoot},
			WorktreeRoot:    fixture.worktreeRoot,
		}},
		Discover: func(
			context.Context,
			[]string,
			...local.DiscoveryOptions,
		) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{
				Repositories: append([]local.Repository(nil), fixture.repositories...),
				Warnings:     append([]local.Warning(nil), fixture.warnings...),
			}, nil
		},
		Current: fixture.current,
		Git:     fixture.git,
		Getwd: func() (string, error) {
			return fixture.repository.Path, nil
		},
		RemoveAll: func(path string) error {
			fixture.removedAll = append(fixture.removedAll, path)
			return os.RemoveAll(path)
		},
		Remove: func(path string) error {
			fixture.removed = append(fixture.removed, path)
			return os.Remove(path)
		},
		ExpiryThreshold: func(context.Context, string, string) (uint64, error) {
			return fixture.expiryThreshold, fixture.expiryErr
		},
		Stdout: &fixture.stdout,
		Stderr: stderr,
	})
	command.SetArgs(args)
	return command.Execute()
}

func (fixture *worktreePruneFixture) mainRecord() gitcmd.Worktree {
	return gitcmd.Worktree{
		Path:   fixture.repository.Path,
		HEAD:   strings.Repeat("0", 40),
		Branch: "main",
	}
}

func (fixture *worktreePruneFixture) makeCandidate(
	id, slot string,
	targetExists bool,
) (string, string) {
	fixture.t.Helper()
	candidate := filepath.Join(fixture.worktreeBase, filepath.FromSlash(slot))
	target := filepath.Join(fixture.repository.Path, ".git", "worktrees", id)
	fixture.mkdirAll(candidate)
	if targetExists {
		fixture.mkdirAll(target)
		fixture.writeFile(
			filepath.Join(target, "gitdir"),
			[]byte(filepath.Join(candidate, ".git")+"\n"),
		)
	}
	fixture.writeFile(filepath.Join(candidate, ".git"), []byte("gitdir: "+target+"\n"))
	fixture.writeFile(filepath.Join(candidate, "tracked.txt"), []byte("worktree contents"))
	return candidate, target
}

func (fixture *worktreePruneFixture) mkdirAll(path string) {
	fixture.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *worktreePruneFixture) writeFile(path string, data []byte) {
	fixture.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *worktreePruneFixture) assertExists(t *testing.T, path string) {
	t.Helper()
	assertWorktreePrunePathExists(t, path)
}

func (fixture *worktreePruneFixture) assertMissing(t *testing.T, path string) {
	t.Helper()
	assertWorktreePrunePathMissing(t, path)
}

func (fixture *worktreePruneFixture) assertStdoutEmpty(t *testing.T) {
	t.Helper()
	if fixture.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", fixture.stdout.String())
	}
}

type worktreePruneTestResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver worktreePruneTestResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

type worktreePruneCall struct {
	dir     string
	options gitcmd.WorktreePruneOptions
}

type worktreePruneFakeGit struct {
	prunes    []worktreePruneCall
	pruneErr  error
	onPrune   func() error
	lists     [][]gitcmd.Worktree
	listCalls int
}

func (git *worktreePruneFakeGit) WorktreePrune(
	_ context.Context,
	dir string,
	options gitcmd.WorktreePruneOptions,
) error {
	git.prunes = append(git.prunes, worktreePruneCall{dir: dir, options: options})
	if git.pruneErr != nil {
		return git.pruneErr
	}
	if git.onPrune != nil {
		return git.onPrune()
	}
	return nil
}

func (git *worktreePruneFakeGit) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	git.listCalls++
	if len(git.lists) == 0 {
		return nil, nil
	}
	index := git.listCalls - 1
	if index >= len(git.lists) {
		index = len(git.lists) - 1
	}
	return append([]gitcmd.Worktree(nil), git.lists[index]...), nil
}

func (git *worktreePruneFakeGit) OutputDir(
	context.Context,
	string,
	...string,
) ([]byte, error) {
	return nil, errors.New("unexpected OutputDir call")
}

type worktreePruneErrorWriter struct {
	err error
}

func (writer worktreePruneErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type worktreePruneRealLayout struct {
	repositoryRoot string
	worktreeRoot   string
	worktreeBase   string
	repository     local.Repository
	candidate      string
	adminTarget    string
	stdout         bytes.Buffer
	stderr         bytes.Buffer
}

func newWorktreePruneRealLayout(t *testing.T) *worktreePruneRealLayout {
	t.Helper()
	root := worktreePrunePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(root, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	worktreeBase := filepath.Join(worktreeRoot, "github.com", "acme", "widget")
	candidate := filepath.Join(worktreeBase, "feature", "nested")
	for _, path := range []string{filepath.Dir(repositoryPath), filepath.Dir(candidate)} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	worktreePruneRunGit(t, "", "init", "-q", repositoryPath)
	worktreePruneRunGit(t, repositoryPath, "config", "user.email", "test@example.com")
	worktreePruneRunGit(t, repositoryPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repositoryPath, "tracked.txt"), []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreePruneRunGit(t, repositoryPath, "add", "tracked.txt")
	worktreePruneRunGit(t, repositoryPath, "commit", "-q", "-m", "initial")
	worktreePruneRunGit(
		t,
		repositoryPath,
		"worktree",
		"add",
		"-q",
		"-b",
		"feature/nested",
		candidate,
	)

	pointerData, err := os.ReadFile(filepath.Join(candidate, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	adminTargetText, err := worktreePruneParseGitPointer(pointerData)
	if err != nil {
		t.Fatal(err)
	}
	adminTarget := filepath.Clean(adminTargetText)
	missing := filepath.Join(root, "missing-worktree", ".git")
	if err := os.WriteFile(
		filepath.Join(adminTarget, "gitdir"),
		[]byte(filepath.ToSlash(missing)+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(adminTarget, old, old); err != nil {
		t.Fatal(err)
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
	return &worktreePruneRealLayout{
		repositoryRoot: repositoryRoot,
		worktreeRoot:   worktreeRoot,
		worktreeBase:   worktreeBase,
		repository:     repository,
		candidate:      candidate,
		adminTarget:    adminTarget,
	}
}

func worktreePruneRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func worktreePrunePhysicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func assertWorktreePrunePathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Lstat(%q) error = %v, want path to exist", path, err)
	}
}

func assertWorktreePrunePathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want path missing", path, err)
	}
}

func TestWorktreePruneResolveExpiryUsesGitDateParsing(t *testing.T) {
	repository := worktreePrunePhysicalPath(t, t.TempDir())
	worktreePruneRunGit(t, "", "init", "-q", repository)
	runner := &gitcmd.Runner{
		Executable: "git",
		Stdout:     io.Discard,
		Stderr:     io.Discard,
	}

	nowThreshold, err := worktreePruneResolveExpiry(
		context.Background(),
		runner,
		repository,
		"now",
	)
	if err != nil {
		t.Fatalf("resolve now expiry: %v", err)
	}
	if nowThreshold != math.MaxUint64 {
		t.Fatalf("now threshold = %d, want %d", nowThreshold, uint64(math.MaxUint64))
	}

	neverThreshold, err := worktreePruneResolveExpiry(
		context.Background(),
		runner,
		repository,
		"never",
	)
	if err != nil {
		t.Fatalf("resolve never expiry: %v", err)
	}
	if neverThreshold != 0 {
		t.Fatalf("never threshold = %d, want 0", neverThreshold)
	}

	if _, err := worktreePruneResolveExpiry(
		context.Background(),
		runner,
		repository,
		"definitely-not-an-expiry",
	); err == nil {
		t.Fatal("invalid expiry error = nil")
	}
}

func TestWorktreePruneTreeAgeChecksEveryEntryWithoutFollowingSymlinks(t *testing.T) {
	root := worktreePrunePhysicalPath(t, t.TempDir())
	base := filepath.Join(root, "base")
	candidate := filepath.Join(base, "candidate")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	newer := time.Unix(300, 0)
	file := filepath.Join(candidate, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, candidate} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	runtime := worktreePrunePrepareRuntime(WorktreePruneDependencies{
		Git: &worktreePruneFakeGit{},
	})
	oldEnough, err := worktreePruneTreeOlderThan(runtime, base, candidate, 200)
	if err != nil || !oldEnough {
		t.Fatalf("TreeOlderThan() = %v, %v, want true, nil", oldEnough, err)
	}
	if err := os.Chtimes(file, newer, newer); err != nil {
		t.Fatal(err)
	}
	oldEnough, err = worktreePruneTreeOlderThan(runtime, base, candidate, 200)
	if err != nil || oldEnough {
		t.Fatalf("TreeOlderThan() = %v, %v, want false, nil", oldEnough, err)
	}
}

func ExampleNewWorktreePruneCommand() {
	command := NewWorktreePruneCommand(WorktreePruneDependencies{})
	fmt.Println(command.Use)
	// Output:
	// prune [-R|--repo selector] [-n|--dry-run] [-v|--verbose] [--expire value]
}
