package herdr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCreateWorkspacePassesCwdLabelAndFocusFlags(t *testing.T) {
	runner, recorder := newHelperRunner(t, "workspace-create")
	runner.Stderr = &bytes.Buffer{}

	workspace, err := runner.CreateWorkspace(context.Background(), CreateOptions{
		Cwd:   "/worktrees/feature",
		Label: "gh-qw@feature/login",
		Focus: true,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	if workspace.ID != "w1X" {
		t.Fatalf("workspace.ID = %q, want %q", workspace.ID, "w1X")
	}

	command := recorder.last(t)
	wantArgs := []string{
		"workspace", "create",
		"--cwd", "/worktrees/feature",
		"--label", "gh-qw@feature/login",
		"--focus",
	}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestCreateWorkspacePassesNoFocusWhenNotFocusing(t *testing.T) {
	runner, recorder := newHelperRunner(t, "workspace-create")
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.CreateWorkspace(context.Background(), CreateOptions{Cwd: "/x"}); err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"workspace", "create", "--cwd", "/x", "--no-focus"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestCreateWorkspaceOmitsCwdAndLabelWhenEmpty(t *testing.T) {
	runner, recorder := newHelperRunner(t, "workspace-create")
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.CreateWorkspace(context.Background(), CreateOptions{}); err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"workspace", "create", "--no-focus"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestCreateWorkspaceRejectsEmptyWorkspaceID(t *testing.T) {
	runner, _ := newHelperRunner(t, "workspace-create-empty-id")
	runner.Stderr = &bytes.Buffer{}

	_, err := runner.CreateWorkspace(context.Background(), CreateOptions{Cwd: "/x"})
	if err == nil {
		t.Fatal("CreateWorkspace() error = nil, want a missing-ID failure")
	}
	if !strings.Contains(err.Error(), "workspace ID") {
		t.Fatalf("error text = %q, want it to mention the missing workspace ID", err.Error())
	}
}

func TestFindWorkspaceForPathMatchesCleanedPath(t *testing.T) {
	runner, recorder := newHelperRunner(t, "worktree-list")
	runner.Stderr = &bytes.Buffer{}

	id, found, err := runner.FindWorkspaceForPath(context.Background(), "/repo", "/repo/worktrees/feature")
	if err != nil {
		t.Fatalf("FindWorkspaceForPath() error = %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if id != "w2" {
		t.Fatalf("id = %q, want %q", id, "w2")
	}

	command := recorder.last(t)
	wantArgs := []string{"worktree", "list", "--cwd", "/repo"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestFindWorkspaceForPathReportsNotFoundWithoutError(t *testing.T) {
	runner, _ := newHelperRunner(t, "worktree-list")
	runner.Stderr = &bytes.Buffer{}

	id, found, err := runner.FindWorkspaceForPath(context.Background(), "/repo", "/does/not/exist")
	if err != nil {
		t.Fatalf("FindWorkspaceForPath() error = %v", err)
	}
	if found {
		t.Fatal("found = true, want false")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
}

func TestFindWorkspaceForPathSkipsWorktreesWithoutAnOpenWorkspace(t *testing.T) {
	runner, _ := newHelperRunner(t, "worktree-list")
	runner.Stderr = &bytes.Buffer{}

	// The fixture's "/repo" entry carries no open_workspace_id.
	_, found, err := runner.FindWorkspaceForPath(context.Background(), "/repo", "/repo")
	if err != nil {
		t.Fatalf("FindWorkspaceForPath() error = %v", err)
	}
	if found {
		t.Fatal("found = true, want false for a worktree without an open workspace")
	}
}

func TestCloseWorkspacePassesWorkspaceID(t *testing.T) {
	runner, recorder := newHelperRunner(t, "ok")
	runner.Stderr = &bytes.Buffer{}

	if err := runner.CloseWorkspace(context.Background(), "w1X"); err != nil {
		t.Fatalf("CloseWorkspace() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"workspace", "close", "w1X"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestCreateWorkspaceReportsInvalidJSON(t *testing.T) {
	runner, _ := newHelperRunner(t, "invalid-json")
	runner.Stderr = &bytes.Buffer{}

	_, err := runner.CreateWorkspace(context.Background(), CreateOptions{Cwd: "/x"})
	if err == nil {
		t.Fatal("CreateWorkspace() error = nil, want a JSON parse failure")
	}
	if !strings.Contains(err.Error(), "parse herdr response") {
		t.Fatalf("error text = %q, want it to mention parsing the response", err.Error())
	}
}

func TestCommandErrorIncludesHerdrsOwnErrorMessage(t *testing.T) {
	runner, _ := newHelperRunner(t, "error-1")
	runner.Stderr = &bytes.Buffer{}

	err := runner.CloseWorkspace(context.Background(), "w9")
	if err == nil {
		t.Fatal("CloseWorkspace() error = nil, want failure")
	}
	if code, ok := CommandExitCode(err); !ok || code != 1 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (1, true)", code, ok)
	}
	if got, want := err.Error(), "herdr exited with status 1: workspace w9 not found"; got != want {
		t.Fatalf("error text = %q, want %q", got, want)
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if !bytes.Contains(commandErr.StderrOutput(), []byte("workspace_not_found")) {
		t.Fatalf("StderrOutput() = %q, want it to contain the herdr error code", commandErr.StderrOutput())
	}
}

func TestCommandErrorFallsBackToPlainStatusForUsageErrors(t *testing.T) {
	runner, _ := newHelperRunner(t, "usage-error-2")
	runner.Stderr = &bytes.Buffer{}

	err := runner.CloseWorkspace(context.Background(), "w9")
	if err == nil {
		t.Fatal("CloseWorkspace() error = nil, want failure")
	}
	if code, ok := CommandExitCode(err); !ok || code != 2 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (2, true)", code, ok)
	}
	if got, want := err.Error(), "herdr exited with status 2"; got != want {
		t.Fatalf("error text = %q, want %q", got, want)
	}
}

func TestInSessionRequiresExactly1(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
		want  bool
	}{
		{name: "set to 1", value: "1", ok: true, want: true},
		{name: "set to true", value: "true", ok: true, want: false},
		{name: "empty", value: "", ok: true, want: false},
		{name: "unset", ok: false, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookupEnv := func(key string) (string, bool) {
				if key != "HERDR_ENV" {
					t.Fatalf("lookupEnv(%q), want HERDR_ENV", key)
				}
				return test.value, test.ok
			}
			if got := InSession(lookupEnv); got != test.want {
				t.Fatalf("InSession() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestInSessionDefaultsToOSLookupEnv(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	if !InSession(nil) {
		t.Fatal("InSession(nil) = false, want true with HERDR_ENV=1 in the process environment")
	}
}

func TestResolveExecutableReportsMissingExecutable(t *testing.T) {
	runner := &Runner{
		Stderr: &bytes.Buffer{},
		lookPath: func(string) (string, error) {
			return "", errors.New("no such file")
		},
	}

	_, err := runner.CreateWorkspace(context.Background(), CreateOptions{Cwd: "/x"})
	if err == nil {
		t.Fatal("CreateWorkspace() error = nil, want resolution failure")
	}
	if _, ok := CommandExitCode(err); ok {
		t.Fatal("CommandExitCode() reported an exit status for a resolution failure")
	}
	if !strings.Contains(err.Error(), "resolve herdr executable") {
		t.Fatalf("error text = %q, want it to mention resolving herdr", err.Error())
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", commandErr.ExitCode)
	}
}

func TestCommandReportsContextDeadline(t *testing.T) {
	runner, _ := newHelperRunner(t, "wait")
	runner.Stderr = &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := runner.CloseWorkspace(ctx, "w1")
	if err == nil {
		t.Fatal("CloseWorkspace() error = nil, want deadline failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline in chain", err)
	}
	if got := err.Error(); got != "herdr command timed out" {
		t.Fatalf("error text = %q, want timeout message", got)
	}
}

// TestPassesThroughAnOSFileStderrDescriptor guards procio.PassthroughFile
// detection in output: when Stderr is an *os.File, herdr must inherit that
// file descriptor directly instead of writing through a pipe, so it can
// detect a terminal exactly as it would running standalone.
func TestPassesThroughAnOSFileStderrDescriptor(t *testing.T) {
	runner, recorder := newHelperRunner(t, "ok")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	if err := runner.CloseWorkspace(context.Background(), "w1"); err != nil {
		t.Fatalf("CloseWorkspace() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stderr != writer {
		t.Fatalf(
			"cmd.Stderr = %#v, want the same *os.File %#v so herdr inherits the descriptor directly",
			command.cmd.Stderr,
			writer,
		)
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
