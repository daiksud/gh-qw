package local

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverRepositoriesExactDepthOrderDuplicatesAndSkips(t *testing.T) {
	base := physicalTempDir(t)
	rootOne := filepath.Join(base, "root-one")
	rootTwo := filepath.Join(base, "root-two")
	if err := os.MkdirAll(rootOne, 0o755); err != nil {
		t.Fatalf("MkdirAll(rootOne) error = %v", err)
	}
	if err := os.MkdirAll(rootTwo, 0o755); err != nil {
		t.Fatalf("MkdirAll(rootTwo) error = %v", err)
	}

	initTestRepository(t, filepath.Join(rootOne, "github.com", "zeta", "zed"))
	initTestRepository(t, filepath.Join(rootOne, "github.com", "acme", "widget"))
	source := filepath.Join(rootOne, "github.com", "acme", "source")
	initTestRepository(t, source)
	initTestRepository(t, filepath.Join(rootTwo, "github.com", "acme", "widget"))
	initTestRepository(t, filepath.Join(rootTwo, "git.example.com", "acme", "api"))

	if err := os.MkdirAll(filepath.Join(rootOne, "github.com", "acme", "plain"), 0o755); err != nil {
		t.Fatalf("MkdirAll(non-git) error = %v", err)
	}
	fake := filepath.Join(rootOne, "github.com", "acme", "fake")
	if err := os.MkdirAll(filepath.Join(fake, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(fake .git) error = %v", err)
	}
	initTestBareRepository(t, filepath.Join(rootOne, "github.com", "acme", "bare.git"))

	linked := filepath.Join(rootOne, "github.com", "acme", "linked")
	addTestWorktree(t, source, linked, "-b", "linked-test")

	deep := filepath.Join(rootOne, "github.com", "acme", "container", "nested")
	initTestRepository(t, deep)

	outside := filepath.Join(base, "outside", "escape")
	initTestRepository(t, outside)
	escape := filepath.Join(rootOne, "github.com", "acme", "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("Symlink(repository escape) error = %v", err)
	}

	internalTarget := filepath.Join(rootOne, "_storage", "internal-target")
	initTestRepository(t, internalTarget)
	internalAlias := filepath.Join(rootOne, "github.com", "acme", "alias")
	if err := os.Symlink(internalTarget, internalAlias); err != nil {
		t.Fatalf("Symlink(contained repository) error = %v", err)
	}

	symlinkGit := filepath.Join(rootOne, "github.com", "acme", "symlink-git")
	if err := os.MkdirAll(symlinkGit, 0o755); err != nil {
		t.Fatalf("MkdirAll(symlink-git) error = %v", err)
	}
	if err := os.Symlink(filepath.Join(source, ".git"), filepath.Join(symlinkGit, ".git")); err != nil {
		t.Fatalf("Symlink(.git) error = %v", err)
	}

	result, err := DiscoverRepositories(context.Background(), []string{rootOne, rootTwo})
	if err != nil {
		t.Fatalf("DiscoverRepositories() error = %v", err)
	}

	var identities []string
	var indices []int
	for _, repository := range result.Repositories {
		identities = append(identities, repository.Identity)
		indices = append(indices, repository.RootIndex)
		if !filepath.IsAbs(repository.Path) || !filepath.IsAbs(repository.Root) {
			t.Fatalf("repository paths are not absolute: %#v", repository)
		}
	}
	wantIdentities := []string{
		"github.com/acme/alias",
		"github.com/acme/source",
		"github.com/acme/widget",
		"github.com/zeta/zed",
		"git.example.com/acme/api",
		"github.com/acme/widget",
	}
	wantIndices := []int{0, 0, 0, 0, 1, 1}
	if !reflect.DeepEqual(identities, wantIdentities) {
		t.Fatalf("discovered identities = %#v, want %#v", identities, wantIdentities)
	}
	if !reflect.DeepEqual(indices, wantIndices) {
		t.Fatalf("root indices = %#v, want %#v", indices, wantIndices)
	}

	for _, skipped := range []string{
		"github.com/acme/plain",
		"github.com/acme/fake",
		"github.com/acme/bare",
		"github.com/acme/linked",
		"github.com/acme/container",
		"github.com/acme/nested",
		"github.com/acme/escape",
		"github.com/acme/symlink-git",
	} {
		for _, identity := range identities {
			if identity == skipped {
				t.Fatalf("discovery unexpectedly included %q", skipped)
			}
		}
	}

	if len(result.Warnings) == 0 {
		t.Fatal("DiscoverRepositories() returned no warnings for unsafe entries")
	}
	var sawEscape, sawSymlinkGit, sawFake bool
	for _, warning := range result.Warnings {
		switch warning.Path {
		case escape:
			sawEscape = warning.Kind == WarningUnsafe
		case filepath.Join(symlinkGit, ".git"):
			sawSymlinkGit = warning.Kind == WarningUnsafe
		case fake:
			sawFake = warning.Kind == WarningInspection
		}
	}
	if !sawEscape || !sawSymlinkGit || !sawFake {
		t.Fatalf(
			"warnings did not include expected entries: escape=%v symlinkGit=%v fake=%v; %#v",
			sawEscape,
			sawSymlinkGit,
			sawFake,
			result.Warnings,
		)
	}
}

func TestDiscoverRepositoriesReportsPermissionsAndContinues(t *testing.T) {
	rootPath := physicalTempDir(t)
	visible := filepath.Join(rootPath, "github.com", "acme", "visible")
	blockedOwner := filepath.Join(rootPath, "github.com", "blocked")
	initTestRepository(t, visible)
	initTestRepository(t, filepath.Join(blockedOwner, "hidden"))

	standardReadDir := os.ReadDir
	var sinkWarnings []Warning
	result, err := DiscoverRepositories(
		context.Background(),
		[]string{rootPath},
		DiscoveryOptions{
			Filesystem: FilesystemOptions{
				ReadDir: func(path string) ([]os.DirEntry, error) {
					if path == blockedOwner {
						return nil, errors.Join(errors.New("injected read failure"), fs.ErrPermission)
					}
					return standardReadDir(path)
				},
			},
			Warn: func(warning Warning) {
				sinkWarnings = append(sinkWarnings, warning)
			},
		},
	)
	if err != nil {
		t.Fatalf("DiscoverRepositories() error = %v", err)
	}
	if len(result.Repositories) != 1 ||
		result.Repositories[0].Identity != "github.com/acme/visible" {
		t.Fatalf("repositories = %#v", result.Repositories)
	}

	var permissionWarning bool
	for _, warning := range result.Warnings {
		if warning.Path == blockedOwner && warning.Kind == WarningPermission {
			permissionWarning = true
		}
	}
	if !permissionWarning {
		t.Fatalf("warnings = %#v, want permission warning for %q", result.Warnings, blockedOwner)
	}
	if !reflect.DeepEqual(sinkWarnings, result.Warnings) {
		t.Fatalf("warning sink = %#v, result warnings = %#v", sinkWarnings, result.Warnings)
	}
}

func TestDiscoverRepositoriesMissingRootIsEmptyWithoutWarning(t *testing.T) {
	missing := filepath.Join(physicalTempDir(t), "missing")
	result, err := DiscoverRepositories(context.Background(), []string{missing})
	if err != nil {
		t.Fatalf("DiscoverRepositories() error = %v", err)
	}
	if len(result.Repositories) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("DiscoverRepositories(missing) = %#v", result)
	}
}
