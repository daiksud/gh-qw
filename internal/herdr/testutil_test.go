package herdr

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

const helperProcessEnv = "GO_WANT_HERDR_HELPER"

type recordedCommand struct {
	executable string
	args       []string
	cmd        *exec.Cmd
}

type commandRecorder struct {
	commands []recordedCommand
}

// newHelperRunner returns a Runner whose commandContext seam re-executes
// this test binary restricted to TestHerdrCommandHelperProcess, which then
// dispatches on mode. This lets tests exercise Runner's real stdout/stderr
// plumbing and exit-status handling without depending on a real herdr
// executable or a running Herdr server.
func newHelperRunner(t *testing.T, mode string) (*Runner, *commandRecorder) {
	t.Helper()

	recorder := &commandRecorder{}
	runner := &Runner{
		Executable: "fake-herdr",
		commandContext: func(ctx context.Context, executable string, args ...string) *exec.Cmd {
			cmd := exec.CommandContext(
				ctx,
				os.Args[0],
				"-test.run=^TestHerdrCommandHelperProcess$",
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

// TestHerdrCommandHelperProcess is not a real test; it is re-executed by
// newHelperRunner's commandContext seam as a fake herdr process. Each mode
// mirrors one observed real-herdr response shape (see internal/herdr's own
// exploration notes): a successful JSON envelope on stdout with exit 0, an
// operational-error JSON envelope on stderr with exit 1, or a plain-text
// usage diagnostic on stderr with exit 2.
func TestHerdrCommandHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}

	mode := helperMode(os.Args)
	switch mode {
	case "workspace-create":
		_, _ = os.Stdout.WriteString(
			`{"id":"cli:workspace:create","result":{"type":"workspace_created",` +
				`"workspace":{"workspace_id":"w1X","label":"probe"}}}`,
		)
		os.Exit(0)
	case "workspace-create-empty-id":
		_, _ = os.Stdout.WriteString(
			`{"id":"cli:workspace:create","result":{"type":"workspace_created",` +
				`"workspace":{"workspace_id":""}}}`,
		)
		os.Exit(0)
	case "worktree-list":
		_, _ = os.Stdout.WriteString(
			`{"id":"cli:worktree:list","result":{"type":"worktree_list","worktrees":[` +
				`{"path":"/repo","branch":"main"},` +
				`{"path":"/repo/../repo/worktrees/feature","open_workspace_id":"w2"}` +
				`]}}`,
		)
		os.Exit(0)
	case "ok":
		_, _ = os.Stdout.WriteString(`{"id":"cli:workspace:close","result":{"type":"ok"}}`)
		os.Exit(0)
	case "invalid-json":
		_, _ = os.Stdout.WriteString("not json")
		os.Exit(0)
	case "error-1":
		_, _ = os.Stderr.WriteString(
			`{"error":{"code":"workspace_not_found","message":"workspace w9 not found"},` +
				`"id":"cli:workspace:close"}`,
		)
		os.Exit(1)
	case "usage-error-2":
		_, _ = os.Stderr.WriteString("usage: herdr workspace close <workspace_id>")
		os.Exit(2)
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
