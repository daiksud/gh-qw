package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/daiksud/gh-qw/internal/registry"
)

func TestCLIListSelectionSeedsQueryAndPreservesOutputFormat(t *testing.T) {
	fixture := newCLIFixture(t)
	fixture.registerRemovalRepository(t, "acme", "widget")
	selectedPath := fixture.registerRemovalRepository(t, "other", "widget")
	selected := "github.com/other/widget"
	for _, test := range []struct {
		name  string
		flags []string
		query string
		seed  string
		want  string
	}{
		{name: "identity", query: "acme", seed: "acme", want: selected},
		{name: "path", flags: []string{"--full-path"}, query: "widget", seed: "widget", want: filepath.ToSlash(selectedPath)},
		{name: "unique across all offered entries", flags: []string{"--unique"}, query: "acme", seed: "acme", want: "other/widget"},
		{name: "URL query", query: "https://github.com/acme/widget.git", seed: testIdentity, want: selected},
	} {
		t.Run(test.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "selector.json")
			env := append(fixture.listSelectorEnvironment(t, "select", test.seed, selected),
				"GHQW_TEST_LIST_FZF_REPORT="+reportPath,
				"GHQW_TEST_LIST_FZF_UI=selector UI")
			args := append([]string{"list", "--fzf"}, test.flags...)
			args = append(args, test.query)
			result := fixture.runCLIWithEnv(t, env, args...)
			assertStatus(t, result, 0)
			assertStdout(t, result, test.want+"\n")
			assertStderr(t, result, "selector UI\n")
			report := readListSelectorReport(t, reportPath)
			assertListCandidateIdentities(t, report.Rows, testIdentity, selected)
		})
	}
}

func TestCLIListExactSelectionRestrictsCandidates(t *testing.T) {
	fixture := newCLIFixture(t)
	fixture.registerRemovalRepository(t, "acme", "widget")
	fixture.registerRemovalRepository(t, "other", "widget")
	reportPath := filepath.Join(fixture.base, "selector.json")
	env := append(fixture.listSelectorEnvironment(t, "select", "", testIdentity),
		"GHQW_TEST_LIST_FZF_REPORT="+reportPath)
	result := fixture.runCLIWithEnv(t, env, "list", "--fzf", "--exact", "acme/widget")
	assertStatus(t, result, 0)
	assertStdout(t, result, testIdentity+"\n")
	assertListCandidateIdentities(t, readListSelectorReport(t, reportPath).Rows, testIdentity)
}

func TestCLIListCapturedOutputAndExplicitFalseSkipSelector(t *testing.T) {
	fixture := newCLIFixture(t)
	fixture.registerRemovalRepository(t, "acme", "widget")
	fixture.registerRemovalRepository(t, "other", "gadget")
	marker := filepath.Join(fixture.base, "selector-called")
	env := append(fixture.listSelectorEnvironment(t, "failure", "", ""),
		"GHQW_TEST_LIST_FZF_CALLED="+marker)
	for _, flags := range [][]string{nil, {"--fzf=false"}, {"--fzf", "--fzf=false"}} {
		args := append([]string{"list"}, flags...)
		result := fixture.runCLIWithEnv(t, env, append(args, "widget")...)
		assertStatus(t, result, 0)
		assertStdout(t, result, testIdentity+"\n")
		assertStderr(t, result, "")
		assertPathMissing(t, marker)
	}
}

func TestCLIListSelectionUsesGitWorktreeScopeAndSearchAliases(t *testing.T) {
	fixture := newCLIFixture(t)
	main := fixture.registerRemovalRepository(t, "acme", "widget")
	linked := fixture.addRemovalWorktree(t, testIdentity, "before")
	fixture.runGit(t, linked, "branch", "-m", "feature/after")
	external := filepath.Join(fixture.base, "external worktree")
	fixture.runGit(t, main, "worktree", "add", "-b", "external", external, "HEAD")
	fixture.runGit(t, main, "config", "branch.feature/after.remote", "origin")
	fixture.runGit(t, main, "config", "branch.feature/after.merge", "refs/heads/missing")
	branchIdentity := testIdentity + "@feature/after"
	stableIdentity := testIdentity + "@" + filepath.Base(filepath.Dir(linked))
	for _, test := range []struct {
		name       string
		flags      []string
		query      string
		seed       string
		selected   string
		identities []string
	}{
		{name: "repositories", selected: testIdentity, identities: []string{testIdentity}},
		{name: "all", flags: []string{"--all"}, selected: branchIdentity, identities: []string{testIdentity, testIdentity + "@external", branchIdentity}},
		{name: "worktrees", flags: []string{"--worktree"}, query: stableIdentity, seed: stableIdentity, selected: branchIdentity, identities: []string{testIdentity + "@external", branchIdentity}},
		{name: "gone", flags: []string{"--worktree", "--gone"}, selected: branchIdentity, identities: []string{branchIdentity}},
		{name: "exact slot", flags: []string{"--worktree", "--exact"}, query: stableIdentity, selected: branchIdentity, identities: []string{branchIdentity}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "selector.json")
			env := append(fixture.listSelectorEnvironment(t, "select", test.seed, test.selected),
				"GHQW_TEST_LIST_FZF_REPORT="+reportPath)
			args := append([]string{"list", "--fzf"}, test.flags...)
			if test.query != "" {
				args = append(args, test.query)
			}
			result := fixture.runCLIWithEnv(t, env, args...)
			assertStatus(t, result, 0)
			assertStdout(t, result, test.selected+"\n")
			report := readListSelectorReport(t, reportPath)
			assertListCandidateIdentities(t, report.Rows, test.identities...)
			for _, row := range report.Rows {
				fields := strings.Split(row, "\t")
				if fields[1] == branchIdentity && !slices.Contains(fields[3:], stableIdentity) {
					t.Fatalf("worktree candidate lacks stable slot search alias: %q", row)
				}
			}
		})
	}
	for _, path := range []string{main, linked, external} {
		assertPathExists(t, path)
	}
}

func TestCLIListSelectionPreservesQuotedPath(t *testing.T) {
	fixture := newCLIFixture(t)
	name := "checkout with spaces"
	if runtime.GOOS != "windows" {
		name += "\tand a newline\n"
	}
	path := filepath.Join(fixture.base, name)
	fixture.initRepository(t, path, "https://"+testIdentity+".git", false)
	fixture.registerListLocation(t, testIdentity, path)
	reportPath := filepath.Join(fixture.base, "selector.json")
	env := append(fixture.listSelectorEnvironment(t, "select", "", testIdentity),
		"GHQW_TEST_LIST_FZF_REPORT="+reportPath)
	result := fixture.runCLIWithEnv(t, env, "list", "--fzf", "--full-path")
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(path)+"\n")
	report := readListSelectorReport(t, reportPath)
	if len(report.Rows) != 1 {
		t.Fatalf("candidate count = %d, want one", len(report.Rows))
	}
	fields := strings.Split(report.Rows[0], "\t")
	if fields[2] != strconv.Quote(filepath.ToSlash(path)) {
		t.Fatalf("display path = %q, want quoted path", fields[2])
	}
}

func TestCLIListSelectorFailuresAndEmptyCandidates(t *testing.T) {
	fixture := newCLIFixture(t)
	marker := filepath.Join(fixture.base, "selector-called")
	env := append(fixture.listSelectorEnvironment(t, "failure", "", ""),
		"GHQW_TEST_LIST_FZF_CALLED="+marker)
	result := fixture.runCLIWithEnv(t, env, "list", "--fzf")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathMissing(t, marker)
	main := fixture.registerRemovalRepository(t, "acme", "widget")
	result = fixture.runCLIWithEnv(t, env, "list", "--fzf", "--exact", "unregistered")
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertPathMissing(t, marker)
	for _, test := range []struct {
		mode   string
		status int
		silent bool
	}{
		{mode: "cancel", status: 130, silent: true},
		{mode: "no-match", status: 1, silent: true},
		{mode: "failure", status: 1},
		{mode: "unknown", status: 1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			result := fixture.runCLIWithEnv(t,
				fixture.listSelectorEnvironment(t, test.mode, "", testIdentity), "list", "--fzf")
			assertStatus(t, result, test.status)
			assertStdout(t, result, "")
			if test.silent {
				assertStderr(t, result, "")
			} else {
				assertContains(t, result.stderr, "gh-qw:")
			}
		})
	}
	assertPathExists(t, main)
	assertStdout(t, fixture.runCLI(t, "list"), testIdentity+"\n")
}

type listSelectorReport struct {
	Arguments []string `json:"args"`
	Rows      []string `json:"rows"`
}

// Seed the explicit registry to exercise list independently of add's Git path
// parsing, including locations containing newlines.
func (fixture *cliFixture) registerListLocation(t *testing.T, identity, path string) {
	t.Helper()
	store, err := registry.NewStoreAt(filepath.Join(fixture.home, "xdg-data", "ghqw", "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register(identity, path); err != nil {
		t.Fatal(err)
	}
}

func readListSelectorReport(t *testing.T, path string) listSelectorReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report listSelectorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func assertListCandidateIdentities(t *testing.T, rows []string, want ...string) {
	t.Helper()
	identities := make([]string, 0, len(rows))
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) < 3 {
			t.Fatalf("invalid candidate row %q", row)
		}
		identities = append(identities, fields[1])
	}
	slices.Sort(identities)
	slices.Sort(want)
	if !slices.Equal(identities, want) {
		t.Fatalf("candidate identities = %q, want %q", identities, want)
	}
}

func (fixture *cliFixture) listSelectorEnvironment(t *testing.T, mode, query, identity string) []string {
	t.Helper()
	return append(fixture.envWithPathPrepended(fixture.buildFakeListFzf(t)),
		"GHQW_TEST_LIST_FZF_MODE="+mode,
		"GHQW_TEST_LIST_FZF_QUERY="+query,
		"GHQW_TEST_LIST_FZF_IDENTITY="+identity,
		"FZF_DEFAULT_OPTS=--filter=unexpected --multi --print-query",
		"FZF_DEFAULT_OPTS_FILE="+filepath.Join(fixture.base, "unexpected-fzf-options"))
}

var (
	sharedFakeListFzfOnce  sync.Once
	sharedFakeListFzfError error
	sharedFakeListFzfLog   string
)

func (fixture *cliFixture) buildFakeListFzf(t *testing.T) string {
	t.Helper()
	sharedCLIExecutable(t)
	directory := filepath.Join(sharedCLIDir, "fake-list-fzf")
	sharedFakeListFzfOnce.Do(func() {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sharedFakeListFzfError = err
			return
		}
		source := filepath.Join(sharedCLIDir, "fakelistfzf.go")
		if err := os.WriteFile(source, []byte(fakeListFzfSource), 0o600); err != nil {
			sharedFakeListFzfError = err
			return
		}
		executable := filepath.Join(directory, "fzf")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		build := exec.Command("go", "build", "-o", executable, source)
		var output bytes.Buffer
		build.Stdout, build.Stderr = &output, &output
		sharedFakeListFzfError = build.Run()
		sharedFakeListFzfLog = output.String()
	})
	if sharedFakeListFzfError != nil {
		t.Fatalf("build shared list fzf: %v\n%s", sharedFakeListFzfError, sharedFakeListFzfLog)
	}
	return directory
}

const fakeListFzfSource = `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func fail(message string) {
	fmt.Fprintln(os.Stderr, "fake list fzf: " + message)
	os.Exit(2)
}

func main() {
	if path := os.Getenv("GHQW_TEST_LIST_FZF_CALLED"); path != "" {
		if err := os.WriteFile(path, []byte("called"), 0600); err != nil { fail(err.Error()) }
	}
	flags := make(map[string]bool)
	query := ""
	for _, arg := range os.Args[1:] {
		flags[arg] = true
		if strings.HasPrefix(arg, "--query=") { query = strings.TrimPrefix(arg, "--query=") }
	}
	for _, flag := range []string{"--read0", "--print0", "--no-multi", "--no-select-1", "--no-exit-0"} {
		if !flags[flag] { fail("missing " + flag) }
	}
	if query != os.Getenv("GHQW_TEST_LIST_FZF_QUERY") { fail("incorrect initial query: " + query) }
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) == 0 || input[len(input)-1] != 0 { fail("incomplete candidate records") }
	rows := strings.Split(string(input[:len(input)-1]), "\x00")
	for _, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) < 3 { fail("expected kind, identity, and quoted path") }
		if _, err := strconv.Unquote(fields[2]); err != nil { fail("path was not quoted") }
	}
	if path := os.Getenv("GHQW_TEST_LIST_FZF_REPORT"); path != "" {
		data, err := json.Marshal(map[string]any{"args": os.Args[1:], "rows": rows})
		if err != nil { fail(err.Error()) }
		if err := os.WriteFile(path, data, 0600); err != nil { fail(err.Error()) }
	}
	if message := os.Getenv("GHQW_TEST_LIST_FZF_UI"); message != "" { fmt.Fprintln(os.Stderr, message) }
	switch os.Getenv("GHQW_TEST_LIST_FZF_MODE") {
	case "cancel": os.Exit(130)
	case "no-match": os.Exit(1)
	case "failure": fail("simulated failure")
	case "unknown": fmt.Print("unoffered candidate\x00")
	case "select":
		for _, row := range rows {
			if strings.Split(row, "\t")[1] != os.Getenv("GHQW_TEST_LIST_FZF_IDENTITY") { continue }
			if path := os.Getenv("GHQW_TEST_LIST_FZF_REMOVE"); path != "" {
				if err := os.RemoveAll(path); err != nil { fail(err.Error()) }
			}
			fmt.Print(row + "\x00")
			return
		}
		fail("requested candidate was not offered")
	default: fail("unspecified fixture response")
	}
}
`
