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

	if _, err := runner.Output(context.Background(), "auth", "status"); err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"auth", "status"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if command.cmd.Dir != "" {
		t.Fatalf("command dir = %q, want the current directory (empty)", command.cmd.Dir)
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

// TestRunnerNeverConnectsCommandToCallerStandardInput verifies that Runner
// leaves Cmd.Stdin nil. This makes gh read from the null device and prevents
// Cmd.Wait from depending on EOF from a caller-owned reader.
func TestRunnerNeverConnectsCommandToCallerStandardInput(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.Output(context.Background(), "auth", "status"); err != nil {
		t.Fatalf("Output() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stdin != nil {
		t.Fatalf("cmd.Stdin = %#v, want nil so gh always reads from the null device", command.cmd.Stdin)
	}
}

// TestRunnerDoesNotBlockOnALiveCallerStandardInput verifies that Runner does
// not depend on EOF from the calling process. The caller's os.Stdin remains
// open while gh reads from the null device, so Wait must return promptly.
func TestRunnerDoesNotBlockOnALiveCallerStandardInput(t *testing.T) {
	runner, _ := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
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
		_, err := runner.Output(context.Background(), "auth", "status")
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

func TestRunnerRunDirPassesThroughAnOSFileStderrDescriptor(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	runner.Stdout = &bytes.Buffer{}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	if err := runner.runDir(context.Background(), "", "repo", "sync"); err != nil {
		t.Fatalf("runDir() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stderr != writer {
		t.Fatalf(
			"cmd.Stderr = %#v, want the same *os.File %#v so gh inherits the descriptor directly",
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

	if err := runner.runDir(context.Background(), "", "repo", "sync"); err != nil {
		t.Fatalf("runDir() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stdout != writer {
		t.Fatalf(
			"cmd.Stdout = %#v, want the same *os.File %#v so gh inherits the descriptor directly",
			command.cmd.Stdout,
			writer,
		)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	const wantPrefix = "||" // empty stdin (null device) and empty GHCMD_TEST_VALUE
	if !strings.HasPrefix(string(got), wantPrefix) {
		t.Fatalf("passed-through stdout = %q, want prefix %q", got, wantPrefix)
	}
}

// TestRunnerOutputDirStderrTailStaysEmptyWhenPassedThrough guards
// CommandError.StderrOutput's documented behavior: once gh's stderr is
// passed through directly to an *os.File, there is nothing left for the
// bounded tail buffer to retain, since gh's diagnostic already reached that
// file on its own.
func TestRunnerOutputDirStderrTailStaysEmptyWhenPassedThrough(t *testing.T) {
	runner, _ := newHelperRunner(t, "output-fail")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	output, err := runner.outputDir(context.Background(), "", "repo", "clone")
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if string(output) != "captured-output" {
		t.Fatalf("outputDir() = %q, want %q", output, "captured-output")
	}
	if err == nil {
		t.Fatal("outputDir() error = nil, want failure")
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

// TestCommandErrorStderrOutputIsEmptyAfterPassthroughRun covers runDir, the
// path used by RepoSync and RepoClone. When gh's stderr is passed directly to
// an *os.File, the returned *CommandError has nothing retained to replay
// because the diagnostic bypasses the bounded tail buffer.
func TestCommandErrorStderrOutputIsEmptyAfterPassthroughRun(t *testing.T) {
	runner, _ := newHelperRunner(t, "output-fail")
	runner.Stdout = &bytes.Buffer{}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	err = runner.runDir(context.Background(), "", "repo", "clone")
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if err == nil {
		t.Fatal("runDir() error = nil, want failure")
	}

	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != 23 {
		t.Fatalf("exit code = %d, want 23", commandErr.ExitCode)
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

func TestRunnerWithTokenSetsGHTokenAndRemovesExistingTokenEntries(t *testing.T) {
	runner := Runner{Env: []string{
		"PATH=/usr/bin",
		"GH_TOKEN=stale-gh-token",
		"GITHUB_TOKEN=stale-github-token",
		"HOME=/home/x",
	}}

	updated := runner.WithToken("gho_fresh")

	want := []string{"PATH=/usr/bin", "HOME=/home/x", "GH_TOKEN=gho_fresh"}
	if !slicesEqual(updated.Env, want) {
		t.Fatalf("Env = %#v, want %#v", updated.Env, want)
	}
	if len(runner.Env) != 4 {
		t.Fatalf("original Runner.Env was mutated: %#v", runner.Env)
	}
}

func TestRunnerWithTokenDefaultsToProcessEnvironmentWhenEnvIsNil(t *testing.T) {
	runner := Runner{}

	updated := runner.WithToken("gho_fresh")

	found := false
	for _, entry := range updated.Env {
		if entry == "GH_TOKEN=gho_fresh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Env = %#v, want GH_TOKEN=gho_fresh present", updated.Env)
	}
	if len(updated.Env) < len(os.Environ()) {
		t.Fatalf("Env has %d entries, want at least the process environment's %d", len(updated.Env), len(os.Environ()))
	}
}

func TestRunnerWithTokenIsNoOpForEmptyToken(t *testing.T) {
	runner := Runner{Env: []string{"PATH=/usr/bin"}}

	updated := runner.WithToken("")

	if !slicesEqual(updated.Env, runner.Env) {
		t.Fatalf("Env = %#v, want unchanged %#v for an empty token", updated.Env, runner.Env)
	}
}

func TestRunnerWithTokenInjectsGHTokenIntoTheSpawnedCommand(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	scoped := runner.WithToken("gho_scoped")

	if err := scoped.RepoSync(context.Background(), t.TempDir(), SyncOptions{Source: "acme/widget"}); err != nil {
		t.Fatalf("RepoSync() error = %v", err)
	}

	command := recorder.last(t)
	found := false
	for _, entry := range command.cmd.Env {
		if entry == "GH_TOKEN=gho_scoped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("spawned command Env = %#v, want GH_TOKEN=gho_scoped", command.cmd.Env)
	}
}
