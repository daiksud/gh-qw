package cmd

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTemporaryGofmtDiagnostics(t *testing.T) {
	paths := []string{"worktree_add.go", "worktree_add_test.go", filepath.Join("..", "gitcmd", "worktree_test.go")}
	for _, path := range paths {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source(original)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(original, formatted) {
			continue
		}
		dir := t.TempDir()
		from, to := filepath.Join(dir, "original.go"), filepath.Join(dir, "gofmt.go")
		if err := os.WriteFile(from, original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, formatted, 0o600); err != nil {
			t.Fatal(err)
		}
		diff, _ := exec.Command("git", "diff", "--no-index", "--", from, to).CombinedOutput()
		fmt.Printf("GOFMT DIFF %s\n%s\n", path, diff)
	}
	t.Fatal("temporary formatting diagnostics only")
}
