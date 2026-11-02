package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the actual credential-free Linux aggregate, not a duplicate of its
// predicate. Hosted event delivery and cancellation remain separate evidence.
func TestNativePRCheckAggregate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the native aggregate runs on Linux, not Windows")
	}
	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "ci-pr.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	_, aggregate, ok := strings.Cut(workflow, "\n  ci-pr:\n")
	if !ok {
		t.Fatal("missing native aggregate")
	}
	for _, required := range []string{
		"    needs: [draft, resolve, commit-stage, ready, acceptance-stage]\n",
		"    if: always() && (github.event.action != 'edited' || github.event.changes.base)\n",
		"    name: ${{ github.event.action == 'edited' && !github.event.changes.base && 'ci-pr-metadata-only' || 'ci-pr' }}\n",
		"    permissions: {}\n",
		"          RESULTS: ${{ toJSON(needs) }}\n",
	} {
		if !strings.Contains(aggregate, required) {
			t.Fatalf("missing aggregate contract %q", required)
		}
	}
	for _, forbidden := range []string{"GH_TOKEN", "statuses: write", "gh api", "uses:", "continue-on-error", "!cancelled()"} {
		if strings.Contains(aggregate, forbidden) {
			t.Fatalf("unsafe or nonblocking aggregate contract %q", forbidden)
		}
	}
	if strings.Contains(workflow, "code-start:") || strings.Contains(workflow, "code-result:") ||
		!strings.Contains(workflow, "types: [opened, synchronize, reopened, edited]") ||
		!strings.Contains(workflow, "group: ci-pr-${{ github.event.pull_request.number }}-${{ github.event.action == 'edited' && !github.event.changes.base && 'metadata' || 'product' }}\n") ||
		!strings.Contains(workflow, "    if: github.event.action != 'edited' || github.event.changes.base\n") {
		t.Fatal("product validation must distinguish metadata edits from code and base changes")
	}
	_, block, ok := strings.Cut(aggregate, "        run: |\n")
	if !ok {
		t.Fatal("missing aggregate script")
	}
	var script strings.Builder
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "          ") {
			t.Fatal("unexpected trailing aggregate content")
		}
		script.WriteString(strings.TrimPrefix(line, "          ") + "\n")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal(err)
	}
	names := []string{"draft", "resolve", "commit-stage", "ready", "acceptance-stage"}
	run := func(t *testing.T, affected, result string, wantOK bool) {
		t.Helper()
		results := map[string]map[string]string{}
		for _, name := range names {
			value := "success"
			if name == affected {
				value = result
			}
			results[name] = map[string]string{"result": value}
		}
		encoded, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		isolated := t.TempDir()
		cmd := exec.Command(bash, "-euo", "pipefail", "-c", script.String())
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + isolated, "XDG_CONFIG_HOME=" + isolated, "GH_CONFIG_DIR=" + isolated, "RESULTS=" + string(encoded)}
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("success=%t, want=%t: %v %s", err == nil, wantOK, err, output)
		}
	}
	t.Run("all-success", func(t *testing.T) { run(t, "", "", true) })
	for _, name := range names {
		for _, result := range []string{"failure", "skipped", "cancelled"} {
			t.Run(fmt.Sprintf("%s-%s", name, result), func(t *testing.T) { run(t, name, result, false) })
		}
	}
}
