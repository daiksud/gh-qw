package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/daiksud/gh-qw/internal/testregistry"
)

type commandContractResolver func() (rootpkg.Result, error)

func (resolve commandContractResolver) Resolve() (rootpkg.Result, error) {
	return resolve()
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
		{name: "too many worktree slots", args: []string{"remove", "--worktree", "one", "two"}, code: 2},
		{name: "conflicting modes", args: []string{"add", "--worktree", "-b", "-B", "topic"}, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := ApplicationDependencies{
				Resolver: commandContractResolver(func() (rootpkg.Result, error) {
					t.Fatal("help and syntax errors must not read configuration")
					return rootpkg.Result{}, nil
				}),
				Registry: testregistry.From(func() (map[string]string, error) {
					t.Fatal("help and syntax errors must not read the registry")
					return map[string]string{}, nil
				}),
				Stdout: io.Discard,
				Stderr: io.Discard,
			}
			if status := Execute(context.Background(), test.args, deps); status != test.code {
				t.Fatalf("status = %d, want %d", status, test.code)
			}
		})
	}
}
