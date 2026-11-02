package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCLICloneOwnerListsRepositoriesWithoutFzfOrLocalRoots(t *testing.T) {
	fixture := newCLIFixture(t)
	env := fixture.cloneListingEnvironment(t, "acme", "acme/zebra", "acme/private", "acme/fork")
	invalidRoot := filepath.Join(fixture.base, "root-is-a-file")
	if err := os.WriteFile(invalidRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	env = append(env, "GHQW_ROOT="+invalidRoot, "GH_HOST=ghe.example.com")
	result := fixture.runCLIWithEnv(t, env, "clone", "acme")
	assertStatus(t, result, 0)
	assertStdout(t, result, "acme/fork\nacme/private\nacme/zebra\n")
	assertContains(t, result.stderr, "fake gh: api ghe.example.com/graphql owner=acme")
	if strings.Contains(result.stderr, "/user") || strings.Contains(result.stderr, "/repos/") {
		t.Fatalf("owner listing performed an unrelated API lookup: %s", result.stderr)
	}
	assertCloneRegistryEmpty(t, fixture)
}

func TestCLICloneMissingRepositoryListsLiteralNameMatchesWithoutFzf(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "acme/widget", want: "acme/my-widget\nacme/widget-tools\n"},
		{input: "acme/acme", want: "acme/acme-tools\n"},
		{input: "acme/no-match", want: ""},
	} {
		t.Run(test.input, func(t *testing.T) {
			fixture := newCLIFixture(t)
			env := fixture.cloneListingEnvironment(t, "acme",
				"acme/widget-tools", "acme/my-widget", "acme/WIDGET", "acme/unrelated", "acme/acme-tools")
			env = append(env, "GHQW_TEST_REPOSITORY_STATUS=404")
			result := fixture.runCLIWithEnv(t, env, "clone", test.input)
			assertStatus(t, result, 0)
			assertStdout(t, result, test.want)
			assertContains(t, result.stderr, "fake gh: HTTP 404")
			assertContains(t, result.stderr, "graphql owner=acme")
			assertCloneRegistryEmpty(t, fixture)
			assertPathMissing(t, filepath.Join(fixture.repository, "github.com", "acme"))
		})
	}
}

func TestCLICloneSelectionOffersAllRepositoriesAndClonesOnlySelectedIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		query string
	}{
		{name: "owner", input: "acme"},
		{name: "missing repository", input: "acme/widget", query: "widget"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCLIFixture(t)
			env := fixture.cloneSelectorEnvironment(t, "select", test.query, "acme/another",
				"acme/another", "acme/widget-tools")
			env = append(env, "GHQW_TEST_REPOSITORY_STATUS=404", "GH_HOST=ghe.example.com")
			result := fixture.runCLIWithEnv(t, env, "clone", "-p", test.input)
			assertStatus(t, result, 0)
			path := filepath.Join(fixture.repository, "ghe.example.com", "acme", "another")
			assertStdout(t, result, filepath.ToSlash(path)+"\n")
			assertPathExists(t, filepath.Join(path, ".git"))
			assertPathMissing(t, filepath.Join(fixture.repository, "ghe.example.com", "acme", "widget"))
			assertPathMissing(t, filepath.Join(fixture.repository, "ghe.example.com", "acme", "widget-tools"))
			origin := strings.TrimSpace(fixture.runGit(t, path, "remote", "get-url", "origin"))
			if origin != "ssh://git@ghe.example.com/acme/another" {
				t.Fatalf("selected clone origin = %q, want SSH on selected host", origin)
			}
			listed := fixture.runCLI(t, "list")
			assertStatus(t, listed, 0)
			assertStdout(t, listed, "ghe.example.com/acme/another\n")
		})
	}
}

func TestCLICloneSelectorFailuresDoNotCloneOrRegister(t *testing.T) {
	for _, test := range []struct {
		mode   string
		status int
	}{
		{mode: "cancel", status: 130},
		{mode: "no-match", status: 1},
		{mode: "failure", status: 1},
		{mode: "unknown", status: 1},
	} {
		t.Run(test.mode, func(t *testing.T) {
			fixture := newCLIFixture(t)
			env := fixture.cloneSelectorEnvironment(t, test.mode, "", "acme/widget", "acme/widget")
			result := fixture.runCLIWithEnv(t, env, "clone", "acme")
			assertStatus(t, result, test.status)
			assertStdout(t, result, "")
			assertPathMissing(t, filepath.Join(fixture.repository, "github.com", "acme"))
			assertCloneRegistryEmpty(t, fixture)
		})
	}
}

func TestCLICloneExactRepositorySkipsOwnerListingAndSelector(t *testing.T) {
	fixture := newCLIFixture(t)
	marker := filepath.Join(fixture.base, "selector-called")
	env := append(fixture.cloneSelectorEnvironment(t, "failure", "", "acme/widget", "acme/widget"),
		"GHQW_TEST_CLONE_FZF_CALLED="+marker)
	result := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")
	assertStatus(t, result, 0)
	path := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	assertStdout(t, result, filepath.ToSlash(path)+"\n")
	assertPathExists(t, filepath.Join(path, ".git"))
	assertPathMissing(t, marker)
	if strings.Contains(result.stderr, "graphql") {
		t.Fatalf("exact repository unnecessarily listed the owner: %s", result.stderr)
	}

	result = fixture.runCLIWithEnv(t, append(env, "GHQW_TEST_GH_FAIL=1"), "clone", "acme/widget")
	assertStatus(t, result, 1)
	assertContains(t, result.stderr, "already registered")
	if strings.Contains(result.stderr, "fake-gh:") || strings.Contains(result.stderr, "fake gh:") {
		t.Fatalf("registered repository invoked gh: %s", result.stderr)
	}
}

func TestCLICloneHTTPFailuresDoNotBecomeRepositorySuggestions(t *testing.T) {
	for _, status := range []string{"401", "403", "429", "500"} {
		t.Run(status, func(t *testing.T) {
			fixture := newCLIFixture(t)
			marker := filepath.Join(fixture.base, "selector-called")
			env := append(fixture.cloneSelectorEnvironment(t, "failure", "widget", "acme/another", "acme/another"),
				"GHQW_TEST_REPOSITORY_STATUS="+status, "GHQW_TEST_CLONE_FZF_CALLED="+marker)
			result := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")
			assertStatus(t, result, 1)
			assertStdout(t, result, "")
			assertContains(t, result.stderr, "HTTP "+status)
			if strings.Contains(result.stderr, "graphql") {
				t.Fatalf("HTTP failure triggered owner enumeration: %s", result.stderr)
			}
			assertPathMissing(t, marker)
			assertPathMissing(t, filepath.Join(fixture.repository, "github.com", "acme"))
			assertCloneRegistryEmpty(t, fixture)
		})
	}
}

func TestCLICloneFailedRepositoryPageDoesNotOfferPartialCandidates(t *testing.T) {
	fixture := newCLIFixture(t)
	marker := filepath.Join(fixture.base, "selector-called")
	env := append(fixture.cloneSelectorEnvironment(t, "select", "", "acme/first", "acme/first"),
		"GHQW_TEST_REPOSITORIES_FAIL=1", "GHQW_TEST_CLONE_FZF_CALLED="+marker)
	result := fixture.runCLIWithEnv(t, env, "clone", "acme")
	assertStatus(t, result, 1)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "repository page failed")
	assertPathMissing(t, marker)
	assertPathMissing(t, filepath.Join(fixture.repository, "github.com", "acme"))
	assertCloneRegistryEmpty(t, fixture)
}

func assertCloneRegistryEmpty(t *testing.T, fixture *cliFixture) {
	t.Helper()
	listed := fixture.runCLI(t, "list")
	assertStatus(t, listed, 0)
	assertStdout(t, listed, "")
}

func (fixture *cliFixture) cloneListingEnvironment(t *testing.T, owner string, names ...string) []string {
	t.Helper()
	env := fixture.envWithPathPrepended(fixture.buildFakeGh(t))
	for index, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			continue
		}
		var directories []string
		for _, directory := range filepath.SplitList(value) {
			if directory == "" {
				continue
			}
			fzfName := "fzf"
			if runtime.GOOS == "windows" {
				fzfName += ".exe"
			}
			if _, err := os.Stat(filepath.Join(directory, fzfName)); err == nil {
				continue
			}
			directories = append(directories, directory)
		}
		env[index] = key + "=" + strings.Join(directories, string(os.PathListSeparator))
	}
	return append(env, "GHQW_TEST_REPOSITORIES_OUTPUT="+cloneRepositoryResponse(t, owner, names))
}

func (fixture *cliFixture) cloneSelectorEnvironment(t *testing.T, mode, query, selected string, names ...string) []string {
	t.Helper()
	paths := fixture.buildFakeCloneFzf(t) + string(os.PathListSeparator) + fixture.buildFakeGh(t)
	return append(fixture.envWithPathPrepended(paths),
		"GHQW_TEST_REPOSITORIES_OUTPUT="+cloneRepositoryResponse(t, "acme", names),
		"GHQW_TEST_CLONE_FZF_MODE="+mode,
		"GHQW_TEST_CLONE_FZF_QUERY="+query,
		"GHQW_TEST_CLONE_FZF_SELECTED="+selected,
		"GHQW_TEST_CLONE_FZF_ROWS="+strings.Join(names, "\n"),
		"FZF_DEFAULT_OPTS=--filter=unexpected --multi --print-query",
		"FZF_DEFAULT_OPTS_FILE="+filepath.Join(fixture.base, "unexpected-fzf-options"))
}

func cloneRepositoryResponse(t *testing.T, owner string, names []string) string {
	t.Helper()
	nodes := make([]map[string]string, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, map[string]string{"nameWithOwner": name})
	}
	data, err := json.Marshal([]any{map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{
		"login": owner,
		"repositories": map[string]any{
			"nodes":    nodes,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var (
	sharedFakeCloneFzfOnce  sync.Once
	sharedFakeCloneFzfError error
	sharedFakeCloneFzfLog   string
)

func (fixture *cliFixture) buildFakeCloneFzf(t *testing.T) string {
	t.Helper()
	sharedCLIExecutable(t)
	directory := filepath.Join(sharedCLIDir, "fake-clone-fzf")
	sharedFakeCloneFzfOnce.Do(func() {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			sharedFakeCloneFzfError = err
			return
		}
		source := filepath.Join(sharedCLIDir, "fakeclonefzf.go")
		if err := os.WriteFile(source, []byte(fakeCloneFzfSource), 0o600); err != nil {
			sharedFakeCloneFzfError = err
			return
		}
		executable := filepath.Join(directory, "fzf")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		build := exec.Command("go", "build", "-o", executable, source)
		var output bytes.Buffer
		build.Stdout, build.Stderr = &output, &output
		sharedFakeCloneFzfError = build.Run()
		sharedFakeCloneFzfLog = output.String()
	})
	if sharedFakeCloneFzfError != nil {
		t.Fatalf("build shared clone fzf: %v\n%s", sharedFakeCloneFzfError, sharedFakeCloneFzfLog)
	}
	return directory
}

const fakeCloneFzfSource = `package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func fail(message string) {
	fmt.Fprintln(os.Stderr, "fake clone fzf: " + message)
	os.Exit(2)
}

func main() {
	if path := os.Getenv("GHQW_TEST_CLONE_FZF_CALLED"); path != "" {
		if err := os.WriteFile(path, []byte("called"), 0600); err != nil { fail(err.Error()) }
	}
	for _, key := range []string{"FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE"} {
		if os.Getenv(key) != "" { fail("inherited " + key) }
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
	if query != os.Getenv("GHQW_TEST_CLONE_FZF_QUERY") { fail("incorrect initial query: " + query) }
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(input) == 0 || input[len(input)-1] != 0 { fail("incomplete candidate records") }
	rows := strings.Split(string(input[:len(input)-1]), "\x00")
	if strings.Join(rows, "\n") != os.Getenv("GHQW_TEST_CLONE_FZF_ROWS") { fail("candidates were filtered or changed") }
	switch os.Getenv("GHQW_TEST_CLONE_FZF_MODE") {
	case "cancel": os.Exit(130)
	case "no-match": os.Exit(1)
	case "failure": fail("simulated failure")
	case "unknown": fmt.Print("other/unoffered\x00")
	case "select": fmt.Print(os.Getenv("GHQW_TEST_CLONE_FZF_SELECTED") + "\x00")
	default: fail("unspecified fixture response")
	}
}
`
