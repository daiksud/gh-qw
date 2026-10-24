package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovalContainmentRejectsEmptyPaths(t *testing.T) {
	if removePathStrictlyWithin("", "child") || removePathStrictlyWithin(t.TempDir(), "") {
		t.Fatal("empty removal boundary or target accepted")
	}
}

func TestPhysicalRegisteredLocationStrictlyWithinCaseAlias(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Parent")
	child := filepath.Join(base, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasChild := filepath.Join(filepath.Dir(base), strings.ToUpper(filepath.Base(base)), "child")
	if _, err := os.Stat(aliasChild); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	basePhysical, err := physicalRegisteredLocation(base)
	if err != nil {
		t.Fatal(err)
	}
	childPhysical, err := physicalRegisteredLocation(aliasChild)
	if err != nil {
		t.Fatal(err)
	}
	within, err := physicalRegisteredLocationStrictlyWithin(basePhysical, childPhysical)
	if err != nil {
		t.Fatal(err)
	}
	if !within {
		t.Fatalf("case-equivalent child %q was not within %q", childPhysical, basePhysical)
	}
}
