package cmd

import (
	"os/exec"
	"testing"
)

// Temporary PR diagnostic: report canonical gofmt diffs for newly edited files.
func TestPrintFormattingDiffs(t *testing.T) {
	for _, path := range []string{
		"add.go", "app.go", "forget.go", "get.go", "managed.go",
		"managed_lifecycle_test.go",
	} {
		output, err := exec.Command("gofmt", "-d", path).CombinedOutput()
		if err != nil {
			t.Errorf("gofmt -d %s: %v\n%s", path, err, output)
		} else if len(output) != 0 {
			t.Errorf("gofmt -d %s:\n%s", path, output)
		}
	}
}
