package fzf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSelectPassesNoMultiAndPromptAndEchoesStdin(t *testing.T) {
	runner, recorder := newHelperRunner(t, "echo")
	var stderr bytes.Buffer
	runner.Stderr = &stderr

	selected, err := runner.Select(
		context.Background(),
		[]string{"a", "b", "c"},
		Options{Prompt: "gh qw> "},
	)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected != "a\nb\nc" {
		t.Fatalf("selected = %q, want %q", selected, "a\nb\nc")
	}

	command := recorder.last(t)
	if command.executable != "fake-fzf" {
		t.Fatalf("executable = %q, want %q", command.executable, "fake-fzf")
	}
	wantArgs := []string{"--no-multi", "--prompt", "gh qw> "}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if stderr.String() != "streamed-stderr" {
		t.Fatalf("stderr = %q, want %q", stderr.String(), "streamed-stderr")
	}
}

func TestSelectOmitsPromptFlagWhenEmpty(t *testing.T) {
	runner, recorder := newHelperRunner(t, "echo")
	runner.Stderr = &bytes.Buffer{}

	if _, err := runner.Select(context.Background(), []string{"only"}, Options{}); err != nil {
		t.Fatalf("Select() error = %v", err)
	}

	command := recorder.last(t)
	wantArgs := []string{"--no-multi"}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
}

func TestSelectReturnsEmptyWithoutStartingCommandForNoItems(t *testing.T) {
	runner, recorder := newHelperRunner(t, "echo")
	runner.Stderr = &bytes.Buffer{}

	selected, err := runner.Select(context.Background(), nil, Options{})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selected != "" {
		t.Fatalf("selected = %q, want empty", selected)
	}
	if len(recorder.commands) != 0 {
		t.Fatalf("commands executed = %d, want 0", len(recorder.commands))
	}
}

func TestSelectClassifiesNoMatchExitStatus(t *testing.T) {
	runner, _ := newHelperRunner(t, "exit-1")
	runner.Stderr = &bytes.Buffer{}

	_, err := runner.Select(context.Background(), []string{"a"}, Options{})
	if err == nil {
		t.Fatal("Select() error = nil, want no-match failure")
	}
	if code, ok := CommandExitCode(err); !ok || code != 1 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (1, true)", code, ok)
	}
	if !IsNoMatch(err) {
		t.Fatal("IsNoMatch() = false, want true")
	}
	if IsCanceled(err) {
		t.Fatal("IsCanceled() = true, want false")
	}
}

func TestSelectClassifiesCanceledExitStatus(t *testing.T) {
	runner, _ := newHelperRunner(t, "exit-130")
	runner.Stderr = &bytes.Buffer{}

	_, err := runner.Select(context.Background(), []string{"a"}, Options{})
	if err == nil {
		t.Fatal("Select() error = nil, want canceled failure")
	}
	if code, ok := CommandExitCode(err); !ok || code != 130 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (130, true)", code, ok)
	}
	if !IsCanceled(err) {
		t.Fatal("IsCanceled() = false, want true")
	}
	if IsNoMatch(err) {
		t.Fatal("IsNoMatch() = true, want false")
	}
}

func TestSelectClassifiesOtherExitStatus(t *testing.T) {
	runner, _ := newHelperRunner(t, "exit-2")
	runner.Stderr = &bytes.Buffer{}

	_, err := runner.Select(context.Background(), []string{"a"}, Options{})
	if err == nil {
		t.Fatal("Select() error = nil, want failure")
	}
	if code, ok := CommandExitCode(err); !ok || code != 2 {
		t.Fatalf("CommandExitCode() = (%d, %v), want (2, true)", code, ok)
	}
	if IsCanceled(err) {
		t.Fatal("IsCanceled() = true, want false")
	}
	if IsNoMatch(err) {
		t.Fatal("IsNoMatch() = true, want false")
	}
	if got := err.Error(); got != "fzf exited with status 2" {
		t.Fatalf("error text = %q, want %q", got, "fzf exited with status 2")
	}
}

// TestSelectPassesThroughAnOSFileStderrDescriptor guards
// procio.PassthroughFile detection in Select: when Stderr is an *os.File,
// fzf must inherit that file descriptor directly instead of writing
// through a pipe, so it can detect a terminal and render its own
// diagnostics exactly as it would running standalone.
func TestSelectPassesThroughAnOSFileStderrDescriptor(t *testing.T) {
	runner, recorder := newHelperRunner(t, "echo")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	defer reader.Close()
	runner.Stderr = writer

	if _, err := runner.Select(context.Background(), []string{"a"}, Options{}); err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	command := recorder.last(t)
	if command.cmd.Stderr != writer {
		t.Fatalf(
			"cmd.Stderr = %#v, want the same *os.File %#v so fzf inherits the descriptor directly",
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

func TestSelectReportsMissingExecutable(t *testing.T) {
	runner := &Runner{
		Stderr: &bytes.Buffer{},
		lookPath: func(string) (string, error) {
			return "", errors.New("no such file")
		},
	}

	_, err := runner.Select(context.Background(), []string{"a"}, Options{})
	if err == nil {
		t.Fatal("Select() error = nil, want resolution failure")
	}
	if _, ok := CommandExitCode(err); ok {
		t.Fatal("CommandExitCode() reported an exit status for a resolution failure")
	}
	if !strings.Contains(err.Error(), "resolve fzf executable") {
		t.Fatalf("error text = %q, want it to mention resolving fzf", err.Error())
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", commandErr.ExitCode)
	}
}

func TestSelectReportsContextDeadline(t *testing.T) {
	runner, _ := newHelperRunner(t, "wait")
	runner.Stderr = &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := runner.Select(ctx, []string{"a"}, Options{})
	if err == nil {
		t.Fatal("Select() error = nil, want deadline failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline in chain", err)
	}
	if got := err.Error(); got != "fzf command timed out" {
		t.Fatalf("error text = %q, want timeout message", got)
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
