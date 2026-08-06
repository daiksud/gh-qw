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

func TestRepoSyncScopesSubprocessToTokenWithoutAddingArguments(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	runner.Env = append(runner.Env, "GH_TOKEN=stale")

	err := runner.RepoSync(context.Background(), t.TempDir(), SyncOptions{
		Source: "acme/widget",
		Token:  "gho_scoped",
	})
	if err != nil {
		t.Fatalf("RepoSync() error = %v", err)
	}

	command := recorder.last(t)
	want := []string{"repo", "sync", "--source", "acme/widget"}
	if !slicesEqual(command.args, want) {
		t.Fatalf("args = %#v, want %#v (token must never appear as an argument)", command.args, want)
	}
	found := false
	for _, entry := range command.cmd.Env {
		if entry == "GH_TOKEN=gho_scoped" {
			found = true
		}
		if entry == "GH_TOKEN=stale" {
			t.Fatalf("spawned command Env retained the stale token: %#v", command.cmd.Env)
		}
	}
	if !found {
		t.Fatalf("spawned command Env = %#v, want GH_TOKEN=gho_scoped", command.cmd.Env)
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
