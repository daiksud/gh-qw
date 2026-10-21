package physicalpath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFilesystemResolveExistingPrefix(t *testing.T) {
	base := physicalTempDir(t)
	directory := filepath.Join(base, "directory")
	file := filepath.Join(base, "file")
	alias := filepath.Join(base, "alias")
	dangling := filepath.Join(base, "dangling")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	aliasErr := os.Symlink(directory, alias)
	danglingErr := os.Symlink(filepath.Join(base, "absent"), dangling)
	filesystem := Filesystem{}
	for _, test := range []struct {
		name, input, want string
		wantError         bool
	}{
		{"directory", directory, directory, false},
		{"regular final file", file, file, false},
		{"missing suffix", filepath.Join(directory, "missing", "child"), filepath.Join(directory, "missing", "child"), false},
		{"symlink prefix", filepath.Join(alias, "missing", "child"), filepath.Join(directory, "missing", "child"), false},
		{"dangling final", dangling, "", true},
		{"dangling prefix", filepath.Join(dangling, "child"), "", true},
		{"relative", "relative", "", true},
		{"empty", "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "symlink prefix" && aliasErr != nil {
				t.Skipf("symbolic link unavailable: %v", aliasErr)
			}
			if (test.name == "dangling final" || test.name == "dangling prefix") && danglingErr != nil {
				t.Skipf("symbolic link unavailable: %v", danglingErr)
			}
			got, err := filesystem.ResolveAbsolute(test.input)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("resolve(%q) = %q, %v; want %q, error=%v", test.input, got, err, test.want, test.wantError)
			}
		})
	}
	// Preserve the OS distinction between a missing path and a non-directory ancestor.
	child := filepath.Join(file, "child")
	_, statErr := os.Lstat(child)
	got, err := filesystem.ResolveAbsolute(child)
	if errors.Is(statErr, fs.ErrNotExist) {
		if err != nil || got != child {
			t.Fatalf("missing path resolution = %q, %v", got, err)
		}
	} else if err == nil {
		t.Fatal("non-directory ancestor accepted")
	}
}

func TestFilesystemEquivalentPaths(t *testing.T) {
	base := physicalTempDir(t)
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	if err := os.WriteFile(first, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linkErr := os.Link(first, second)
	filesystem := Filesystem{PathEqual: func(first, second string) bool { return first == second }}
	for _, test := range []struct {
		first, second string
		want          bool
	}{
		{first, second, true},
		{filepath.Join(base, "missing"), filepath.Join(base, "missing"), true},
		{filepath.Join(base, "missing"), filepath.Join(base, "other"), false},
	} {
		t.Run(filepath.Base(test.first)+"-"+filepath.Base(test.second), func(t *testing.T) {
			if test.first == first && linkErr != nil {
				t.Skipf("hard link unavailable: %v", linkErr)
			}
			got, err := filesystem.Equivalent(test.first, test.second)
			if err != nil || got != test.want {
				t.Fatalf("equivalence = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPathPrefixSafety(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "roots", "repo")
	if StrictlyWithin(base, filepath.Join(string(filepath.Separator), "roots", "repository", "child")) {
		t.Fatal("StrictlyWithin() accepted a raw prefix collision")
	}
	if !StrictlyWithin(base, filepath.Join(base, "child")) {
		t.Fatal("StrictlyWithin() rejected a real descendant")
	}

	if runtime.GOOS == "windows" {
		if !EqualFoldASCII(`C:\ROOT\Repo`, `c:\root\repo`) {
			t.Fatal("EqualFoldASCII() rejected a Windows case-only difference")
		}
	}
}

func TestFilesystemPreservesObservationErrors(t *testing.T) {
	base := physicalTempDir(t)
	failure := errors.New("observation failed")
	for _, filesystem := range []Filesystem{
		{Lstat: func(string) (fs.FileInfo, error) { return nil, failure }},
		{EvalSymlinks: func(string) (string, error) { return "", failure }},
	} {
		if _, err := filesystem.ResolveAbsolute(base); !errors.Is(err, failure) {
			t.Fatalf("resolve error=%v", err)
		}
	}
	filesystem := Filesystem{Stat: func(string) (fs.FileInfo, error) { return nil, failure }}
	if _, err := filesystem.Equivalent(filepath.Join(base, "one"), filepath.Join(base, "two")); !errors.Is(err, failure) {
		t.Fatalf("equivalence error=%v", err)
	}
}

func TestFilesystemEquivalenceUsesResolvedPathPolicy(t *testing.T) {
	base := physicalTempDir(t)
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	calls := 0
	filesystem := Filesystem{
		EvalSymlinks: func(path string) (string, error) { return filepath.Join(path, "physical"), nil },
		PathEqual: func(left, right string) bool {
			calls++
			if left != filepath.Join(base, "physical", "first") || right != filepath.Join(base, "physical", "second") {
				t.Fatalf("comparison received unresolved paths %q and %q", left, right)
			}
			return true
		},
	}
	equal, err := filesystem.Equivalent(first, second)
	if err != nil || !equal || calls != 1 {
		t.Fatalf("equivalence = %v, %v; policy calls=%d", equal, err, calls)
	}
}
