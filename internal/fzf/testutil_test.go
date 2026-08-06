package fzf

import (
	"context"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

const helperProcessEnv = "GO_WANT_FZF_HELPER"

type recordedCommand struct {
	executable string
	args       []string
	cmd        *exec.Cmd
}

type commandRecorder struct {
	commands []recordedCommand
}

// newHelperRunner returns a Runner whose commandContext seam re-executes
// this test binary restricted to TestFzfCommandHelperProcess, which then
// dispatches on mode. This lets tests exercise Select's real stdin/stdout/
// stderr plumbing and exit-status handling without depending on a real fzf
// executable.
func newHelperRunner(t *testing.T, mode string) (*Runner, *commandRecorder) {
	t.Helper()

	recorder := &commandRecorder{}
	runner := &Runner{
		Executable: "fake-fzf",
		commandContext: func(ctx context.Context, executable string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(
				ctx,
				os.Args[0],
				"-test.run=^TestFzfCommandHelperProcess$",
				"--",
				mode,
			)
			cmd.Env = append(os.Environ(), helperProcessEnv+"=1")
			recorder.commands = append(recorder.commands, recordedCommand{
				executable: executable,
				args:       slices.Clone(args),
				cmd:        cmd,
			})
			return cmd
		},
	}
	return runner, recorder
}

func (r *commandRecorder) last(t *testing.T) recordedCommand {
	t.Helper()
	if len(r.commands) == 0 {
		t.Fatal("no command was executed")
	}
	return r.commands[len(r.commands)-1]
}

func TestFzfCommandHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}

	mode := helperMode(os.Args)
	switch mode {
	case "echo":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(90)
		}
		_, _ = os.Stdout.Write(input)
		_, _ = os.Stderr.Write([]byte("streamed-stderr"))
		os.Exit(0)
	case "exit-1":
		_, _ = os.Stderr.Write([]byte("no-match-diagnostic"))
		os.Exit(1)
	case "exit-2":
		_, _ = os.Stderr.Write([]byte("fzf-error-diagnostic"))
		os.Exit(2)
	case "exit-130":
		_, _ = os.Stderr.Write([]byte("canceled-diagnostic"))
		os.Exit(130)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		os.Exit(92)
	}
}

func helperMode(args []string) string {
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
