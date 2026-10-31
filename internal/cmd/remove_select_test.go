package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
)

func TestRemoveSelectedRepositoryKeepsConfirmationAndForgetsRemovedPath(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "declined", true: "confirmed"}[confirmed], func(t *testing.T) {
			fixture := newRemoveTestFixture(t, "topic")
			fixture.prompt = func(context.Context, io.Writer, string) (bool, error) {
				fixture.promptCalls++
				return confirmed, nil
			}
			deps := removeSelectionDependencies(fixture)
			deps.Select = func(_ context.Context, rows []string) (string, error) {
				return removeSelectionRow(t, rows, "repository\t"), nil
			}
			forgotten := false
			deps.Forget = func(identity, path string) error {
				forgotten = true
				if identity != fixture.repository.Identity || path != fixture.repository.Path {
					t.Fatalf("forgot %q at %q", identity, path)
				}
				return nil
			}
			command := NewRemoveCommand(deps)
			command.SetArgs([]string{})
			err := command.Execute()
			if confirmed {
				if err != nil || !forgotten {
					t.Fatalf("confirmed removal = %v, forgotten=%v", err, forgotten)
				}
				if _, err := os.Stat(fixture.repository.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("repository remains after removal: %v", err)
				}
			} else {
				if !errors.Is(err, ErrRemoveDeclined) || forgotten {
					t.Fatalf("declined removal = %v, forgotten=%v", err, forgotten)
				}
				fixture.assertNoMutation(t)
			}
			if fixture.promptCalls != 1 || !strings.Contains(fixture.stderr.String(), "Removal plan:") {
				t.Fatalf("confirmation calls=%d, stderr=%q", fixture.promptCalls, fixture.stderr.String())
			}
			fixture.assertStdoutEmpty(t)
		})
	}
}

func TestRemoveSelectedTargetRejectsChangesWhilePickerIsOpen(t *testing.T) {
	for _, kind := range []string{"repository", "worktree"} {
		for _, change := range []string{"directory", "git metadata", "branch", "HEAD", "registry location"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				fixture := newRemoveTestFixture(t, "topic")
				selected := fixture.worktrees[0]
				if kind == "worktree" {
					selected = fixture.linkedRecord("topic")
				}
				deps := removeSelectionDependencies(fixture)
				deps.Select = func(_ context.Context, rows []string) (string, error) {
					row := removeSelectionRow(t, rows, kind+"\t")
					switch change {
					case "directory":
						if err := os.Rename(selected.Path, selected.Path+"-original"); err != nil {
							t.Fatal(err)
						}
						if err := os.MkdirAll(selected.Path, 0o755); err != nil {
							t.Fatal(err)
						}
						if kind == "repository" {
							if err := os.Mkdir(filepath.Join(selected.Path, ".git"), 0o755); err != nil {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(filepath.Join(selected.Path, ".git"), []byte("gitdir"), 0o600); err != nil {
							t.Fatal(err)
						}
					case "git metadata":
						path := filepath.Join(selected.Path, ".git")
						if err := os.Rename(path, path+"-original"); err != nil {
							t.Fatal(err)
						}
						if kind == "repository" {
							if err := os.Mkdir(path, 0o755); err != nil {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(path, []byte("gitdir"), 0o600); err != nil {
							t.Fatal(err)
						}
					case "branch", "HEAD":
						if change == "branch" {
							selected.Branch = "changed-branch"
						} else {
							selected.HEAD = "changed-head"
						}
						for index := range fixture.git.registered {
							if fixture.git.registered[index].Path == selected.Path {
								fixture.git.registered[index].Branch = selected.Branch
								fixture.git.registered[index].HEAD = selected.HEAD
							}
						}
					case "registry location":
						fixture.repositories[0].Path = filepath.Join(fixture.repositoryRoot, "replacement")
					}
					return row, nil
				}
				command := NewRemoveCommand(deps)
				command.SetArgs([]string{})
				err := command.Execute()
				if !errors.Is(err, ErrRemoveSafety) || ExitCode(err) != 1 {
					t.Fatalf("changed selection = %v, exit=%d; want safety refusal", err, ExitCode(err))
				}
				fixture.assertNoMutation(t)
				if fixture.promptCalls != 0 {
					t.Fatal("stale selection reached removal confirmation")
				}
				if _, err := os.Stat(selected.Path); err != nil {
					t.Fatalf("changed target was removed: %v", err)
				}
				fixture.assertStdoutEmpty(t)
			})
		}
	}
}

func TestRemovePickerWarningFailureDoesNotSelectOrMutate(t *testing.T) {
	fixture := newRemoveTestFixture(t, "topic")
	fixture.warnings = []local.Warning{{Path: fixture.repository.Path}}
	fixture.stderrWriter = &removeTestShortWriter{}
	deps := removeSelectionDependencies(fixture)
	deps.Select = func(context.Context, []string) (string, error) {
		t.Fatal("selector started after warning output failed")
		return "", nil
	}
	command := NewRemoveCommand(deps)
	command.SetArgs([]string{})
	if err := command.Execute(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("warning failure = %v", err)
	}
	fixture.assertNoMutation(t)
}

func removeSelectionRow(t *testing.T, rows []string, prefix string) string {
	t.Helper()
	for _, row := range rows {
		if strings.HasPrefix(row, prefix) {
			return row
		}
	}
	t.Fatalf("no candidate prefixed %q in %q", prefix, rows)
	return ""
}

func removeSelectionDependencies(fixture *removeTestFixture) RemoveDependencies {
	deps := fixture.dependencies()
	deps.Worktree = WorktreeRemoveDependencies{
		Resolver: deps.Resolver, Discover: deps.Discover,
		ResolveManaged: deps.ResolveManaged, Enumerate: deps.Enumerate,
		ValidateAssociation: deps.ValidateAssociation,
		Git:                 removeSelectionGit{fixture.git}, Filesystem: deps.Filesystem,
		Prompt: deps.Prompt, Remove: deps.Remove, Stdout: deps.Stdout, Stderr: deps.Stderr,
	}
	return deps
}

// Single-worktree selection must not query upstreams or require remote refs.
type removeSelectionGit struct{ *removeTestGit }

func (removeSelectionGit) BranchUpstreams(context.Context, string) ([]gitcmd.BranchUpstream, error) {
	panic("interactive removal queried branch upstreams")
}

func (removeSelectionGit) RefExists(context.Context, string, string) (bool, error) {
	panic("interactive removal queried remote refs")
}
