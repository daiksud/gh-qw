package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

type commandContractResolver func() (rootpkg.Result, error)

func (resolve commandContractResolver) Resolve() (rootpkg.Result, error) {
	return resolve()
}

func TestCloneStopsBeforeMutationWhenWarningsCannotBeWritten(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordered", true: "parallel"}[parallel], func(t *testing.T) {
			root := t.TempDir()
			var stdout bytes.Buffer
			command := NewCloneCommand(CloneDependencies{
				RootResolver: commandContractResolver(func() (rootpkg.Result, error) {
					return rootpkg.Result{RepositoryRoots: []string{root}}, nil
				}),
				Discover: func(context.Context, []string) (local.DiscoveryResult, error) {
					return local.DiscoveryResult{Warnings: []local.Warning{{Path: root}}}, nil
				},
				GitFactory: func(io.Writer, io.Writer) CloneGitOperations {
					t.Fatal("a failed warning must prevent clone")
					return nil
				},
				MkdirAll: func(string, fs.FileMode) error {
					t.Fatal("a failed warning must prevent directory creation")
					return nil
				},
				Stdout: &stdout,
				Stderr: commandTestWriter(func([]byte) (int, error) { return 0, nil }),
			})
			args := []string{"acme/widget"}
			if parallel {
				args = append([]string{"--parallel"}, args...)
			}
			command.SetArgs(args)
			err := command.Execute()
			if !errors.Is(err, io.ErrShortWrite) || ExitCode(err) != 1 {
				t.Fatalf("warning failure = %v, exit = %d", err, ExitCode(err))
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed acquisition wrote result %q", stdout.String())
			}
			if _, err := os.Stat(filepath.Join(root, "github.com", "acme", "widget")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("failed warning changed repository path: %v", err)
			}
		})
	}
}

func TestWorktreeAddStopsBeforeMutationWhenWarningsCannotBeWritten(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-warning")
	fixture.discoveryWarning = &local.Warning{Path: fixture.repository.Path}
	fixture.stderrWriter = commandTestWriter(func([]byte) (int, error) { return 0, nil })
	err := fixture.worktreeAddExecute("feature-warning")
	if !errors.Is(err, io.ErrShortWrite) || ExitCode(err) != 1 {
		t.Fatalf("warning failure = %v, exit = %d", err, ExitCode(err))
	}
	if len(fixture.git.adds) != 0 || fixture.api.calls != 0 || len(fixture.gh.syncs) != 0 {
		t.Fatal("failed discovery warning was followed by Git or GitHub work")
	}
	if _, err := os.Stat(fixture.destination); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed warning changed worktree destination: %v", err)
	}
}

func TestWorktreeAddReportsShortOutputWithoutUndoingCreation(t *testing.T) {
	fixture := worktreeAddNewFixture(t, "feature-output")
	fixture.stdoutWriter = commandTestWriter(func([]byte) (int, error) { return 0, nil })
	err := fixture.worktreeAddExecute("feature-output")
	if !errors.Is(err, io.ErrShortWrite) || ExitCode(err) != 1 {
		t.Fatalf("result failure = %v, exit = %d", err, ExitCode(err))
	}
	if len(fixture.git.adds) != 1 {
		t.Fatalf("Git add calls = %d, want one", len(fixture.git.adds))
	}
	if _, err := os.Stat(fixture.destination); err != nil {
		t.Fatalf("created worktree was removed after output failure: %v", err)
	}
}

func TestApplicationHelpAndSyntaxErrorsDoNotResolveConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{name: "help", args: []string{"--help"}},
		{name: "version", args: []string{"--version"}},
		{name: "worktree help", args: []string{"add", "--worktree", "--help"}},
		{name: "invalid flag", args: []string{"--unknown"}, code: 2},
		{name: "missing operand", args: []string{"remove", "--worktree"}, code: 2},
		{name: "conflicting modes", args: []string{"add", "--worktree", "-b", "-B", "topic"}, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := ApplicationDependencies{
				Resolver: commandContractResolver(func() (rootpkg.Result, error) {
					t.Fatal("help and syntax errors must not read configuration")
					return rootpkg.Result{}, nil
				}),
				Discover: func(context.Context, []string, ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
					t.Fatal("help and syntax errors must not discover repositories")
					return local.DiscoveryResult{}, nil
				},
				Stdout: io.Discard,
				Stderr: io.Discard,
			}
			if status := Execute(context.Background(), test.args, deps); status != test.code {
				t.Fatalf("status = %d, want %d", status, test.code)
			}
		})
	}
}
