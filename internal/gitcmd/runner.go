package gitcmd

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/daiksud/gh-qw/internal/procio"
)

type commandContextFunc = procio.CommandContextFunc

// Runner executes Git without a shell, inheriting only the configured process
// streams and environment. Child stdin always reads from the null device.
type Runner struct {
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer
	Env        []string

	commandContext commandContextFunc
}

func NewRunner() *Runner {
	return &Runner{Executable: "git", Stdout: os.Stdout, Stderr: os.Stderr}
}

// CommandError reports a sanitized Git process failure without embedding
// command arguments, environment values, or stderr into its message.
type CommandError struct {
	ExitCode int
	procio.Failure
}

func (e *CommandError) Error() string { return e.Failure.Message("git", e.ExitCode) }

func CommandExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode < 0 {
		return 0, false
	}
	return commandErr.ExitCode, true
}

func (r *Runner) Run(ctx context.Context, args ...string) error { return r.RunDir(ctx, "", args...) }

func (r *Runner) RunDir(ctx context.Context, dir string, args ...string) error {
	result, err := procio.Execute(ctx, r.command(ctx, dir, args...), r.stdout(), r.stderr(), false)
	if err != nil {
		return &CommandError{ExitCode: result.ExitCode, Failure: result.Failure}
	}
	return nil
}

func (r *Runner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return r.OutputDir(ctx, "", args...)
}

func (r *Runner) OutputDir(ctx context.Context, dir string, args ...string) ([]byte, error) {
	result, err := procio.Execute(ctx, r.command(ctx, dir, args...), r.stdout(), r.stderr(), true)
	if err != nil {
		return result.Stdout, &CommandError{ExitCode: result.ExitCode, Failure: result.Failure}
	}
	return result.Stdout, nil
}

func (r *Runner) command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	executable := "git"
	var factory commandContextFunc
	var env []string
	if r != nil {
		if r.Executable != "" {
			executable = r.Executable
		}
		factory = r.commandContext
		env = r.Env
	}
	return procio.Command(ctx, executable, dir, env, factory, args...)
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
