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

func TestCLIRepositoryAndWorktreeLifecycle(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)

	mainPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	mainOutput := filepath.ToSlash(mainPath)

	// `gh qw get` requires a real GitHub host and gh authentication, so its
	// cloning path is covered by internal/cmd and internal/ghcmd unit tests.
	// This local, network-free fixture seeds an ordinary clone with `origin`
	// pointing at remoteURL and the default branch checked out.
	fixture.cloneMain(t, remoteURL, mainPath)
	assertPathExists(t, mainPath)

	result := fixture.runCLI(t, "list")
	assertStatus(t, result, 0)
	assertStdout(t, result, testIdentity+"\n")
	assertStderr(t, result, "")

	featureBranch := "feature/e2e"
	featurePath := filepath.Join(
		fixture.worktreeRoot,
		"github.com",
		"acme",
		"widget",
		filepath.FromSlash(featureBranch),
	)
	featureOutput := filepath.ToSlash(featurePath)
	result = fixture.runCLI(
		t,
		"worktree",
		"add",
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

	result = fixture.runCLI(t, "list", "--worktree")
	assertStatus(t, result, 0)
	assertStdout(
		t,
		result,
		testIdentity+"\n"+testIdentity+"@"+featureBranch+"\n",
	)
	assertStderr(t, result, "")

	head := strings.TrimSpace(fixture.runGit(t, mainPath, "rev-parse", "HEAD"))
	result = fixture.runCLI(t, "worktree", "list", "-R", testIdentity)
	assertStatus(t, result, 0)
	assertStdout(
		t,
		result,
		fmt.Sprintf(
			"%s %s [main]\n%s@%s %s [%s]\n",
			testIdentity,
			head,
			testIdentity,
			featureBranch,
			head,
			featureBranch,
		),
	)
	assertStderr(t, result, "")

	result = fixture.runCLI(
		t,
		"worktree",
		"list",
		"-R",
		testIdentity,
		"--porcelain",
	)
	assertStatus(t, result, 0)
	assertStdout(
		t,
		result,
		fmt.Sprintf(
			"identity %s\npath %s\nhead %s\nkind main\nbranch main\n\n"+
				"identity %s@%s\npath %s\nhead %s\nkind linked\nbranch %s\n\n",
			testIdentity,
			mainOutput,
			head,
			testIdentity,
			featureBranch,
			featureOutput,
			head,
			featureBranch,
		),
	)
	assertStderr(t, result, "")

	result = fixture.runCLI(
		t,
		"worktree",
		"remove",
		"-R",
		testIdentity,
		featureBranch,
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "removed worktree "+featureOutput)
	assertPathMissing(t, featurePath)

	staleBranch := "stale/e2e"
	stalePath := filepath.Join(
		fixture.worktreeRoot,
		"github.com",
		"acme",
		"widget",
		filepath.FromSlash(staleBranch),
	)
	result = fixture.runCLI(
		t,
		"worktree",
		"add",
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

	result = fixture.runCLI(
		t,
		"worktree",
		"prune",
		"-R",
		testIdentity,
		"--dry-run",
		"--verbose",
		"--expire",
		"now",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "Removing worktrees/")
	if output := fixture.runGit(t, mainPath, "worktree", "list", "--porcelain"); !strings.Contains(
		output,
		filepath.ToSlash(stalePath),
	) {
		t.Fatalf("dry-run pruned registered worktree metadata:\n%s", output)
	}

	result = fixture.runCLI(
		t,
		"worktree",
		"prune",
		"-R",
		testIdentity,
		"--expire",
		"now",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	if output := fixture.runGit(t, mainPath, "worktree", "list", "--porcelain"); strings.Contains(
		output,
		filepath.ToSlash(stalePath),
	) {
		t.Fatalf("prune retained stale registered worktree metadata:\n%s", output)
	}

	rmBranch := "topic/rm@v2"
	rmPath := filepath.Join(
		fixture.worktreeRoot,
		"github.com",
		"acme",
		"widget",
		filepath.FromSlash(rmBranch),
	)
	result = fixture.runCLI(
		t,
		"worktree",
		"add",
		"-R",
		testIdentity,
		"-b",
		rmBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(rmPath)+"\n")

	if runtime.GOOS == "windows" {
		result = fixture.runCLI(t, "rm", "--dry-run", testIdentity+"@"+rmBranch)
		assertStatus(t, result, 0)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "Removal plan:")
		assertContains(t, result.stderr, filepath.ToSlash(rmPath))
		assertPathExists(t, rmPath)
	} else {
		result = fixture.runRemoveWithTTY(t, testIdentity+"@"+rmBranch)
		assertStatus(t, result, 0)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "Removal plan:")
		assertContains(t, result.stderr, filepath.ToSlash(rmPath))
		assertContains(t, result.stderr, "removed linked worktree")
		assertPathMissing(t, rmPath)
		assertPathExists(t, mainPath)
	}

	wholeBranch := "whole/linked"
	wholePath := filepath.Join(
		fixture.worktreeRoot,
		"github.com",
		"acme",
		"widget",
		filepath.FromSlash(wholeBranch),
	)
	result = fixture.runCLI(
		t,
		"worktree",
		"add",
		"-R",
		testIdentity,
		"-b",
		wholeBranch,
		"main",
	)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(wholePath)+"\n")

	if runtime.GOOS == "windows" {
		result = fixture.runCLI(t, "rm", "--dry-run", testIdentity)
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

	migrateRemoteURL := fixture.createBareRemote(t, "acme", "migrated")
	migrateSource := filepath.Join(fixture.base, "legacy", "migrated")
	fixture.initRepository(t, migrateSource, migrateRemoteURL, false)
	migrateDestination := filepath.Join(
		fixture.repository,
		"github.com",
		"acme",
		"migrated",
	)

	result = fixture.runCLI(t, "migrate", "--dry-run", migrateSource)
	assertStatus(t, result, 0)
	assertStdout(t, result, "")
	assertContains(t, result.stderr, "Migration plan:")
	assertContains(
		t,
		result.stderr,
		filepath.ToSlash(migrateSource)+" -> "+filepath.ToSlash(migrateDestination),
	)
	assertPathExists(t, migrateSource)
	assertPathMissing(t, migrateDestination)
}

// TestCLIGetDoesNotBlockOnAnOpenCallerStandardInput verifies that `gh qw get`
// completes without waiting for EOF from the calling process. The test keeps
// gh-qw's standard input connected to an open pipe and uses a fake gh on PATH
// so the scenario remains local and network-free.
func TestCLIGetDoesNotBlockOnAnOpenCallerStandardInput(t *testing.T) {
	fixture := newCLIFixture(t)
	fakeGhDir := fixture.buildFakeGh(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, fixture.binary, "get", "acme/widget")
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
		t.Fatalf("start gh qw get: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf(
				"gh qw get failed: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String(),
			)
		}
	case <-ctx.Done():
		t.Fatalf(
			"gh qw get did not exit within 30s while its standard input stayed open;"+
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

// TestCLIGetFailsClearlyWhenGhIsNotInstalled verifies that cloning requires
// `gh` on PATH. The command must report a runtime failure on stderr without
// printing a path or creating the requested repository.
func TestCLIGetFailsClearlyWhenGhIsNotInstalled(t *testing.T) {
	fixture := newCLIFixture(t)

	result := fixture.runCLIWithEnv(t, fixture.envWithoutGh(t), "get", "acme/widget")

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

// TestCLIGetFailsClearlyWhenGhSubprocessFails verifies that a failing gh
// subprocess produces a clear runtime diagnostic without stdout, repository
// creation, or a panic.
func TestCLIGetFailsClearlyWhenGhSubprocessFails(t *testing.T) {
	fixture := newCLIFixture(t)
	failingGhDir := fixture.buildFailingFakeGh(t)

	result := fixture.runCLIWithEnv(t, fixture.envWithPathPrepended(failingGhDir), "get", "acme/widget")

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

// TestCLIListFzfSelectionAndExitStatuses exercises `list --fzf` end-to-end
// against a fake fzf (see buildFakeFzf) standing in for the real,
// interactive picker. It confirms: a real selection prints the entry's
// absolute path exactly like other gh-qw path output (so a caller can
// `cd "$(gh qw list --fzf)"`); canceling fzf (Esc/Ctrl-C, exit 130) and fzf
// finding no match (exit 1) both exit with that same status but produce no
// output at all, including no "gh-qw: " diagnostic line; and any other fzf
// failure (exit 2) is reported clearly with gh-qw's usual diagnostic
// prefix.
func TestCLIListFzfSelectionAndExitStatuses(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)

	mainPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	fixture.cloneMain(t, remoteURL, mainPath)
	assertPathExists(t, mainPath)

	fakeFzfDir := fixture.buildFakeFzf(t)
	baseEnv := fixture.envWithPathPrepended(fakeFzfDir)

	t.Run("selection prints absolute path", func(t *testing.T) {
		env := append(append([]string(nil), baseEnv...), "FAKE_FZF_SELECT="+testIdentity)
		result := fixture.runCLIWithEnv(t, env, "list", "--fzf")
		assertStatus(t, result, 0)
		assertStdout(t, result, filepath.ToSlash(mainPath)+"\n")
		assertStderr(t, result, "")
	})

	t.Run("cancellation exits 130 without any output", func(t *testing.T) {
		env := append(append([]string(nil), baseEnv...), "FAKE_FZF_EXIT_CODE=130")
		result := fixture.runCLIWithEnv(t, env, "list", "--fzf")
		assertStatus(t, result, 130)
		assertStdout(t, result, "")
		assertStderr(t, result, "")
	})

	t.Run("no match exits 1 without any output", func(t *testing.T) {
		env := append(append([]string(nil), baseEnv...), "FAKE_FZF_EXIT_CODE=1")
		result := fixture.runCLIWithEnv(t, env, "list", "--fzf")
		assertStatus(t, result, 1)
		assertStdout(t, result, "")
		assertStderr(t, result, "")
	})

	t.Run("other fzf failure is reported", func(t *testing.T) {
		env := append(append([]string(nil), baseEnv...), "FAKE_FZF_EXIT_CODE=2")
		result := fixture.runCLIWithEnv(t, env, "list", "--fzf")
		assertStatus(t, result, 1)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "gh-qw:")
	})
}

// TestCLIWorktreeAddHerdrIntegration exercises `worktree add --herdr`
// end-to-end against a fake herdr (see buildFakeHerdr) standing in for the
// real, running Herdr server: a successful integration still writes only
// the new worktree's absolute path to stdout (herdr's own JSON response
// never leaks there); a failed Herdr workspace creation is a status-1
// error that still keeps the worktree and its path output (see
// worktree_add.go); and an explicit --herdr outside of a Herdr-managed
// pane (HERDR_ENV unset) is a status-2 usage error before any mutation.
func TestCLIWorktreeAddHerdrIntegration(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)

	mainPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	fixture.cloneMain(t, remoteURL, mainPath)
	assertPathExists(t, mainPath)

	fakeHerdrDir := fixture.buildFakeHerdr(t)
	baseEnv := append(fixture.envWithPathPrepended(fakeHerdrDir), "HERDR_ENV=1")

	t.Run("successful integration keeps stdout as the path alone", func(t *testing.T) {
		branch := "feature/herdr-success"
		featurePath := filepath.Join(
			fixture.worktreeRoot, "github.com", "acme", "widget", filepath.FromSlash(branch),
		)
		env := append(append([]string(nil), baseEnv...), "FAKE_HERDR_WORKSPACE_ID=w9")

		result := fixture.runCLIWithEnv(
			t, env, "worktree", "add", "-R", testIdentity, "--herdr", "-b", branch, "main",
		)
		assertStatus(t, result, 0)
		assertStdout(t, result, filepath.ToSlash(featurePath)+"\n")
		if strings.Contains(result.stdout, "workspace_id") || strings.Contains(result.stdout, "cli:workspace") {
			t.Fatalf("stdout leaked herdr's own JSON response: %q", result.stdout)
		}
		assertPathExists(t, featurePath)
	})

	t.Run("Herdr failure is a status-1 error that keeps the worktree and its path", func(t *testing.T) {
		branch := "feature/herdr-failure"
		featurePath := filepath.Join(
			fixture.worktreeRoot, "github.com", "acme", "widget", filepath.FromSlash(branch),
		)
		env := append(append([]string(nil), baseEnv...), "FAKE_HERDR_CREATE_EXIT_CODE=1")

		result := fixture.runCLIWithEnv(
			t, env, "worktree", "add", "-R", testIdentity, "--herdr", "-b", branch, "main",
		)
		assertStatus(t, result, 1)
		assertStdout(t, result, filepath.ToSlash(featurePath)+"\n")
		assertContains(t, result.stderr, "gh-qw:")
		assertPathExists(t, featurePath)
	})

	t.Run("explicit flag outside a Herdr session is a status-2 usage error", func(t *testing.T) {
		branch := "feature/herdr-outside"
		featurePath := filepath.Join(
			fixture.worktreeRoot, "github.com", "acme", "widget", filepath.FromSlash(branch),
		)
		env := fixture.envWithPathPrepended(fakeHerdrDir) // deliberately without HERDR_ENV=1

		result := fixture.runCLIWithEnv(
			t, env, "worktree", "add", "-R", testIdentity, "--herdr", "-b", branch, "main",
		)
		assertStatus(t, result, 2)
		assertStdout(t, result, "")
		assertContains(t, result.stderr, "HERDR_ENV")
		assertPathMissing(t, featurePath)
	})
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()

	rawBase := t.TempDir()
	base, err := filepath.EvalSymlinks(rawBase)
	if err != nil {
		t.Fatalf("resolve physical fixture path: %v", err)
	}
	fixture := &cliFixture{
		binary:       filepath.Join(base, "bin", "gh-qw"),
		base:         base,
		home:         filepath.Join(base, "home"),
		repository:   filepath.Join(base, "repositories"),
		remoteRoot:   filepath.Join(base, "remotes"),
		worktreeRoot: filepath.Join(base, "worktrees"),
	}
	if runtime.GOOS == "windows" {
		fixture.binary += ".exe"
	}
	for _, directory := range []string{
		filepath.Dir(fixture.binary),
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

	repositoryRoot, err := filepath.Abs(filepath.Join(".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	build := exec.Command("go", "build", "-o", fixture.binary, ".")
	build.Dir = repositoryRoot
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		t.Fatalf("build gh-qw: %v\n%s", err, buildOutput.String())
	}

	fixture.env = isolatedEnvironment(fixture)
	return fixture
}

// isolatedEnvironment strips the inherited process environment down to a
// deterministic baseline, then sets fixture-scoped replacements below. It
// blocks GH_TOKEN and GITHUB_TOKEN so the host machine's own gh
// authentication (if any) can never bypass the account-resolution scenarios
// these tests construct explicitly.
func isolatedEnvironment(fixture *cliFixture) []string {
	blocked := map[string]struct{}{
		"EMAIL":               {},
		"GH_CONFIG_DIR":       {},
		"GH_TOKEN":            {},
		"GHQ_ROOT":            {},
		"GHQW_HERDR":          {},
		"GHQW_ROOT":           {},
		"GHQW_WORKTREE_ROOT":  {},
		"GITHUB_TOKEN":        {},
		"GIT_ALLOW_PROTOCOL":  {},
		"GIT_AUTHOR_EMAIL":    {},
		"GIT_AUTHOR_NAME":     {},
		"GIT_COMMITTER_EMAIL": {},
		"GIT_COMMITTER_NAME":  {},
		"GIT_CONFIG_COUNT":    {},
		"GIT_CONFIG_GLOBAL":   {},
		"GIT_CONFIG_NOSYSTEM": {},
		"GIT_CONFIG_SYSTEM":   {},
		"GIT_DIR":             {},
		"HERDR_ENV":           {},
		"GIT_TERMINAL_PROMPT": {},
		"GIT_WORK_TREE":       {},
		"HOME":                {},
		"LANG":                {},
		"LC_ALL":              {},
		"NO_COLOR":            {},
		"XDG_CACHE_HOME":      {},
		"XDG_CONFIG_HOME":     {},
		"XDG_DATA_HOME":       {},
	}
	environment := make([]string, 0, len(os.Environ())+24)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, remove := blocked[key]; !remove {
			environment = append(environment, entry)
		}
	}
	environment = append(
		environment,
		"HOME="+fixture.home,
		"XDG_CONFIG_HOME="+filepath.Join(fixture.home, "xdg-config"),
		"XDG_DATA_HOME="+filepath.Join(fixture.home, "xdg-data"),
		"XDG_CACHE_HOME="+filepath.Join(fixture.home, "xdg-cache"),
		"GH_CONFIG_DIR="+filepath.Join(fixture.home, "gh"),
		"GHQW_ROOT="+strings.Join(
			[]string{fixture.repository, fixture.remoteRoot},
			string(os.PathListSeparator),
		),
		"GHQW_WORKTREE_ROOT="+fixture.worktreeRoot,
		"GHQ_ROOT="+filepath.Join(fixture.base, "unused-ghq"),
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

// fakeGhSource is a minimal stand-in for the real `gh` executable, built by
// buildFakeGh. It only implements enough of `gh repo clone` and `gh auth
// status`/`gh auth token` to let a real `gh qw get` invocation run end-to-end
// without a real GitHub host or gh authentication, keeping
// TestCLIGetDoesNotBlockOnAnOpenCallerStandardInput local and network-free
// like the rest of this suite. `repo clone <url> <dest> ...` creates an
// ordinary one-commit Git repository at <dest>; `auth status --json hosts`
// reports exactly one successfully authenticated "acme" account on
// github.com, and `auth token --user acme ...` returns a fixed fake token
// for it, so gh-qw's account resolution (internal/ghauth) deterministically
// selects that sole account for the "acme/widget" repository this test
// requests, regardless of what gh accounts, if any, happen to be configured
// on the machine running this test.
const fakeGhSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) >= 2 && args[0] == "repo" && args[1] == "clone":
		cloneRepo(args[2:])
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		fmt.Fprintln(os.Stdout, "{\"hosts\":{\"github.com\":[{\"state\":\"success\",\"active\":true,\"login\":\"acme\"}]}}")
	case len(args) >= 2 && args[0] == "auth" && args[1] == "token":
		authToken(args[2:])
	default:
		fmt.Fprintf(os.Stderr, "fake gh: unsupported invocation %q\n", strings.Join(args, " "))
		os.Exit(1)
	}
}

func authToken(args []string) {
	login := ""
	for index, value := range args {
		if value == "--user" && index+1 < len(args) {
			login = args[index+1]
		}
	}
	if login != "acme" {
		fmt.Fprintf(os.Stderr, "fake gh: no token available for user %q\n", login)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "gho_fake_e2e_token")
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

// buildFakeGh compiles fakeGhSource into an executable named gh (gh.exe on
// Windows) and returns the directory containing it, ready to be prepended
// onto PATH (see envWithPathPrepended) so gh-qw's own `gh` subprocess calls
// resolve to it instead of any real gh installed on the host running this
// test.
func (fixture *cliFixture) buildFakeGh(t *testing.T) string {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "fakegh.go")
	if err := os.WriteFile(sourcePath, []byte(fakeGhSource), 0o644); err != nil {
		t.Fatalf("write fake gh source: %v", err)
	}

	fakeGhDir := filepath.Join(fixture.base, "fakebin")
	if err := os.MkdirAll(fakeGhDir, 0o755); err != nil {
		t.Fatalf("create fake gh directory: %v", err)
	}
	fakeGhPath := filepath.Join(fakeGhDir, "gh")
	if runtime.GOOS == "windows" {
		fakeGhPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", fakeGhPath, sourcePath)
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		t.Fatalf("build fake gh: %v\n%s", err, buildOutput.String())
	}
	return fakeGhDir
}

// failingFakeGhSource is a fake `gh` that unconditionally exits 1 with a
// distinctive stderr message for every invocation, regardless of
// arguments. It confirms that gh-qw surfaces subprocess failure clearly
// instead of succeeding silently or crashing.
const failingFakeGhSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "fake-gh: simulated failure")
	os.Exit(1)
}
`

// buildFailingFakeGh compiles failingFakeGhSource into an executable named
// gh (gh.exe on Windows) and returns the directory containing it, ready to
// be prepended onto PATH (see envWithPathPrepended). It uses a separate
// fixture subdirectory so tests can select the failing executable explicitly.
func (fixture *cliFixture) buildFailingFakeGh(t *testing.T) string {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "failingfakegh.go")
	if err := os.WriteFile(sourcePath, []byte(failingFakeGhSource), 0o644); err != nil {
		t.Fatalf("write failing fake gh source: %v", err)
	}

	failingGhDir := filepath.Join(fixture.base, "fakebin-failing")
	if err := os.MkdirAll(failingGhDir, 0o755); err != nil {
		t.Fatalf("create failing fake gh directory: %v", err)
	}
	failingGhPath := filepath.Join(failingGhDir, "gh")
	if runtime.GOOS == "windows" {
		failingGhPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", failingGhPath, sourcePath)
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		t.Fatalf("build failing fake gh: %v\n%s", err, buildOutput.String())
	}
	return failingGhDir
}

// fakeFzfSource is a minimal stand-in for the real fzf executable, built by
// buildFakeFzf. It always drains its candidate list from stdin first,
// mirroring real fzf's own stdin usage (see internal/fzf.Runner.Select), so
// gh-qw's stdin-copy goroutine never sees a broken pipe. Its behavior is
// then controlled entirely by environment variables instead of an
// interactive terminal: a non-empty FAKE_FZF_EXIT_CODE exits with that
// status and no output, letting a test simulate cancellation (130), no
// match (1), or an fzf-reported error (2); otherwise it prints
// FAKE_FZF_SELECT to stdout and exits 0, simulating a real selection. This
// lets CLI tests exercise gh-qw's real `list --fzf` pipeline end-to-end
// without depending on a real, interactive fzf being installed or a
// controlling terminal being available in CI.
const fakeFzfSource = `package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	if _, err := io.ReadAll(os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "fake fzf: read stdin: %v\n", err)
		os.Exit(1)
	}

	if raw := os.Getenv("FAKE_FZF_EXIT_CODE"); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake fzf: invalid FAKE_FZF_EXIT_CODE %q\n", raw)
			os.Exit(1)
		}
		os.Exit(code)
	}

	selection := os.Getenv("FAKE_FZF_SELECT")
	if selection == "" {
		fmt.Fprintln(os.Stderr, "fake fzf: FAKE_FZF_SELECT is required")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, selection)
}
`

// buildFakeFzf compiles fakeFzfSource into an executable named fzf (fzf.exe
// on Windows) and returns the directory containing it, ready to be
// prepended onto PATH (see envWithPathPrepended) so gh-qw's own `list
// --fzf` resolves it instead of any real fzf installed on the host running
// this test.
func (fixture *cliFixture) buildFakeFzf(t *testing.T) string {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "fakefzf.go")
	if err := os.WriteFile(sourcePath, []byte(fakeFzfSource), 0o644); err != nil {
		t.Fatalf("write fake fzf source: %v", err)
	}

	fakeFzfDir := filepath.Join(fixture.base, "fakebin-fzf")
	if err := os.MkdirAll(fakeFzfDir, 0o755); err != nil {
		t.Fatalf("create fake fzf directory: %v", err)
	}
	fakeFzfPath := filepath.Join(fakeFzfDir, "fzf")
	if runtime.GOOS == "windows" {
		fakeFzfPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", fakeFzfPath, sourcePath)
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		t.Fatalf("build fake fzf: %v\n%s", err, buildOutput.String())
	}
	return fakeFzfDir
}

// fakeHerdrSource is a minimal stand-in for the real herdr executable,
// built by buildFakeHerdr. It answers `workspace create`, `workspace
// close`, and `worktree list` with the same JSON envelope shapes the real
// herdr uses (see internal/herdr), so CLI tests can exercise gh-qw's real
// `worktree add --herdr`/`worktree remove --herdr` pipeline end to end
// without depending on a real, running Herdr server.
//
// FAKE_HERDR_CREATE_EXIT_CODE and FAKE_HERDR_CLOSE_EXIT_CODE, when set,
// make the matching subcommand fail with that exit status and herdr's own
// documented JSON error envelope on stderr, instead of succeeding.
// FAKE_HERDR_WORKSPACE_ID overrides the workspace ID a successful
// `workspace create` reports (default "w1"). FAKE_HERDR_FIND_PATH and
// FAKE_HERDR_FIND_ID make `worktree list` report one worktree already open
// under that workspace ID (default "w1"); leaving FAKE_HERDR_FIND_PATH
// unset reports no worktrees at all.
const fakeHerdrSource = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func writeJSON(id string, result any) {
	data, err := json.Marshal(struct {
		ID     string ` + "`json:\"id\"`" + `
		Result any    ` + "`json:\"result\"`" + `
	}{ID: id, Result: result})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake herdr: marshal response: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func writeError(id string, exitCode int) {
	data, err := json.Marshal(struct {
		ID    string ` + "`json:\"id\"`" + `
		Error struct {
			Code    string ` + "`json:\"code\"`" + `
			Message string ` + "`json:\"message\"`" + `
		} ` + "`json:\"error\"`" + `
	}{
		ID: id,
		Error: struct {
			Code    string ` + "`json:\"code\"`" + `
			Message string ` + "`json:\"message\"`" + `
		}{Code: "fake_error", Message: "fake herdr failure"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake herdr: marshal error response: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, string(data))
	os.Exit(exitCode)
}

func exitCodeFromEnv(name string) (int, bool) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, false
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake herdr: invalid %s %q\n", name, raw)
		os.Exit(1)
	}
	return code, true
}

func main() {
	args := os.Args[1:]
	switch {
	case len(args) >= 2 && args[0] == "workspace" && args[1] == "create":
		if code, failing := exitCodeFromEnv("FAKE_HERDR_CREATE_EXIT_CODE"); failing {
			writeError("cli:workspace:create", code)
			return
		}
		id := os.Getenv("FAKE_HERDR_WORKSPACE_ID")
		if id == "" {
			id = "w1"
		}
		writeJSON("cli:workspace:create", map[string]any{
			"type":      "workspace_created",
			"workspace": map[string]string{"workspace_id": id},
		})
	case len(args) >= 2 && args[0] == "workspace" && args[1] == "close":
		if code, failing := exitCodeFromEnv("FAKE_HERDR_CLOSE_EXIT_CODE"); failing {
			writeError("cli:workspace:close", code)
			return
		}
		writeJSON("cli:workspace:close", map[string]string{"type": "ok"})
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "list":
		worktrees := []map[string]string{}
		if path := os.Getenv("FAKE_HERDR_FIND_PATH"); path != "" {
			id := os.Getenv("FAKE_HERDR_FIND_ID")
			if id == "" {
				id = "w1"
			}
			worktrees = append(worktrees, map[string]string{"path": path, "open_workspace_id": id})
		}
		writeJSON("cli:worktree:list", map[string]any{
			"type":      "worktree_list",
			"worktrees": worktrees,
		})
	default:
		fmt.Fprintf(os.Stderr, "fake herdr: unsupported invocation %v\n", args)
		os.Exit(2)
	}
}
`

// buildFakeHerdr compiles fakeHerdrSource into an executable named herdr
// (herdr.exe on Windows) and returns the directory containing it, ready to
// be prepended onto PATH (see envWithPathPrepended) so gh-qw's own
// `worktree add --herdr`/`worktree remove --herdr` resolves it instead of
// any real herdr installed on the host running this test.
func (fixture *cliFixture) buildFakeHerdr(t *testing.T) string {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "fakeherdr.go")
	if err := os.WriteFile(sourcePath, []byte(fakeHerdrSource), 0o644); err != nil {
		t.Fatalf("write fake herdr source: %v", err)
	}

	fakeHerdrDir := filepath.Join(fixture.base, "fakebin-herdr")
	if err := os.MkdirAll(fakeHerdrDir, 0o755); err != nil {
		t.Fatalf("create fake herdr directory: %v", err)
	}
	fakeHerdrPath := filepath.Join(fakeHerdrDir, "herdr")
	if runtime.GOOS == "windows" {
		fakeHerdrPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", fakeHerdrPath, sourcePath)
	var buildOutput bytes.Buffer
	build.Stdout = &buildOutput
	build.Stderr = &buildOutput
	if err := build.Run(); err != nil {
		t.Fatalf("build fake herdr: %v\n%s", err, buildOutput.String())
	}
	return fakeHerdrDir
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

	shellCommand := `exec "$GHQW_TEST_BINARY" rm "$GHQW_TEST_SELECTOR" >"$GHQW_TEST_STDOUT"`
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
		t.Fatalf("start rm with controlling terminal: %v", err)
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
			t.Fatalf("rm did not finish after confirmation: %v\n%s", ctx.Err(), terminal.String())
		}
	case commandErr = <-done:
		_ = stdin.Close()
	case <-ctx.Done():
		_ = stdin.Close()
		t.Fatalf("rm did not reach confirmation prompt: %v\n%s", ctx.Err(), terminal.String())
	}

	stdout, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read captured rm stdout: %v", err)
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
