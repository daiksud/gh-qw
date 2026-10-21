package cmd_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/config"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestRemovedHerdrFlagsAreUsageErrors(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"worktree", "add", "-R", "acme/widget", "topic"}, {"worktree", "remove", "-R", "acme/widget", "topic"}, {"rm", "acme/widget"}} {
		for _, flag := range []string{"--herdr", "--no-herdr"} {
			t.Run(strings.Join(command, " ")+" "+flag, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				args := append(append([]string(nil), command...), flag)
				status := cmd.Execute(context.Background(), args, cmd.ApplicationDependencies{
					Resolver: appRootResolver{result: rootpkg.Result{RepositoryRoots: []string{root}}},
					Stdout:   &stdout,
					Stderr:   &stderr,
				})
				if status != 2 || !strings.Contains(stderr.String(), "gh-qw: unknown flag: "+flag) {
					t.Fatalf("status=%d stderr=%q, want status 2 and unknown flag %s", status, stderr.String(), flag)
				}
				if stdout.Len() != 0 {
					t.Fatalf("stdout=%q, want empty", stdout.String())
				}
			})
		}
	}
}

func TestWorktreeHelpOmitsRemovedHerdrFlags(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"worktree", "add", "--help"}, {"worktree", "remove", "--help"}, {"rm", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := cmd.Execute(context.Background(), args, cmd.ApplicationDependencies{Stdout: &stdout, Stderr: &stderr})
			if status != 0 || !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q, want successful help", status, stdout.String(), stderr.String())
			}
			for _, flag := range []string{"--herdr", "--no-herdr"} {
				if strings.Contains(stdout.String(), flag) {
					t.Fatalf("help advertises removed %s: %s", flag, stdout.String())
				}
			}
		})
	}
}

func TestRetiredHerdrSettings(t *testing.T) {
	t.Parallel()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		document    string
		environment map[string]string
		wantStatus  int
		wantError   string
	}{
		{name: "ordinary defaults"},
		{name: "retired TOML key", document: "herdr = true", wantStatus: 2, wantError: `unknown top-level key or table "herdr"`},
		{name: "retired environment ignored", environment: map[string]string{"GHQW_HERDR": "maybe", "HERDR_ENV": "1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) { value, ok := test.environment[key]; return value, ok }
			homeDir := func() (string, error) { return home, nil }
			loader := config.NewLoaderWithOptions(config.LoaderOptions{
				LookupEnv: lookup, HomeDir: homeDir,
				ReadFile: func(string) ([]byte, error) { return []byte(test.document), nil },
			})
			resolver := rootpkg.NewResolverWithOptions(rootpkg.Options{ConfigLoader: loader, LookupEnv: lookup, HomeDir: homeDir})
			var stdout, stderr bytes.Buffer
			status := cmd.Execute(context.Background(), []string{"root"}, cmd.ApplicationDependencies{Resolver: resolver, Stdout: &stdout, Stderr: &stderr})
			if status != test.wantStatus {
				t.Fatalf("status=%d stderr=%q, want %d", status, stderr.String(), test.wantStatus)
			}
			if test.wantError != "" {
				if !strings.Contains(stderr.String(), test.wantError) || stdout.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q, want empty stdout and %q", stdout.String(), stderr.String(), test.wantError)
				}
			} else {
				want := filepath.ToSlash(filepath.Join(home, "ghqw")) + "\n"
				if stdout.String() != want || stderr.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q, want %q and no diagnostic", stdout.String(), stderr.String(), want)
				}
			}
		})
	}
}
