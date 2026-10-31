package fzfcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/daiksud/gh-qw/internal/procio"
)

var (
	// ErrCanceled reports that the user interrupted fzf with Esc or Ctrl-C.
	ErrCanceled = errors.New("fzf selection canceled")
	// ErrNoMatch reports that fzf exited without a matching candidate.
	ErrNoMatch = errors.New("fzf selection has no match")
)

// Selector chooses one offered record without interpreting its display text.
type Selector struct {
	Stderr io.Writer

	commandContext procio.CommandContextFunc
}

// CommandError preserves process and context failures without exposing candidate
// paths, environment values, or stderr in its formatted message.
type CommandError struct {
	ExitCode int
	procio.Failure
}

func (err *CommandError) Error() string {
	if errors.Is(err.Failure.Unwrap(), exec.ErrNotFound) {
		return "fzf is required for interactive removal; install fzf or provide an explicit removal target"
	}
	return err.Failure.Message("fzf", err.ExitCode)
}

// Select runs fzf with a fixed single-selection protocol. User defaults are
// excluded because they can auto-select targets or change the output format.
func (selector *Selector) Select(ctx context.Context, candidates []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", &CommandError{ExitCode: -1, Failure: procio.NewFailure(ctx, err, nil)}
	}
	if len(candidates) == 0 {
		return "", ErrNoMatch
	}
	allowed := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || strings.ContainsRune(candidate, '\x00') {
			return "", errors.New("fzf candidates must be nonempty records without NUL bytes")
		}
		if _, exists := allowed[candidate]; exists {
			return "", errors.New("fzf candidates must be distinct")
		}
		allowed[candidate] = struct{}{}
	}

	var factory procio.CommandContextFunc
	var stderr io.Writer = os.Stderr
	if selector != nil {
		factory = selector.commandContext
		if selector.Stderr != nil {
			stderr = selector.Stderr
		}
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "FZF_DEFAULT_OPTS") || strings.EqualFold(name, "FZF_DEFAULT_OPTS_FILE") {
			continue
		}
		env = append(env, entry)
	}
	command := procio.Command(ctx, "fzf", "", env, factory,
		"--read0", "--print0", "--no-multi", "--no-select-1", "--no-exit-0",
		"--prompt=Remove> ",
		"--header=Repositories include all linked worktrees. Enter: select; Esc: cancel.",
	)
	command.Stdin = strings.NewReader(strings.Join(candidates, "\x00") + "\x00")
	result, err := procio.Execute(ctx, command, io.Discard, stderr, true)
	if err != nil {
		failure := &CommandError{ExitCode: result.ExitCode, Failure: result.Failure}
		if ctx.Err() == nil {
			switch result.ExitCode {
			case 1:
				return "", errors.Join(ErrNoMatch, failure)
			case 130:
				return "", errors.Join(ErrCanceled, failure)
			}
		}
		return "", failure
	}

	selected, complete := bytes.CutSuffix(result.Stdout, []byte{0})
	if !complete || len(selected) == 0 || bytes.IndexByte(selected, 0) >= 0 {
		return "", errors.New("fzf returned an invalid single-selection record")
	}
	selection := string(selected)
	if _, exists := allowed[selection]; !exists {
		return "", errors.New("fzf returned a selection that was not offered")
	}
	return selection, nil
}
