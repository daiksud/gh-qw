package repospec

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestResolveRepositoryForms(t *testing.T) {
	authenticated := func() (string, string, error) {
		return "GHE.Example.COM:8443", "AuthUser", nil
	}

	tests := []struct {
		name    string
		input   string
		options Options
		want    Spec
	}{
		{
			name:  "bare repository uses authenticated identity",
			input: "Project.git@feature/parser",
			options: Options{
				ResolveIdentity: authenticated,
			},
			want: Spec{
				CloneURL: "https://ghe.example.com:8443/AuthUser/Project.git",
				Identity: Identity{Host: "ghe.example.com", Owner: "AuthUser", Repo: "Project"},
				Branch:   "feature/parser",
			},
		},
		{
			name:  "owner and repository default to GitHub",
			input: "Acme/Project",
			want: Spec{
				CloneURL: "https://github.com/Acme/Project",
				Identity: Identity{Host: "github.com", Owner: "Acme", Repo: "Project"},
			},
		},
		{
			name:  "safe owner and repository punctuation is preserved",
			input: "Acme-Team/Project_name.v2",
			want: Spec{
				CloneURL: "https://github.com/Acme-Team/Project_name.v2",
				Identity: Identity{Host: "github.com", Owner: "Acme-Team", Repo: "Project_name.v2"},
			},
		},
		{
			name:  "enterprise host is lowercased",
			input: "GIT.Example.COM/Acme/Project.git",
			want: Spec{
				CloneURL: "https://git.example.com/Acme/Project.git",
				Identity: Identity{Host: "git.example.com", Owner: "Acme", Repo: "Project"},
			},
		},
		{
			name:  "host shorthand retains port in URLs",
			input: "GIT.Example.COM:8443/Acme/Project",
			want: Spec{
				CloneURL: "https://git.example.com:8443/Acme/Project",
				Identity: Identity{Host: "git.example.com", Owner: "Acme", Repo: "Project"},
			},
		},
		{
			name:  "HTTPS URL preserves clone suffix and slash branch",
			input: "https://GHE.Example.COM/Org/Repo.git@release/v2",
			want: Spec{
				CloneURL: "https://ghe.example.com/Org/Repo.git",
				Identity: Identity{Host: "ghe.example.com", Owner: "Org", Repo: "Repo"},
				Branch:   "release/v2",
			},
		},
		{
			name:  "HTTP URL remains HTTP for cloning",
			input: "http://git.example.com/Org/Repo",
			want: Spec{
				CloneURL: "http://git.example.com/Org/Repo",
				Identity: Identity{Host: "git.example.com", Owner: "Org", Repo: "Repo"},
			},
		},
		{
			name:  "SSH URL keeps user and port",
			input: "ssh://deploy@GHE.Example.COM:2222/Org/Repo.git",
			want: Spec{
				CloneURL: "ssh://deploy@ghe.example.com:2222/Org/Repo.git",
				Identity: Identity{Host: "ghe.example.com", Owner: "Org", Repo: "Repo"},
			},
		},
		{
			name:  "SSH authority at sign is not a branch",
			input: "ssh://git@github.com/Org/Repo",
			want: Spec{
				CloneURL: "ssh://git@github.com/Org/Repo",
				Identity: Identity{Host: "github.com", Owner: "Org", Repo: "Repo"},
			},
		},
		{
			name:  "scp-like URL with user",
			input: "deploy@GHE.Example.COM:Org/Repo.git@topic/one",
			want: Spec{
				CloneURL: "ssh://deploy@ghe.example.com/Org/Repo.git",
				Identity: Identity{Host: "ghe.example.com", Owner: "Org", Repo: "Repo"},
				Branch:   "topic/one",
			},
		},
		{
			name:  "scp-like URL without user and with root path",
			input: "GHE.Example.COM:/Org/Repo",
			want: Spec{
				CloneURL: "ssh://ghe.example.com/Org/Repo",
				Identity: Identity{Host: "ghe.example.com", Owner: "Org", Repo: "Repo"},
			},
		},
		{
			name:  "file URL with repository host authority",
			input: "file://GHE.Example.COM/Org/Repo.git@topic",
			want: Spec{
				CloneURL: "file://ghe.example.com/Org/Repo.git",
				Identity: Identity{Host: "ghe.example.com", Owner: "Org", Repo: "Repo"},
				Branch:   "topic",
			},
		},
		{
			name:  "branch may contain another at sign",
			input: "Org/Repo@topic@two",
			want: Spec{
				CloneURL: "https://github.com/Org/Repo",
				Identity: Identity{Host: "github.com", Owner: "Org", Repo: "Repo"},
				Branch:   "topic@two",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAndResolve(test.input, test.options)
			if err != nil {
				t.Fatalf("parseAndResolve(%q) error = %v", test.input, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseAndResolve(%q)\ngot:  %#v\nwant: %#v", test.input, got, test.want)
			}
		})
	}
}

func TestResolveSSHOption(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantClone string
	}{
		{
			name:      "plain repository uses git user",
			input:     "Org/Repo.git",
			wantClone: "ssh://git@github.com/Org/Repo.git",
		},
		{
			name:      "HTTPS explicit user is preserved",
			input:     "https://Deploy@GHE.Example.COM/Org/Repo",
			wantClone: "ssh://Deploy@ghe.example.com/Org/Repo",
		},
		{
			name:      "HTTP port is preserved",
			input:     "http://GHE.Example.COM:8080/Org/Repo",
			wantClone: "ssh://git@ghe.example.com:8080/Org/Repo",
		},
		{
			name:      "existing SSH URL is unchanged",
			input:     "ssh://deploy@GHE.Example.COM/Org/Repo",
			wantClone: "ssh://deploy@ghe.example.com/Org/Repo",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAndResolve(test.input, Options{SSH: true})
			if err != nil {
				t.Fatalf("parseAndResolve(%q) error = %v", test.input, err)
			}
			if got.CloneURL != test.wantClone {
				t.Fatalf("CloneURL = %q, want %q", got.CloneURL, test.wantClone)
			}
		})
	}
}

func TestResolveRelativeRepository(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repos")
	workingDir := filepath.Join(root, "GitHub.COM", "Acme", "Current")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := parseAndResolve("../Target.git@feature/parser", Options{
		Roots:      []string{root},
		WorkingDir: workingDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Spec{
		CloneURL: "https://github.com/Acme/Target.git",
		Identity: Identity{Host: "github.com", Owner: "Acme", Repo: "Target"},
		Branch:   "feature/parser",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relative Parse\ngot:  %#v\nwant: %#v", got, want)
	}

	got, err = parseAndResolve(".", Options{
		SSH:        true,
		Roots:      []string{root},
		WorkingDir: workingDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.String() != "github.com/Acme/Current" ||
		got.CloneURL != "ssh://git@github.com/Acme/Current" {
		t.Fatalf("parseAndResolve(.) = %#v", got)
	}
}

func TestResolveLocalFileURL(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repos@work with space")
	repository := filepath.Join(root, "GitHub.COM", "Acme", "Repo.git")
	if err := os.MkdirAll(filepath.Dir(repository), 0o755); err != nil {
		t.Fatal(err)
	}

	inputPath := filepath.ToSlash(repository) + "@release/v1"
	if runtime.GOOS == "windows" {
		inputPath = "/" + inputPath
	}
	input := (&url.URL{Scheme: "file", Path: inputPath}).String()
	got, err := parseAndResolve(input, Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}

	clonePath := filepath.ToSlash(repository)
	if runtime.GOOS == "windows" {
		clonePath = "/" + clonePath
	}
	wantClone := (&url.URL{Scheme: "file", Path: clonePath}).String()
	if got.CloneURL != wantClone ||
		got.Identity.String() != "github.com/Acme/Repo" ||
		got.Branch != "release/v1" {
		t.Fatalf("parseAndResolve(%q) = %#v, want clone %q", input, got, wantClone)
	}
}

func TestRelativePathContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repos")
	outsideRoot := filepath.Join(base, "repos-elsewhere")
	outsideWorkingDir := filepath.Join(outsideRoot, "github.com", "Acme", "Current")
	if err := os.MkdirAll(outsideWorkingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	assertUsageError(t, "../Target", Options{
		Roots:      []string{root},
		WorkingDir: outsideWorkingDir,
	})
	assertUsageError(t, "./github.com//Acme/Repo", Options{
		Roots:      []string{root},
		WorkingDir: root,
	})
	assertUsageError(t, "./github.com/Other/../Acme/Repo", Options{
		Roots:      []string{root},
		WorkingDir: root,
	})

	if runtime.GOOS == "windows" {
		return
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "Acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "github.com")); err != nil {
		t.Fatal(err)
	}
	assertUsageError(t, "./github.com/Acme/Repo", Options{
		Roots:      []string{root},
		WorkingDir: root,
	})
}

func TestBareRepositoryResolverErrorsAreUsageErrors(t *testing.T) {
	resolverErr := errors.New("multiple authenticated hosts")
	_, err := parseAndResolve("Repo", Options{
		ResolveIdentity: func() (string, string, error) {
			return "", "", resolverErr
		},
	})
	if err == nil {
		t.Fatal("Parse succeeded")
	}
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("errors.As(%T) = false", err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("errors.Is(%v, ErrUsage) = false", err)
	}
	if !errors.Is(err, resolverErr) {
		t.Fatalf("resolver error was not preserved: %v", err)
	}
}

func TestResolveRejectsInvalidSpecifications(t *testing.T) {
	identity := func() (string, string, error) {
		return "github.com", "Acme", nil
	}

	tests := []struct {
		name    string
		input   string
		options Options
	}{
		{name: "empty", input: ""},
		{name: "leading whitespace", input: " Org/Repo"},
		{name: "trailing whitespace", input: "Org/Repo "},
		{name: "NUL", input: "Org/Repo\x00"},
		{name: "backslash", input: `Org\Repo`},
		{name: "bare without resolver", input: "Repo"},
		{
			name:  "bare with empty resolver identity",
			input: "Repo",
			options: Options{ResolveIdentity: func() (string, string, error) {
				return "", "", nil
			}},
		},
		{
			name:  "bare with malformed resolver host",
			input: "Repo",
			options: Options{ResolveIdentity: func() (string, string, error) {
				return "bad_host", "Acme", nil
			}},
		},
		{name: "missing repository", input: "Org/"},
		{name: "leading empty host", input: "/Org/Repo"},
		{name: "empty component", input: "Host//Repo"},
		{name: "too many components", input: "Host/Org/Repo/Extra"},
		{name: "dot owner", input: "Host/./Repo"},
		{name: "dot dot owner", input: "Host/../Repo"},
		{name: "repository becomes empty after git suffix", input: "Org/.git"},
		{name: "repository trailing dot", input: "Org/Repo."},
		{name: "unsafe repository colon", input: "Org/Repo:bad"},
		{name: "unsafe repository question mark", input: "Org/Repo?bad"},
		{name: "unsafe repository percent", input: "Org/Repo%2Fbad"},
		{name: "unsafe repository semicolon", input: "Org/Repo;bad"},
		{name: "URL missing repository", input: "https://example.com/Org"},
		{name: "URL missing owner", input: "https://example.com//Repo"},
		{name: "URL extra path", input: "https://example.com/Org/Repo/Extra"},
		{name: "URL encoded traversal", input: "https://example.com/Org/%2e%2e"},
		{name: "URL encoded separator", input: "https://example.com/Org/Repo%2FExtra"},
		{name: "URL query", input: "https://example.com/Org/Repo?q=1"},
		{name: "URL fragment", input: "https://example.com/Org/Repo#readme"},
		{name: "unsupported scheme", input: "ftp://example.com/Org/Repo"},
		{name: "URL password", input: "https://user:secret@example.com/Org/Repo"},
		{name: "bad DNS host", input: "https://bad_host/Org/Repo"},
		{name: "empty URL port", input: "https://example.com:/Org/Repo"},
		{name: "zero URL port", input: "https://example.com:0/Org/Repo"},
		{name: "oversized URL port", input: "https://example.com:65536/Org/Repo"},
		{name: "nonnumeric URL port", input: "https://example.com:no/Org/Repo"},
		{name: "IPv6 host", input: "https://[2001:db8::1]/Org/Repo"},
		{name: "zero shorthand port", input: "example.com:0/Org/Repo"},
		{name: "oversized shorthand port", input: "example.com:65536/Org/Repo"},
		{name: "scp multiple users", input: "one@two@example.com:Org/Repo"},
		{name: "scp missing path", input: "example.com:"},
		{name: "scp missing owner", input: "example.com:Repo"},
		{name: "scp extra path", input: "example.com:One/Two/Three"},
		{name: "empty branch", input: "Org/Repo@"},
		{name: "URL empty branch", input: "https://example.com/Org/Repo@"},
		{name: "file URL has user", input: "file://user@example.com/Org/Repo"},
		{name: "file URL has port", input: "file://example.com:1234/Org/Repo"},
		{name: "relative without roots", input: "../Repo", options: Options{ResolveIdentity: identity}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertUsageError(t, test.input, test.options)
		})
	}
}

func TestLocalFileURLMustBeUnderRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside", "github.com", "Org", "Repo")
	inputPath := filepath.ToSlash(outside)
	if runtime.GOOS == "windows" {
		inputPath = "/" + inputPath
	}
	input := (&url.URL{Scheme: "file", Path: inputPath}).String()
	assertUsageError(t, input, Options{Roots: []string{root}})

	traversalPath := filepath.ToSlash(filepath.Join(root, "github.com", "Other")) +
		"/../Org/Repo"
	if runtime.GOOS == "windows" {
		traversalPath = "/" + traversalPath
	}
	traversalURL := "file://" + traversalPath
	assertUsageError(t, traversalURL, Options{Roots: []string{root}})
}

func TestIdentityResolverIsOnlyUsedForBareRepositories(t *testing.T) {
	calls := 0
	resolver := func() (string, string, error) {
		calls++
		return "example.com", "User", nil
	}

	for _, input := range []string{
		"Org/Repo",
		"example.com/Org/Repo",
		"https://example.com/Org/Repo",
		"git@example.com:Org/Repo",
	} {
		if _, err := parseAndResolve(input, Options{ResolveIdentity: resolver}); err != nil {
			t.Fatalf("parseAndResolve(%q) error = %v", input, err)
		}
	}
	if calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", calls)
	}

	if _, err := parseAndResolve("Repo", Options{ResolveIdentity: resolver}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
}

func assertUsageError(t *testing.T, input string, options Options) {
	t.Helper()
	_, err := parseAndResolve(input, options)
	if err == nil {
		t.Fatalf("parseAndResolve(%q) succeeded", input)
	}
	var usageErr *UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("parseAndResolve(%q) error %T does not match *UsageError: %v", input, err, err)
	}
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("errors.Is(parseAndResolve(%q), ErrUsage) = false: %v", input, err)
	}
	if !strings.Contains(err.Error(), "invalid repository specification") {
		t.Fatalf("parseAndResolve(%q) error = %q", input, err)
	}
}

func TestResolveRejectsFileSchemeWhenConfigured(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repos")
	repository := filepath.Join(root, "GitHub.COM", "Acme", "Repo.git")
	if err := os.MkdirAll(filepath.Dir(repository), 0o755); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.ToSlash(repository)
	if runtime.GOOS == "windows" {
		localPath = "/" + localPath
	}
	localInput := (&url.URL{Scheme: "file", Path: localPath}).String()

	tests := []string{
		localInput,
		"file://ghe.example.com/Org/Repo.git",
		"file:///absolute/path",
		"file://localhost/absolute/path",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			assertUsageError(t, input, Options{Roots: []string{root}, RejectFileScheme: true})
		})
	}
}

func TestResolveAllowsFileSchemeByDefault(t *testing.T) {
	// clone is the only caller that sets RejectFileScheme; list and migrate
	// support file:// input with the default options.
	got, err := parseAndResolve("file://ghe.example.com/Org/Repo.git", Options{})
	if err != nil {
		t.Fatalf("parseAndResolve() error = %v, want file:// accepted when RejectFileScheme is unset", err)
	}
	if got.CloneURL != "file://ghe.example.com/Org/Repo.git" {
		t.Fatalf("CloneURL = %q, want the file:// URL preserved", got.CloneURL)
	}
}

func parseAndResolve(input string, options Options) (Spec, error) {
	parsed, err := Parse(input)
	if err != nil {
		return Spec{}, err
	}
	return parsed.Resolve(context.Background(), options)
}
