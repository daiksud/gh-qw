package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewCommandWiresCompleteSurface(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	command := cmd.NewCommand(cmd.ApplicationDependencies{
		Stdin:   strings.NewReader(""),
		Stdout:  &stdout,
		Stderr:  &stderr,
		Version: "1.2.3",
	})

	if got, want := command.Name(), "qw"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if got, want := command.Annotations["cobra_annotation_command_display_name"], "gh qw"; got != want {
		t.Fatalf("display name = %q, want %q", got, want)
	}

	for _, path := range [][]string{
		{"get"},
		{"clone"},
		{"list"},
		{"root"},
		{"rm"},
		{"migrate"},
		{"worktree", "add"},
		{"worktree", "list"},
		{"worktree", "remove"},
		{"worktree", "prune"},
	} {
		found, remaining, err := command.Find(path)
		if err != nil {
			t.Fatalf("Find(%q) error = %v", path, err)
		}
		if len(remaining) != 0 {
			t.Fatalf("Find(%q) remaining = %q, want none", path, remaining)
		}
		if got, want := found.Name(), path[len(path)-1]; path[0] != "clone" && got != want {
			t.Fatalf("Find(%q) name = %q, want %q", path, got, want)
		}
		if path[0] == "clone" && found.Name() != "get" {
			t.Fatalf("Find(%q) name = %q, want get alias", path, found.Name())
		}
	}
}

func TestNewCommandHelpAndVersionUseExtensionName(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "help",
			args: []string{"--help"},
			want: []string{
				"Usage:",
				"gh qw [command]",
				"get",
				"worktree",
			},
		},
		{
			name: "version",
			args: []string{"--version"},
			want: []string{"gh qw version 1.2.3\n"},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			command := cmd.NewCommand(cmd.ApplicationDependencies{
				Stdin:   strings.NewReader(""),
				Stdout:  &stdout,
				Stderr:  &stderr,
				Version: "1.2.3",
			})
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout = %q, want substring %q", stdout.String(), want)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestExecuteMapsUsageAndRuntimeErrors(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Join(t.TempDir(), "repositories")
	tests := []struct {
		name       string
		args       []string
		resolver   cmd.RootResolver
		wantStatus int
		wantError  string
		wantOutput string
	}{
		{
			name:       "success",
			args:       []string{"root"},
			resolver:   appRootResolver{result: rootpkg.Result{RepositoryRoots: []string{rootPath}}},
			wantStatus: 0,
			wantOutput: filepath.ToSlash(rootPath) + "\n",
		},
		{
			name:       "unknown command",
			args:       []string{"unknown"},
			wantStatus: 2,
			wantError:  `gh-qw: unknown command "unknown" for "gh qw"`,
		},
		{
			name:       "unknown flag",
			args:       []string{"--unknown"},
			wantStatus: 2,
			wantError:  "gh-qw: unknown flag: --unknown",
		},
		{
			name:       "missing argument",
			args:       []string{"rm"},
			wantStatus: 2,
			wantError:  "gh-qw: accepts 1 arg(s), received 0",
		},
		{
			name:       "runtime failure",
			args:       []string{"root"},
			resolver:   appRootResolver{err: errors.New("cannot read roots")},
			wantStatus: 1,
			wantError:  "gh-qw: cannot read roots",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			status := cmd.Execute(context.Background(), test.args, cmd.ApplicationDependencies{
				Resolver: test.resolver,
				Stdin:    strings.NewReader(""),
				Stdout:   &stdout,
				Stderr:   &stderr,
			})

			if status != test.wantStatus {
				t.Fatalf("Execute() status = %d, want %d", status, test.wantStatus)
			}
			if got := stdout.String(); got != test.wantOutput {
				t.Fatalf("stdout = %q, want %q", got, test.wantOutput)
			}
			if test.wantError == "" {
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
			} else if got := strings.TrimSpace(stderr.String()); got != test.wantError {
				t.Fatalf("stderr = %q, want %q", got, test.wantError)
			}
			if strings.Contains(stderr.String(), "Usage:") {
				t.Fatalf("stderr unexpectedly contains usage: %q", stderr.String())
			}
		})
	}
}

func TestExitCodeClassifiesTypedFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "command usage", err: fmt.Errorf("wrapped: %w", cmd.ErrCommandUsage), want: 2},
		{name: "get usage", err: fmt.Errorf("wrapped: %w", cmd.ErrGetUsage), want: 2},
		{name: "repository specification", err: fmt.Errorf("wrapped: %w", repospec.ErrUsage), want: 2},
		{name: "configuration", err: fmt.Errorf("wrapped: %w", config.ErrInvalid), want: 2},
		{name: "root", err: fmt.Errorf("wrapped: %w", rootpkg.ErrInvalidRoot), want: 2},
		{name: "selector", err: fmt.Errorf("wrapped: %w", local.ErrSelector), want: 2},
		{name: "runtime", err: errors.New("Git failed"), want: 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := cmd.ExitCode(test.err); got != test.want {
				t.Fatalf("ExitCode(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

type appRootResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver appRootResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

var _ cmd.RootResolver = appRootResolver{}
var _ cmd.RootResolver = appRootResolver{}
