package removal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeRetainsIdentityAcrossReplacement(t *testing.T) {
	t.Parallel()
	filesystem := Filesystem{Lstat: os.Lstat, EvalSymlinks: filepath.EvalSymlinks, SameFile: os.SameFile}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	node, err := CaptureNode(filesystem, root, path, Directory, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := RevalidateNode(filesystem, node, "target"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RevalidateNode(filesystem, node, "target"); !errors.Is(err, ErrSafety) {
		t.Fatalf("RevalidateNode() = %v, want identity refusal", err)
	}
}

func TestCaptureNodeRejectsUnsafeShapeAndBoundary(t *testing.T) {
	t.Parallel()
	filesystem := Filesystem{Lstat: os.Lstat, EvalSymlinks: filepath.EvalSymlinks, SameFile: os.SameFile}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, boundary, path string
		kind                 Kind
	}{
		{"boundary itself", root, root, Directory},
		{"outside boundary", filepath.Join(root, "child"), root, Directory},
		{"wrong shape", root, file, Directory},
		{"relative path", root, "file", RegularFile},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CaptureNode(filesystem, test.boundary, test.path, test.kind, true); err == nil {
				t.Fatal("CaptureNode() accepted unsafe object")
			}
		})
	}
	if _, err := CaptureNode(filesystem, root, file, RegularFile, true); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(root, "link")
		if err := os.Symlink(file, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := CaptureNode(filesystem, root, link, RegularFile, true); err == nil {
			t.Fatal("CaptureNode() accepted symlink")
		}
	})
}
