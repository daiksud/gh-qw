package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestWorktreeRemoveMissingSlotPreservesManagedError(t *testing.T) {
	for _, test := range []struct {
		name, slot  string
		missingBase bool
	}{
		{name: "missing nested parent", slot: "feature-missing"},
		{name: "missing base for nested slot", slot: "feature-missing", missingBase: true},
		{name: "missing base for flat slot", slot: "missing", missingBase: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, test.slot)
			missing := filepath.Dir(fixture.target)
			if test.missingBase {
				missing = fixture.base
			}
			if err := os.RemoveAll(missing); err != nil {
				t.Fatal(err)
			}
			fixture.resolveManagedErr = &local.WorktreeError{
				Kind: local.ErrWorktreeNotFound, Slot: fixture.branch, Reason: "slot is not registered",
			}
			err := fixture.worktreeRemoveExecute(fixture.branch)
			if !errors.Is(err, local.ErrWorktreeNotFound) {
				t.Fatalf("Execute() = %v, want managed-worktree-not-found classification", err)
			}
			if len(fixture.git.removals) != 0 || len(fixture.cleanupRemovals) != 0 {
				t.Fatalf("missing target caused mutation: Git=%v cleanup=%v", fixture.git.removals, fixture.cleanupRemovals)
			}
		})
	}
}

func TestWorktreeRemoveRequiresConfirmedRegistrationRemoval(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "retained registration"
		if unknown {
			name = "unknown registration state"
		}
		t.Run(name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature-registration")
			listErr := errors.New("registration observation failed")
			if unknown {
				fixture.git.postRemovalListErr = listErr
			} else {
				fixture.git.postRemovalRecords = []gitcmd.Worktree{{Path: fixture.target}}
			}
			err := fixture.worktreeRemoveExecute(fixture.branch)
			if err == nil || !strings.Contains(err.Error(), "registr") {
				t.Fatalf("Execute() = %v, want registration verification failure", err)
			}
			if unknown && !errors.Is(err, listErr) {
				t.Fatalf("Execute() = %v, want underlying observation error", err)
			}
			if len(fixture.cleanupRemovals) != 0 || fixture.stderr.Len() != 0 || fixture.stdout.Len() != 0 {
				t.Fatalf("unverified removal cleaned parents or reported success: cleanup=%v stdout=%q stderr=%q", fixture.cleanupRemovals, fixture.stdout.String(), fixture.stderr.String())
			}
			if _, err := os.Lstat(fixture.target); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("target should already be removed by Git: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(fixture.target)); err != nil {
				t.Fatalf("unverified removal changed parent: %v", err)
			}
		})
	}
}

func TestRemoveReportsMutationWhenGitFailsAfterRemovingTarget(t *testing.T) {
	fixture := newRemoveTestFixture(t, "alpha", "zeta")
	gitErr := errors.New("Git failed after removing the first worktree")
	fixture.git.afterRemove = func(string) error { return gitErr }
	err := fixture.execute("widget")
	if !errors.Is(err, gitErr) {
		t.Fatalf("Execute() = %v, want original Git failure", err)
	}
	for _, fragment := range []string{
		"removed [" + removeOutputPath(fixture.linkedPath("alpha")) + "]",
		"remaining [" + removeOutputPath(fixture.linkedPath("zeta")) + ", " + removeOutputPath(fixture.repository.Path) + "]",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Execute() = %v, want %q", err, fragment)
		}
	}
	if len(fixture.git.removals) != 1 || len(fixture.removeAllCalls) != 0 {
		t.Fatalf("continued after failure: Git=%v main=%v", fixture.git.removals, fixture.removeAllCalls)
	}
	for _, path := range []string{fixture.linkedPath("zeta"), fixture.repository.Path} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("remaining path %q changed: %v", path, err)
		}
	}
}

func TestWorktreeRemoveGonePostRemovalFailurePolicies(t *testing.T) {
	for _, failure := range []string{"unknown registrations", "cancellation", "output", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			root, roots, repository, paths := removalExecutionRepositories(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injectedErr := errors.New("injected " + failure + " failure")
			var stdout, stderr bytes.Buffer
			git := &removalExecutionRunner{Runner: &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: io.Discard}}
			dependencies := WorktreeRemoveDependencies{
				Resolver: worktreeRemoveStaticResolver{result: roots},
				Git:      git,
				Getwd:    func() (string, error) { return root, nil },
				Stdout:   &stdout,
				Stderr:   &stderr,
			}
			switch failure {
			case "unknown registrations":
				git.afterRemoval = func() { git.listErr = injectedErr }
			case "cancellation":
				git.afterRemoval = cancel
				injectedErr = context.Canceled
			case "output":
				dependencies.Stderr = &removalProgressErrorWriter{destination: &stderr, err: injectedErr}
			case "cleanup":
				dependencies.Remove = func(path string) error {
					if path == filepath.Dir(paths[0]) {
						return injectedErr
					}
					return os.Remove(path)
				}
			}
			command := newWorktreeRemoveTestCommand(dependencies)
			command.SetArgs([]string{"--worktree", "-R", "acme/widget", "--gone", "--yes"})
			err := command.ExecuteContext(ctx)
			if err == nil || ExitCode(err) != 1 {
				t.Fatalf("Execute() = %v, want partial failure; stderr=%q", err, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if _, err := os.Lstat(paths[0]); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("first target was not removed: %v", err)
			}
			if failure == "cleanup" {
				if len(git.removals) != 2 {
					t.Fatalf("cleanup error stopped independent candidate: %v", git.removals)
				}
				if _, err := os.Lstat(paths[1]); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("independent candidate remains: %v", err)
				}
				for _, fragment := range []string{"removed worktree " + local.NormalizePathForOutput(paths[0]), injectedErr.Error(), "removed worktree " + local.NormalizePathForOutput(paths[1])} {
					if !strings.Contains(stderr.String(), fragment) {
						t.Fatalf("stderr = %q, want %q", stderr.String(), fragment)
					}
				}
			} else {
				if !errors.Is(err, injectedErr) {
					t.Fatalf("Execute() = %v, want original failure %v", err, injectedErr)
				}
				if len(git.removals) != 1 {
					t.Fatalf("continued after stopping failure: %v", git.removals)
				}
				if _, err := os.Stat(paths[1]); err != nil {
					t.Fatalf("later candidate changed: %v", err)
				}
			}
			if failure != "output" {
				if _, err := os.Stat(filepath.Dir(paths[0])); err != nil {
					t.Fatalf("parent changed despite failed cleanup or unverified removal: %v", err)
				}
			}
			if _, err := os.Stat(repository); err != nil {
				t.Fatalf("main repository changed: %v", err)
			}
		})
	}
}

// Use native Git for registration, upstream, cleanliness, and lifecycle behavior;
// inject failures only at the subprocess and diagnostic boundaries.
func removalExecutionRepositories(t *testing.T) (string, rootpkg.Result, string, []string) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	roots := rootpkg.Result{RepositoryRoots: []string{filepath.Join(root, "repositories")}, WorktreeRoot: filepath.Join(root, "repositories", "worktrees")}
	repository := filepath.Join(roots.RepositoryRoots[0], "github.com", "acme", "widget")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repository, "init", "-b", "main")
	worktreeRemoveRunGit(t, repository, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repository, "config", "user.email", "gh-qw@example.invalid")
	worktreeRemoveRunGit(t, repository, "commit", "--allow-empty", "-m", "initial")
	worktreeRemoveRunGit(t, repository, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	var paths []string
	for _, branch := range []string{"a-first", "b-second"} {
		path := filepath.Join(roots.WorktreeRoot, branch, "widget")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		worktreeRemoveRunGit(t, repository, "worktree", "add", "-b", branch, path, "HEAD")
		worktreeRemoveRunGit(t, repository, "config", "branch."+branch+".remote", "origin")
		worktreeRemoveRunGit(t, repository, "config", "branch."+branch+".merge", "refs/heads/"+branch)
		paths = append(paths, path)
	}
	return root, roots, repository, paths
}

type removalExecutionRunner struct {
	*gitcmd.Runner
	removals     []string
	listErr      error
	afterRemoval func()
}

func (git *removalExecutionRunner) WorktreeRemove(ctx context.Context, dir string, options gitcmd.WorktreeRemoveOptions) error {
	git.removals = append(git.removals, options.Path)
	if err := git.Runner.WorktreeRemove(ctx, dir, options); err != nil {
		return err
	}
	if git.afterRemoval != nil {
		git.afterRemoval()
	}
	return nil
}

func (git *removalExecutionRunner) WorktreeList(ctx context.Context, dir string) ([]gitcmd.Worktree, error) {
	if git.listErr != nil {
		return nil, git.listErr
	}
	return git.Runner.WorktreeList(ctx, dir)
}

type removalProgressErrorWriter struct {
	destination io.Writer
	err         error
}

func (writer *removalProgressErrorWriter) Write(data []byte) (int, error) {
	if bytes.HasPrefix(data, []byte("removed worktree ")) {
		return 0, writer.err
	}
	return writer.destination.Write(data)
}
