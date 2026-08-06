package fsidentity

import (
	"io/fs"
	"os"
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
