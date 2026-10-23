package registry

import (
    "os/exec"
    "testing"
)

func TestTemporaryFormattingDiff(t *testing.T) {
    output, err := exec.Command("gofmt", "-d", "store_test.go").CombinedOutput()
    if err != nil || len(output) != 0 {
        t.Errorf("format status %v:\n%s", err, output)
    }
}
