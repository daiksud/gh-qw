package tests

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWorktreeCreationStartsAtSelectedMainHEADWithoutGh(t *testing.T) {
	for _, mode := range []string{"-b", "-B"} {
		for _, explicitRepository := range []bool{false, true} {
			name := mode + "/current repository"
			if explicitRepository {
				name = mode + "/explicit repository"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newCLIFixture(t)
				main := filepath.Join(fixture.repository, "github.com", "acme", "widget")
				fixture.runGit(t, "", "init", "-b", "main", main)
				fixture.runGit(t, main, "commit", "--allow-empty", "-m", "Initial commit")
				fixture.runGit(t, main, "remote", "add", "origin", "https://github.com/acme/widget.git")
				assertStatus(t, fixture.runCLI(t, "add", main), 0)

				linked := filepath.Join(fixture.worktreeRoot, "github-com-acme-widget-current", "widget")
				fixture.runGit(t, main, "worktree", "add", "-b", "current", linked)
				fixture.runGit(t, linked, "commit", "--allow-empty", "-m", "Linked checkout commit")
				fixture.runGit(t, main, "checkout", "-b", "local-base")
				fixture.runGit(t, main, "commit", "--allow-empty", "-m", "Main checkout commit")
				wantHEAD := strings.TrimSpace(fixture.runGit(t, main, "rev-parse", "HEAD"))
				if mode == "-B" {
					fixture.runGit(t, linked, "branch", "new-task", "HEAD")
				}

				args := []string{"add", "--worktree", mode, "new-task"}
				if explicitRepository {
					args = append(args, "-R", testIdentity)
				}
				command := exec.Command(fixture.binary, args...)
				command.Dir = linked
				command.Env = append(fixture.env, "GH_PATH="+filepath.Join(fixture.base, "missing-gh"))
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				if err := command.Run(); err != nil {
					t.Fatalf("add worktree: %v; stderr = %q", err, stderr.String())
				}
				destination := filepath.Join(fixture.worktreeRoot, "github-com-acme-widget-new-task", "widget")
				if stdout.String() != filepath.ToSlash(destination)+"\n" {
					t.Fatalf("stdout = %q, want new worktree path", stdout.String())
				}
				if got := strings.TrimSpace(fixture.runGit(t, destination, "rev-parse", "HEAD")); got != wantHEAD {
					t.Fatalf("new worktree HEAD = %q, want selected main HEAD %q", got, wantHEAD)
				}
			})
		}
	}
}
