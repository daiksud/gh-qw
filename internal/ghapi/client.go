package ghapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
)

const githubHost = "github.com"

type restClient interface {
	DoWithContext(context.Context, string, string, io.Reader, any) error
}

// Client provides the GitHub authentication and API operations used by gh-qw.
type Client struct {
	hostOverride func() string
	knownHosts   func() []string
	tokenForHost func(string) (string, string)
	newREST      func(string, string) (restClient, error)
}

// Identity is an authenticated GitHub account and its host.
type Identity struct {
	Host  string
	Login string
}

// IdentityError means a repository owner could not be completed from gh
// authentication. Callers should treat this as a usage error.
type IdentityError struct {
	message string
}

func (e *IdentityError) Error() string {
	return e.message
}

// NewClient returns a Client backed by go-gh authentication and REST clients.
func NewClient() *Client {
	return &Client{
		hostOverride: func() string {
			return os.Getenv("GH_HOST")
		},
		knownHosts:   auth.KnownHosts,
		tokenForHost: auth.TokenForHost,
		newREST: func(host, token string) (restClient, error) {
			return api.NewRESTClient(api.ClientOptions{
				Host:         host,
				AuthToken:    token,
				LogIgnoreEnv: true,
			})
		},
	}
}

// ResolveIdentity returns the authenticated login and host to use when
// completing a repository name that has no owner.
func (c *Client) ResolveIdentity(ctx context.Context) (Identity, error) {
	host, token, err := c.resolveHost()
	if err != nil {
		return Identity{}, err
	}

	client, err := c.newREST(host, token)
	if err != nil {
		return Identity{}, newIdentityError(
			"cannot resolve authenticated GitHub user for %q: create REST client: %s; specify <owner>/<repo>",
			host, safeError(err, token),
		)
	}

	var response struct {
		Login string `json:"login"`
	}
	if err := client.DoWithContext(ctx, http.MethodGet, "user", nil, &response); err != nil {
		return Identity{}, newIdentityError(
			"cannot resolve authenticated GitHub user for %q: query current user: %s; specify <owner>/<repo>",
			host, safeError(err, token),
		)
	}

	login := strings.TrimSpace(response.Login)
	if login == "" {
		return Identity{}, newIdentityError(
			"cannot resolve authenticated GitHub user for %q: API response did not include a login; specify <owner>/<repo>",
			host,
		)
	}

	return Identity{Host: host, Login: login}, nil
}

// DefaultBranch returns the default branch for a repository on an explicit
// GitHub host. When tokenOverride is non-empty, it is used to authenticate
// the request instead of the token gh has stored for host; this lets a
// caller target a specific gh-authenticated account for a repository whose
// owner differs from gh's active account (see internal/ghauth). When it is
// empty, the client uses gh's token for host.
func (c *Client) DefaultBranch(ctx context.Context, host, owner, repo, tokenOverride string) (string, error) {
	host = normalizeHost(host)
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	identity := repositoryIdentity(host, owner, repo)

	if host == "" || owner == "" || repo == "" {
		return "", fmt.Errorf("lookup default branch for %q: host, owner, and repository are required", identity)
	}

	token := tokenOverride
	if token == "" {
		token, _ = c.tokenForHost(host)
	}
	if token == "" {
		return "", fmt.Errorf("lookup default branch for %q: authentication token not found for host %q", identity, host)
	}

	client, err := c.newREST(host, token)
	if err != nil {
		return "", fmt.Errorf("lookup default branch for %q: create REST client: %s", identity, safeError(err, token))
	}

	var response struct {
		DefaultBranch string `json:"default_branch"`
	}
	path := fmt.Sprintf("repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	if err := client.DoWithContext(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", fmt.Errorf("lookup default branch for %q: GET repository: %s", identity, safeError(err, token))
	}

	branch := strings.TrimSpace(response.DefaultBranch)
	if branch == "" {
		return "", fmt.Errorf("lookup default branch for %q: API response did not include default_branch", identity)
	}

	return branch, nil
}

type authenticatedHost struct {
	host  string
	token string
}

func (c *Client) resolveHost() (string, string, error) {
	if override := normalizeHost(c.hostOverride()); override != "" {
		token, _ := c.tokenForHost(override)
		if token == "" {
			return "", "", newIdentityError(
				"cannot resolve authenticated GitHub user for %q: no authentication token found; run \"gh auth login --hostname %s\" or specify <owner>/<repo>",
				override, override,
			)
		}
		return override, token, nil
	}

	if token, _ := c.tokenForHost(githubHost); token != "" {
		return githubHost, token, nil
	}

	seen := map[string]struct{}{githubHost: {}}
	hosts := make([]authenticatedHost, 0)
	for _, candidate := range c.knownHosts() {
		host := normalizeHost(candidate)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}

		token, _ := c.tokenForHost(host)
		if token != "" {
			hosts = append(hosts, authenticatedHost{host: host, token: token})
		}
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].host < hosts[j].host
	})

	switch len(hosts) {
	case 0:
		return "", "", newIdentityError(
			"cannot resolve authenticated GitHub user: no authenticated host found; run \"gh auth login\" or specify <owner>/<repo>",
		)
	case 1:
		return hosts[0].host, hosts[0].token, nil
	default:
		names := make([]string, len(hosts))
		for i, host := range hosts {
			names[i] = host.host
		}
		return "", "", newIdentityError(
			"cannot resolve authenticated GitHub user: multiple authenticated hosts found (%s); set GH_HOST or specify <owner>/<repo>",
			strings.Join(names, ", "),
		)
	}
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return auth.NormalizeHostname(host)
}

func repositoryIdentity(host, owner, repo string) string {
	return strings.Trim(strings.Join([]string{host, owner, repo}, "/"), "/")
}

func newIdentityError(format string, args ...any) *IdentityError {
	return &IdentityError{message: fmt.Sprintf(format, args...)}
}

func safeError(err error, secret string) string {
	message := err.Error()
	if secret != "" {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}
