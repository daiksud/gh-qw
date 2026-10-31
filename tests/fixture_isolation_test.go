package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIExcludesAmbientAuthenticationAndGitConfiguration(t *testing.T) {
	// Build before poisoning the parent environment: only child execution is
	// under test, not the Go toolchain's own configuration or VCS inspection.
	initial := newCLIFixture(t)
	fakeGhDir := initial.buildFakeGh(t)
	for _, key := range []string{
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"GH_HOST", "GH_REPO", "GH_PATH", "GIT_DIR", "GIT_COMMON_DIR",
		"GIT_CONFIG_PARAMETERS", "GIT_WORK_TREE", "SSH_AUTH_SOCK",
	} {
		t.Setenv(key, "test-only-invalid-value")
	}
	fixture := newCLIFixture(t)
	env := append(fixture.envWithPathPrepended(fakeGhDir),
		"GHQW_TEST_ASSERT_CLEAN_ENV=1")
	result := fixture.runCLIWithEnv(t, env, "clone", "isolated-user/widget")
	assertStatus(t, result, 0)
	want := filepath.Join(fixture.repository, "github.com", "isolated-user", "widget")
	assertStdout(t, result, filepath.ToSlash(want)+"\n")
	assertContains(t, result.stderr, "fake gh: api github.com/repos/isolated-user/widget")
	assertPathExists(t, filepath.Join(want, ".git"))
	result = fixture.runCLI(t, "list")
	assertStatus(t, result, 0)
	assertStdout(t, result, "github.com/isolated-user/widget\n")
}

func TestCLIFixturesShareExecutablesButNotMutableState(t *testing.T) {
	first := newCLIFixture(t)
	second := newCLIFixture(t)
	firstEnv := first.envWithPathPrepended(first.buildFakeGh(t))
	secondEnv := second.envWithPathPrepended(second.buildFakeGh(t))

	for _, test := range []struct {
		fixture *cliFixture
		env     []string
		owner   string
	}{
		{first, firstEnv, "first-user"},
		{second, secondEnv, "second-user"},
	} {
		result := test.fixture.runCLIWithEnv(t, test.env, "clone", test.owner+"/widget")
		assertStatus(t, result, 0)
		want := filepath.Join(test.fixture.repository, "github.com", test.owner, "widget")
		assertStdout(t, result, filepath.ToSlash(want)+"\n")
		assertPathExists(t, filepath.Join(want, ".git"))
	}

	// A fixture-local failure response and user configuration must not alter
	// the behavior of the same immutable fake executable in another fixture.
	if err := os.WriteFile(filepath.Join(first.home, ".gitconfig"), []byte("not a valid Git configuration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := first.runCLIWithEnv(t, append(firstEnv, "GHQW_TEST_GH_FAIL=1"), "clone", "acme/rejected")
	assertStatus(t, failed, 1)
	assertStdout(t, failed, "")
	assertContains(t, failed.stderr, "fake-gh: simulated failure")
	assertPathMissing(t, filepath.Join(first.repository, "github.com", "acme", "rejected"))

	succeeded := second.runCLIWithEnv(t, secondEnv, "clone", "acme/accepted")
	assertStatus(t, succeeded, 0)
	assertPathExists(t, filepath.Join(second.repository, "github.com", "acme", "accepted", ".git"))
	result := second.runCLI(t, "list")
	assertStatus(t, result, 0)
	assertStdout(t, result, "github.com/acme/accepted\ngithub.com/second-user/widget\n")
	if strings.Contains(result.stdout, "first-user") {
		t.Fatal("repository discovery crossed fixture boundaries")
	}
}

func TestCLIUsesNativeCustomFetchMappingWithoutGitHubLookup(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)
	mainPath := filepath.Join(fixture.repository, "github.com", "acme", "widget")
	fixture.cloneMain(t, remoteURL, mainPath)
	registration := fixture.runCLI(t, "add", mainPath)
	assertStatus(t, registration, 0)
	fixture.runGit(t, mainPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/cache/origin/*")
	branch := "feature-custom"
	upstream := "refs/cache/origin/" + branch
	fixture.runGit(t, mainPath, "update-ref", upstream, "HEAD")
	env := append(fixture.env, "GH_PATH="+filepath.Join(fixture.base, "missing-gh"))
	result := fixture.runCLIWithEnv(t, env, "add", "--worktree", "-R", testIdentity, branch)
	assertStatus(t, result, 0)
	want := filepath.Join(fixture.worktreeRoot, "github-com-acme-widget-"+branch, "widget")
	assertStdout(t, result, filepath.ToSlash(want)+"\n")
	gotBranch := strings.TrimSpace(fixture.runGit(t, want, "branch", "--show-current"))
	if gotBranch != branch {
		t.Fatalf("checked-out branch = %q, want %q", gotBranch, branch)
	}
	gotUpstream := strings.TrimSpace(fixture.runGit(t, want, "rev-parse", "--symbolic-full-name", "@{upstream}"))
	if gotUpstream != upstream {
		t.Fatalf("tracking ref = %q, want %q", gotUpstream, upstream)
	}
}
