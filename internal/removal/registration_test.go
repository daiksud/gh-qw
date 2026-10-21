package removal

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestRegistrationPathsPhysicalizeMissingSymlinkSuffix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation requires privileges on Windows")
	}

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	physicalParent := filepath.Join(root, "physical")
	if err := os.MkdirAll(physicalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(root, "alias")
	if err := os.Symlink(physicalParent, aliasParent); err != nil {
		t.Fatal(err)
	}
	registered := filepath.Join(aliasParent, "missing", "child")
	paths, err := RegistrationPaths(Filesystem{
		Lstat:        os.Lstat,
		EvalSymlinks: filepath.EvalSymlinks,
	}, registered)
	if err != nil {
		t.Fatalf("RegistrationPaths() error = %v", err)
	}
	want := []string{
		filepath.Clean(registered),
		filepath.Join(physicalParent, "missing", "child"),
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("registration paths = %#v, want %#v", paths, want)
	}
}
