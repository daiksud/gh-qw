package ghapi

import (
	"context"
	"io"
	"os"

	"github.com/daiksud/gh-qw/internal/ghcmd"
)

// Client provides the GitHub API operations used by gh-qw through gh api.
type Client struct {
	output func(context.Context, ...string) ([]byte, error)
}

// NewClient returns a Client backed by the external gh executable. API response
// output is captured; gh diagnostics go to stderr. A nil writer uses os.Stderr.
// The executable is resolved only when an API operation runs.
func NewClient(stderr io.Writer) *Client {
	return &Client{output: func(ctx context.Context, args ...string) ([]byte, error) {
		// Captured HTTP headers and JSON must remain machine-readable even when
		// the caller forces terminal formatting. Authentication stays with gh.
		runner := &ghcmd.Runner{
			Stderr: stderr,
			Env:    append(os.Environ(), "NO_COLOR=1", "CLICOLOR_FORCE=0", "GH_FORCE_TTY="),
		}
		return runner.Output(ctx, args...)
	}}
}
