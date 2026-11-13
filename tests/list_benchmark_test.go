package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// BenchmarkCLIListWorktrees measures the complete executable with real Git and
// isolated filesystem state. Fixture creation and process tracing are untimed.
func BenchmarkCLIListWorktrees(b *testing.B) {
	for _, count := range []int{0, 10, 50} {
		b.Run(fmt.Sprintf("worktrees=%d", count), func(b *testing.B) {
			b.StopTimer()
			fixture := newListBenchmarkFixture(b, count)
			trace := filepath.Join(fixture.base, "git-trace.json")
			env := append(slices.Clone(fixture.env), "GIT_TRACE2_EVENT="+trace)
			result := fixture.runCLIWithEnv(b, env, "list", "--worktree")
			if result.status != 0 || len(strings.Fields(result.stdout)) != count {
				b.Fatalf("list returned status %d, stdout %q, stderr %q", result.status, result.stdout, result.stderr)
			}
			gitCalls, refChecks := benchmarkGitCalls(b, trace)
			b.ResetTimer()
			b.StartTimer()
			for b.Loop() {
				result := fixture.runCLI(b, "list", "--worktree")
				if result.status != 0 {
					b.Fatalf("list failed: %s", result.stderr)
				}
			}
			b.ReportMetric(float64(gitCalls), "git/op")
			b.ReportMetric(float64(refChecks), "ref-checks/op")
		})
	}
}

func newListBenchmarkFixture(t testing.TB, count int) *cliFixture {
	t.Helper()
	fixture := newCLIFixture(t)
	main := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	fixture.runGit(t, "", "init", "--initial-branch=main", main)
	fixture.runGit(t, main, "commit", "--allow-empty", "-m", "Benchmark fixture")
	fixture.runGit(t, main, "remote", "add", "origin", "https://github.com/acme/widget.git")
	if result := fixture.runCLI(t, "add", main); result.status != 0 {
		t.Fatalf("register benchmark repository: %s", result.stderr)
	}
	for index := range count {
		branch := fmt.Sprintf("task-%d", index)
		destination := filepath.Join(fixture.worktreeRoot, "github-com-acme-widget-"+branch, "widget")
		fixture.runGit(t, main, "worktree", "add", "--quiet", "-b", branch, destination, "HEAD")
	}
	return fixture
}

func benchmarkGitCalls(t testing.TB, path string) (int, int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var calls, refs int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode Git trace event: %v", err)
		}
		if event.Event == "start" {
			calls++
			if slices.Contains(event.Argv, "check-ref-format") {
				refs++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return calls, refs
}
