package physicalpath

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemovalContainmentContracts(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repo")
	for _, test := range []struct {
		path string
		want bool
	}{
		{base, false}, {filepath.Dir(base), false}, {base + "s", false},
		{filepath.Join(base+"s", "child"), false}, {filepath.Join(base, "missing", "child"), true},
	} {
		if got := StrictlyWithin(base, test.path); got != test.want {
			t.Fatalf("within(%q,%q)=%v", base, test.path, got)
		}
	}
}

func TestRemovalPathCaseContracts(t *testing.T) {
	for _, test := range []struct {
		first, second string
		want          bool
	}{
		{"repo/../same", "same", true}, {"Ä", "ä", runtime.GOOS == "windows"},
		{"K", "K", runtime.GOOS == "windows"}, {"repo", "repository", false},
	} {
		if got := SamePath(test.first, test.second); got != test.want {
			t.Fatalf("same(%q,%q)=%v", test.first, test.second, got)
		}
	}
	want := filepath.Clean("Ä/../K")
	if runtime.GOOS == "windows" {
		want = strings.ToLower(want)
	}
	if got := Key("Ä/../K"); got != want {
		t.Fatalf("key=%q,want%q", got, want)
	}
}

func TestWindowsVolumeBoundary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive semantics")
	}
	if StrictlyWithin(`C:\repos`, `D:\repos\child`) {
		t.Fatal("accepted another drive")
	}
	if !StrictlyWithin(`C:\repos`, `c:\repos\child`) {
		t.Fatal("rejected same Windows drive")
	}
}
