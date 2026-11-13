package ghcmd

import (
	"bytes"
	"context"
	"errors"
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

func TestRepoClonePreservesStandardAuthenticationEnvironment(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	values := []string{"GH_TOKEN=dummy-gh", "GITHUB_TOKEN=dummy-github", "GH_ENTERPRISE_TOKEN=dummy-enterprise", "GITHUB_ENTERPRISE_TOKEN=dummy-github-enterprise", "GH_HOST=ghe.example.com"}
	runner.Env = append(runner.Env, values...)
	if err := runner.RepoClone(context.Background(), CloneOptions{URL: "https://ghe.example.com/acme/widget", Destination: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	command := recorder.last(t)
	if !slicesEqual(command.cmd.Env, runner.Env) {
		t.Fatal("subprocess environment differs from caller environment")
	}
}
