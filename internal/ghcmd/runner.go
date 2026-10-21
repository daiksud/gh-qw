package ghcmd

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/daiksud/gh-qw/internal/procio"
)

type commandContextFunc = procio.CommandContextFunc

// Runner executes gh through its own native authentication, using GH_PATH or
// PATH unless an executable is explicitly set. Child stdin is always null.
type Runner struct {
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer
	Env        []string

	commandContext commandContextFunc
}

func NewRunner() *Runner { return &Runner{Stdout: os.Stdout, Stderr: os.Stderr} }

// CommandError never formats command arguments, environment values, or stderr.
type CommandError struct {
	ExitCode int
	procio.Failure
}

func (e *CommandError) Error() string { return e.Failure.Message("gh", e.ExitCode) }

func CommandExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode < 0 {
		return 0, false
	}
	return commandErr.ExitCode, true
}

func (r *Runner) runDir(ctx context.Context, dir string, args ...string) error {
	cmd, err := r.command(ctx, dir, args...)
	if err != nil {
		return &CommandError{ExitCode: -1, Failure: procio.NewFailure(ctx, err, nil)}
	}
	result, err := procio.Execute(ctx, cmd, r.stdout(), r.stderr(), false)
	if err != nil {
		return &CommandError{ExitCode: result.ExitCode, Failure: result.Failure}
	}
	return nil
}

func (r *Runner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return r.outputDir(ctx, "", args...)
}

func (r *Runner) outputDir(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd, err := r.command(ctx, dir, args...)
	if err != nil {
		return nil, &CommandError{ExitCode: -1, Failure: procio.NewFailure(ctx, err, nil)}
	}
	result, err := procio.Execute(ctx, cmd, r.stdout(), r.stderr(), true)
	if err != nil {
		return result.Stdout, &CommandError{ExitCode: result.ExitCode, Failure: result.Failure}
	}
	return result.Stdout, nil
}

func (r *Runner) command(ctx context.Context, dir string, args ...string) (*exec.Cmd, error) {
	var executable string
	var factory commandContextFunc
	var env []string
	if r != nil {
		executable = r.Executable
		factory = r.commandContext
		env = r.Env
	}
	if executable == "" {
		var err error
		executable, err = ResolveExecutable(os.LookupEnv, exec.LookPath)
		if err != nil {
			return nil, err
		}
	}
	return procio.Command(ctx, executable, dir, env, factory, args...), nil
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
