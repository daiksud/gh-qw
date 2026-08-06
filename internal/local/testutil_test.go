package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func initTestRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	runGit(t, "", "init", "-q", "-b", "main", path)
	runGit(
		t,
		path,
		"-c", "user.name=gh-qw tests",
		"-c", "user.email=gh-qw@example.invalid",
		"commit", "-q", "--allow-empty", "-m", "initial",
	)
}

func initTestBareRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	runGit(t, "", "init", "-q", "--bare", path)
}

func addTestWorktree(t *testing.T, mainPath, worktreePath string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(worktreePath), err)
	}
	command := append([]string{"worktree", "add", "-q"}, args...)
	command = append(command, worktreePath)
	runGit(t, mainPath, command...)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"git %s in %q error = %v\n%s",
			strings.Join(args, " "),
			dir,
			err,
			output,
		)
	}
	return string(output)
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return physical
}

func testRepository(t *testing.T, rootPath, identity string, rootIndex int) Repository {
	t.Helper()
	parts, err := ParseIdentity(identity)
	if err != nil {
		t.Fatalf("ParseIdentity(%q) error = %v", identity, err)
	}
	path, err := MainPath(rootPath, parts.Host, parts.Owner, parts.Repo)
	if err != nil {
		t.Fatalf("MainPath(%q) error = %v", identity, err)
	}
	return Repository{
		Identity:  parts.Identity,
		Host:      parts.Host,
		Owner:     parts.Owner,
		Repo:      parts.Repo,
		Path:      path,
		Root:      rootPath,
		RootIndex: rootIndex,
	}
}

type fakeWorktreeLister struct {
	worktrees []gitcmd.Worktree
	err       error
}

func (f fakeWorktreeLister) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	return append([]gitcmd.Worktree(nil), f.worktrees...), f.err
}
