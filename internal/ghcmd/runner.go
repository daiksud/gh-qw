package ghcmd

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

// Runner executes the GitHub CLI (gh) without involving a shell. Its zero
// value uses the gh executable, the process standard output and error
// streams, and the inherited environment. A gh subprocess never receives the
// calling process's standard input; its own standard input is always the
// null device, so it can never block a caller waiting on a terminal that
// never reaches EOF. Interactive confirmation (see internal/ghauth,
// internal/cmd rm/migrate) opens the controlling terminal directly instead
// of going through gh's standard input.
//
// When Stdout or Stderr resolves to an *os.File (see procio.PassthroughFile),
// gh inherits that file descriptor directly instead of writing through a
// pipe, so it can detect a terminal on that stream and render interactive
// progress and color exactly as it would running standalone.
type Runner struct {
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer

	// Env replaces the inherited environment when non-nil. Use os.Environ when
	// adding variables to the current environment.
	Env []string

	commandContext commandContextFunc
}

// NewRunner returns a Runner configured for an interactive CLI. Callers
// typically set Executable from ResolveExecutable.
func NewRunner() *Runner {
	return &Runner{
		Executable: "gh",
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}
}

// WithToken returns a copy of r whose subprocess environment authenticates
// as token instead of gh's own active account, by setting GH_TOKEN and
// removing any existing GH_TOKEN or GITHUB_TOKEN entry. The base
// environment is r.Env if already set, otherwise the current process
// environment, so unrelated inherited variables (PATH, HOME, and so on) are
// preserved. An empty token returns r unchanged, deferring to gh's own
// ambient authentication.
func (r Runner) WithToken(token string) Runner {
	if token == "" {
		return r
	}

	base := r.Env
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GH_TOKEN" || key == "GITHUB_TOKEN" {
			continue
		}
		env = append(env, entry)
	}
	r.Env = append(env, "GH_TOKEN="+token)
	return r
}

// CommandError describes a gh process failure. Error deliberately omits
// command arguments, environment values, and stderr so tokens embedded in
// URLs or gh diagnostics are not accidentally included in higher-level
// errors. gh's stderr is still streamed live to Runner.Stderr while the
// command executes.
type CommandError struct {
	ExitCode int

	err    error
	stderr []byte
}

// Error implements error.
func (e *CommandError) Error() string {
	switch {
	case errors.Is(e.err, context.Canceled):
		return "gh command canceled"
	case errors.Is(e.err, context.DeadlineExceeded):
		return "gh command timed out"
	case e.ExitCode >= 0:
		return fmt.Sprintf("gh command failed with exit code %d", e.ExitCode)
	default:
		return "gh command could not be started"
	}
}

// Unwrap returns the underlying process and context errors.
func (e *CommandError) Unwrap() error {
	return e.err
}

// StderrOutput returns a copy of the retained tail of gh's stderr. gh's
// stderr is also streamed to Runner.Stderr while the command executes.
// StderrOutput is empty when Runner.Stderr was passed through directly to
// gh as an inherited file descriptor (see procio.PassthroughFile): gh's
// output already reached that file directly, so there is nothing left to
// retain or replay.
func (e *CommandError) StderrOutput() []byte {
	return bytes.Clone(e.stderr)
}

// CommandExitCode extracts a gh exit status from err.
func CommandExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode < 0 {
		return 0, false
	}
	return commandErr.ExitCode, true
}

// runDir executes gh in dir with the configured standard streams. An empty
// dir uses the current working directory. When Stdout or Stderr resolves to
// an *os.File (see procio.PassthroughFile), gh inherits that file
// descriptor directly instead of writing through a pipe, restoring
// terminal detection for interactive progress and color. A passed-through
// Stderr is not tailed, since gh's output already reached the caller's file
// directly; see CommandError.StderrOutput.
func (r *Runner) runDir(ctx context.Context, dir string, args ...string) error {
	cmd := r.command(ctx, dir, args...)
	tail := newTailBuffer(stderrTailLimit)

	if file := procio.PassthroughFile(r.stdout()); file != nil {
		cmd.Stdout = file
	} else {
		cmd.Stdout = r.stdout()
	}
	if file := procio.PassthroughFile(r.stderr()); file != nil {
		cmd.Stderr = file
	} else {
		cmd.Stderr = io.MultiWriter(r.stderr(), tail)
	}

	if err := cmd.Run(); err != nil {
		return commandError(ctx, err, tail.Bytes())
	}
	return nil
}

// Output executes gh in the current directory and captures stdout, using
// the configured standard streams for stdin and stderr. This is used for
// host-scoped gh subcommands (such as `gh auth status`/`gh auth token` in
// internal/ghauth) that are not tied to a repository's working directory.
func (r *Runner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return r.outputDir(ctx, "", args...)
}

// outputDir executes gh in dir and captures stdout. Stderr continues to
// stream to the configured stderr writer and is retained in a bounded tail on
// failure, unless Stderr resolves to an *os.File (see procio.PassthroughFile),
// in which case gh inherits that file descriptor directly and the tail
// stays empty; see CommandError.StderrOutput. An empty dir uses the current
// working directory.
func (r *Runner) outputDir(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := r.command(ctx, dir, args...)
	var stdout bytes.Buffer
	tail := newTailBuffer(stderrTailLimit)
	cmd.Stdout = &stdout
	if file := procio.PassthroughFile(r.stderr()); file != nil {
		cmd.Stderr = file
	} else {
		cmd.Stderr = io.MultiWriter(r.stderr(), tail)
	}

	if err := cmd.Run(); err != nil {
		return bytes.Clone(stdout.Bytes()), commandError(ctx, err, tail.Bytes())
	}
	return bytes.Clone(stdout.Bytes()), nil
}

func (r *Runner) command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	executable := "gh"
	factory := commandContextFunc(exec.CommandContext)
	if r != nil {
		if r.Executable != "" {
			executable = r.Executable
		}
		if r.commandContext != nil {
			factory = r.commandContext
		}
	}

	cmd := factory(ctx, executable, args...)
	cmd.Dir = dir
	// cmd.Stdin is deliberately left nil so gh always reads from the null
	// device: a *os.File is the only Cmd.Stdin value os/exec connects
	// directly, so anything else (including os.Stdin itself, wrapped or not)
	// makes exec.Cmd start a background copy goroutine that Wait blocks on
	// until it observes EOF. A terminal's standard input never reaches EOF
	// on its own, so that goroutine — and Wait with it — would otherwise
	// hang until a human pressed Enter, even though gh had already exited.
	if r != nil && r.Env != nil {
		cmd.Env = append([]string(nil), r.Env...)
	}
	return cmd
}

func (r *Runner) stdout() io.Writer {
	if r != nil && r.Stdout != nil {
		return r.Stdout
	}
	return os.Stdout
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
