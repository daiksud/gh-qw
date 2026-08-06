package migrate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestMoveFallsBackAcrossDevicesAndPreservesTree(t *testing.T) {
	t.Parallel()

	base := moveTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination")
	destination := filepath.Join(destinationRoot, "host", "owner", "repo")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "nested", "file")
	if err := os.WriteFile(file, []byte("content"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("nested", "file"), filepath.Join(source, "link")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if err := os.Mkdir(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Move(source, destination, MoveOptions{
		DestinationRoot: destinationRoot,
		Filesystem: Filesystem{
			Rename: func(oldPath, newPath string) error {
				return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
			},
		},
	})
	if err != nil {
		t.Fatalf("Move() error = %v", err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source lstat error = %v, want not exist", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "nested", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "content"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Join(destination, "nested", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if got, want := info.Mode().Perm(), fs.FileMode(0o640); got != want {
			t.Fatalf("mode = %v, want %v", got, want)
		}
	}
	target, err := os.Readlink(filepath.Join(destination, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := target, filepath.Join("nested", "file"); got != want {
		t.Fatalf("symlink target = %q, want %q", got, want)
	}
}

func TestMoveCopyFailureCleansOnlyPartialDestination(t *testing.T) {
	t.Parallel()

	base := moveTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination")
	destination := filepath.Join(destinationRoot, "host", "owner", "repo")
	sibling := filepath.Join(destinationRoot, "keep")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("copy failed")

	err := Move(source, destination, MoveOptions{
		DestinationRoot: destinationRoot,
		Filesystem: Filesystem{
			Rename: func(oldPath, newPath string) error {
				return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
			},
			CopyFile: func(string, string, fs.FileMode) error {
				return wantErr
			},
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Move() error = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination lstat error = %v, want not exist", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling was removed: %v", err)
	}
}

func TestMoveRefusesDestinationCollision(t *testing.T) {
	t.Parallel()

	base := moveTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination")
	destination := filepath.Join(destinationRoot, "host", "owner", "repo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Move(source, destination, MoveOptions{DestinationRoot: destinationRoot})
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Move() error = %v, want ErrDestinationExists", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was changed: %v", err)
	}
}

func TestMoveDoesNotCleanDestinationCreatedDuringRename(t *testing.T) {
	t.Parallel()

	base := moveTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination")
	destination := filepath.Join(destinationRoot, "host", "owner", "repo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Move(source, destination, MoveOptions{
		DestinationRoot: destinationRoot,
		Filesystem: Filesystem{
			Rename: func(oldPath, newPath string) error {
				if err := os.MkdirAll(newPath, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(newPath, "keep"), []byte("keep"), 0o644); err != nil {
					return err
				}
				return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
			},
		},
	})
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Move() error = %v, want ErrDestinationExists", err)
	}
	if content, readErr := os.ReadFile(filepath.Join(destination, "keep")); readErr != nil || string(content) != "keep" {
		t.Fatalf("concurrent destination was cleaned: content %q, error %v", content, readErr)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source was changed: %v", err)
	}
}

func TestDestinationPathTreatsDanglingSymlinkAsCollision(t *testing.T) {
	t.Parallel()

	root := moveTestPhysical(t, t.TempDir())
	parent := filepath.Join(root, "github.com", "owner")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "repo")
	if err := os.Symlink(filepath.Join(root, "missing"), target); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}

	destination, err := DestinationPath(root, "github.com", "owner", "repo", Filesystem{})
	if err != nil {
		t.Fatalf("DestinationPath() error = %v", err)
	}
	if destination != target {
		t.Fatalf("destination = %q, want %q", destination, target)
	}
	physical, exists, err := CheckDestination(root, destination, Filesystem{})
	if err != nil {
		t.Fatalf("CheckDestination() error = %v", err)
	}
	if !exists || physical != target {
		t.Fatalf("CheckDestination() = %q, %v, want %q, true", physical, exists, target)
	}
}

func moveTestPhysical(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
