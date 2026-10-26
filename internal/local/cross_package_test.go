package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/daiksud/gh-qw/internal/root"
)

type staticConfigLoader struct {
	config config.Config
}

func (l staticConfigLoader) Load() (config.Config, error) {
	return l.config, nil
}

func TestRepositorySpecificationsRoundTripThroughDeterministicMainPath(t *testing.T) {
	rootPath := physicalTestDir(t)
	const identity = "ghe.example.com/Acme/Widget"
	wantPath := filepath.Join(rootPath, "ghe.example.com", "Acme", "Widget")
	inputs := []string{
		"GHE.Example.COM:8443/Acme/Widget.git",
		"https://GHE.Example.COM/Acme/Widget.git@feature/login",
		"ssh://deploy@GHE.Example.COM:2222/Acme/Widget.git",
		"deploy@GHE.Example.COM:Acme/Widget.git",
		"file://GHE.Example.COM/Acme/Widget.git",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			parsed, err := repospec.Parse(input)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := parsed.Resolve(context.Background(), repospec.Options{})
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", input, err)
			}
			if spec.Identity.String() != identity {
				t.Fatalf("Parse(%q) identity = %q, want %q", input, spec.Identity.String(), identity)
			}

			path, err := local.MainPath(rootPath, spec.Host, spec.Owner, spec.Repo)
			if err != nil {
				t.Fatalf("MainPath(%q) error = %v", spec.Identity.String(), err)
			}
			if path != wantPath {
				t.Fatalf("MainPath(%q) = %q, want %q", spec.Identity.String(), path, wantPath)
			}

			parts, err := local.ParseIdentity(spec.Identity.String())
			if err != nil {
				t.Fatalf("ParseIdentity(%q) error = %v", spec.Identity.String(), err)
			}
			if parts.Host != spec.Host || parts.Owner != spec.Owner || parts.Repo != spec.Repo {
				t.Fatalf("ParseIdentity(%q) = %#v, want parser components %#v", spec.Identity.String(), parts, spec)
			}
			normalized, err := local.NormalizeIdentityForOutput(spec.Identity.String())
			if err != nil {
				t.Fatalf("NormalizeIdentityForOutput(%q) error = %v", spec.Identity.String(), err)
			}
			if normalized != identity {
				t.Fatalf("NormalizeIdentityForOutput(%q) = %q, want %q", spec.Identity.String(), normalized, identity)
			}
		})
	}

	if err := os.MkdirAll(wantPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", wantPath, err)
	}
	parsed, err := repospec.Parse(".")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.Resolve(context.Background(), repospec.Options{
		Roots:      []string{rootPath},
		WorkingDir: wantPath,
	})
	if err != nil {
		t.Fatalf("Parse(.) from deterministic path error = %v", err)
	}
	if roundTrip.Identity.String() != identity {
		t.Fatalf("Parse(.) identity = %q, want %q", roundTrip.Identity.String(), identity)
	}
}

func TestResolvedWorktreeRootRejectsRuntimeSymlinkEscape(t *testing.T) {
	base := physicalTestDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(repositoryRoot, "worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	resolver := root.NewResolverWithOptions(root.Options{
		ConfigLoader: staticConfigLoader{config: config.Config{
			Roots: []string{repositoryRoot},
		}},
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	resolved, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.WorktreeRoot != worktreeRoot {
		t.Fatalf("WorktreeRoot = %q, want %q", resolved.WorktreeRoot, worktreeRoot)
	}

	path, err := local.WorktreePath(
		context.Background(),
		resolved.WorktreeRoot,
		"github.com",
		"acme",
		"widget",
		"feature-login",
	)
	if err != nil {
		t.Fatalf("WorktreePath() error = %v", err)
	}
	wantPath := filepath.Join(worktreeRoot, "feature-login", "widget")
	if path != wantPath {
		t.Fatalf("WorktreePath() = %q, want %q", path, wantPath)
	}

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(worktreeRoot, "feature-login")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	_, err = local.WorktreePath(
		context.Background(), resolved.WorktreeRoot,
		"github.com", "acme", "widget", "feature-login",
	)
	if !errors.Is(err, root.ErrUnsafeTarget) {
		t.Fatalf("WorktreePath() symlink escape = %v, want root.ErrUnsafeTarget", err)
	}
	var safetyError *root.SafetyError
	if !errors.As(err, &safetyError) {
		t.Fatalf("WorktreePath() error = %T %v, want *root.SafetyError", err, err)
	}
	if safetyError.Root() != worktreeRoot {
		t.Fatalf("SafetyError.Root() = %q, want %q", safetyError.Root(), worktreeRoot)
	}
}

func physicalTestDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return physical
}
