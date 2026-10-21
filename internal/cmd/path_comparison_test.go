package cmd

import "testing"

func TestRemovalContainmentRejectsEmptyPaths(t *testing.T) {
	if removePathStrictlyWithin("", "child") || removePathStrictlyWithin(t.TempDir(), "") {
		t.Fatal("empty removal boundary or target accepted")
	}
}
