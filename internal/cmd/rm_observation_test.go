package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestRemoveLinkedObservesAssociationOncePerFreshPhase(t *testing.T) {
	for _, changeAfterConfirmation := range []bool{false, true} {
		name := "unchanged"
		if changeAfterConfirmation {
			name = "dirty after confirmation"
		}
		t.Run(name, func(t *testing.T) {
			root := removeTestPhysicalPath(t, t.TempDir())
			repositoryRoot, worktreeRoot := filepath.Join(root, "repositories"), filepath.Join(root, "worktrees")
			repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
			target := filepath.Join(worktreeRoot, "github.com", "acme", "widget", "slot")
			if err := os.MkdirAll(repositoryPath, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			removeTestRunGit(t, repositoryPath, "init", "-b", "main")
			removeTestRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
			removeTestRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
			if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			removeTestRunGit(t, repositoryPath, "add", "README")
			removeTestRunGit(t, repositoryPath, "commit", "-m", "initial")
			removeTestRunGit(t, repositoryPath, "worktree", "add", "-b", "actual-branch", target, "HEAD")
			git := &removeObservationRunner{Runner: &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: io.Discard}, target: target, calls: map[int][]string{}}
			prompts := 0
			command := NewRemoveCommand(RemoveDependencies{
				Resolver: &removeTestResolver{result: rootpkg.Result{RepositoryRoots: []string{repositoryRoot}, WorktreeRoot: worktreeRoot}},
				Git:      git,
				ResolveManaged: func(ctx context.Context, repository local.Repository, root, slot string, options ...local.ManagedWorktreeOptions) (local.Worktree, error) {
					git.phase++
					return local.ResolveManagedWorktree(ctx, repository, root, slot, options...)
				},
				Prompt: func(context.Context, io.Writer, string) (bool, error) {
					prompts++
					if changeAfterConfirmation {
						if err := os.WriteFile(filepath.Join(target, "README"), []byte("changed\n"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					return true, nil
				},
				Stdout: io.Discard, Stderr: io.Discard,
			})
			command.SetArgs([]string{"acme/widget@slot"})
			err := command.Execute()
			if (err != nil) != changeAfterConfirmation {
				t.Fatalf("Execute()=%v, changed=%t", err, changeAfterConfirmation)
			}
			if prompts != 1 || git.phase != 2 {
				t.Fatalf("prompts=%d phases=%d, want 1 and 2", prompts, git.phase)
			}
			for phase := 1; phase <= 2; phase++ {
				if want := []string{"--show-toplevel", "--git-common-dir"}; !reflect.DeepEqual(git.calls[phase], want) {
					t.Errorf("phase %d association queries=%v, want %v", phase, git.calls[phase], want)
				}
			}
			_, pathErr := os.Stat(target)
			if changeAfterConfirmation && pathErr != nil {
				t.Fatalf("changed target removed: %v", pathErr)
			}
			if !changeAfterConfirmation && !os.IsNotExist(pathErr) {
				t.Fatalf("target remains: %v", pathErr)
			}
			if _, err := os.Stat(repositoryPath); err != nil {
				t.Fatalf("main changed: %v", err)
			}
		})
	}
}

type removeObservationRunner struct {
	*gitcmd.Runner
	target string
	phase  int
	calls  map[int][]string
}

func (git *removeObservationRunner) OutputDir(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir == git.target && len(args) == 2 && args[0] == "rev-parse" && (args[1] == "--show-toplevel" || args[1] == "--git-common-dir") {
		git.calls[git.phase] = append(git.calls[git.phase], args[1])
	}
	return git.Runner.OutputDir(ctx, dir, args...)
}
