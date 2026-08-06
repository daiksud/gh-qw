package root

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/config"
)

func TestPhysicalizeTargetContainmentModes(t *testing.T) {
	base, home := newLayout(t)
	rootPath := filepath.Join(base, "repositories")
	mustMkdirAll(t, rootPath)
	physicalRoot := mustEvalSymlinks(t, rootPath)
	resolver, _ := newTestResolver(home, config.Config{}, nil)

	t.Run("strict descendant", func(t *testing.T) {
		target := filepath.Join(rootPath, "github.com", "acme", "widget")
		got, err := resolver.PhysicalizeTarget(physicalRoot, target, StrictlyUnder)
		if err != nil {
			t.Fatalf("PhysicalizeTarget() error = %v", err)
		}
		want := filepath.Join(physicalRoot, "github.com", "acme", "widget")
		if got != want {
			t.Fatalf("PhysicalizeTarget() = %q, want %q", got, want)
		}
		assertDoesNotExist(t, filepath.Join(rootPath, "github.com"))
	})

	t.Run("equal rejected in strict mode", func(t *testing.T) {
		_, err := resolver.PhysicalizeTarget(physicalRoot, rootPath, StrictlyUnder)
		assertSafetyError(t, err)
		if !strings.Contains(err.Error(), "strictly under") {
			t.Fatalf("PhysicalizeTarget() error = %v, want strict-mode reason", err)
		}
	})

	t.Run("equal accepted explicitly", func(t *testing.T) {
		got, err := resolver.PhysicalizeTarget(physicalRoot, rootPath, AllowEqual)
		if err != nil {
			t.Fatalf("PhysicalizeTarget() error = %v", err)
		}
		if got != physicalRoot {
			t.Fatalf("PhysicalizeTarget() = %q, want root %q", got, physicalRoot)
		}
	})

	t.Run("missing root and target remain deterministic", func(t *testing.T) {
		missingRoot := filepath.Join(mustEvalSymlinks(t, base), "future-root")
		target := filepath.Join(base, "future-root", "child")
		got, err := resolver.PhysicalizeTarget(missingRoot, target, StrictlyUnder)
		if err != nil {
			t.Fatalf("PhysicalizeTarget() error = %v", err)
		}
		if got != filepath.Join(missingRoot, "child") {
			t.Fatalf("PhysicalizeTarget() = %q, want missing descendant", got)
		}
		assertDoesNotExist(t, filepath.Join(base, "future-root"))
	})

	t.Run("outside target", func(t *testing.T) {
		target := filepath.Join(base, "outside", "widget")
		_, err := resolver.PhysicalizeTarget(physicalRoot, target, StrictlyUnder)
		assertSafetyError(t, err)
	})

	t.Run("component prefix is outside", func(t *testing.T) {
		target := filepath.Join(base, "repositories-other", "widget")
		_, err := resolver.PhysicalizeTarget(physicalRoot, target, StrictlyUnder)
		assertSafetyError(t, err)
	})

	t.Run("existing file target is allowed when contained", func(t *testing.T) {
		target := filepath.Join(rootPath, "metadata")
		mustWriteFile(t, target)
		got, err := resolver.PhysicalizeTarget(physicalRoot, target, StrictlyUnder)
		if err != nil {
			t.Fatalf("PhysicalizeTarget() error = %v", err)
		}
		if got != filepath.Join(physicalRoot, "metadata") {
			t.Fatalf("PhysicalizeTarget() = %q, want contained file", got)
		}
	})
}

func TestPhysicalizeTargetRejectsSymlinkEscapes(t *testing.T) {
	t.Run("intermediate symlink escape", func(t *testing.T) {
		base, home := newLayout(t)
		rootPath := filepath.Join(base, "repositories")
		outside := filepath.Join(base, "outside")
		link := filepath.Join(rootPath, "escape")
		mustMkdirAll(t, rootPath)
		mustMkdirAll(t, outside)
		mustSymlink(t, outside, link)
		resolver, _ := newTestResolver(home, config.Config{}, nil)

		target := filepath.Join(link, "future", "widget")
		_, err := resolver.PhysicalizeTarget(
			mustEvalSymlinks(t, rootPath),
			target,
			StrictlyUnder,
		)
		assertSafetyError(t, err)
		physicalTarget := filepath.Join(mustEvalSymlinks(t, outside), "future", "widget")
		if !strings.Contains(err.Error(), strconv.Quote(physicalTarget)) {
			t.Fatalf("PhysicalizeTarget() error = %v, want physical escape path", err)
		}
	})

	t.Run("final symlink escape", func(t *testing.T) {
		base, home := newLayout(t)
		rootPath := filepath.Join(base, "repositories")
		outside := filepath.Join(base, "outside")
		link := filepath.Join(rootPath, "escape")
		mustMkdirAll(t, rootPath)
		mustMkdirAll(t, outside)
		mustSymlink(t, outside, link)
		resolver, _ := newTestResolver(home, config.Config{}, nil)

		_, err := resolver.PhysicalizeTarget(
			mustEvalSymlinks(t, rootPath),
			link,
			StrictlyUnder,
		)
		assertSafetyError(t, err)
	})

	t.Run("symlink resolving within root is allowed", func(t *testing.T) {
		base, home := newLayout(t)
		rootPath := filepath.Join(base, "repositories")
		realDirectory := filepath.Join(rootPath, "real")
		link := filepath.Join(rootPath, "alias")
		mustMkdirAll(t, realDirectory)
		mustSymlink(t, realDirectory, link)
		resolver, _ := newTestResolver(home, config.Config{}, nil)

		got, err := resolver.PhysicalizeTarget(
			mustEvalSymlinks(t, rootPath),
			filepath.Join(link, "future"),
			StrictlyUnder,
		)
		if err != nil {
			t.Fatalf("PhysicalizeTarget() error = %v", err)
		}
		want := filepath.Join(mustEvalSymlinks(t, realDirectory), "future")
		if got != want {
			t.Fatalf("PhysicalizeTarget() = %q, want %q", got, want)
		}
	})

	t.Run("root replacement cannot move trusted boundary", func(t *testing.T) {
		base, home := newLayout(t)
		configuredRoot := filepath.Join(base, "repositories")
		originalRoot := filepath.Join(base, "repositories-original")
		outside := filepath.Join(base, "outside")
		worktrees := filepath.Join(base, "worktrees")
		mustMkdirAll(t, configuredRoot)
		mustMkdirAll(t, outside)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{configuredRoot},
			WorktreeRoot: worktrees,
		}, nil)
		resolved, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}

		if err := os.Rename(configuredRoot, originalRoot); err != nil {
			t.Fatalf("Rename(%q, %q) error = %v", configuredRoot, originalRoot, err)
		}
		mustSymlink(t, outside, configuredRoot)

		_, err = resolver.PhysicalizeTarget(
			resolved.Primary(),
			filepath.Join(resolved.Primary(), "future"),
			StrictlyUnder,
		)
		assertSafetyError(t, err)
	})
}

func TestPhysicalizeTargetValidationAndRuntimeErrors(t *testing.T) {
	base, home := newLayout(t)
	rootPath := filepath.Join(base, "repositories")
	mustMkdirAll(t, rootPath)
	physicalRoot := mustEvalSymlinks(t, rootPath)
	resolver, _ := newTestResolver(home, config.Config{}, nil)

	tests := []struct {
		name   string
		root   string
		target string
		mode   ContainmentMode
	}{
		{
			name:   "empty root",
			root:   "",
			target: filepath.Join(rootPath, "child"),
			mode:   StrictlyUnder,
		},
		{
			name:   "relative root",
			root:   "relative-root",
			target: filepath.Join(rootPath, "child"),
			mode:   StrictlyUnder,
		},
		{
			name:   "empty target",
			root:   physicalRoot,
			target: "",
			mode:   StrictlyUnder,
		},
		{
			name:   "relative target",
			root:   physicalRoot,
			target: "relative-target",
			mode:   StrictlyUnder,
		},
		{
			name:   "unexpanded target",
			root:   physicalRoot,
			target: "~/target",
			mode:   StrictlyUnder,
		},
		{
			name:   "unknown mode",
			root:   physicalRoot,
			target: filepath.Join(rootPath, "child"),
			mode:   ContainmentMode(100),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolver.PhysicalizeTarget(test.root, test.target, test.mode)
			assertSafetyError(t, err)
		})
	}

	t.Run("existing non-directory intermediate component", func(t *testing.T) {
		filePath := filepath.Join(rootPath, "file")
		mustWriteFile(t, filePath)
		_, err := resolver.PhysicalizeTarget(
			physicalRoot,
			filepath.Join(filePath, "child"),
			StrictlyUnder,
		)
		assertSafetyError(t, err)
		if !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("PhysicalizeTarget() error = %v, want path-shape reason", err)
		}
	})

	t.Run("dangling symlink", func(t *testing.T) {
		link := filepath.Join(rootPath, "dangling")
		mustSymlink(t, filepath.Join(rootPath, "missing"), link)
		_, err := resolver.PhysicalizeTarget(physicalRoot, link, StrictlyUnder)
		assertSafetyError(t, err)
		if !strings.Contains(err.Error(), "dangling symbolic link") {
			t.Fatalf("PhysicalizeTarget() error = %v, want dangling-symlink reason", err)
		}
	})

	t.Run("filesystem error remains runtime error", func(t *testing.T) {
		deniedPath := filepath.Join(rootPath, "denied")
		guard := NewResolverWithOptions(Options{
			Lstat: func(path string) (fs.FileInfo, error) {
				if path == deniedPath {
					return nil, &os.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
				}
				return os.Lstat(path)
			},
		})

		_, err := guard.PhysicalizeTarget(physicalRoot, deniedPath, StrictlyUnder)
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("PhysicalizeTarget() error = %v, want permission error", err)
		}
		if errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("PhysicalizeTarget() error = %v, must remain a runtime error", err)
		}
	})
}

func TestPackagePhysicalizeTarget(t *testing.T) {
	base, _ := newLayout(t)
	rootPath := filepath.Join(base, "repositories")
	mustMkdirAll(t, rootPath)
	physicalRoot := mustEvalSymlinks(t, rootPath)

	got, err := PhysicalizeTarget(
		physicalRoot,
		filepath.Join(rootPath, "child"),
		StrictlyUnder,
	)
	if err != nil {
		t.Fatalf("PhysicalizeTarget() error = %v", err)
	}
	if got != filepath.Join(physicalRoot, "child") {
		t.Fatalf("PhysicalizeTarget() = %q, want contained child", got)
	}
}

func TestLexicalRelationshipUsesComponentsAndVolumes(t *testing.T) {
	resolver := NewResolver()
	base := filepath.Clean(string(filepath.Separator) + filepath.Join("base", "repos"))

	tests := []struct {
		name   string
		first  string
		second string
		want   pathRelation
	}{
		{
			name:   "equal cleaned path",
			first:  base,
			second: filepath.Join(base, "."),
			want:   relationEqual,
		},
		{
			name:   "strict descendant",
			first:  base,
			second: filepath.Join(base, "owner", "repo"),
			want:   relationContains,
		},
		{
			name:   "strict ancestor",
			first:  filepath.Join(base, "owner"),
			second: base,
			want:   relationInside,
		},
		{
			name:   "component prefix false positive",
			first:  base,
			second: base + "-other",
			want:   relationDisjoint,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolver.lexicalRelationship(test.first, test.second); got != test.want {
				t.Fatalf("lexicalRelationship(%q, %q) = %v, want %v", test.first, test.second, got, test.want)
			}
		})
	}

	if runtime.GOOS == "windows" {
		if got := resolver.lexicalRelationship(`C:\repos`, `D:\repos\child`); got != relationDisjoint {
			t.Fatalf("cross-volume relationship = %v, want disjoint", got)
		}
		if got := resolver.lexicalRelationship(`C:\Repos`, `c:\repos\child`); got != relationContains {
			t.Fatalf("case-folded Windows relationship = %v, want contains", got)
		}
	}
}

func assertSafetyError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want unsafe target")
	}
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("error = %v, want errors.Is(..., ErrUnsafeTarget)", err)
	}
	var safetyError *SafetyError
	if !errors.As(err, &safetyError) {
		t.Fatalf("error type = %T, want *SafetyError", err)
	}
	if safetyError.Reason() == "" {
		t.Fatal("SafetyError.Reason() is empty")
	}
}
