package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
)

// repositoryUsageError is the shared classification for invalid command
// operands. Keeping the original error in the chain preserves domain errors.
type repositoryUsageError struct {
	err     error
	message string
}

func (e *repositoryUsageError) Error() string {
	if e == nil {
		return repospec.ErrUsage.Error()
	}
	if e.message != "" {
		return e.message
	}
	if e.err != nil {
		return e.err.Error()
	}
	return repospec.ErrUsage.Error()
}

func (e *repositoryUsageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (*repositoryUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

// commandWriteAll writes the full output or reports the underlying writer's
// error, including short writes with no error.
func commandWriteAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// commandWriteWarnings preserves the per-warning diagnostics, but never drops
// an I/O failure. Callers may attach operation context.
func commandWriteWarnings(writer io.Writer, warnings []local.Warning) error {
	if len(warnings) == 0 {
		return nil
	}
	var output bytes.Buffer
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(&output, "gh-qw: warning: %v\n", warning)
	}
	return commandWriteAll(writer, output.Bytes())
}

func commandStdin(reader io.Reader) io.Reader {
	if reader == nil {
		return os.Stdin
	}
	return reader
}

func commandStdout(writer io.Writer) io.Writer {
	if writer == nil {
		return os.Stdout
	}
	return writer
}

func commandStderr(writer io.Writer) io.Writer {
	if writer == nil {
		return os.Stderr
	}
	return writer
}
