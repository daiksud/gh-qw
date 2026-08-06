package gitcmd

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

// TestRunnerNeverConnectsCommandToCallerStandardInput guards the fix for a
// gh-qw hang: exec.Cmd only avoids a background copy-to-pipe goroutine (and
// the Wait call blocking on it until EOF) when Cmd.Stdin is nil or a
// *os.File. Runner has no Stdin field to plumb a caller's reader through, so
// this asserts the built command's Stdin stays nil however Runner is used.
func TestRunnerNeverConnectsCommandToCallerStandardInput(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.Output(context.Background(), "status"); err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stdin != nil {
		t.Fatalf("cmd.Stdin = %#v, want nil so git always reads from the null device", command.cmd.Stdin)
	}
}

// TestRunnerDoesNotBlockOnALiveCallerStandardInput reproduces the
// conditions that made a delegated `git`/`gh` invocation hang until Enter
// was pressed: a calling process (a terminal) whose own standard input
// never reaches EOF. Before Stdin was removed from Runner, wiring that
// reader into cmd.Stdin (even indirectly through os.Stdin) made Wait block
// on a copy goroutine that never saw EOF. With no Stdin field to wire it
// through, git's own child process always gets the null device, so Wait
// must return promptly even while the test process's os.Stdin is a pipe
// deliberately left open.
func TestRunnerDoesNotBlockOnALiveCallerStandardInput(t *testing.T) {
	runner, _ := newHelperRunner(t, "success")
	runner.Stderr = &bytes.Buffer{}

	originalStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = reader.Close()
		_ = writer.Close()
	})
	os.Stdin = reader

	done := make(chan error, 1)
	go func() {
		_, err := runner.Output(context.Background(), "status")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Output() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Output() did not return; it blocked as if waiting on the caller's standard input")
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

// TestRunnerRunDirPassesThroughAnOSFileStderrDescriptor guards
// procio.PassthroughFile detection in RunDir: when Stderr is an *os.File,
// Git must inherit that file descriptor directly instead of writing
// through a pipe, so it can detect a terminal and render interactive
// progress and color.
func TestRunnerRunDirPassesThroughAnOSFileStderrDescriptor(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	runner.Stdout = &bytes.Buffer{}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	if err := runner.RunDir(context.Background(), "", "status", "--short"); err != nil {
		t.Fatalf("RunDir() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stderr != writer {
		t.Fatalf(
			"cmd.Stderr = %#v, want the same *os.File %#v so Git inherits the descriptor directly",
			command.cmd.Stderr,
			writer,
		)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "streamed-stderr" {
		t.Fatalf("passed-through stderr = %q, want %q", got, "streamed-stderr")
	}
}

func TestRunnerRunDirPassesThroughAnOSFileStdoutDescriptor(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	runner.Stderr = &bytes.Buffer{}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stdout = writer

	if err := runner.RunDir(context.Background(), "", "status", "--short"); err != nil {
		t.Fatalf("RunDir() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stdout != writer {
		t.Fatalf(
			"cmd.Stdout = %#v, want the same *os.File %#v so Git inherits the descriptor directly",
			command.cmd.Stdout,
			writer,
		)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	const wantPrefix = "||" // empty stdin (null device) and empty GITCMD_TEST_VALUE
	if !strings.HasPrefix(string(got), wantPrefix) {
		t.Fatalf("passed-through stdout = %q, want prefix %q", got, wantPrefix)
	}
}

// TestRunnerOutputDirStderrTailStaysEmptyWhenPassedThrough guards
// CommandError.StderrOutput's documented behavior: once Git's stderr is
// passed through directly to an *os.File, there is nothing left for the
// bounded tail buffer to retain, since Git's diagnostic already reached
// that file on its own.
func TestRunnerOutputDirStderrTailStaysEmptyWhenPassedThrough(t *testing.T) {
	runner, _ := newHelperRunner(t, "output-fail")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	output, err := runner.OutputDir(context.Background(), "", "ls-remote")
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if string(output) != "captured-output" {
		t.Fatalf("OutputDir() = %q, want %q", output, "captured-output")
	}
	if err == nil {
		t.Fatal("OutputDir() error = nil, want failure")
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if got := commandErr.StderrOutput(); len(got) != 0 {
		t.Fatalf("StderrOutput() = %q, want empty because stderr was passed through directly", got)
	}

	got, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatalf("ReadAll() error = %v", readErr)
	}
	if string(got) != "stderr-super-secret" {
		t.Fatalf("passed-through stderr = %q, want %q", got, "stderr-super-secret")
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

func TestTailBufferKeepsOnlyTail(t *testing.T) {
	buffer := newTailBuffer(5)
	_, _ = buffer.Write([]byte("abc"))
	_, _ = buffer.Write([]byte("defg"))
	if got := string(buffer.Bytes()); got != "cdefg" {
		t.Fatalf("tail = %q, want %q", got, "cdefg")
	}
	_, _ = buffer.Write([]byte("123456"))
	if got := string(buffer.Bytes()); got != "23456" {
		t.Fatalf("tail after oversized write = %q, want %q", got, "23456")
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
