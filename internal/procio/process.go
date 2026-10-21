package procio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// StderrTailLimit is the maximum diagnostic suffix retained from a pipe.
const StderrTailLimit = 64 * 1024

// CommandContextFunc constructs a subprocess without invoking a shell.
type CommandContextFunc func(context.Context, string, ...string) *exec.Cmd

// Command creates a child with a null-device stdin. A non-nil env replaces the
// inherited environment; an empty dir uses the caller's working directory.
func Command(ctx context.Context, executable, dir string, env []string, factory CommandContextFunc, args ...string) *exec.Cmd {
	if factory == nil {
		factory = exec.CommandContext
	}
	cmd := factory(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	if env != nil {
		cmd.Env = append([]string(nil), env...)
	}
	return cmd
}

// Failure stores diagnostic data without exposing child arguments, environment,
// or stderr in its formatted message. The adapters supply their command name.
type Failure struct {
	cause  error
	stderr []byte
}

// NewFailure retains the process error, context cause, and bounded stderr tail.
func NewFailure(ctx context.Context, err error, stderr []byte) Failure {
	cause := err
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
		cause = errors.Join(err, ctxErr)
	}
	return Failure{cause: cause, stderr: bytes.Clone(stderr)}
}

// Unwrap exposes the original error and any context cancellation or timeout.
func (f Failure) Unwrap() error { return f.cause }

// StderrOutput returns an independent copy of retained pipe-based stderr.
func (f Failure) StderrOutput() []byte { return bytes.Clone(f.stderr) }

// Message formats a sanitized adapter-specific process failure.
func (f Failure) Message(program string, exitCode int) string {
	switch {
	case errors.Is(f.cause, context.Canceled):
		return program + " command canceled"
	case errors.Is(f.cause, context.DeadlineExceeded):
		return program + " command timed out"
	case exitCode >= 0:
		return fmt.Sprintf("%s command failed with exit code %d", program, exitCode)
	default:
		return program + " command could not be started"
	}
}

// Result is the captured process outcome, including any partial stdout.
type Result struct {
	Stdout   []byte
	ExitCode int
	Failure  Failure
}

// Execute streams child output, retaining only a bounded stderr tail when it
// goes through a pipe. Files from PassthroughFile are inherited directly, so
// the child retains terminal detection without duplicated output or replay.
// With captureStdout, stdout is collected rather than sent to the caller.
func Execute(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.Writer, captureStdout bool) (Result, error) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	var captured bytes.Buffer
	if captureStdout {
		cmd.Stdout = &captured
	} else if file := PassthroughFile(stdout); file != nil {
		cmd.Stdout = file
	} else {
		cmd.Stdout = stdout
	}

	tail := NewTailBuffer(StderrTailLimit)
	if file := PassthroughFile(stderr); file != nil {
		cmd.Stderr = file
	} else {
		cmd.Stderr = io.MultiWriter(stderr, tail)
	}

	err := cmd.Run()
	result := Result{Stdout: bytes.Clone(captured.Bytes()), ExitCode: -1}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		result.Failure = NewFailure(ctx, err, tail.Bytes())
	}
	return result, err
}

// TailBuffer discards all but the last limit bytes written to it.
type TailBuffer struct {
	limit int
	data  []byte
}

// NewTailBuffer creates a bounded, write-only stderr tail.
func NewTailBuffer(limit int) *TailBuffer { return &TailBuffer{limit: limit} }

func (b *TailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.limit <= 0 {
		return written, nil
	}
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return written, nil
	}
	b.data = append(b.data, p...)
	if overflow := len(b.data) - b.limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:b.limit]
	}
	return written, nil
}

// Bytes returns a copy of the retained suffix.
func (b *TailBuffer) Bytes() []byte { return bytes.Clone(b.data) }
