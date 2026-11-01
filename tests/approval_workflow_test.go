package tests

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// Execute the production inline Python against API fixtures without live credentials.
func TestCodexApprovalEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the approval workflow runs on Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "test_approval_workflow.py", "-q")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("approval workflow contract: %v\n%s", err, output)
	}
}
