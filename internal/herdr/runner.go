package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/daiksud/gh-qw/internal/procio"
)

const stderrTailLimit = 64 * 1024

// EnvironmentVariable is the environment variable Herdr sets to "1" inside
// every pane it manages. InSession checks it and gh-qw's own command layer
// uses it to decide whether an explicit --herdr outside of Herdr is a
// usage error (see internal/cmd).
const EnvironmentVariable = "HERDR_ENV"

type commandContextFunc func(context.Context, string, ...string) *exec.Cmd

// Runner runs the external herdr executable so gh-qw commands can
// integrate with a Herdr-managed terminal session: creating and focusing a
// workspace for a new linked worktree, resolving which workspace (if any)
// has one open for an existing worktree, and closing a workspace again on
// removal. Its zero value resolves "herdr" from PATH on every call and
// writes herdr's own diagnostics to the process standard error stream.
//
// herdr's machine-readable JSON response is always read from its own
// standard output and parsed internally by Runner; it is never written to
// gh-qw's own standard output, which stays reserved for gh-qw's own result
// data (an absolute worktree path, for `gh qw worktree add`). herdr's own
// standard error carries only diagnostics and, on failure, a JSON error
// envelope; Runner streams it to Stderr the same way internal/fzf and
// internal/ghcmd stream their own external tool's stderr.
//
// When Stderr resolves to an *os.File (see procio.PassthroughFile), herdr
// inherits that file descriptor directly instead of writing through a
// pipe, so it can detect a terminal exactly as it would running
// standalone.
type Runner struct {
	// Executable overrides PATH resolution; tests set this to a fake
	// executable name recognized by an injected commandContext. Production
	// callers leave it empty so every call resolves "herdr" from PATH.
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

// InSession reports whether the current process is running inside a
// Herdr-managed pane, per Herdr's own HERDR_ENV=1 contract (see the herdr
// agent skill). lookupEnv is typically os.LookupEnv; tests inject a fake so
// they do not depend on the calling process's own environment. A nil
// lookupEnv uses os.LookupEnv.
func InSession(lookupEnv func(string) (string, bool)) bool {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	value, ok := lookupEnv(EnvironmentVariable)
	return ok && value == "1"
}

// Workspace describes a workspace created by CreateWorkspace.
type Workspace struct {
	// ID is the opaque workspace identifier herdr assigned (for example
	// "w1"), suitable for a later CloseWorkspace call.
	ID string
}

// CreateOptions configures CreateWorkspace.
type CreateOptions struct {
	// Cwd is the working directory of the workspace's initial pane. It is
	// typically the new linked worktree's own absolute path. Empty defers
	// to herdr's own default (its own current working directory).
	Cwd string
	// Label is the workspace's display label. Empty defers to herdr's own
	// default (its cwd's base name).
	Label string
	// Focus selects between herdr's own --focus and --no-focus flags. The
	// zero value (false) does not focus the created workspace, matching
	// Go's usual zero-value-is-the-safe-default convention; callers that
	// want the person to notice the new workspace immediately, such as
	// `gh qw worktree add --herdr`, must set it explicitly.
	Focus bool
}

// CreateWorkspace runs `herdr workspace create` with the given options and
// reports the new workspace's ID.
func (r *Runner) CreateWorkspace(ctx context.Context, options CreateOptions) (Workspace, error) {
	args := []string{"workspace", "create"}
	if options.Cwd != "" {
		args = append(args, "--cwd", options.Cwd)
	}
	if options.Label != "" {
		args = append(args, "--label", options.Label)
	}
	if options.Focus {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}

	var response struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
		} `json:"result"`
	}
	if err := r.runJSON(ctx, &response, args...); err != nil {
		return Workspace{}, err
	}
	if response.Result.Workspace.WorkspaceID == "" {
		return Workspace{}, errors.New("herdr workspace create: response did not include a workspace ID")
	}
	return Workspace{ID: response.Result.Workspace.WorkspaceID}, nil
}

// FindWorkspaceForPath runs `herdr worktree list` against repoPath (the
// main repository's own absolute path, which every worktree Git registers
// for it, linked or not, is listed relative to) and reports the workspace
// ID herdr currently has open for worktreePath, if any.
//
// The second return value is false, with an empty ID and a nil error, when
// herdr has no workspace open for that path. This is an ordinary, expected
// outcome rather than a failure: a linked worktree need not have any
// workspace open at all, for example when it was created without --herdr
// or its workspace was already closed by other means.
func (r *Runner) FindWorkspaceForPath(
	ctx context.Context,
	repoPath string,
	worktreePath string,
) (string, bool, error) {
	var response struct {
		Result struct {
			Worktrees []struct {
				Path            string `json:"path"`
				OpenWorkspaceID string `json:"open_workspace_id"`
			} `json:"worktrees"`
		} `json:"result"`
	}
	if err := r.runJSON(ctx, &response, "worktree", "list", "--cwd", repoPath); err != nil {
		return "", false, err
	}

	for _, worktree := range response.Result.Worktrees {
		if worktree.OpenWorkspaceID == "" {
			continue
		}
		if samePath(worktree.Path, worktreePath) {
			return worktree.OpenWorkspaceID, true, nil
		}
	}
	return "", false, nil
}

// CloseWorkspace runs `herdr workspace close` for workspaceID.
func (r *Runner) CloseWorkspace(ctx context.Context, workspaceID string) error {
	_, err := r.output(ctx, "workspace", "close", workspaceID)
	return err
}

// CommandError describes a herdr failure: it could not be resolved or
// started, or it exited with a non-zero status. ExitCode is -1 when herdr
// never started, including a failed PATH resolution.
type CommandError struct {
	ExitCode int

	err    error
	stderr []byte
}

// Error implements error. When herdr's own stderr is its documented JSON
// error envelope (see errorMessage), the message it carries is included so
// a caller does not have to parse JSON itself to report something useful;
// a plain-text usage error (herdr's own exit status 2) falls back to the
// bare exit status.
func (e *CommandError) Error() string {
	switch {
	case errors.Is(e.err, context.Canceled):
		return "herdr command canceled"
	case errors.Is(e.err, context.DeadlineExceeded):
		return "herdr command timed out"
	case e.ExitCode >= 0:
		if message := errorMessage(e.stderr); message != "" {
			return fmt.Sprintf("herdr exited with status %d: %s", e.ExitCode, message)
		}
		return fmt.Sprintf("herdr exited with status %d", e.ExitCode)
	case e.err != nil:
		return e.err.Error()
	default:
		return "herdr command could not be started"
	}
}

// Unwrap returns the underlying process, resolution, and context errors.
func (e *CommandError) Unwrap() error {
	return e.err
}

// StderrOutput returns a copy of the retained tail of herdr's stderr.
// herdr's stderr is also streamed to Runner.Stderr while the command
// executes. StderrOutput is empty when Runner.Stderr was passed through
// directly to herdr as an inherited file descriptor (see
// procio.PassthroughFile): herdr's output already reached that file
// directly, so there is nothing left to retain or replay.
func (e *CommandError) StderrOutput() []byte {
	return bytes.Clone(e.stderr)
}

// CommandExitCode extracts a herdr exit status from err. It reports false
// when herdr never started, including a failed PATH resolution.
func CommandExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode < 0 {
		return 0, false
	}
	return commandErr.ExitCode, true
}

// runJSON runs herdr with args and decodes its captured standard output as
// JSON into into.
func (r *Runner) runJSON(ctx context.Context, into any, args ...string) error {
	stdout, err := r.output(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stdout, into); err != nil {
		return fmt.Errorf("parse herdr response: %w", err)
	}
	return nil
}

// output runs herdr with args and returns its captured standard output.
// herdr's own stdout is always fully captured here rather than streamed
// anywhere: unlike fzf's interactive UI or gh's own progress output, it is
// pure machine-readable JSON that gh-qw parses internally and never
// surfaces on its own standard output.
func (r *Runner) output(ctx context.Context, args ...string) ([]byte, error) {
	executable, err := r.resolveExecutable()
	if err != nil {
		return nil, &CommandError{ExitCode: -1, err: err}
	}

	factory := commandContextFunc(exec.CommandContext)
	if r != nil && r.commandContext != nil {
		factory = r.commandContext
	}
	cmd := factory(ctx, executable, args...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	tail := newTailBuffer(stderrTailLimit)
	if file := procio.PassthroughFile(r.stderr()); file != nil {
		cmd.Stderr = file
	} else {
		cmd.Stderr = io.MultiWriter(r.stderr(), tail)
	}

	if runErr := cmd.Run(); runErr != nil {
		return nil, commandError(ctx, runErr, tail.Bytes())
	}
	return bytes.Clone(stdout.Bytes()), nil
}

func (r *Runner) resolveExecutable() (string, error) {
	if r != nil && r.Executable != "" {
		return r.Executable, nil
	}
	lookPath := exec.LookPath
	if r != nil && r.lookPath != nil {
		lookPath = r.lookPath
	}
	path, err := lookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("resolve herdr executable: %w", err)
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

// errorMessage extracts herdr's own JSON error envelope's message field
// from a stderr tail, when present, so CommandError.Error can surface it
// without every caller having to parse JSON itself. It returns "" when
// stderr is not a parseable herdr error envelope, such as herdr's own
// plain-text usage error for a malformed invocation.
func errorMessage(stderr []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stderr), &envelope); err != nil {
		return ""
	}
	return envelope.Error.Message
}

// samePath reports whether a and b name the same filesystem path after
// cleaning, matching case-insensitively on Windows the same way gh-qw's
// own worktree registration comparisons do elsewhere (see
// internal/cmd/worktree_add.go).
func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(a, b)
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
