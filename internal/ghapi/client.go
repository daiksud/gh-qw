package ghapi

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

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

// DefaultBranch returns the repository's REST default_branch using gh's
// authentication for the explicit host, including for an empty repository.
func (c *Client) DefaultBranch(ctx context.Context, host, owner, repo string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	identity := strings.Trim(strings.Join([]string{host, owner, repo}, "/"), "/")

	if host == "" || owner == "" || repo == "" {
		return "", fmt.Errorf("lookup default branch for %q: host, owner, and repository are required", identity)
	}

	path := fmt.Sprintf("repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	output, err := c.output(ctx, "api", "--hostname", host, path, "--jq", ".default_branch // empty")
	if err != nil {
		return "", fmt.Errorf("lookup default branch for %q: GET repository: %w", identity, err)
	}

	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", fmt.Errorf("lookup default branch for %q: API response did not include default_branch", identity)
	}

	return branch, nil
}
