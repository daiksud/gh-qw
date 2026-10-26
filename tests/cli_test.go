package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testIdentity = "github.com/acme/widget"

type cliFixture struct {
	binary       string
	base         string
	home         string
	repository   string
	remoteRoot   string
	worktreeRoot string
	env          []string
}

type commandResult struct {
	status int
	stdout string
	stderr string
}

var (
	sharedCLIOnce     sync.Once
	sharedCLIBinary   string
	sharedCLIDir      string
	sharedCLIError    error
	sharedCLILog      string
	sharedFakeGhOnce  sync.Once
	sharedFakeGhError error
	sharedFakeGhLog   string
)

// TestMain keeps only the immutable executable shared across the package.
// Every fixture still gets independent HOME, config, roots, and Git state.
func TestMain(m *testing.M) {
	status := m.Run()
	if sharedCLIDir != "" {
		if err := os.RemoveAll(sharedCLIDir); err != nil {
			fmt.Fprintf(os.Stderr, "remove shared CLI test executable: %v\n", err)
			if status == 0 {
				status = 1
			}
		}
	}
	os.Exit(status)
}

func sharedCLIExecutable(t *testing.T) string {
	t.Helper()
	sharedCLIOnce.Do(func() {
		sharedCLIDir, sharedCLIError = os.MkdirTemp("", "gh-qw-cli-tests-")
		if sharedCLIError != nil {
			return
		}
		if supplied := os.Getenv("GHQW_TEST_EXECUTABLE"); supplied != "" {
			if !filepath.IsAbs(supplied) {
				sharedCLIError = fmt.Errorf("GHQW_TEST_EXECUTABLE must be absolute: %q", supplied)
				return
			}
			info, err := os.Stat(supplied)
			if err != nil {
				sharedCLIError = fmt.Errorf("inspect GHQW_TEST_EXECUTABLE: %w", err)
				return
			}
			if !info.Mode().IsRegular() {
				sharedCLIError = fmt.Errorf("GHQW_TEST_EXECUTABLE is not a regular file: %q", supplied)
				return
			}
			sharedCLIBinary = supplied
			return
		}
		sharedCLIBinary = filepath.Join(sharedCLIDir, "gh-qw")
		if runtime.GOOS == "windows" {
			sharedCLIBinary += ".exe"
		}
		repositoryRoot, err := filepath.Abs(filepath.Join(".."))
		if err != nil {
			sharedCLIError = fmt.Errorf("resolve repository root: %w", err)
			return
		}
		build := exec.Command("go", "build", "-o", sharedCLIBinary, ".")
		build.Dir = repositoryRoot
		var output bytes.Buffer
		build.Stdout = &output
		build.Stderr = &output
		sharedCLIError = build.Run()
		sharedCLILog = output.String()
	})
	if sharedCLIError != nil {
		t.Fatalf("prepare shared gh-qw executable: %v\n%s", sharedCLIError, sharedCLILog)
	}
	return sharedCLIBinary
}

func TestCLIRepositoryAndWorktreeLifecycle(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)

	mainPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	mainOutput := filepath.ToSlash(mainPath)

	// `gh qw clone` requires a real GitHub host and gh authentication, so its
	// cloning path is covered by the dedicated real-binary acquisition tests.
	// This local, network-free fixture seeds an ordinary clone with `origin`
	// pointing at remoteURL and the default branch checked out.
	fixture.cloneMain(t, remoteURL, mainPath)
	assertPathExists(t, mainPath)
	// Explicit registration replaces the former implicit root scan.
	registration := fixture.runCLI(t, "add", mainPath)
	assertStatus(t, registration, 0)
	assertStdout(t, registration, mainOutput+"\n")

	result := fixture.runCLI(t, "list")
	assertStatus(t, result, 0)
	assertStdout(t, result, testIdentity+"\n")
	assertStderr(t, result, "")

	featureBranch := "feature/e2e"
	featurePath := filepath.Join(
		fixture.worktreeRoot,
		"feature-e2e",
		"widget",
	)
	featureOutput := filepath.ToSlash(featurePath)
	result = fixture.runCLI(
		t,
		"add",
		"--worktree",
		"-R",
		testIdentity,
		"-b",
		featureBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, featureOutput+"\n")
	assertContains(t, result.stderr, "Preparing worktree")
	assertPathExists(t, featurePath)

	if got := strings.TrimSpace(fixture.runGit(t, featurePath, "branch", "--show-current")); got != featureBranch {
		t.Fatalf("Git branch = %q, want %q", got, featureBranch)
	}

	collision := fixture.runCLI(t, "add", "--worktree", "-R", testIdentity, "-b", "feature-e2e", "main")
	assertStatus(t, collision, 1)
	assertPathExists(t, featurePath)

	result = fixture.runCLI(t, "list", "--worktree")
	assertStatus(t, result, 0)
	assertStdout(t, result, testIdentity+"@feature-e2e\n")
	assertStderr(t, result, "")

	result = fixture.runCLI(
		t,
		"remove",
		"--worktree",
		"-R",
		testIdentity,
		featureBranch,
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "removed worktree "+featureOutput)
	assertPathMissing(t, featurePath)

	goneBranch := "gone-e2e"
	gonePath := filepath.Join(
		fixture.worktreeRoot,
		goneBranch,
		"widget",
	)
	result = fixture.runCLI(
		t,
		"add",
		"--worktree",
		"-R",
		testIdentity,
		"-b",
		goneBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertPathExists(t, gonePath)
	goneUpstream := "refs/remotes/origin/gone-e2e"
	fixture.runGit(t, mainPath, "update-ref", goneUpstream, "HEAD")
	fixture.runGit(t, mainPath, "branch", "--set-upstream-to=origin/gone-e2e", goneBranch)
	fixture.runGit(t, mainPath, "update-ref", "-d", goneUpstream)
	result = fixture.runCLI(t, "list", "--worktree", "--gone")
	assertStatus(t, result, 0)
	assertStdout(t, result, testIdentity+"@"+goneBranch+"\n")
	assertStderr(t, result, "")
	result = fixture.runCLI(t, "list", "--worktree", "--gone", "--full-path")
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(gonePath)+"\n")
	assertStderr(t, result, "")

	result = fixture.runCLI(
		t,
		"remove",
		"--worktree",
		"-R",
		testIdentity,
		"--gone",
		"--yes",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "remove slot=\""+goneBranch+"\"")
	assertContains(t, result.stderr, "removed worktree "+filepath.ToSlash(gonePath))
	assertPathMissing(t, gonePath)
	if output := fixture.runGit(t, mainPath, "show-ref", "--verify", "refs/heads/"+goneBranch); strings.TrimSpace(output) == "" {
		t.Fatalf("gone cleanup removed local branch %q", goneBranch)
	}

	staleBranch := "stale-e2e"
	stalePath := filepath.Join(
		fixture.worktreeRoot,
		staleBranch,
		"widget",
	)
	result = fixture.runCLI(
		t,
		"add",
		"--worktree",
		"-R",
		testIdentity,
		"-b",
		staleBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(stalePath)+"\n")
	if err := os.RemoveAll(stalePath); err != nil {
		t.Fatalf("remove stale worktree directory: %v", err)
	}

	fixture.runGit(t, mainPath, "worktree", "prune", "--expire", "now")
	if output := fixture.runGit(t, mainPath, "worktree", "list", "--porcelain"); strings.Contains(
		output,
		filepath.ToSlash(stalePath),
	) {
		t.Fatalf("Git prune retained stale worktree metadata:\n%s", output)
	}

	removeBranch := "topic-remove@v2"
	removePath := filepath.Join(
		fixture.worktreeRoot,
		removeBranch,
		"widget",
	)
	result = fixture.runCLI(
		t,
		"add",
		"--worktree",
		"-R",
		testIdentity,
		"-b",
		removeBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(removePath)+"\n")

	result = fixture.runCLI(t, "remove", "--worktree", "-R", testIdentity, removeBranch)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "removed worktree "+filepath.ToSlash(removePath))
	assertPathMissing(t, removePath)
	assertPathExists(t, mainPath)

	wholeBranch := "whole-linked"
	wholePath := filepath.Join(
		fixture.worktreeRoot,
		wholeBranch,
		"widget",
	)
	result = fixture.runCLI(
		t,
		"add",
		"--worktree",
		"-R",
		testIdentity,
		"-b",
		wholeBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(wholePath)+"\n")

	if runtime.GOOS == "windows" {
		result = fixture.runCLI(t, "remove", "--dry-run", testIdentity)
		assertStatus(t, result, 0)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "Removal plan:")
		assertContains(t, result.stderr, filepath.ToSlash(wholePath))
		assertContains(t, result.stderr, mainOutput)
		assertPathExists(t, wholePath)
		assertPathExists(t, mainPath)
	} else {
		result = fixture.runRemoveWithTTY(t, testIdentity)
		assertStatus(t, result, 0)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "Removal plan:")
		assertContains(t, result.stderr, filepath.ToSlash(wholePath))
		assertContains(t, result.stderr, mainOutput)
		assertContains(t, result.stderr, "removed main repository")
		assertPathMissing(t, wholePath)
		assertPathMissing(t, mainPath)

		result = fixture.runCLI(t, "list")
		assertStatus(t, result, 0)
		assertStdout(t, result, "")
		assertStderr(t, result, "")
	}
}

func TestCLICloneAcquiresOnlyNewDestinationsAndRegistersSuccess(t *testing.T) {
	fixture := newCLIFixture(t)
	env := fixture.envWithPathPrepended(fixture.buildFakeGh(t))
	cache := filepath.Join(fixture.home, "xdg-cache", "ghqw", "accounts.json")
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "legacy account cache is deliberately invalid\n"
	if err := os.WriteFile(cache, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	result := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(path)+"\n")
	assertPathExists(t, filepath.Join(path, ".git"))

	listed := fixture.runCLI(t, "list")
	assertStatus(t, listed, 0)
	assertStdout(t, listed, "github.com/acme/widget\n")

	registeredCollision := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")
	assertStatus(t, registeredCollision, 1)
	assertStdout(t, registeredCollision, "")
	assertContains(t, registeredCollision.stderr, "already registered")
	assertContains(t, registeredCollision.stderr, "use add")

	forgotten := fixture.runCLI(t, "forget", "acme/widget")
	assertStatus(t, forgotten, 0)
	marker := filepath.Join(path, "README.md")
	const preserved = "existing checkout must not be overwritten\n"
	if err := os.WriteFile(marker, []byte(preserved), 0o644); err != nil {
		t.Fatal(err)
	}
	unregisteredCollision := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")
	assertStatus(t, unregisteredCollision, 1)
	assertStdout(t, unregisteredCollision, "")
	assertContains(t, unregisteredCollision.stderr, "already exists")
	assertContains(t, unregisteredCollision.stderr, "use add")
	if data, err := os.ReadFile(marker); err != nil || string(data) != preserved {
		t.Fatalf("existing destination changed: %q, %v", data, err)
	}

	failedPath := filepath.Join(fixture.repository, "github.com", "acme", "rejected")
	failed := fixture.runCLIWithEnv(t, append(env, "GHQW_TEST_GH_FAIL=1"), "clone", "acme/rejected")
	assertStatus(t, failed, 1)
	assertStdout(t, failed, "")
	assertPathMissing(t, failedPath)

	listed = fixture.runCLI(t, "list")
	assertStatus(t, listed, 0)
	assertStdout(t, listed, "")

	for _, args := range [][]string{
		{"get", "acme/legacy"},
		{"migrate", path},
		{"worktree", "add", "topic"},
		{"worktree", "list"},
		{"worktree", "prune"},
	} {
		obsolete := fixture.runCLIWithEnv(t, env, args...)
		assertStatus(t, obsolete, 2)
		assertStdout(t, obsolete, "")
		assertContains(t, obsolete.stderr, "unknown command")
	}
	assertPathMissing(t, filepath.Join(fixture.repository, "github.com", "acme", "legacy"))

	data, err := os.ReadFile(cache)
	if err != nil || string(data) != sentinel {
		t.Fatalf("legacy account cache changed: %q, %v", data, err)
	}
}

func TestCLIAPILookupsUseGHPathAndKeepStdoutPure(t *testing.T) {
	fixture := newCLIFixture(t)
	ghPath := filepath.Join(fixture.buildFakeGh(t), "gh")
	if runtime.GOOS == "windows" {
		ghPath += ".exe"
	}
	env := append(fixture.env, "GH_PATH="+ghPath, "GH_HOST=ghe.example.com", "GHQW_TEST_API_OUTPUT=octocat")
	mainPath := filepath.Join(fixture.repository, "ghe.example.com", "octocat", "widget")

	result := fixture.runCLIWithEnv(t, env, "clone", "widget")
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(mainPath)+"\n")
	assertContains(t, result.stderr, "fake gh: api ghe.example.com/user")
	assertPathExists(t, mainPath)

	// The explicit repository host wins over the ambient host. The local main
	// branch supplies the start point returned by the external REST lookup.
	env = append(env, "GH_HOST=unrelated.example.com", "GHQW_TEST_API_OUTPUT=main")
	result = fixture.runCLIWithEnv(t, env, "add", "--worktree", "-R", "ghe.example.com/octocat/widget", "-b", "feature-from-api")
	assertStatus(t, result, 0)
	worktreePath := filepath.Join(fixture.worktreeRoot, "feature-from-api", "widget")
	assertStdout(t, result, filepath.ToSlash(worktreePath)+"\n")
	assertContains(t, result.stderr, "fake gh: api ghe.example.com/repos/octocat/widget")

	result = fixture.runCLIWithEnv(t, append(env, "GHQW_TEST_API_OUTPUT="), "add", "--worktree", "-R", "ghe.example.com/octocat/widget", "-b", "feature-missing-default")
	assertStatus(t, result, 1)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "API response did not include default_branch")
	assertPathMissing(t, filepath.Join(fixture.worktreeRoot, "feature-missing-default", "widget"))

	// An unusable GH_PATH must not affect help, local inputs, or attaching an
	// existing branch: none requires resolving or starting gh.
	localEnv := append(env, "GH_PATH="+filepath.Join(fixture.base, "missing-gh"))
	fixture.runGit(t, mainPath, "branch", "feature-existing", "main")
	for _, args := range [][]string{
		{"--help"}, {"--version"}, {"root"}, {"list"},
		{"add", "--worktree", "-R", "ghe.example.com/octocat/widget", "feature-existing"},
	} {
		result := fixture.runCLIWithEnv(t, localEnv, args...)
		assertStatus(t, result, 0)
		if strings.Contains(result.stderr, "fake gh:") {
			t.Fatalf("local command %q invoked gh: %s", args, result.stderr)
		}
	}
	result = fixture.runCLIWithEnv(t, localEnv, "clone", "ghe.example.com/octocat/widget")
	assertStatus(t, result, 1)
	assertContains(t, result.stderr, "already registered")
	if strings.Contains(result.stderr, "missing-gh") || strings.Contains(result.stderr, "fake gh:") {
		t.Fatalf("registered clone collision invoked gh: %s", result.stderr)
	}
}

// TestCLICloneDoesNotBlockOnAnOpenCallerStandardInput verifies that `gh qw clone`
// completes without waiting for EOF from the calling process. The test keeps
// gh-qw's standard input connected to an open pipe and uses a fake gh on PATH
// so the scenario remains local and network-free.
func TestCLICloneDoesNotBlockOnAnOpenCallerStandardInput(t *testing.T) {
	fixture := newCLIFixture(t)
	fakeGhDir := fixture.buildFakeGh(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, fixture.binary, "clone", "acme/widget")
	command.Dir = fixture.base
	command.Env = fixture.envWithPathPrepended(fakeGhDir)

	// The open write end keeps EOF from reaching the command. Cleanup closes
	// both ends so any blocked copy goroutine can exit with the test.
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create standard input pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
	})
	command.Stdin = stdinReader
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		t.Fatalf("start gh qw clone: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf(
				"gh qw clone failed: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String(),
			)
		}
	case <-ctx.Done():
		t.Fatalf(
			"gh qw clone did not exit within 30s while its standard input stayed open;"+
				" it appears to be blocking on the caller's standard input again\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String(),
		)
	}

	wantPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	assertPathExists(t, wantPath)
	if got, want := strings.TrimSpace(stdout.String()), filepath.ToSlash(wantPath); got != want {
		t.Fatalf("stdout = %q, want %q\nstderr:\n%s", got, want, stderr.String())
	}
}

// TestCLICloneFailsClearlyWhenGhIsNotInstalled verifies that cloning requires
// `gh` on PATH. The command must report a runtime failure on stderr without
// printing a path or creating the requested repository.
func TestCLICloneFailsClearlyWhenGhIsNotInstalled(t *testing.T) {
	fixture := newCLIFixture(t)

	result := fixture.runCLIWithEnv(t, fixture.envWithoutGh(t), "clone", "acme/widget")

	assertStatus(t, result, 1)
	if trimmed := strings.TrimSpace(result.stdout); trimmed != "" {
		t.Fatalf("stdout = %q, want empty; nothing should have been cloned", trimmed)
	}
	if trimmed := strings.TrimSpace(result.stderr); trimmed == "" {
		t.Fatal("stderr is empty, want a clear diagnostic that gh could not be found")
	}
	// gh-qw wraps every command failure with a "gh-qw: " prefixed message
	// (see Execute in internal/cmd/app.go); its presence confirms this is
	// gh-qw's own reported failure, not an unrelated crash or empty output.
	assertContains(t, result.stderr, "gh-qw:")

	wantPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	assertPathMissing(t, wantPath)
}

// TestCLICloneFailsClearlyWhenGhSubprocessFails verifies that a failing gh
// subprocess produces a clear runtime diagnostic without stdout, repository
// creation, or a panic.
func TestCLICloneFailsClearlyWhenGhSubprocessFails(t *testing.T) {
	fixture := newCLIFixture(t)
	fakeGhDir := fixture.buildFakeGh(t)

	env := append(fixture.envWithPathPrepended(fakeGhDir), "GHQW_TEST_GH_FAIL=1")
	result := fixture.runCLIWithEnv(t, env, "clone", "acme/widget")

	assertStatus(t, result, 1)
	if trimmed := strings.TrimSpace(result.stdout); trimmed != "" {
		t.Fatalf("stdout = %q, want empty; nothing should have been cloned", trimmed)
	}
	if trimmed := strings.TrimSpace(result.stderr); trimmed == "" {
		t.Fatal("stderr is empty, want a clear diagnostic surfacing the gh subprocess failure")
	}
	if strings.Contains(result.stderr, "panic:") || strings.Contains(result.stderr, "goroutine ") {
		t.Fatalf("stderr looks like a Go panic/stack trace, want a clean failure message instead:\n%s", result.stderr)
	}
	// The fake gh's distinctive message is relayed live to gh-qw's own
	// stderr (see Runner.runDir/outputDir in internal/ghcmd), and gh-qw
	// additionally wraps the failure with its own "gh-qw: " prefixed
	// message (see Execute in internal/cmd/app.go).
	assertContains(t, result.stderr, "fake-gh: simulated failure")
	assertContains(t, result.stderr, "gh-qw:")

	wantPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	assertPathMissing(t, wantPath)
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()

	rawBase := t.TempDir()
	base, err := filepath.EvalSymlinks(rawBase)
	if err != nil {
		t.Fatalf("resolve physical fixture path: %v", err)
	}
	fixture := &cliFixture{
		binary:       sharedCLIExecutable(t),
		base:         base,
		home:         filepath.Join(base, "home"),
		repository:   filepath.Join(base, "repositories"),
		remoteRoot:   filepath.Join(base, "remotes"),
		worktreeRoot: filepath.Join(base, "repositories", "worktrees"),
	}
	for _, directory := range []string{
		fixture.home,
		fixture.repository,
		fixture.remoteRoot,
		fixture.worktreeRoot,
		filepath.Join(fixture.home, "gh"),
		filepath.Join(fixture.home, "xdg-config"),
		filepath.Join(fixture.home, "xdg-data"),
		filepath.Join(fixture.home, "xdg-cache"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create fixture directory %q: %v", directory, err)
		}
	}

	fixture.env = isolatedEnvironment(fixture)
	return fixture
}

// isolatedEnvironment removes inherited GitHub authentication and host settings
// before installing fixture-scoped paths. Tests add explicit environment values
// only when those values are part of the contract under test.
func isolatedEnvironment(fixture *cliFixture) []string {
	blocked := map[string]struct{}{
		"EMAIL": {}, "HOME": {}, "USERPROFILE": {}, "APPDATA": {}, "LOCALAPPDATA": {},
		"LANG": {}, "LC_ALL": {}, "NO_COLOR": {}, "SSH_AUTH_SOCK": {}, "SSH_AGENT_PID": {},
		"XDG_CACHE_HOME": {}, "XDG_CONFIG_HOME": {}, "XDG_DATA_HOME": {},
	}
	environment := make([]string, 0, len(os.Environ())+28)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if _, remove := blocked[key]; remove {
			continue
		}
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "GH_") ||
			strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "GHQ_") ||
			strings.HasPrefix(key, "GHQW_") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(
		environment,
		"HOME="+fixture.home,
		"USERPROFILE="+fixture.home,
		"APPDATA="+filepath.Join(fixture.home, "xdg-config"),
		"LOCALAPPDATA="+filepath.Join(fixture.home, "xdg-data"),
		"XDG_CONFIG_HOME="+filepath.Join(fixture.home, "xdg-config"),
		"XDG_DATA_HOME="+filepath.Join(fixture.home, "xdg-data"),
		"XDG_CACHE_HOME="+filepath.Join(fixture.home, "xdg-cache"),
		"GH_CONFIG_DIR="+filepath.Join(fixture.home, "gh"),
		"GHQW_ROOT="+strings.Join(
			[]string{fixture.repository, fixture.remoteRoot},
			string(os.PathListSeparator),
		),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=gh-qw e2e",
		"GIT_AUTHOR_EMAIL=gh-qw-e2e@example.invalid",
		"GIT_COMMITTER_NAME=gh-qw e2e",
		"GIT_COMMITTER_EMAIL=gh-qw-e2e@example.invalid",
		"EMAIL=gh-qw-e2e@example.invalid",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=protocol.file.allow",
		"GIT_CONFIG_VALUE_0=always",
		"GIT_CONFIG_KEY_1=init.defaultBranch",
		"GIT_CONFIG_VALUE_1=main",
		"LC_ALL=C",
		"LANG=C",
		"NO_COLOR=1",
	)
	return environment
}

func (fixture *cliFixture) createBareRemote(
	t *testing.T,
	owner string,
	repository string,
) string {
	t.Helper()

	path := filepath.Join(
		fixture.remoteRoot,
		"github.com",
		owner,
		repository,
	)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create bare remote parent: %v", err)
	}
	fixture.runGit(t, "", "init", "--bare", "--initial-branch=main", path)
	return cliFileURL(path)
}

func (fixture *cliFixture) seedRemote(t *testing.T, remoteURL string) {
	t.Helper()

	seed := filepath.Join(fixture.base, "seed", "widget")
	fixture.initRepository(t, seed, remoteURL, true)
}

// cloneMain seeds mainPath as an ordinary clone of remoteURL. The local,
// network-free fixture cannot exercise the authenticated `gh repo clone`
// path, whose argument mapping is covered by internal/cmd and internal/ghcmd
// unit tests.
func (fixture *cliFixture) cloneMain(t *testing.T, remoteURL, mainPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatalf("create main repository parent: %v", err)
	}
	fixture.runGit(t, "", "clone", "--quiet", remoteURL, mainPath)
}

// fakeGhSource delegates authentication entirely to the external command boundary.
// API calls require an explicit fixture response; any auth preflight or mutation
// fails the test. Clone creates a local Git repository, keeping tests network-free.
const fakeGhSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if os.Getenv("GHQW_TEST_GH_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "fake-gh: simulated failure")
		os.Exit(1)
	}
	if os.Getenv("GHQW_TEST_ASSERT_CLEAN_ENV") == "1" {
		for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_REPO", "GH_PATH", "GIT_DIR", "GIT_COMMON_DIR", "GIT_CONFIG_PARAMETERS", "GIT_WORK_TREE", "SSH_AUTH_SOCK"} {
			if _, present := os.LookupEnv(key); present {
				fmt.Fprintln(os.Stderr, "fake gh: unexpected ambient variable " + key)
				os.Exit(1)
			}
		}
	}
	args := os.Args[1:]
	switch {
	case len(args) >= 2 && args[0] == "repo" && args[1] == "clone":
		cloneRepo(args[2:])
	case len(args) == 4 && args[0] == "repo" && args[1] == "sync" && args[2] == "--source":
		fmt.Fprintln(os.Stderr, "fake gh: synchronized "+args[3])
	case len(args) == 6 && args[0] == "api" && args[1] == "--hostname" && args[4] == "--jq":
		output, ok := os.LookupEnv("GHQW_TEST_API_OUTPUT")
		if !ok || (args[3] == "user" && args[5] != ".login // empty") ||
			(strings.HasPrefix(args[3], "repos/") && args[5] != ".default_branch // empty") {
			fmt.Fprintln(os.Stderr, "fake gh: unexpected API lookup")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, output)
		fmt.Fprintln(os.Stderr, "fake gh: api "+args[2]+"/"+args[3])
	default:
		fmt.Fprintf(os.Stderr, "fake gh: unsupported invocation %q\n", strings.Join(args, " "))
		os.Exit(1)
	}
}

func cloneRepo(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "fake gh: repo clone requires a URL and destination")
		os.Exit(1)
	}
	url, dest := args[0], args[1]

	run := func(name string, arguments ...string) {
		cmd := exec.Command(name, arguments...)
		cmd.Dir = dest
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "fake gh: %s %s: %v\n", name, strings.Join(arguments, " "), err)
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "fake gh: create destination: %v\n", err)
		os.Exit(1)
	}
	readme := filepath.Join(dest, "README.md")
	if err := os.WriteFile(readme, []byte("fake gh clone of "+url+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "fake gh: write fixture file: %v\n", err)
		os.Exit(1)
	}
	run("git", "init", "--quiet", "--initial-branch=main")
	run("git", "add", "README.md")
	run("git", "commit", "--quiet", "-m", "fake gh clone")
	run("git", "remote", "add", "origin", url)
}
`

// buildFakeGh shares only the immutable fake executable. Its responses, HOME,
// configuration, and repository state are supplied by each fixture's environment.
func (fixture *cliFixture) buildFakeGh(t *testing.T) string {
	t.Helper()
	sharedCLIExecutable(t)
	fakeGhDir := filepath.Join(sharedCLIDir, "fakebin")
	sharedFakeGhOnce.Do(func() {
		if err := os.MkdirAll(fakeGhDir, 0o755); err != nil {
			sharedFakeGhError = err
			return
		}
		sourcePath := filepath.Join(sharedCLIDir, "fakegh.go")
		if err := os.WriteFile(sourcePath, []byte(fakeGhSource), 0o600); err != nil {
			sharedFakeGhError = err
			return
		}
		fakeGhPath := filepath.Join(fakeGhDir, "gh")
		if runtime.GOOS == "windows" {
			fakeGhPath += ".exe"
		}
		build := exec.Command("go", "build", "-o", fakeGhPath, sourcePath)
		var output bytes.Buffer
		build.Stdout = &output
		build.Stderr = &output
		sharedFakeGhError = build.Run()
		sharedFakeGhLog = output.String()
	})
	if sharedFakeGhError != nil {
		t.Fatalf("build shared fake gh: %v\n%s", sharedFakeGhError, sharedFakeGhLog)
	}
	return fakeGhDir
}

// envWithPathPrepended returns a copy of fixture.env with directory
// prepended onto the PATH entry, so an executable placed there (see
// buildFakeGh) is resolved before any same-named executable already on
// PATH.
func (fixture *cliFixture) envWithPathPrepended(directory string) []string {
	env := make([]string, 0, len(fixture.env)+1)
	found := false
	for _, entry := range fixture.env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			env = append(env, key+"="+directory+string(os.PathListSeparator)+value)
			found = true
			continue
		}
		env = append(env, entry)
	}
	if !found {
		env = append(env, "PATH="+directory)
	}
	return env
}

// envWithoutGh returns a copy of fixture.env whose PATH entry omits every
// directory that contains a gh (gh.exe on Windows) executable, so gh-qw's
// own `gh` subprocess calls cannot resolve to any real gh, including one
// installed on the host running this test. This is hermetic: it does not
// rely on the host lacking gh, it strips PATH down so gh cannot be found
// regardless. Directories that only contain unrelated tools (such as git)
// are kept so unrelated PATH lookups keep working.
func (fixture *cliFixture) envWithoutGh(t *testing.T) []string {
	t.Helper()

	ghName := "gh"
	if runtime.GOOS == "windows" {
		ghName = "gh.exe"
	}

	env := make([]string, 0, len(fixture.env))
	for _, entry := range fixture.env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			env = append(env, entry)
			continue
		}
		var kept []string
		for _, directory := range filepath.SplitList(value) {
			if directory == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(directory, ghName)); err == nil {
				continue
			}
			kept = append(kept, directory)
		}
		env = append(env, key+"="+strings.Join(kept, string(os.PathListSeparator)))
	}
	return env
}

func cliFileURL(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

func (fixture *cliFixture) initRepository(
	t *testing.T,
	path string,
	remoteURL string,
	push bool,
) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create repository %q: %v", path, err)
	}
	fixture.runGit(t, "", "init", "--initial-branch=main", path)
	if err := os.WriteFile(
		filepath.Join(path, "tracked.txt"),
		[]byte("gh-qw end-to-end fixture\n"),
		0o644,
	); err != nil {
		t.Fatalf("write repository fixture: %v", err)
	}
	fixture.runGit(t, path, "add", "tracked.txt")
	fixture.runGit(t, path, "commit", "-m", "Initialize test repository")
	fixture.runGit(t, path, "remote", "add", "origin", remoteURL)
	if push {
		fixture.runGit(t, path, "push", "-u", "origin", "main")
	}
}

func (fixture *cliFixture) runCLI(t *testing.T, arguments ...string) commandResult {
	t.Helper()

	return fixture.runCLIWithEnv(t, fixture.env, arguments...)
}

// runCLIWithEnv behaves like runCLI but runs the built binary with env
// instead of fixture.env, so a test can exercise a deliberately modified
// PATH (see envWithoutGh, envWithPathPrepended) without disturbing
// fixture.env for other tests or callers.
func (fixture *cliFixture) runCLIWithEnv(t *testing.T, env []string, arguments ...string) commandResult {
	t.Helper()

	command := exec.Command(fixture.binary, arguments...)
	command.Dir = fixture.base
	command.Env = env
	command.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return commandResult{
		status: exitStatus(t, err),
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func (fixture *cliFixture) runRemoveWithTTY(
	t *testing.T,
	selector string,
) commandResult {
	t.Helper()

	script, err := exec.LookPath("script")
	if err != nil {
		t.Fatalf("find script for controlling-terminal test: %v", err)
	}
	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	shellCommand := `exec "$GHQW_TEST_BINARY" remove "$GHQW_TEST_SELECTOR" >"$GHQW_TEST_STDOUT"`
	arguments := []string{"-q", os.DevNull, "sh", "-c", shellCommand}
	if runtime.GOOS == "linux" {
		arguments = []string{"-q", "-c", shellCommand, os.DevNull}
	}
	command := exec.CommandContext(ctx, script, arguments...)
	command.Dir = fixture.base
	command.Env = append(
		append([]string(nil), fixture.env...),
		"GHQW_TEST_BINARY="+fixture.binary,
		"GHQW_TEST_SELECTOR="+selector,
		"GHQW_TEST_STDOUT="+stdoutPath,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("create script stdin: %v", err)
	}
	terminal := newPromptCapture("Proceed with removal?")
	var scriptStderr bytes.Buffer
	command.Stdout = terminal
	command.Stderr = &scriptStderr
	if err := command.Start(); err != nil {
		t.Fatalf("start remove with controlling terminal: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()

	var commandErr error
	select {
	case <-terminal.prompt:
		if _, err := io.WriteString(stdin, "y\n"); err != nil {
			t.Fatalf("answer removal prompt: %v", err)
		}
		select {
		case commandErr = <-done:
			_ = stdin.Close()
		case <-ctx.Done():
			_ = stdin.Close()
			t.Fatalf("remove did not finish after confirmation: %v\n%s", ctx.Err(), terminal.String())
		}
	case commandErr = <-done:
		_ = stdin.Close()
	case <-ctx.Done():
		_ = stdin.Close()
		t.Fatalf("remove did not reach confirmation prompt: %v\n%s", ctx.Err(), terminal.String())
	}

	stdout, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read captured remove stdout: %v", err)
	}
	return commandResult{
		status: exitStatus(t, commandErr),
		stdout: string(stdout),
		stderr: terminal.String() + scriptStderr.String(),
	}
}

func (fixture *cliFixture) runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()

	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	} else {
		command.Dir = fixture.base
	}
	command.Env = fixture.env
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output.String())
	}
	return output.String()
}

type promptCapture struct {
	mu       sync.Mutex
	output   bytes.Buffer
	needle   string
	prompt   chan struct{}
	signaled sync.Once
}

func newPromptCapture(needle string) *promptCapture {
	return &promptCapture{
		needle: needle,
		prompt: make(chan struct{}),
	}
}

func (capture *promptCapture) Write(data []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()

	written, err := capture.output.Write(data)
	if strings.Contains(capture.output.String(), capture.needle) {
		capture.signaled.Do(func() {
			close(capture.prompt)
		})
	}
	return written, err
}

func (capture *promptCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.output.String()
}

func exitStatus(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	t.Fatalf("execute command: %v", err)
	return -1
}

func assertStatus(t *testing.T, result commandResult, want int) {
	t.Helper()
	if result.status != want {
		t.Fatalf(
			"exit status = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			result.status,
			want,
			result.stdout,
			result.stderr,
		)
	}
}

func assertStdout(t *testing.T, result commandResult, want string) {
	t.Helper()
	if result.stdout != want {
		t.Fatalf("stdout = %q, want %q\nstderr:\n%s", result.stdout, want, result.stderr)
	}
}

func assertStderr(t *testing.T, result commandResult, want string) {
	t.Helper()
	if result.stderr != want {
		t.Fatalf("stderr = %q, want %q\nstdout:\n%s", result.stderr, want, result.stdout)
	}
}

func assertContains(t *testing.T, value string, want string) {
	t.Helper()
	if !strings.Contains(value, want) {
		t.Fatalf("%q does not contain %q", value, want)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected path %q to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected path %q to be absent, stat error = %v", path, err)
	}
}
