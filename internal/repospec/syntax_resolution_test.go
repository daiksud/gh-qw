package repospec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestParseIsIndependentOfResolution(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, raw := range []string{
		"widget",
		"owner/widget",
		"owner/widget@bad..ref",
		"ssh://deploy@example.com:2222/owner/widget.git@topic/next",
		"deploy@example.com:owner/widget.git@topic/next",
		"file:///missing/github.com/owner/widget",
		"./missing/github.com/owner/widget",
		".",
	} {
		parsed, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) accessed resolution requirements: %v", raw, err)
		}
		if parsed.raw != raw {
			t.Fatalf("Parse(%q) changed the original input to %q", raw, parsed.raw)
		}
	}
	parsed, err := Parse("owner/widget@@{-1}")
	if err != nil || parsed.repository.branch != "@{-1}" {
		t.Fatalf("Parse() branch = %q, error = %v; want literal checkout shorthand", parsed.repository.branch, err)
	}
	if _, err := Parse("invalid?repository"); !errors.Is(err, ErrUsage) {
		t.Fatalf("malformed bare repository error = %v, want a syntax error before authentication", err)
	}
}

func TestResolveOnlyUsesRequiredEnvironment(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	options := Options{
		Roots:      []string{filepath.Join(t.TempDir(), "missing-root")},
		WorkingDir: filepath.Join(t.TempDir(), "missing-cwd"),
		ResolveIdentity: func() (string, string, error) {
			t.Fatal("explicit or local repository must not authenticate")
			return "", "", nil
		},
	}
	for _, raw := range []string{
		"owner/widget",
		"example.com/owner/widget",
		"https://example.com/owner/widget.git",
		"git@example.com:owner/widget.git",
	} {
		if _, err := parseAndResolve(raw, options); err != nil {
			t.Fatalf("Resolve(%q) required Git or local paths: %v", raw, err)
		}
	}
	root := t.TempDir()
	options.Roots = []string{root}
	options.WorkingDir = root
	spec, err := parseAndResolve("./github.com/owner/widget", options)
	if err != nil || spec.Identity.String() != "github.com/owner/widget" {
		t.Fatalf("local Resolve() = %#v, %v; want offline resolution of missing repository", spec, err)
	}
	_, err = parseAndResolve("owner/widget@topic", options)
	var commandErr *gitcmd.CommandError
	if !errors.As(err, &commandErr) || errors.Is(err, gitcmd.ErrInvalidRef) || errors.Is(err, ErrUsage) {
		t.Fatalf("missing Git error = %v, want discoverable execution failure", err)
	}
	_, err = parseAndResolve("./github.com/owner/widget@topic", options)
	if !errors.As(err, &commandErr) || errors.Is(err, gitcmd.ErrInvalidRef) || errors.Is(err, ErrUsage) {
		t.Fatalf("local suffix hid Git execution failure: %v", err)
	}
}

func TestResolvePreservesLiteralRefContracts(t *testing.T) {
	for _, branch := range []string{"HEAD", "feature/next", "topic@two", "topic\u00a0name", "topic./next"} {
		spec, err := parseAndResolve("owner/widget@"+branch, Options{})
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", branch, err)
		}
		if spec.Branch != branch || spec.Identity.String() != "github.com/owner/widget" || spec.CloneURL != "https://github.com/owner/widget" {
			t.Fatalf("Resolve(%q) changed literal branch, identity, or transport: %#v", branch, spec)
		}
	}
	for _, branch := range []string{"@", "@{-1}", "-topic", "bad..ref"} {
		_, err := parseAndResolve("owner/widget@"+branch, Options{})
		if !errors.Is(err, ErrUsage) || !errors.Is(err, gitcmd.ErrInvalidRef) {
			t.Fatalf("Resolve(%q) error = %v, want Git ref usage error", branch, err)
		}
	}
}

func TestResolveRejectsInvalidBranchBeforeAuthentication(t *testing.T) {
	_, err := parseAndResolve("widget@@{-1}", Options{
		ResolveIdentity: func() (string, string, error) {
			t.Fatal("invalid branch must not authenticate")
			return "", "", nil
		},
	})
	if !errors.Is(err, gitcmd.ErrInvalidRef) {
		t.Fatalf("Resolve() error = %v, want Git ref rejection", err)
	}
}

func TestResolveCancellationPreventsAuthentication(t *testing.T) {
	parsed, err := Parse("widget")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = parsed.Resolve(ctx, Options{ResolveIdentity: func() (string, string, error) {
		t.Fatal("canceled resolution must not authenticate")
		return "", "", nil
	}})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUsage) {
		t.Fatalf("Resolve() error = %v, want context cancellation", err)
	}
}

func TestResolveRejectsDanglingRepositorySymlink(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "github.com", "owner")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(parent, "widget")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := parseAndResolve("./github.com/owner/widget", Options{Roots: []string{root}, WorkingDir: root}); err == nil {
		t.Fatal("dangling repository symlink was treated as a missing contained path")
	}
}
