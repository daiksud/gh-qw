package ghapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/repospec"
)

func TestIdentityErrorRemainsDiscoverableThroughRepositoryUsageError(t *testing.T) {
	client := &Client{
		hostOverride: func() string {
			return ""
		},
		knownHosts: func() []string {
			return nil
		},
		tokenForHost: func(string) (string, string) {
			return "", ""
		},
		newREST: func(string, string) (restClient, error) {
			t.Fatal("REST client must not be created without authentication")
			return nil, nil
		},
	}

	_, identityErr := client.ResolveIdentity(context.Background())
	var typedIdentityErr *IdentityError
	if !errors.As(identityErr, &typedIdentityErr) {
		t.Fatalf("ResolveIdentity() error = %T %v, want *IdentityError", identityErr, identityErr)
	}

	_, err := repospec.Parse("widget", repospec.Options{
		ResolveIdentity: func() (string, string, error) {
			return "", "", identityErr
		},
	})
	if !errors.Is(err, repospec.ErrUsage) {
		t.Fatalf("Parse() error = %v, want repospec.ErrUsage", err)
	}
	var usageErr *repospec.UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("Parse() error = %T %v, want *repospec.UsageError", err, err)
	}
	var wrappedIdentityErr *IdentityError
	if !errors.As(err, &wrappedIdentityErr) {
		t.Fatalf("Parse() error did not preserve *IdentityError: %v", err)
	}
	if wrappedIdentityErr != typedIdentityErr {
		t.Fatalf("Parse() preserved IdentityError %p, want original %p", wrappedIdentityErr, typedIdentityErr)
	}
	if usageErr.Input != "widget" ||
		!strings.Contains(err.Error(), "specify <owner>/<repo>") {
		t.Fatalf("Parse() usage error = %#v: %v", usageErr, err)
	}
}
