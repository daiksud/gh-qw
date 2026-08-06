package local_test

import (
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
			spec, err := repospec.Parse(input, repospec.Options{})
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", input, err)
			}
			if spec.Identity != identity {
				t.Fatalf("Parse(%q) identity = %q, want %q", input, spec.Identity, identity)
			}

			path, err := local.MainPath(rootPath, spec.Host, spec.Owner, spec.Repo)
			if err != nil {
				t.Fatalf("MainPath(%q) error = %v", spec.Identity, err)
			}
			if path != wantPath {
				t.Fatalf("MainPath(%q) = %q, want %q", spec.Identity, path, wantPath)
			}

			parts, err := local.ParseIdentity(spec.Identity)
			if err != nil {
				t.Fatalf("ParseIdentity(%q) error = %v", spec.Identity, err)
			}
			if parts.Host != spec.Host || parts.Owner != spec.Owner || parts.Repo != spec.Repo {
				t.Fatalf("ParseIdentity(%q) = %#v, want parser components %#v", spec.Identity, parts, spec)
			}
			normalized, err := local.NormalizeIdentityForOutput(spec.Identity)
			if err != nil {
				t.Fatalf("NormalizeIdentityForOutput(%q) error = %v", spec.Identity, err)
			}
			if normalized != identity {
				t.Fatalf("NormalizeIdentityForOutput(%q) = %q, want %q", spec.Identity, normalized, identity)
			}
		})
	}

	if err := os.MkdirAll(wantPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", wantPath, err)
	}
	roundTrip, err := repospec.Parse(".", repospec.Options{
		Roots:      []string{rootPath},
		WorkingDir: wantPath,
	})
	if err != nil {
		t.Fatalf("Parse(.) from deterministic path error = %v", err)
	}
	if roundTrip.Identity != identity {
		t.Fatalf("Parse(.) identity = %q, want %q", roundTrip.Identity, identity)
	}
}

func TestResolvedWorktreeRootContainsSlashSlotsAndRejectsRuntimeSymlinkEscape(t *testing.T) {
	base := physicalTestDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	physicalWorktreeRoot := filepath.Join(base, "physical-worktrees")
	worktreeRootAlias := filepath.Join(base, "worktree-root-alias")
	for _, path := range []string{repositoryRoot, physicalWorktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}
	if err := os.Symlink(physicalWorktreeRoot, worktreeRootAlias); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	resolver := root.NewResolverWithOptions(root.Options{
		ConfigLoader: staticConfigLoader{config: config.Config{
			Roots:        []string{repositoryRoot},
			WorktreeRoot: worktreeRootAlias,
		}},
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	resolved, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.WorktreeRoot != physicalWorktreeRoot {
		t.Fatalf("WorktreeRoot = %q, want physical path %q", resolved.WorktreeRoot, physicalWorktreeRoot)
	}

	path, err := local.WorktreePath(
		resolved.WorktreeRoot,
		"github.com",
		"acme",
		"widget",
		"feature/login",
	)
	if err != nil {
		t.Fatalf("WorktreePath(feature/login) error = %v", err)
	}
	wantPath := filepath.Join(
		physicalWorktreeRoot,
		"github.com",
		"acme",
		"widget",
		"feature",
		"login",
	)
	if path != wantPath {
		t.Fatalf("WorktreePath(feature/login) = %q, want %q", path, wantPath)
	}

	worktreeBase, err := local.WorktreeBasePath(
		resolved.WorktreeRoot,
		"github.com",
		"acme",
		"widget",
	)
	if err != nil {
		t.Fatalf("WorktreeBasePath() error = %v", err)
	}
	if err := os.MkdirAll(worktreeBase, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", worktreeBase, err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", outside, err)
	}
	if err := os.Symlink(outside, filepath.Join(worktreeBase, "feature")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	_, err = local.WorktreePath(
		resolved.WorktreeRoot,
		"github.com",
		"acme",
		"widget",
		"feature/login",
	)
	if !errors.Is(err, root.ErrUnsafeTarget) {
		t.Fatalf("WorktreePath() symlink escape error = %v, want root.ErrUnsafeTarget", err)
	}
	var safetyError *root.SafetyError
	if !errors.As(err, &safetyError) {
		t.Fatalf("WorktreePath() error = %T %v, want *root.SafetyError", err, err)
	}
	if safetyError.Root() != worktreeBase {
		t.Fatalf("SafetyError.Root() = %q, want per-repository boundary %q", safetyError.Root(), worktreeBase)
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
