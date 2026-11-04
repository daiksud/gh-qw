package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIShellInitCommand(t *testing.T) {
	fixture := newCLIFixture(t)
	for _, shell := range []string{"bash", "zsh"} {
		result := fixture.runCLI(t, "shell-init", shell)
		assertStatus(t, result, 0)
		assertContains(t, result.stdout, "gh() {")
		assertContains(t, result.stdout, "GHQW_SHELL_CD=1 command gh")
		assertStderr(t, result, "")
	}

	invalid := fixture.runCLI(t, "shell-init", "fish")
	assertStatus(t, invalid, 2)
	assertContains(t, invalid.stderr, "unsupported shell")
	assertStdout(t, invalid, "")
}

func TestCLIAddCDWithoutShellIntegrationDoesNotMutate(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)
	mainPath := filepath.Join(fixture.base, "working copy", "widget")
	fixture.cloneMain(t, remoteURL, mainPath)

	result := fixture.runCLI(t, "add", "--cd", mainPath)
	assertStatus(t, result, 2)
	assertContains(t, result.stderr, "--cd requires shell integration")
	assertStdout(t, result, "")

	registered := fixture.runCLI(t, "list")
	assertStatus(t, registered, 0)
	assertStdout(t, registered, "")
}

func TestShellAddCDNavigatesToRepositoryAndWorktree(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			fixture := newCLIFixture(t)
			remoteURL := fixture.createBareRemote(t, "acme", "widget")
			fixture.seedRemote(t, remoteURL)
			mainPath := filepath.Join(fixture.base, "working copy", "widget")
			fixture.cloneMain(t, remoteURL, mainPath)
			worktreePath := filepath.Join(
				fixture.worktreeRoot, "github-com-acme-widget-feature-nav", "widget",
			)

			result := runCDShell(t, fixture, shell, mainPath, `set -e
eval "$(gh qw shell-init "$GHQW_TEST_SHELL")"
gh qw add --cd "$GHQW_TEST_MAIN"
printf 'repository:%s\n' "$PWD"
gh qw add --worktree --cd -R github.com/acme/widget -b feature/nav main
printf 'worktree:%s\n' "$PWD"
if gh qw add --cd "$GHQW_TEST_BAD" >/dev/null 2>/dev/null; then
  printf 'unexpected successful add\n' >&2
  exit 91
fi
printf 'after-error:%s\n' "$PWD"
gh --version
`)
			assertStatus(t, result, 0)
			assertStdout(t, result,
				filepath.ToSlash(mainPath)+"\n"+
					"repository:"+filepath.ToSlash(mainPath)+"\n"+
					filepath.ToSlash(worktreePath)+"\n"+
					"worktree:"+filepath.ToSlash(worktreePath)+"\n"+
					"after-error:"+filepath.ToSlash(worktreePath)+"\n"+
					"native-gh:--version\n",
			)
			assertPathExists(t, worktreePath)
			if branch := strings.TrimSpace(fixture.runGit(t, worktreePath, "branch", "--show-current")); branch != "feature/nav" {
				t.Fatalf("worktree branch = %q, want feature/nav", branch)
			}
		})
	}
}

func TestShellAddCDNavigatesToMovedRepository(t *testing.T) {
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)
	source := filepath.Join(fixture.base, "working copy", "widget")
	fixture.cloneMain(t, remoteURL, source)
	destination := filepath.Join(fixture.repository, "github.com", "acme", "widget")

	result := runCDShell(t, fixture, "bash", source, `set -e
eval "$(gh qw shell-init bash)"
gh qw add --move --cd "$GHQW_TEST_MAIN"
printf 'moved:%s\n' "$PWD"
`)
	assertStatus(t, result, 0)
	assertStdout(t, result, filepath.ToSlash(destination)+"\n"+"moved:"+filepath.ToSlash(destination)+"\n")
	assertPathMissing(t, source)
	assertPathExists(t, destination)
}

func runCDShell(t *testing.T, fixture *cliFixture, shell, mainPath, script string) commandResult {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell integration tests require Unix Bash or Zsh")
	}
	shellPath, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s not installed: %v", shell, err)
	}

	binDir := t.TempDir()
	ghStub := `#!/bin/sh
if [ "$1" = "qw" ]; then
  shift
  exec "$GHQW_TEST_BINARY" "$@"
fi
printf 'native-gh:%s\n' "$1"
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghStub), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(shellPath, "-c", script)
	command.Dir = fixture.base
	command.Env = append(
		append([]string(nil), fixture.env...),
		"GHQW_TEST_BINARY="+fixture.binary,
		"GHQW_TEST_MAIN="+mainPath,
		"GHQW_TEST_BAD="+filepath.Join(fixture.base, "missing"),
		"GHQW_TEST_SHELL="+shell,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	return commandResult{status: exitStatus(t, err), stdout: stdout.String(), stderr: stderr.String()}
}

func TestShellAddCDHelpDoesNotNavigate(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			fixture := newCLIFixture(t)
			result := runCDShell(t, fixture, shell, "", `set -e
eval "$(gh qw shell-init "$GHQW_TEST_SHELL")"
for flag in -h --help; do
  help="$(gh qw add --cd "$flag")"
  case "$help" in
    *"Usage:"*) ;;
    *) printf 'missing help output\n' >&2; exit 92 ;;
  esac
done
printf 'after-help:%s\n' "$PWD"
`)
			assertStatus(t, result, 0)
			assertStdout(t, result, "after-help:"+filepath.ToSlash(fixture.base)+"\n")
			registered := fixture.runCLI(t, "list")
			assertStatus(t, registered, 0)
			assertStdout(t, registered, "")
		})
	}
}

func TestShellAddCDPropagatesResultWriteFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full result-write check is Linux-only")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skipf("/dev/full unavailable: %v", err)
	}
	fixture := newCLIFixture(t)
	remoteURL := fixture.createBareRemote(t, "acme", "widget")
	fixture.seedRemote(t, remoteURL)
	mainPath := filepath.Join(fixture.base, "working copy", "widget")
	fixture.cloneMain(t, remoteURL, mainPath)

	result := runCDShell(t, fixture, "bash", mainPath, `set -e
eval "$(gh qw shell-init bash)"
if gh qw add --cd "$GHQW_TEST_MAIN" >/dev/full 2>/dev/null; then
  printf 'unexpected successful output\n' >&2
  exit 93
fi
printf 'after-write-error:%s\n' "$PWD"
`)
	assertStatus(t, result, 0)
	assertStdout(t, result, "after-write-error:"+filepath.ToSlash(mainPath)+"\n")
	registered := fixture.runCLI(t, "list")
	assertStatus(t, registered, 0)
	assertStdout(t, registered, testIdentity+"\n")
}
