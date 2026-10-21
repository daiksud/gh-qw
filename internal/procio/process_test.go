package procio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSubprocessHelper(t *testing.T) {
	if os.Getenv("PROFICIO_HELPER") != "1" {
		return
	}
	mode := os.Getenv("PROFICIO_MODE")
	switch mode {
	case "io":
		input, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write([]byte("stdout:" + string(input)))
		_, _ = os.Stderr.Write([]byte("stderr"))
		os.Exit(0)
	case "fail":
		_, _ = os.Stdout.Write([]byte("partial"))
		_, _ = os.Stderr.Write([]byte("secret-" + strings.Repeat("x", 80000)))
		os.Exit(23)
	case "fail-small":
		_, _ = os.Stdout.Write([]byte("partial"))
		_, _ = os.Stderr.Write([]byte("secret-diagnostic"))
		os.Exit(23)
	case "wait":
		time.Sleep(10 * time.Second)
	}
	os.Exit(90)
}

func helper(ctx context.Context, t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := Command(ctx, os.Args[0], "", append(os.Environ(), "PROFICIO_HELPER=1", "PROFICIO_MODE="+mode), nil, "-test.run=^TestSubprocessHelper$")
	if cmd.Stdin != nil {
		t.Fatal("child stdin must read from null")
	}
	return cmd
}

func TestExecuteCapturesPartialOutputAndBoundsStderr(t *testing.T) {
	cmd := helper(context.Background(), t, "fail")
	var stderr bytes.Buffer
	result, err := Execute(context.Background(), cmd, nil, &stderr, true)
	if err == nil || result.ExitCode != 23 || string(result.Stdout) != "partial" {
		t.Fatalf("result %#v, err %v", result, err)
	}
	if len(result.Failure.StderrOutput()) != StderrTailLimit {
		t.Fatalf("tail size %d", len(result.Failure.StderrOutput()))
	}
	if stderr.Len() != len("secret-")+80000 {
		t.Fatalf("stream length %d", stderr.Len())
	}
	if !strings.HasSuffix(string(result.Failure.StderrOutput()), strings.Repeat("x", 256)) {
		t.Fatal("tail not ending in latest bytes")
	}
	if strings.Contains(result.Failure.Message("git", result.ExitCode), "secret") {
		t.Fatal("message leaked stderr")
	}
	clone := result.Failure.StderrOutput()
	clone[0] = 'z'
	if bytes.Equal(clone, result.Failure.StderrOutput()) {
		t.Fatal("tail returned internal slice")
	}
}

func TestExecutePassesFilesDirectlyAndNeverCapturesInheritedStderr(t *testing.T) {
	cmd := helper(context.Background(), t, "io")
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutReader.Close()
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderrReader.Close()
	result, runErr := Execute(context.Background(), cmd, stdoutWriter, stderrWriter, false)
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	if cmd.Stdout != stdoutWriter || cmd.Stderr != stderrWriter {
		t.Fatal("file descriptors were wrapped in pipes")
	}
	stdout, _ := io.ReadAll(stdoutReader)
	stderr, _ := io.ReadAll(stderrReader)
	if string(stdout) != "stdout:" || string(stderr) != "stderr" || len(result.Stdout) != 0 {
		t.Fatalf("stdout %q, stderr %q result %q", stdout, stderr, result.Stdout)
	}
}

func TestExecuteDirectStderrHasNoTailOnFailure(t *testing.T) {
	cmd := helper(context.Background(), t, "fail-small")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	result, runErr := Execute(context.Background(), cmd, io.Discard, writer, true)
	_ = writer.Close()
	if runErr == nil || result.ExitCode != 23 || len(result.Failure.StderrOutput()) != 0 {
		t.Fatalf("result %#v, err %v", result, runErr)
	}
	if cmd.Stderr != writer {
		t.Fatal("stderr not inherited")
	}
	all, _ := io.ReadAll(reader)
	if !bytes.HasPrefix(all, []byte("secret-")) {
		t.Fatalf("inherited stderr starts with %q", all[:min(len(all), 16)])
	}
}

func TestExecutePreservesContextAndNoErrorLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cmd := helper(ctx, t, "wait")
	res, err := Execute(ctx, cmd, io.Discard, io.Discard, true)
	if err == nil || !errors.Is(res.Failure.Unwrap(), context.DeadlineExceeded) {
		t.Fatalf("deadline failed: %v, %#v", err, res)
	}
	if got := res.Failure.Message("gh", res.ExitCode); got != "gh command timed out" {
		t.Fatalf("message %q", got)
	}
}

func TestExecutePreservesExecutableStartFailure(t *testing.T) {
	ctx := context.Background()
	cmd := Command(ctx, "/nonexistent-bin-123456", "", nil, nil, "secret-argument")
	res, err := Execute(ctx, cmd, io.Discard, io.Discard, true)
	if err == nil || res.ExitCode != -1 || !strings.Contains(res.Failure.Message("git", res.ExitCode), "could not be started") {
		t.Fatalf("result %#v, err %v", res, err)
	}
	if strings.Contains(res.Failure.Message("git", res.ExitCode), "secret") {
		t.Fatal("argument leaked")
	}
}

func TestTailBufferRetainsSuffixAndCopies(t *testing.T) {
	b := NewTailBuffer(5)
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defg"))
	if string(b.Bytes()) != "cdefg" {
		t.Fatalf("first tail %q", b.Bytes())
	}
	_, _ = b.Write([]byte("123456"))
	if string(b.Bytes()) != "23456" {
		t.Fatalf("oversized tail %q", b.Bytes())
	}
	clone := b.Bytes()
	clone[0] = '!'
	if string(b.Bytes()) != "23456" {
		t.Fatal("tail leaked internal bytes")
	}
}
