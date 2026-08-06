package procio

import (
	"io"
	"os"
)

// FileProvider is implemented by writers that can expose the *os.File they
// ultimately write to, so a caller composing streams (for example, a
// mutex-guarded writer wrapping the process's own standard error) can still
// be recognized as safe to pass through to a subprocess. PassthroughFile
// returns nil for a value that has no such file, deferring to the
// pipe-based path.
type FileProvider interface {
	PassthroughFile() *os.File
}

// PassthroughFile returns the *os.File behind w when the caller may safely
// hand w to a child process as an inherited standard stream (see
// os/exec.Cmd.Stdout and Cmd.Stderr), or nil when no such file is
// available.
//
// os/exec only connects a child's standard stream directly to the calling
// process's own file descriptor when Cmd.Stdout or Cmd.Stderr is exactly an
// *os.File; anything else — a wrapped *os.File, a bytes.Buffer,
// io.MultiWriter, io.Discard, and so on — makes exec.Cmd open an OS pipe and
// copy through a background goroutine instead. A subprocess connected
// through a pipe can never detect a terminal on that stream, so Git and gh
// suppress interactive progress rendering and color even when the calling
// process's own terminal would otherwise show them.
//
// PassthroughFile lets a caller detect the *os.File case explicitly —
// either directly, or one level through FileProvider for a writer that
// wraps one, such as a mutex guarding concurrent writes to the real stream
// — and inherit it, restoring terminal detection in the child. Anything
// else, including every test double and io.Discard, safely falls back to
// nil so callers keep using the existing pipe-based path.
func PassthroughFile(w io.Writer) *os.File {
	switch value := w.(type) {
	case nil:
		return nil
	case *os.File:
		return value
	case FileProvider:
		return value.PassthroughFile()
	default:
		return nil
	}
}
