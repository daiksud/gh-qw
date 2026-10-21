package ghapi

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/ghcmd"
)

func TestResolveIdentityUsesSelectedHost(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		want string
	}{
		{name: "default GitHub.com", want: "github.com"},
		{name: "explicit Enterprise", host: " GHE.EXAMPLE.COM ", want: "ghe.example.com"},
		{name: "GitHub API alias is passed to gh", host: "api.github.com", want: "api.github.com"},
		{name: "tenant API alias is passed to gh", host: "api.acme.ghe.com", want: "api.acme.ghe.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GH_HOST", test.host)
			ctx := context.WithValue(context.Background(), struct{}{}, "identity request")
			var calls int
			client := &Client{output: func(gotCtx context.Context, args ...string) ([]byte, error) {
				calls++
				if gotCtx != ctx {
					t.Fatal("request context was not propagated")
				}
				want := []string{"api", "--hostname", test.want, "user", "--jq", ".login // empty"}
				if !slices.Equal(args, want) {
					t.Fatalf("gh args = %#v, want %#v", args, want)
				}
				return []byte(" octocat\r\n"), nil
			}}

			identity, err := client.ResolveIdentity(ctx)
			if err != nil {
				t.Fatalf("ResolveIdentity() error = %v", err)
			}
			if want := (Identity{Host: test.want, Login: "octocat"}); identity != want {
				t.Fatalf("ResolveIdentity() = %#v, want %#v", identity, want)
			}
			if calls != 1 {
				t.Fatalf("gh calls = %d, want one API call without auth preflight", calls)
			}
		})
	}
}

func TestResolveIdentityRejectsEmptyLogin(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.example.com")
	for _, output := range []string{"", " \r\n\t"} {
		client := &Client{output: func(context.Context, ...string) ([]byte, error) {
			return []byte(output), nil
		}}
		_, err := client.ResolveIdentity(context.Background())
		var identityErr *IdentityError
		if !errors.As(err, &identityErr) || !strings.Contains(err.Error(), "did not include a login") ||
			!strings.Contains(err.Error(), "specify <owner>/<repo>") {
			t.Fatalf("ResolveIdentity() error = %v, want typed missing-login usage error", err)
		}
	}
}

func TestResolveIdentityPreservesProcessFailure(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.example.com")
	failure := &ghcmd.CommandError{ExitCode: 4}
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		return []byte("partial response"), failure
	}}

	identity, err := client.ResolveIdentity(context.Background())
	var identityErr *IdentityError
	if !errors.As(err, &identityErr) || !errors.Is(err, failure) {
		t.Fatalf("error = %v, want typed identity error preserving process failure", err)
	}
	if identity != (Identity{}) {
		t.Fatalf("identity = %#v, want no identity after failed gh command", identity)
	}
	if !strings.Contains(err.Error(), `for "ghe.example.com": query current user`) ||
		!strings.Contains(err.Error(), "specify <owner>/<repo>") {
		t.Fatalf("error lacks operation context: %v", err)
	}
}

func TestDefaultBranchUsesExplicitHostAndRESTField(t *testing.T) {
	t.Setenv("GH_HOST", "unrelated.example.com")
	ctx := context.WithValue(context.Background(), struct{}{}, "default-branch request")
	for _, branch := range []string{"trunk", "null"} {
		t.Run(branch, func(t *testing.T) {
			var calls int
			client := &Client{output: func(gotCtx context.Context, args ...string) ([]byte, error) {
				calls++
				if gotCtx != ctx {
					t.Fatal("request context was not propagated")
				}
				// The REST field exists for empty repositories. `// empty`
				// distinguishes a missing/null field from a branch named null.
				want := []string{"api", "--hostname", "ghe.example.com", "repos/acme/widgets", "--jq", ".default_branch // empty"}
				if !slices.Equal(args, want) {
					t.Fatalf("gh args = %#v, want %#v", args, want)
				}
				return []byte(" " + branch + "\r\n"), nil
			}}

			got, err := client.DefaultBranch(ctx, "GHE.EXAMPLE.COM", "acme", "widgets")
			if err != nil || got != branch {
				t.Fatalf("DefaultBranch() = %q, %v, want %q", got, err, branch)
			}
			if calls != 1 {
				t.Fatalf("gh calls = %d, want one REST lookup", calls)
			}
		})
	}
}

func TestDefaultBranchEscapesEndpointComponents(t *testing.T) {
	client := &Client{output: func(_ context.Context, args ...string) ([]byte, error) {
		if args[3] != "repos/team%2Fpart/widgets%3Fquery" {
			t.Fatalf("endpoint = %q", args[3])
		}
		return []byte("main\n"), nil
	}}
	if _, err := client.DefaultBranch(context.Background(), "github.com", "team/part", "widgets?query"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBranchRequiresRepositoryIdentity(t *testing.T) {
	for _, parts := range [][3]string{{"", "acme", "widgets"}, {"github.com", " ", "widgets"}, {"github.com", "acme", ""}} {
		client := &Client{output: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("gh must not run with an incomplete repository identity")
			return nil, nil
		}}
		_, err := client.DefaultBranch(context.Background(), parts[0], parts[1], parts[2])
		if err == nil || !strings.Contains(err.Error(), "host, owner, and repository are required") {
			t.Fatalf("DefaultBranch(%q) error = %v", parts, err)
		}
	}
}

func TestDefaultBranchRejectsEmptyResponse(t *testing.T) {
	for _, output := range []string{"", " \r\n\t"} {
		client := &Client{output: func(context.Context, ...string) ([]byte, error) {
			return []byte(output), nil
		}}
		_, err := client.DefaultBranch(context.Background(), "github.com", "acme", "widgets")
		if err == nil || !strings.Contains(err.Error(), "did not include default_branch") {
			t.Fatalf("DefaultBranch() error = %v", err)
		}
	}
}

func TestDefaultBranchPreservesProcessFailure(t *testing.T) {
	failure := &ghcmd.CommandError{ExitCode: 1}
	client := &Client{output: func(context.Context, ...string) ([]byte, error) {
		return []byte("partial response"), failure
	}}
	branch, err := client.DefaultBranch(context.Background(), "ghe.example.com", "acme", "widgets")
	if !errors.Is(err, failure) || branch != "" {
		t.Fatalf("DefaultBranch() = %q, %v, want failure without a branch", branch, err)
	}
	if !strings.Contains(err.Error(), `lookup default branch for "ghe.example.com/acme/widgets": GET repository`) {
		t.Fatalf("error lacks operation context: %v", err)
	}
}
