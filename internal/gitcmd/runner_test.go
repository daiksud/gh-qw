package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerRunDirStreamsConfiguredIO(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	var stdout, stderr bytes.Buffer
	runner.Stdout = &stdout
	runner.Stderr = &stderr
	runner.Env = append(runner.Env, "GITCMD_TEST_VALUE=environment-data")
	dir := t.TempDir()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}

	err = runner.RunDir(context.Background(), dir, "status", "--short")
	if err != nil {
		t.Fatalf("RunDir() error = %v", err)
	}

	command := recorder.last(t)
	if command.executable != "fake-git" {
		t.Fatalf("executable = %q, want %q", command.executable, "fake-git")
	}
	wantArgs := []string{"status", "--short"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if command.cmd.Dir != dir {
		t.Fatalf("command dir = %q, want %q", command.cmd.Dir, dir)
	}
	// The helper process's "stream" mode reads its own os.Stdin to
	// completion before writing this prefix. cmd.Stdin is never set (see
	// TestRunnerNeverConnectsCommandToCallerStandardInput), so the git
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

	output, err := runner.Output(context.Background(), "ls-remote", "--", secretArgument)
	if string(output) != "captured-output" {
		t.Fatalf("Output() = %q, want %q", output, "captured-output")
	}
	if err == nil {
		t.Fatal("Output() error = nil, want failure")
	}
	if streamedStdout.Len() != 0 {
		t.Fatalf("configured stdout received captured output: %q", streamedStdout.String())
	}
	if streamedStderr.String() != "stderr-super-secret" {
		t.Fatalf("streamed stderr = %q, want Git diagnostic", streamedStderr.String())
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
	retained := commandErr.StderrOutput()
	retained[0] = 'X'
	if got := string(commandErr.StderrOutput()); got != "stderr-super-secret" {
		t.Fatalf("retained stderr was mutable: %q", got)
	}
	if strings.Contains(err.Error(), secretArgument) || strings.Contains(err.Error(), "stderr-super-secret") {
		t.Fatalf("safe error exposed secret data: %q", err)
	}
	if got := err.Error(); got != "git command failed with exit code 23" {
		t.Fatalf("error text = %q", got)
	}

	command := recorder.last(t)
	wantArgs := []string{"ls-remote", "--", secretArgument}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestRunnerReportsContextDeadline(t *testing.T) {
	runner, _ := newHelperRunner(t, "wait")
	runner.Stderr = &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := runner.Output(ctx, "status")
	if err == nil {
		t.Fatal("Output() error = nil, want deadline failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline in chain", err)
	}
	if got := err.Error(); got != "git command timed out" {
		t.Fatalf("error text = %q, want timeout message", got)
	}
}

func TestRunnerWrapsExecutableStartFailure(t *testing.T) {
	runner := &Runner{
		Executable: filepath.Join(t.TempDir(), "missing-git"),
		Stderr:     &bytes.Buffer{},
	}

	_, err := runner.Output(context.Background(), "status")
	if err == nil {
		t.Fatal("Output() error = nil, want start failure")
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
	var pathErr *os.PathError
	var execErr *exec.Error
	if !errors.As(err, &pathErr) && !errors.As(err, &execErr) {
		t.Fatalf("wrapped error does not preserve executable failure: %v", err)
	}
	if got := err.Error(); got != "git command could not be started" {
		t.Fatalf("error text = %q", got)
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
