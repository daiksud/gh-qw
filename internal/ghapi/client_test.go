package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type restClientFunc func(context.Context, string, string, io.Reader, any) error

func (f restClientFunc) DoWithContext(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
	response any,
) error {
	return f(ctx, method, path, body, response)
}

func TestResolveIdentityHonorsGHHost(t *testing.T) {
	client := testClient(
		"ghe.example.com",
		map[string]string{
			"github.com":        "github-token",
			"ghe.example.com":   "enterprise-token",
			"other.example.com": "other-token",
		},
		func(host, token string) (restClient, error) {
			if host != "ghe.example.com" {
				t.Fatalf("REST host = %q, want ghe.example.com", host)
			}
			if token != "enterprise-token" {
				t.Fatalf("REST token = %q, want enterprise-token", token)
			}
			return jsonResponse(t, `{"login":"enterprise-user"}`), nil
		},
	)

	identity, err := client.ResolveIdentity(context.Background())
	if err != nil {
		t.Fatalf("ResolveIdentity() error = %v", err)
	}
	if identity != (Identity{Host: "ghe.example.com", Login: "enterprise-user"}) {
		t.Fatalf("ResolveIdentity() = %#v", identity)
	}
}

func TestResolveIdentityDefaultsToAuthenticatedGitHubCom(t *testing.T) {
	client := testClient(
		"",
		map[string]string{
			"github.com":      "github-token",
			"ghe.example.com": "enterprise-token",
		},
		func(host, token string) (restClient, error) {
			if host != "github.com" {
				t.Fatalf("REST host = %q, want github.com", host)
			}
			return jsonResponse(t, `{"login":"octocat"}`), nil
		},
	)

	identity, err := client.ResolveIdentity(context.Background())
	if err != nil {
		t.Fatalf("ResolveIdentity() error = %v", err)
	}
	if identity != (Identity{Host: "github.com", Login: "octocat"}) {
		t.Fatalf("ResolveIdentity() = %#v", identity)
	}
}

func TestResolveIdentityDoesNotInferEnterpriseHost(t *testing.T) {
	client := testClient("", map[string]string{"ghe.example.com": "enterprise-token"}, func(string, string) (restClient, error) {
		t.Fatal("REST must not use an inferred Enterprise host")
		return nil, nil
	})
	_, err := client.ResolveIdentity(context.Background())
	var identityErr *IdentityError
	if !errors.As(err, &identityErr) || !strings.Contains(err.Error(), "github.com") {
		t.Fatalf("error=%v, want github.com identity failure", err)
	}
}

func TestResolveIdentityNoAuthentication(t *testing.T) {
	client := testClient("", nil, func(string, string) (restClient, error) {
		t.Fatal("REST client must not be created without authentication")
		return nil, nil
	})

	_, err := client.ResolveIdentity(context.Background())
	var identityErr *IdentityError
	if !errors.As(err, &identityErr) {
		t.Fatalf("ResolveIdentity() error = %T %v, want *IdentityError", err, err)
	}
	if !strings.Contains(err.Error(), "no authentication token found") ||
		!strings.Contains(err.Error(), "<owner>/<repo>") {
		t.Fatalf("ResolveIdentity() error = %q", err)
	}
}

func TestResolveIdentityMissingLogin(t *testing.T) {
	client := testClient(
		"ghe.example.com",
		map[string]string{"ghe.example.com": "enterprise-token"},
		func(string, string) (restClient, error) {
			return jsonResponse(t, `{}`), nil
		},
	)

	_, err := client.ResolveIdentity(context.Background())
	var identityErr *IdentityError
	if !errors.As(err, &identityErr) {
		t.Fatalf("ResolveIdentity() error = %T %v, want *IdentityError", err, err)
	}
	if !strings.Contains(err.Error(), "did not include a login") {
		t.Fatalf("ResolveIdentity() error = %q", err)
	}
}

func TestDefaultBranchSuccessUsesExplicitHost(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-context")

	client := testClient(
		"",
		map[string]string{"ghe.example.com": "enterprise-token"},
		func(host, token string) (restClient, error) {
			if host != "ghe.example.com" {
				t.Fatalf("REST host = %q, want ghe.example.com", host)
			}
			if token != "enterprise-token" {
				t.Fatalf("REST token = %q, want enterprise-token", token)
			}
			return restClientFunc(func(gotCtx context.Context, method, path string, _ io.Reader, response any) error {
				if gotCtx.Value(contextKey{}) != "request-context" {
					t.Fatal("request context was not propagated")
				}
				if method != http.MethodGet {
					t.Fatalf("method = %q, want GET", method)
				}
				if path != "repos/acme/widgets" {
					t.Fatalf("path = %q, want repos/acme/widgets", path)
				}
				return json.Unmarshal([]byte(`{"default_branch":"trunk"}`), response)
			}), nil
		},
	)

	branch, err := client.DefaultBranch(ctx, "GHE.EXAMPLE.COM", "acme", "widgets")
	if err != nil {
		t.Fatalf("DefaultBranch() error = %v", err)
	}
	if branch != "trunk" {
		t.Fatalf("DefaultBranch() = %q, want trunk", branch)
	}
}

func TestDefaultBranchRESTErrorHasContextAndRedactsToken(t *testing.T) {
	const token = "secret-enterprise-token"
	client := testClient(
		"",
		map[string]string{"ghe.example.com": token},
		func(string, string) (restClient, error) {
			return restClientFunc(func(context.Context, string, string, io.Reader, any) error {
				return errors.New("request rejected for token " + token)
			}), nil
		},
	)

	_, err := client.DefaultBranch(context.Background(), "ghe.example.com", "acme", "widgets")
	if err == nil {
		t.Fatal("DefaultBranch() error = nil")
	}
	if !strings.Contains(err.Error(), `lookup default branch for "ghe.example.com/acme/widgets"`) ||
		!strings.Contains(err.Error(), "GET repository") {
		t.Fatalf("DefaultBranch() error lacks operation context: %q", err)
	}
	if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("DefaultBranch() error leaked token: %q", err)
	}
}

func TestDefaultBranchMissingDefaultBranch(t *testing.T) {
	client := testClient(
		"",
		map[string]string{"github.com": "github-token"},
		func(string, string) (restClient, error) {
			return jsonResponse(t, `{}`), nil
		},
	)

	_, err := client.DefaultBranch(context.Background(), "github.com", "acme", "widgets")
	if err == nil || !strings.Contains(err.Error(), "did not include default_branch") {
		t.Fatalf("DefaultBranch() error = %v", err)
	}
}

func TestDefaultBranchUsesHostToken(t *testing.T) {
	client := testClient(
		"",
		map[string]string{"github.com": "host-token"},
		func(host, token string) (restClient, error) {
			if token != "host-token" {
				t.Fatalf("REST token = %q, want host-token", token)
			}
			return jsonResponse(t, `{"default_branch":"main"}`), nil
		},
	)

	if _, err := client.DefaultBranch(context.Background(), "github.com", "acme", "widgets"); err != nil {
		t.Fatalf("DefaultBranch() error = %v", err)
	}
}

func testClient(
	override string,
	tokens map[string]string,
	newREST func(string, string) (restClient, error),
) *Client {
	return &Client{
		hostOverride: func() string {
			return override
		},
		tokenForHost: func(host string) (string, string) {
			return tokens[host], "test"
		},
		newREST: newREST,
	}
}

func jsonResponse(t *testing.T, responseJSON string) restClient {
	t.Helper()
	return restClientFunc(func(_ context.Context, method, path string, body io.Reader, response any) error {
		if method != http.MethodGet {
			t.Fatalf("method = %q, want GET", method)
		}
		if body != nil {
			t.Fatalf("body = %v, want nil", body)
		}
		return json.Unmarshal([]byte(responseJSON), response)
	})
}
