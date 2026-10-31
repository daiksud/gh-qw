package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCLIRemoveSelectedWorktreeUsesStableSlotAndCanonicalRepository(t *testing.T) {
	fixture := newCLIFixture(t)
	first := fixture.registerRemovalRepository(t, "acme", "widget")
	second := fixture.registerRemovalRepository(t, "other", "widget")
	firstWorktree := fixture.addRemovalWorktree(t, testIdentity, "topic-before")
	secondIdentity := "github.com/other/widget"
	secondWorktree := fixture.addRemovalWorktree(t, secondIdentity, "topic-before")
	for _, path := range []string{firstWorktree, secondWorktree} {
		fixture.runGit(t, path, "branch", "-m", "feature/after")
	}

	// Git also owns registrations outside gh-qw's managed worktree layout.
	// They must not be offered as targets for the managed removal command.
	external := filepath.Join(fixture.base, "external-worktree")
	fixture.runGit(t, first, "worktree", "add", "-b", "outside", external, "main")
	env := fixture.removalSelectorEnvironment(t, "select", "worktree", secondIdentity+"@feature/after")
	env = append(env, "GHQW_TEST_FZF_EXPECT_COUNT=4")
	result := fixture.runCLIWithEnv(t, env, "remove")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "removed worktree "+filepath.ToSlash(secondWorktree))
	assertPathMissing(t, secondWorktree)
	for _, path := range []string{first, second, firstWorktree, external} {
		assertPathExists(t, path)
	}
	listed := fixture.runCLI(t, "list")
	assertStatus(t, listed, 0)
	assertStdout(t, listed, testIdentity+"\n"+secondIdentity+"\n")
}

func TestCLIRemoveSelectedWorktreeAllowsDetachedMain(t *testing.T) {
	fixture := newCLIFixture(t)
	main := fixture.registerRemovalRepository(t, "acme", "widget")
	worktree := fixture.addRemovalWorktree(t, testIdentity, "feature/cleanup")
	fixture.runGit(t, main, "checkout", "--detach", "HEAD")
	head := fixture.runGit(t, main, "rev-parse", "HEAD")

	result := fixture.runCLIWithEnv(t,
		fixture.removalSelectorEnvironment(t, "select", "worktree", testIdentity+"@feature/cleanup"),
		"remove")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathMissing(t, worktree)
	assertPathExists(t, main)
	if branch := strings.TrimSpace(fixture.runGit(t, main, "branch", "--show-current")); branch != "" {
		t.Fatalf("main branch = %q, want detached HEAD", branch)
	}
	if got := fixture.runGit(t, main, "rev-parse", "HEAD"); got != head {
		t.Fatalf("main HEAD = %q, want unchanged %q", got, head)
	}
	listed := fixture.runCLI(t, "list")
	assertStatus(t, listed, 0)
	assertStdout(t, listed, testIdentity+"\n")
}

func TestCLIRemoveSelectedDryRunPreservesRepositoryAndWorktree(t *testing.T) {
	for _, kind := range []string{"repository", "worktree"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newCLIFixture(t)
			main := fixture.registerRemovalRepository(t, "acme", "widget")
			worktree := fixture.addRemovalWorktree(t, testIdentity, "feature/preview")
			label := testIdentity
			if kind == "worktree" {
				label += "@feature/preview"
			}
			result := fixture.runCLIWithEnv(t,
				fixture.removalSelectorEnvironment(t, "select", kind, label),
				"remove", "--dry-run")
			assertStatus(t, result, 0)
			assertStdout(t, result, "")
			assertContains(t, result.stderr, filepath.ToSlash(worktree))
			if kind == "repository" {
				assertContains(t, result.stderr, filepath.ToSlash(main))
			}
			if strings.Contains(result.stderr, "Proceed with removal?") {
				t.Fatalf("dry run asked for confirmation: %s", result.stderr)
			}
			assertPathExists(t, main)
			assertPathExists(t, worktree)
		})
	}
}

func TestCLIRemoveSelectorFailuresDoNotDelete(t *testing.T) {
	for _, test := range []struct {
		mode   string
		status int
		silent bool
	}{
		{mode: "cancel", status: 130, silent: true},
		{mode: "no-match", status: 1, silent: true},
		{mode: "empty", status: 1},
		{mode: "unknown", status: 1},
		{mode: "multiple", status: 1},
		{mode: "unterminated", status: 1},
		{mode: "failure", status: 1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			fixture := newCLIFixture(t)
			main := fixture.registerRemovalRepository(t, "acme", "widget")
			worktree := fixture.addRemovalWorktree(t, testIdentity, "topic")
			result := fixture.runCLIWithEnv(t,
				fixture.removalSelectorEnvironment(t, test.mode, "worktree", testIdentity+"@topic"),
				"remove")
			assertStatus(t, result, test.status)
			assertStdout(t, result, "")
			if test.silent {
				assertStderr(t, result, "")
			} else {
				assertContains(t, result.stderr, "gh-qw:")
			}
			assertPathExists(t, main)
			assertPathExists(t, worktree)
			listed := fixture.runCLI(t, "list", "--worktree")
			assertStatus(t, listed, 0)
			assertStdout(t, listed, testIdentity+"@topic\n")
		})
	}
}

func TestCLIRemoveExplicitTargetAndEmptyRegistryDoNotStartSelector(t *testing.T) {
	fixture := newCLIFixture(t)
	marker := filepath.Join(fixture.base, "selector-called")
	env := append(fixture.removalSelectorEnvironment(t, "failure", "", ""),
		"GHQW_TEST_FZF_CALLED="+marker)
	result := fixture.runCLIWithEnv(t, env, "remove")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathMissing(t, marker)

	main := fixture.registerRemovalRepository(t, "acme", "widget")
	worktree := fixture.addRemovalWorktree(t, testIdentity, "explicit")
	result = fixture.runCLIWithEnv(t, env, "remove", "--dry-run", testIdentity)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathExists(t, main)
	assertPathExists(t, worktree)
	result = fixture.runCLIWithEnv(t, env, "remove", "--worktree", "-R", testIdentity,
		filepath.Base(filepath.Dir(worktree)))
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathMissing(t, worktree)
	assertPathExists(t, main)
	assertPathMissing(t, marker)
}

func TestCLIRemoveSelectorKeepsRegisteredPathsWithWhitespaceIntact(t *testing.T) {
	fixture := newCLIFixture(t)
	name := "checkout with spaces"
	if runtime.GOOS != "windows" {
		name += "\tand tabs"
	}
	path := filepath.Join(fixture.base, name)
	fixture.initRepository(t, path, "https://github.com/acme/widget.git", false)
	assertStatus(t, fixture.runCLI(t, "add", path), 0)
	result := fixture.runCLIWithEnv(t,
		fixture.removalSelectorEnvironment(t, "select", "repository", testIdentity),
		"remove", "--dry-run")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathExists(t, path)
	assertPathExists(t, filepath.Join(path, "tracked.txt"))
}

func (fixture *cliFixture) registerRemovalRepository(t *testing.T, owner, name string) string {
	t.Helper()
	path := filepath.Join(fixture.repository, "github.com", owner, name)
	fixture.initRepository(t, path, "https://github.com/"+owner+"/"+name+".git", false)
	result := fixture.runCLI(t, "add", path)
	assertStatus(t, result, 0)
	return path
}

func (fixture *cliFixture) addRemovalWorktree(t *testing.T, identity, branch string) string {
	t.Helper()
	result := fixture.runCLI(t, "add", "--worktree", "-R", identity, "-b", branch, "main")
	assertStatus(t, result, 0)
	return filepath.FromSlash(strings.TrimSuffix(result.stdout, "\n"))
}

func (fixture *cliFixture) removalSelectorEnvironment(t *testing.T, mode, kind, identity string) []string {
	t.Helper()
	return append(fixture.envWithPathPrepended(fixture.buildFakeFzf(t)),
		"GHQW_TEST_FZF_MODE="+mode,
		"GHQW_TEST_FZF_KIND="+kind,
		"GHQW_TEST_FZF_IDENTITY="+identity,
		"FZF_DEFAULT_OPTS=--filter=widget --select-1 --multi --print-query",
		"FZF_DEFAULT_OPTS_FILE="+filepath.Join(fixture.base, "unexpected-fzf-options"))
}

var (
	sharedFakeFzfOnce  sync.Once
	sharedFakeFzfError error
	sharedFakeFzfLog   string
)

// The executable is shared; selection responses and every mutable path belong
// to the fixture environment, just like the fake gh boundary.
func (fixture *cliFixture) buildFakeFzf(t *testing.T) string {
	t.Helper()
	sharedCLIExecutable(t)
	directory := filepath.Join(sharedCLIDir, "fake-fzf")
	sharedFakeFzfOnce.Do(func() {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sharedFakeFzfError = err
			return
		}
		source := filepath.Join(sharedCLIDir, "fakefzf.go")
		if err := os.WriteFile(source, []byte(fakeFzfSource), 0o600); err != nil {
			sharedFakeFzfError = err
			return
		}
		executable := filepath.Join(directory, "fzf")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		build := exec.Command("go", "build", "-o", executable, source)
		var output bytes.Buffer
		build.Stdout = &output
		build.Stderr = &output
		sharedFakeFzfError = build.Run()
		sharedFakeFzfLog = output.String()
	})
	if sharedFakeFzfError != nil {
		t.Fatalf("build shared fake fzf: %v\n%s", sharedFakeFzfError, sharedFakeFzfLog)
	}
	return directory
}

const fakeFzfSource = `package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func fail(message string) {
	fmt.Fprintln(os.Stderr, "fake fzf: " + message)
	os.Exit(2)
}

func main() {
	if path := os.Getenv("GHQW_TEST_FZF_CALLED"); path != "" {
		if err := os.WriteFile(path, []byte("called"), 0600); err != nil {
			fail(err.Error())
		}
	}
	for _, key := range []string{"FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE"} {
		if os.Getenv(key) != "" {
			fail("unexpected inherited " + key)
		}
	}
	flags := make(map[string]bool)
	for _, arg := range os.Args[1:] {
		flags[arg] = true
	}
	for _, flag := range []string{"--read0", "--print0", "--no-multi"} {
		if !flags[flag] {
			fail("missing " + flag)
		}
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) == 0 || input[len(input)-1] != 0 {
		fail("expected complete NUL-delimited candidates")
	}
	rows := strings.Split(string(input[:len(input)-1]), "\x00")
	if count := os.Getenv("GHQW_TEST_FZF_EXPECT_COUNT"); count != "" {
		want, err := strconv.Atoi(count)
		if err != nil || len(rows) != want {
			fail(fmt.Sprintf("candidate count = %d, want %s", len(rows), count))
		}
	}
	switch os.Getenv("GHQW_TEST_FZF_MODE") {
	case "cancel":
		os.Exit(130)
	case "no-match":
		os.Exit(1)
	case "failure":
		fail("simulated failure")
	case "empty":
		return
	case "unknown":
		fmt.Print("unknown candidate\x00")
		return
	case "multiple":
		fmt.Print(rows[0] + "\x00" + rows[len(rows)-1] + "\x00")
		return
	case "unterminated":
		fmt.Print(rows[0])
		return
	case "select":
		for _, row := range rows {
			fields := strings.Split(row, "\t")
			if len(fields) != 3 {
				fail("expected kind, identity, and escaped path")
			}
			if fields[0] == os.Getenv("GHQW_TEST_FZF_KIND") &&
				fields[1] == os.Getenv("GHQW_TEST_FZF_IDENTITY") {
				if _, err := strconv.Unquote(fields[2]); err != nil {
					fail("path was not quoted")
				}
				fmt.Print(row + "\x00")
				return
			}
		}
		fail("requested candidate was not offered")
	default:
		fail("unspecified fixture response")
	}
}
`
