package fsidentity

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPrimeCapturesIdentity(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	called := false
	err = Prime(info, func(first, second fs.FileInfo) bool {
		called = true
		return first == info && second == info
	})
	if err != nil {
		t.Fatalf("Prime() error = %v", err)
	}
	if !called {
		t.Fatal("Prime() did not compare the object with itself")
	}
}

func TestPrimeFailsClosed(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		info     fs.FileInfo
		sameFile func(fs.FileInfo, fs.FileInfo) bool
	}{
		{name: "missing info", sameFile: os.SameFile},
		{name: "missing comparer", info: info},
		{
			name: "unavailable identity",
			info: info,
			sameFile: func(fs.FileInfo, fs.FileInfo) bool {
				return false
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Prime(test.info, test.sameFile); err == nil {
				t.Fatal("Prime() error = nil")
			}
		})
	}
}

func TestCapturePrimesBeforeReturning(t *testing.T) {
	path := t.TempDir()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	compared := false
	got, err := Capture(path, func(gotPath string) (fs.FileInfo, error) {
		if gotPath != path {
			t.Fatal("inspection path changed")
		}
		return info, nil
	}, func(first, second fs.FileInfo) bool { compared = true; return first == info && second == info })
	if err != nil || got != info || !compared {
		t.Fatalf("Capture returned before identity capture: info=%v err=%v primed=%v", got != nil, err, compared)
	}
}

func TestCaptureFailures(t *testing.T) {
	path := t.TempDir()
	failure := errors.New("inspect failed")
	if info, err := Capture(path, func(string) (fs.FileInfo, error) { return nil, failure }, os.SameFile); info != nil || !errors.Is(err, failure) {
		t.Fatalf("inspection failure = %v, %v", info, err)
	}
	if info, err := Capture(path, os.Lstat, func(fs.FileInfo, fs.FileInfo) bool { return false }); info != nil || err == nil {
		t.Fatal("unavailable identity accepted")
	}
}

func TestCapturedIdentityDetectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := Capture(path, os.Lstat, os.SameFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := Capture(path, os.Lstat, os.SameFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("replacement retained old identity")
	}
}
