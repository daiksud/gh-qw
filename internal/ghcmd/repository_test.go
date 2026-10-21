package ghcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestRepoCloneArgumentOrdering(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	options := CloneOptions{
		URL:         "https://github.example/owner/repo",
		Destination: "/repos/repo",
		Shallow:     true,
		Branch:      "release/v1",
		Submodules:  SubmodulesRecursive,
		Filter:      PartialFilterTreeless,
	}

	err := runner.RepoClone(context.Background(), options)
	if err != nil {
		t.Fatalf("RepoClone() error = %v", err)
	}

	command := recorder.last(t)
	want := []string{
		"repo", "clone",
		"https://github.example/owner/repo",
		"/repos/repo",
		"--no-upstream",
		"--",
		"--depth=1",
		"--branch", "release/v1",
		"--single-branch",
		"--recursive",
		"--filter=tree:0",
	}
	if !slicesEqual(command.args, want) {
		t.Fatalf("args = %#v, want %#v", command.args, want)
	}
}

func TestRepoCloneOmitsGitFlagSeparatorWhenNoExtraFlagsAreNeeded(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoClone(context.Background(), CloneOptions{URL: "acme/widget"})
	if err != nil {
		t.Fatalf("RepoClone() error = %v", err)
	}

	want := []string{"repo", "clone", "acme/widget", "--no-upstream"}
	if got := recorder.last(t).args; !slicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRepoCloneSupportsDisabledSubmodulesAndBloblessFilter(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoClone(context.Background(), CloneOptions{
		URL:        "ssh://git@example.com/owner/repo.git",
		Submodules: SubmodulesDisabled,
		Filter:     PartialFilterBlobless,
	})
	if err != nil {
		t.Fatalf("RepoClone() error = %v", err)
	}

	want := []string{
		"repo", "clone",
		"ssh://git@example.com/owner/repo.git",
		"--no-upstream",
		"--",
		"--no-recursive",
		"--filter=blob:none",
	}
	if got := recorder.last(t).args; !slicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRepoCloneRejectsInvalidOptionsBeforeExecution(t *testing.T) {
	tests := []struct {
		name    string
		options CloneOptions
	}{
		{name: "missing URL"},
		{
			name: "invalid filter",
			options: CloneOptions{
				URL:    "https://example.com/owner/repo.git",
				Filter: PartialFilter("unexpected"),
			},
		},
		{
			name: "invalid submodule mode",
			options: CloneOptions{
				URL:        "https://example.com/owner/repo.git",
				Submodules: SubmoduleMode(99),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner, recorder := newHelperRunner(t, "success")
			runner.Stdout = &bytes.Buffer{}
			runner.Stderr = &bytes.Buffer{}

			err := runner.RepoClone(context.Background(), test.options)
			if err == nil {
				t.Fatal("RepoClone() error = nil, want validation failure")
			}
			if len(recorder.commands) != 0 {
				t.Fatalf("commands executed = %d, want 0 (validated before execution)", len(recorder.commands))
			}
		})
	}
}

func TestRepoCloneReturnsFailureFromGh(t *testing.T) {
	runner, _ := newHelperRunner(t, "exit-1")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoClone(context.Background(), CloneOptions{URL: "acme/private"})
	if err == nil {
		t.Fatal("RepoClone() error = nil, want failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", commandErr.ExitCode)
	}
}

func TestRepoSyncArgumentOrderingAndWorkingDirectory(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	dir := t.TempDir()

	err := runner.RepoSync(context.Background(), dir, SyncOptions{
		Source: "acme/widget",
		Branch: "main",
	})
	if err != nil {
		t.Fatalf("RepoSync() error = %v", err)
	}

	command := recorder.last(t)
	want := []string{"repo", "sync", "--source", "acme/widget", "--branch", "main"}
	if !slicesEqual(command.args, want) {
		t.Fatalf("args = %#v, want %#v", command.args, want)
	}
	if command.cmd.Dir != dir {
		t.Fatalf("command dir = %q, want %q", command.cmd.Dir, dir)
	}
}

func TestRepoSyncOmitsBranchWhenNotProvided(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{Source: "acme/widget"})
	if err != nil {
		t.Fatalf("RepoSync() error = %v", err)
	}

	want := []string{"repo", "sync", "--source", "acme/widget"}
	if got := recorder.last(t).args; !slicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestRepoSyncRejectsMissingSourceBeforeExecution(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{Branch: "main"})
	if err == nil {
		t.Fatal("RepoSync() error = nil, want validation failure")
	}
	if len(recorder.commands) != 0 {
		t.Fatalf("commands executed = %d, want 0 (validated before execution)", len(recorder.commands))
	}
}

func TestRepoSyncReturnsFailureFromGh(t *testing.T) {
	runner, _ := newHelperRunner(t, "exit-1")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	err := runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{Source: "acme/widget"})
	if err == nil {
		t.Fatal("RepoSync() error = nil, want failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", commandErr.ExitCode)
	}
}

func TestRepositoryCommandsPreserveStandardAuthenticationEnvironment(t *testing.T) {
	for _, operation := range []string{"clone", "sync"} {
		t.Run(operation, func(t *testing.T) {
			runner, recorder := newHelperRunner(t, "success")
			runner.Stdout = &bytes.Buffer{}
			runner.Stderr = &bytes.Buffer{}
			values := []string{"GH_TOKEN=dummy-gh", "GITHUB_TOKEN=dummy-github", "GH_ENTERPRISE_TOKEN=dummy-enterprise", "GITHUB_ENTERPRISE_TOKEN=dummy-github-enterprise", "GH_HOST=ghe.example.com"}
			runner.Env = append(runner.Env, values...)
			var err error
			if operation == "clone" {
				err = runner.RepoClone(context.Background(), CloneOptions{URL: "https://ghe.example.com/acme/widget", Destination: t.TempDir()})
			} else {
				err = runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{Source: "acme/widget"})
			}
			if err != nil {
				t.Fatal(err)
			}
			command := recorder.last(t)
			if !slicesEqual(command.cmd.Env, runner.Env) {
				t.Fatal("subprocess environment differs from caller environment")
			}
		})
	}
}

func TestRepoSyncUsesSelectedHostWithoutChangingAuthentication(t *testing.T) {
	keys := []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	for _, key := range keys {
		t.Setenv(key, "dummy-"+key)
	}
	t.Setenv("GH_HOST", "ambient.example.com")
	t.Setenv(helperProcessEnv, "1")
	for _, inherited := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "inherited"}[inherited], func(t *testing.T) {
			runner, recorder := newHelperRunner(t, "auth-environment")
			if inherited {
				runner.Env = nil
			}
			before := slices.Clone(runner.Env)
			for _, host := range []string{"github.com", "selected.example.com"} {
				var output bytes.Buffer
				runner.Stdout = &output
				runner.Stderr = &bytes.Buffer{}
				if err := runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{Host: host, Source: "acme/widget"}); err != nil {
					t.Fatal(err)
				}
				want := []string{host}
				for _, key := range keys {
					want = append(want, "dummy-"+key)
				}
				if output.String() != strings.Join(want, "\n") {
					t.Fatal("subprocess host or authentication environment differs")
				}
				if !slices.Equal(recorder.last(t).args, []string{"repo", "sync", "--source", "acme/widget"}) {
					t.Fatal("sync must remain a local operation")
				}
				if !slices.Equal(runner.Env, before) || os.Getenv("GH_HOST") != "ambient.example.com" {
					t.Fatal("sync mutated caller environment")
				}
			}
		})
	}
}
