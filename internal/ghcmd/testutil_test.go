package ghcmd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

const helperProcessEnv = "GO_WANT_GHCMD_HELPER"

type recordedCommand struct {
	executable string
	args       []string
	cmd        *exec.Cmd
}

type commandRecorder struct {
	commands []recordedCommand
}

func newHelperRunner(t *testing.T, mode string) (*Runner, *commandRecorder) {
	t.Helper()

	recorder := &commandRecorder{}
	runner := &Runner{
		Executable: "fake-gh",
		Env:        append(os.Environ(), helperProcessEnv+"=1"),
		commandContext: func(ctx context.Context, executable string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(
				ctx,
				os.Args[0],
				"-test.run=^TestGhCommandHelperProcess$",
				"--",
				mode,
			)
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

func TestGhCommandHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}

	mode := helperMode(os.Args)
	switch mode {
	case "success":
		os.Exit(0)
	case "stream":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(90)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(91)
		}
		_, _ = os.Stdout.Write([]byte(string(input) + "|" + os.Getenv("GHCMD_TEST_VALUE") + "|" + cwd))
		_, _ = os.Stderr.Write([]byte("streamed-stderr"))
		os.Exit(0)
	case "output-fail":
		_, _ = os.Stdout.Write([]byte("captured-output"))
		_, _ = os.Stderr.Write([]byte("stderr-super-secret"))
		os.Exit(23)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "exit-1":
		os.Exit(1)
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
