package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/ghcmd"
)

func TestRepositoryExistsDistinguishesHTTPNotFoundFromFailures(t *testing.T) {
	requestFailure := &ghcmd.CommandError{ExitCode: 1}
	authFailure := &ghcmd.CommandError{ExitCode: 4}
	for _, test := range []struct {
		name    string
		output  string
		failure error
		exists  bool
		wantErr bool
	}{
		{name: "exists", output: "HTTP/2.0 200 OK\nContent-Type: application/json\r\n\r\n", exists: true},
		{name: "not found", output: "HTTP/2.0 404 Not Found\nContent-Type: application/json\r\n\r\n", failure: requestFailure},
		{name: "unauthenticated", output: "HTTP/2.0 401 Unauthorized\r\n\r\n", failure: requestFailure, wantErr: true},
		{name: "forbidden", output: "HTTP/2.0 403 Forbidden\r\n\r\n", failure: requestFailure, wantErr: true},
		{name: "rate limited", output: "HTTP/2.0 429 Too Many Requests\r\n\r\n", failure: requestFailure, wantErr: true},
		{name: "server failure", output: "HTTP/2.0 500 Internal Server Error\r\n\r\n", failure: requestFailure, wantErr: true},
		{name: "transport failure", failure: requestFailure, wantErr: true},
		{name: "not found text is insufficient", output: "repository not found (HTTP 404)", failure: requestFailure, wantErr: true},
		{name: "incomplete HTTP response", output: "HTTP/2.0 404 Not Found\r\nContent-Type: application/json", failure: requestFailure, wantErr: true},
		{name: "authentication exit is not not found", output: "HTTP/2.0 404 Not Found\r\n\r\n", failure: authFailure, wantErr: true},
		{name: "process failure after success headers", output: "HTTP/2.0 200 OK\r\n\r\n", failure: requestFailure, wantErr: true},
		{name: "empty successful output", wantErr: true},
		{name: "unfollowed redirect", output: "HTTP/2.0 301 Moved Permanently\r\n\r\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{output: func(context.Context, ...string) ([]byte, error) {
				return []byte(test.output), test.failure
			}}
			exists, err := client.RepositoryExists(context.Background(), "github.com", "acme", "widgets")
			if exists != test.exists || (err != nil) != test.wantErr {
				t.Fatalf("RepositoryExists() = %v, %v, want exists=%v error=%v", exists, err, test.exists, test.wantErr)
			}
			if test.wantErr && test.failure != nil && !errors.Is(err, test.failure) {
				t.Fatalf("error %v did not preserve %v", err, test.failure)
			}
		})
	}
}

func TestRepositoryExistsUsesExplicitHostAndStructuredStatus(t *testing.T) {
	t.Setenv("GH_HOST", "unrelated.example.com")
	ctx := context.WithValue(context.Background(), struct{}{}, "repository request")
	client := &Client{output: func(gotCtx context.Context, args ...string) ([]byte, error) {
		if gotCtx != ctx {
			t.Fatal("request context was not propagated")
		}
		want := []string{"api", "--hostname", "ghe.example.com", "repos/Acme/Widget", "--method", "HEAD", "--include", "--silent"}
		if !slices.Equal(args, want) {
			t.Fatalf("gh args = %#v, want %#v", args, want)
		}
		return []byte("HTTP/1.1 200 OK\r\n\r\n"), nil
	}}
	if exists, err := client.RepositoryExists(ctx, " GHE.EXAMPLE.COM ", "Acme", "Widget"); err != nil || !exists {
		t.Fatalf("RepositoryExists() = %v, %v", exists, err)
	}
}

func TestRepositoryExistsPreservesCancellationAfterHTTPNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		cancel()
		return []byte("HTTP/2.0 404 Not Found\r\n\r\n"), &ghcmd.CommandError{ExitCode: 1}
	}}
	exists, err := client.RepositoryExists(ctx, "github.com", "acme", "widgets")
	if exists || !errors.Is(err, context.Canceled) {
		t.Fatalf("RepositoryExists() = %v, %v, want cancellation", exists, err)
	}
}

func TestRepositoryDiscoveryRetainsHostPort(t *testing.T) {
	client := &Client{output: func(_ context.Context, args ...string) ([]byte, error) {
		if args[2] != "ghe.example.com:8443" {
			t.Fatalf("API hostname = %q, want transport port", args[2])
		}
		if args[3] == "graphql" {
			return []byte("[" + ownerRepositoriesTestPage("acme", false, "acme/widget") + "]"), nil
		}
		return []byte("HTTP/1.1 200 OK\r\n\r\n"), nil
	}}
	if exists, err := client.RepositoryExists(context.Background(), "ghe.example.com:8443", "acme", "widget"); err != nil || !exists {
		t.Fatalf("RepositoryExists() = %v, %v", exists, err)
	}
	if repositories, err := client.OwnerRepositories(context.Background(), "ghe.example.com:8443", "acme"); err != nil || !slices.Equal(repositories, []string{"acme/widget"}) {
		t.Fatalf("OwnerRepositories() = %v, %v", repositories, err)
	}
}

func TestOwnerRepositoriesCollectsOwnedRepositoriesAcrossAllPages(t *testing.T) {
	t.Setenv("GH_HOST", "unrelated.example.com")
	ctx := context.WithValue(context.Background(), struct{}{}, "owner repositories request")
	var calls int
	client := &Client{output: func(gotCtx context.Context, args ...string) ([]byte, error) {
		calls++
		if gotCtx != ctx {
			t.Fatal("request context was not propagated")
		}
		want := []string{"api", "--hostname", "ghe.example.com", "graphql", "--paginate", "--slurp",
			"--raw-field", "query=" + ownerRepositoriesQuery, "--raw-field", "owner=acme"}
		if !slices.Equal(args, want) {
			t.Fatalf("gh args = %#v, want %#v", args, want)
		}
		return []byte("[" + ownerRepositoriesTestPage("Acme", true, "Acme/private", "Acme/fork") + "," +
			ownerRepositoriesTestPage("Acme", false, "Acme/fork", "Acme/archived", "Acme/public") + "]"), nil
	}}
	got, err := client.OwnerRepositories(ctx, " GHE.EXAMPLE.COM ", "acme")
	want := []string{"Acme/archived", "Acme/fork", "Acme/private", "Acme/public"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("OwnerRepositories() = %#v, %v, want %#v", got, err, want)
	}
	if calls != 1 {
		t.Fatalf("gh calls = %d, want native gh pagination", calls)
	}
	if !strings.Contains(ownerRepositoriesQuery, "ownerAffiliations: OWNER") ||
		strings.Contains(ownerRepositoriesQuery, "isFork:") || strings.Contains(ownerRepositoriesQuery, "privacy:") ||
		strings.Contains(ownerRepositoriesQuery, "visibility:") || strings.Contains(ownerRepositoriesQuery, "isArchived:") {
		t.Fatalf("owner query excludes owned repositories: %s", ownerRepositoriesQuery)
	}
}

func TestOwnerRepositoriesAllowsAnEmptyOwner(t *testing.T) {
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		return []byte("[" + ownerRepositoriesTestPage("acme", false) + "]"), nil
	}}
	got, err := client.OwnerRepositories(context.Background(), "github.com", "acme")
	if err != nil || len(got) != 0 {
		t.Fatalf("OwnerRepositories() = %#v, %v, want empty success", got, err)
	}
}

func TestOwnerRepositoriesDiscardsPartialResultsOnFailure(t *testing.T) {
	failure := &ghcmd.CommandError{ExitCode: 1}
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		return []byte("[" + ownerRepositoriesTestPage("acme", true, "acme/first") + "]"), failure
	}}
	got, err := client.OwnerRepositories(context.Background(), "github.com", "acme")
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("OwnerRepositories() = %#v, %v, want preserved failure and no partial list", got, err)
	}
}

func TestOwnerRepositoriesRejectsMalformedAndIncompleteResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
	}{
		{name: "empty"},
		{name: "malformed JSON", output: "[{"},
		{name: "no pages", output: "[]"},
		{name: "missing owner", output: `[{"data":{"repositoryOwner":null}}]`},
		{name: "unrelated owner", output: "[" + ownerRepositoriesTestPage("other", false, "other/repository") + "]"},
		{name: "GraphQL error", output: `[{"errors":[{"type":"FORBIDDEN"}]}]`},
		{name: "missing connection", output: `[{"data":{"repositoryOwner":{"login":"acme"}}}]`},
		{name: "unfinished pagination", output: "[" + ownerRepositoriesTestPage("acme", true, "acme/first") + "]"},
		{name: "extra page", output: "[" + ownerRepositoriesTestPage("acme", false, "acme/first") + "," + ownerRepositoriesTestPage("acme", false, "acme/last") + "]"},
		{name: "unrelated repository", output: "[" + ownerRepositoriesTestPage("acme", false, "acme/good", "other/repository") + "]"},
		{name: "unsafe repository", output: "[" + ownerRepositoriesTestPage("acme", false, "acme/good", "acme/../escape") + "]"},
		{name: "terminal control", output: "[" + ownerRepositoriesTestPage("acme", false, "acme/good", "acme/\x1b[31m") + "]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{output: func(context.Context, ...string) ([]byte, error) {
				return []byte(test.output), nil
			}}
			got, err := client.OwnerRepositories(context.Background(), "github.com", "acme")
			if err == nil || got != nil {
				t.Fatalf("OwnerRepositories() = %#v, %v, want failure and no partial list", got, err)
			}
		})
	}
}

func TestRepositoryDiscoveryRejectsInvalidIdentityBeforeGh(t *testing.T) {
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("gh must not run for an invalid identity")
		return nil, nil
	}}
	for _, parts := range [][3]string{{"", "acme", "widgets"}, {"github.com", "", "widgets"}, {"github.com", "acme", ""}, {"github.com", "../acme", "widgets"}} {
		if _, err := client.RepositoryExists(context.Background(), parts[0], parts[1], parts[2]); err == nil {
			t.Fatalf("RepositoryExists(%q) accepted an invalid identity", parts)
		}
	}
	for _, parts := range [][2]string{{"", "acme"}, {"github.com", ""}, {"github.com", "../acme"}, {"invalid/host", "acme"}} {
		if _, err := client.OwnerRepositories(context.Background(), parts[0], parts[1]); err == nil {
			t.Fatalf("OwnerRepositories(%q) accepted an invalid identity", parts)
		}
	}
}

func TestRepositoryDiscoveryUsesNativeGhProcess(t *testing.T) {
	for _, operation := range []string{"exists", "list"} {
		t.Run(operation, func(t *testing.T) {
			output := "HTTP/2.0 200 OK\r\n\r\n"
			if operation == "list" {
				output = "[" + ownerRepositoriesTestPage("acme", false, "acme/private") + "]"
			}
			record := prepareAPIGhProcess(t, "success", output)
			var diagnostics bytes.Buffer
			client := NewClient(&diagnostics)
			if operation == "exists" {
				if exists, err := client.RepositoryExists(context.Background(), "selected.example.com", "acme", "widgets"); err != nil || !exists {
					t.Fatalf("RepositoryExists() = %v, %v", exists, err)
				}
			} else {
				if names, err := client.OwnerRepositories(context.Background(), "selected.example.com", "acme"); err != nil || !slices.Equal(names, []string{"acme/private"}) {
					t.Fatalf("OwnerRepositories() = %#v, %v", names, err)
				}
			}
			if diagnostics.String() != "gh api diagnostic" {
				t.Fatalf("diagnostics = %q, want only gh stderr", diagnostics.String())
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var invocation apiInvocation
			if err := json.Unmarshal(data, &invocation); err != nil {
				t.Fatal(err)
			}
			if invocation.Stdin != "" || !slices.Contains(invocation.Env, "GH_ENTERPRISE_TOKEN=dummy-GH_ENTERPRISE_TOKEN") ||
				!slices.Contains(invocation.Env, "GH_TOKEN=dummy-GH_TOKEN") || invocation.Args[2] != "selected.example.com" {
				t.Fatalf("invocation = %#v, want explicit host, native credentials, and null stdin", invocation)
			}
		})
	}
}

func ownerRepositoriesTestPage(owner string, more bool, names ...string) string {
	encodedOwner, _ := json.Marshal(owner)
	nodes := make([]string, 0, len(names))
	for _, name := range names {
		encodedName, _ := json.Marshal(name)
		nodes = append(nodes, fmt.Sprintf(`{"nameWithOwner":%s}`, encodedName))
	}
	return fmt.Sprintf(`{"data":{"repositoryOwner":{"login":%s,"repositories":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"cursor"}}}}}`,
		encodedOwner, strings.Join(nodes, ","), more)
}
