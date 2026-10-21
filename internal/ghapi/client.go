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

const githubHost = "github.com"

// Client provides the GitHub API operations used by gh-qw through gh api.
type Client struct {
	output func(context.Context, ...string) ([]byte, error)
}

// Identity is an authenticated GitHub account and its selected host.
type Identity struct {
	Host  string
	Login string
}

// IdentityError means a repository owner could not be completed from gh
// authentication. Callers should treat this as a usage error.
type IdentityError struct {
	err error
}

func (e *IdentityError) Error() string {
	return e.err.Error()
}

// Unwrap preserves the gh process failure and context cancellation, if any.
func (e *IdentityError) Unwrap() error {
	return e.err
}

// NewClient returns a Client backed by the external gh executable. API response
// output is captured; gh diagnostics go to stderr. A nil writer uses os.Stderr.
// The executable is resolved only when an API operation runs.
func NewClient(stderr io.Writer) *Client {
	return &Client{output: (&ghcmd.Runner{Stderr: stderr}).Output}
}

// ResolveIdentity returns the authenticated login on GH_HOST or github.com when
// completing a repository name that has no owner. gh owns authentication for the
// selected host; stored accounts do not change gh-qw's shorthand host.
func (c *Client) ResolveIdentity(ctx context.Context) (Identity, error) {
	host := strings.ToLower(strings.TrimSpace(os.Getenv("GH_HOST")))
	if host == "" {
		host = githubHost
	}

	output, err := c.output(ctx, "api", "--hostname", host, "user", "--jq", ".login // empty")
	if err != nil {
		return Identity{}, &IdentityError{err: fmt.Errorf(
			"cannot resolve authenticated GitHub user for %q: query current user: %w; specify <owner>/<repo>",
			host, err,
		)}
	}

	login := strings.TrimSpace(string(output))
	if login == "" {
		return Identity{}, &IdentityError{err: fmt.Errorf(
			"cannot resolve authenticated GitHub user for %q: API response did not include a login; specify <owner>/<repo>",
			host,
		)}
	}

	return Identity{Host: host, Login: login}, nil
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
