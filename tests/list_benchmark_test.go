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

func TestCLIListValidatesEachWorktreeOnce(t *testing.T) {
	const count = 3
	fixture := newListWorktreeFixture(t, count)
	trace := filepath.Join(fixture.base, "git-trace.json")
	env := append(slices.Clone(fixture.env), "GIT_TRACE2_EVENT="+trace)
	result := fixture.runCLIWithEnv(t, env, "list", "--worktree")
	if result.status != 0 || len(strings.Fields(result.stdout)) != count {
		t.Fatalf("list returned status %d, stdout %q, stderr %q", result.status, result.stdout, result.stderr)
	}
	gitCalls, refChecks := traceGitCalls(t, trace)
	if gitCalls != count+1 || refChecks != count {
		t.Fatalf("list used %d Git processes and %d ref checks for %d worktrees", gitCalls, refChecks, count)
	}
}

// BenchmarkCLIListWorktrees measures the complete executable with real Git and
// isolated filesystem state. Fixture creation and process tracing are untimed.
func BenchmarkCLIListWorktrees(b *testing.B) {
	for _, count := range []int{0, 10, 50} {
		b.Run(fmt.Sprintf("worktrees=%d", count), func(b *testing.B) {
			b.StopTimer()
			fixture := newListWorktreeFixture(b, count)
			trace := filepath.Join(fixture.base, "git-trace.json")
			env := append(slices.Clone(fixture.env), "GIT_TRACE2_EVENT="+trace)
			result := fixture.runCLIWithEnv(b, env, "list", "--worktree")
			if result.status != 0 || len(strings.Fields(result.stdout)) != count {
				b.Fatalf("list returned status %d, stdout %q, stderr %q", result.status, result.stdout, result.stderr)
			}
			gitCalls, refChecks := traceGitCalls(b, trace)
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

func newListWorktreeFixture(t testing.TB, count int) *cliFixture {
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

func traceGitCommands(t testing.TB, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var commands [][]string
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
			commands = append(commands, event.Argv)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return commands
}

func traceGitCalls(t testing.TB, path string) (int, int) {
	t.Helper()
	commands := traceGitCommands(t, path)
	refs := 0
	for _, args := range commands {
		if slices.Contains(args, "check-ref-format") {
			refs++
		}
	}
	return len(commands), refs
}

func TestCLIWorktreeInventoryIsScopedToReadPhase(t *testing.T) {
	fixture := newListWorktreeFixture(t, 0)
	main := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	current := *fixture
	current.base = main
	for _, step := range []struct {
		name  string
		args  []string
		lists int
	}{
		{name: "add", args: []string{"add", "--worktree", "-b", "topic", "HEAD"}, lists: 2},
		{name: "gone dry run", args: []string{"remove", "--worktree", "--gone", "--dry-run"}, lists: 1},
		{name: "remove", args: []string{"remove", "--worktree", "github-com-acme-widget-topic"}, lists: 2},
	} {
		if step.name == "gone dry run" {
			fixture.runGit(t, main, "config", "branch.topic.remote", "origin")
			fixture.runGit(t, main, "config", "branch.topic.merge", "refs/heads/topic")
		}
		if !t.Run(step.name, func(t *testing.T) {
			trace := filepath.Join(fixture.base, strings.ReplaceAll(step.name, " ", "-")+".json")
			env := append(slices.Clone(fixture.env), "GIT_TRACE2_EVENT="+trace)
			result := current.runCLIWithEnv(t, env, step.args...)
			if result.status != 0 {
				t.Fatalf("command failed with %d: %s", result.status, result.stderr)
			}
			lists := 0
			for _, args := range traceGitCommands(t, trace) {
				for index := 0; index+1 < len(args); index++ {
					if args[index] == "worktree" && args[index+1] == "list" {
						lists++
					}
				}
			}
			if lists != step.lists {
				t.Fatalf("native worktree list reads = %d, want %d for %s", lists, step.lists, step.name)
			}
		}) {
			t.FailNow()
		}
	}
}
