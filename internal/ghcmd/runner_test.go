package ghcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerOutputExecutesInTheCurrentDirectory(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.Output(context.Background(), "api", "user"); err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"api", "user"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if command.cmd.Dir != "" {
		t.Fatalf("command dir = %q, want the current directory (empty)", command.cmd.Dir)
	}
}

func TestRunnerResolvesGHPathLazilyAndPreservesExplicitExecutable(t *testing.T) {
	t.Setenv("GH_PATH", "first-gh")
	t.Setenv("PATH", t.TempDir())
	runner, recorder := newHelperRunner(t, "success")
	runner.Executable = ""
	runner.Stderr = io.Discard
	// Resolution must observe the environment at execution, not construction.
	t.Setenv("GH_PATH", "selected-gh")
	if _, err := runner.Output(context.Background(), "api", "user"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.last(t).executable; got != "selected-gh" {
		t.Fatalf("executable = %q, want selected-gh", got)
	}
	runner.Executable = "explicit-gh"
	if err := runner.runDir(context.Background(), "", "repo", "sync"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.last(t).executable; got != "explicit-gh" {
		t.Fatalf("executable = %q, want explicit-gh", got)
	}
}

func TestRunnerWrapsMissingPathExecutable(t *testing.T) {
	t.Setenv("GH_PATH", "")
	t.Setenv("PATH", t.TempDir())
	runner := NewRunner()
	for _, output := range []bool{false, true} {
		var err error
		if output {
			_, err = runner.Output(context.Background(), "api", "user")
		} else {
			err = runner.runDir(context.Background(), "", "repo", "sync")
		}
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || !errors.Is(err, exec.ErrNotFound) || commandErr.ExitCode != -1 {
			t.Fatalf("error = %v, want sanitized, unwrap-able executable lookup failure", err)
		}
	}
}

func TestRunnerRunDirStreamsConfiguredIO(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	var stdout, stderr bytes.Buffer
	runner.Stdout = &stdout
	runner.Stderr = &stderr
	runner.Env = append(runner.Env, "GHCMD_TEST_VALUE=environment-data")
	dir := t.TempDir()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	err = runner.runDir(context.Background(), dir, "repo", "sync")
	if err != nil {
		t.Fatalf("runDir() error = %v", err)
	}

	command := recorder.last(t)
	if command.executable != "fake-gh" {
		t.Fatalf("executable = %q, want %q", command.executable, "fake-gh")
	}
	wantArgs := []string{"repo", "sync"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if command.cmd.Dir != dir {
		t.Fatalf("command dir = %q, want %q", command.cmd.Dir, dir)
	}
	// The helper process's "stream" mode reads its own os.Stdin to
	// completion before writing this prefix. cmd.Stdin is never set (see
	// TestRunnerNeverConnectsCommandToCallerStandardInput), so the gh
	// subprocess's standard input is the null device and reads as empty
	// here, regardless of what the calling test process's own standard
	// input looks like.
	const stdoutPrefix = "|environment-data|"
	if !strings.HasPrefix(stdout.String(), stdoutPrefix) {
		t.Fatalf("stdout = %q, want prefix %q", stdout.String(), stdoutPrefix)
	}
	actualDir := strings.TrimPrefix(stdout.String(), stdoutPrefix)
	actualInfo, err := os.Stat(actualDir)
	if err != nil {
		t.Fatalf("Stat(stdout directory %q) error = %v", actualDir, err)
	}
	resolvedInfo, err := os.Stat(resolvedDir)
	if err != nil {
		t.Fatalf("Stat(resolved directory %q) error = %v", resolvedDir, err)
	}
	if !os.SameFile(actualInfo, resolvedInfo) {
		t.Fatalf("stdout directory = %q, want same directory as %q", actualDir, resolvedDir)
	}
	if stderr.String() != "streamed-stderr" {
		t.Fatalf("stderr = %q, want %q", stderr.String(), "streamed-stderr")
	}
}

func TestRunnerOutputPreservesFailureWithoutLeakingDetails(t *testing.T) {
	runner, recorder := newHelperRunner(t, "output-fail")
	var streamedStdout, streamedStderr bytes.Buffer
	runner.Stdout = &streamedStdout
	runner.Stderr = &streamedStderr
	secretArgument := "https://token-super-secret@example.com/owner/repo.git"

	output, err := runner.outputDir(context.Background(), "", "repo", "clone", secretArgument)
	if string(output) != "captured-output" {
		t.Fatalf("outputDir() = %q, want %q", output, "captured-output")
	}
	if err == nil {
		t.Fatal("outputDir() error = nil, want failure")
	}
	if streamedStdout.Len() != 0 {
		t.Fatalf("configured stdout received captured output: %q", streamedStdout.String())
	}
	if streamedStderr.String() != "stderr-super-secret" {
		t.Fatalf("streamed stderr = %q, want gh diagnostic", streamedStderr.String())
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != 23 {
		t.Fatalf("exit code = %d, want 23", commandErr.ExitCode)
	}
	if exitCode, ok := CommandExitCode(err); !ok || exitCode != 23 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (23, true)", exitCode, ok)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("wrapped error does not preserve *exec.ExitError: %v", err)
	}
	if got := string(commandErr.StderrOutput()); got != "stderr-super-secret" {
		t.Fatalf("retained stderr = %q, want %q", got, "stderr-super-secret")
	}
	if strings.Contains(err.Error(), secretArgument) || strings.Contains(err.Error(), "stderr-super-secret") {
		t.Fatalf("safe error exposed secret data: %q", err)
	}
	if got := err.Error(); got != "gh command failed with exit code 23" {
		t.Fatalf("error text = %q", got)
	}

	command := recorder.last(t)
	wantArgs := []string{"repo", "clone", secretArgument}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestRunnerReportsContextDeadline(t *testing.T) {
	runner, _ := newHelperRunner(t, "wait")
	runner.Stderr = &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := runner.outputDir(ctx, "", "repo", "sync")
	if err == nil {
		t.Fatal("outputDir() error = nil, want deadline failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline in chain", err)
	}
	if got := err.Error(); got != "gh command timed out" {
		t.Fatalf("error text = %q, want timeout message", got)
	}
}

func TestRunnerWrapsExecutableStartFailure(t *testing.T) {
	runner := &Runner{
		Executable: filepath.Join(t.TempDir(), "missing-gh"),
		Stderr:     &bytes.Buffer{},
	}

	_, err := runner.outputDir(context.Background(), "", "repo", "sync")
	if err == nil {
		t.Fatal("outputDir() error = nil, want start failure")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", commandErr.ExitCode)
	}
	if _, ok := CommandExitCode(err); ok {
		t.Fatal("CommandExitCode() reported an exit status for a start failure")
	}
	if got := err.Error(); got != "gh command could not be started" {
		t.Fatalf("error text = %q", got)
	}
}
