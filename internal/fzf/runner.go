package fzf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/daiksud/gh-qw/internal/procio"
)

const stderrTailLimit = 64 * 1024

type commandContextFunc func(context.Context, string, ...string) *exec.Cmd

// Runner runs the external fzf executable so a person can pick one line
// from a candidate list, such as for `gh qw list --fzf`. Its zero value
// resolves "fzf" from PATH on every Select call and writes fzf's own
// diagnostics to the process standard error stream.
//
// fzf reads its candidate list from standard input but renders its
// interactive UI and reads keystrokes directly against the controlling
// terminal, so Select redirecting standard input to supply candidates does
// not interfere with keyboard interaction; this mirrors ordinary shell
// usage such as `choice=$(printf '%s\n' "${items[@]}" | fzf)`. The selected
// line is captured from fzf's standard output.
//
// When Stderr resolves to an *os.File (see procio.PassthroughFile), fzf
// inherits that file descriptor directly instead of writing through a
// pipe, so it can detect a terminal and render any diagnostic of its own
// exactly as it would running standalone.
type Runner struct {
	// Executable overrides PATH resolution; tests set this to a fake
	// executable name recognized by an injected commandContext. Production
	// callers leave it empty so Select resolves "fzf" from PATH.
	Executable string
	Stderr     io.Writer

	commandContext commandContextFunc
	lookPath       func(string) (string, error)
}

// NewRunner returns a Runner configured for an interactive CLI.
func NewRunner() *Runner {
	return &Runner{
		Stderr: os.Stderr,
	}
}

// Options configures one Select call.
type Options struct {
	// Prompt sets fzf's input prompt (--prompt). Empty keeps fzf's own
	// built-in default prompt.
	Prompt string
}

// CommandError describes an fzf failure: it could not be resolved or
// started, or it exited with a non-zero status. ExitCode is -1 when fzf
// never started, including a failed PATH resolution. Otherwise it is fzf's
// own documented exit status: 1 when nothing matched the typed query, 2
// for an fzf-reported error, or 130 when the person canceled with Esc or
// Ctrl-C.
type CommandError struct {
	ExitCode int

	err    error
	stderr []byte
}

// Error implements error.
func (e *CommandError) Error() string {
	switch {
	case errors.Is(e.err, context.Canceled):
		return "fzf command canceled"
	case errors.Is(e.err, context.DeadlineExceeded):
		return "fzf command timed out"
	case e.ExitCode >= 0:
		return fmt.Sprintf("fzf exited with status %d", e.ExitCode)
	case e.err != nil:
		return e.err.Error()
	default:
		return "fzf command could not be started"
	}
}

// Unwrap returns the underlying process, resolution, and context errors.
func (e *CommandError) Unwrap() error {
	return e.err
}

// StderrOutput returns a copy of the retained tail of fzf's stderr. fzf's
// stderr is also streamed to Runner.Stderr while the command executes.
// StderrOutput is empty when Runner.Stderr was passed through directly to
// fzf as an inherited file descriptor (see procio.PassthroughFile): fzf's
// output already reached that file directly, so there is nothing left to
// retain or replay.
func (e *CommandError) StderrOutput() []byte {
	return bytes.Clone(e.stderr)
}

// CommandExitCode extracts an fzf exit status from err. It reports false
// when fzf never started, including a failed PATH resolution.
func CommandExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode < 0 {
		return 0, false
	}
	return commandErr.ExitCode, true
}

// IsCanceled reports whether err represents the person canceling fzf with
// Esc or Ctrl-C, which fzf reports as exit status 130.
func IsCanceled(err error) bool {
	code, ok := CommandExitCode(err)
	return ok && code == 130
}

// IsNoMatch reports whether err represents fzf finding no match for the
// currently typed query, which fzf reports as exit status 1.
func IsNoMatch(err error) bool {
	code, ok := CommandExitCode(err)
	return ok && code == 1
}

// Select runs fzf with items (one candidate per line, in the given order)
// as its input and returns the selected line with any trailing newline
// removed. Multiple selection is always disabled (--no-multi), so a
// successful return is always exactly one of items.
//
// Select returns ("", nil) without starting fzf when items is empty: fzf
// would have nothing meaningful to select from.
func (r *Runner) Select(ctx context.Context, items []string, options Options) (string, error) {
	if len(items) == 0 {
		return "", nil
	}

	executable, err := r.resolveExecutable()
	if err != nil {
		return "", &CommandError{ExitCode: -1, err: err}
	}

	args := []string{"--no-multi"}
	if options.Prompt != "" {
		args = append(args, "--prompt", options.Prompt)
	}

	factory := commandContextFunc(exec.CommandContext)
	if r != nil && r.commandContext != nil {
		factory = r.commandContext
	}
	cmd := factory(ctx, executable, args...)
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n") + "\n")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	tail := newTailBuffer(stderrTailLimit)
	if file := procio.PassthroughFile(r.stderr()); file != nil {
		cmd.Stderr = file
	} else {
		cmd.Stderr = io.MultiWriter(r.stderr(), tail)
	}

	if runErr := cmd.Run(); runErr != nil {
		return "", commandError(ctx, runErr, tail.Bytes())
	}
	return strings.TrimSuffix(stdout.String(), "\n"), nil
}

func (r *Runner) resolveExecutable() (string, error) {
	if r != nil && r.Executable != "" {
		return r.Executable, nil
	}
	lookPath := exec.LookPath
	if r != nil && r.lookPath != nil {
		lookPath = r.lookPath
	}
	path, err := lookPath("fzf")
	if err != nil {
		return "", fmt.Errorf("resolve fzf executable: %w", err)
	}
	return path, nil
}

func (r *Runner) stderr() io.Writer {
	if r != nil && r.Stderr != nil {
		return r.Stderr
	}
	return os.Stderr
}

func commandError(ctx context.Context, err error, stderr []byte) error {
	cause := err
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
		cause = errors.Join(err, ctxErr)
	}

	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}

	return &CommandError{
		ExitCode: exitCode,
		err:      cause,
		stderr:   bytes.Clone(stderr),
	}
}

type tailBuffer struct {
	limit int
	data  []byte
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
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

func (b *tailBuffer) Bytes() []byte {
	return bytes.Clone(b.data)
}
