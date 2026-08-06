package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const worktreePorcelainFixture = "" +
	"worktree /repo/main\x00" +
	"HEAD 1111111111111111111111111111111111111111\x00" +
	"branch refs/heads/main\x00" +
	"future-key ignored value\x00" +
	"\x00" +
	"worktree /repo/feature\nwith-space \x00" +
	"HEAD 2222222222222222222222222222222222222222\x00" +
	"detached\x00" +
	"locked checkout in use\x00" +
	"prunable gitdir missing\nsecond line\x00" +
	"\x00" +
	"worktree /repo/bare\x00" +
	"bare\x00" +
	"locked\x00"

func TestParseWorktreeListPorcelainZ(t *testing.T) {
	got, err := parseWorktreeList([]byte(worktreePorcelainFixture))
	if err != nil {
		t.Fatalf("parseWorktreeList() error = %v", err)
	}

	want := []Worktree{
		{
			Path:   "/repo/main",
			HEAD:   "1111111111111111111111111111111111111111",
			Branch: "main",
		},
		{
			Path:           "/repo/feature\nwith-space ",
			HEAD:           "2222222222222222222222222222222222222222",
			Detached:       true,
			Locked:         true,
			LockedReason:   "checkout in use",
			Prunable:       true,
			PrunableReason: "gitdir missing\nsecond line",
		},
		{
			Path:   "/repo/bare",
			Bare:   true,
			Locked: true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreeList() = %#v, want %#v", got, want)
	}
}

func TestParseWorktreeListHandlesRecordWithoutBlankSeparator(t *testing.T) {
	input := "" +
		"worktree /one\x00" +
		"HEAD one\x00" +
		"branch refs/heads/feature/x\x00" +
		"worktree /two\x00" +
		"HEAD two\x00" +
		"detached"

	got, err := parseWorktreeList([]byte(input))
	if err != nil {
		t.Fatalf("parseWorktreeList() error = %v", err)
	}
	want := []Worktree{
		{Path: "/one", HEAD: "one", Branch: "feature/x"},
		{Path: "/two", HEAD: "two", Detached: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreeList() = %#v, want %#v", got, want)
	}
}

func TestParseWorktreeListRejectsMalformedKnownFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "field before record", input: "HEAD abc\x00"},
		{name: "empty path", input: "worktree\x00"},
		{name: "empty HEAD", input: "worktree /repo\x00HEAD\x00"},
		{name: "empty branch", input: "worktree /repo\x00branch\x00"},
		{
			name: "branch and detached",
			input: "" +
				"worktree /repo\x00" +
				"branch refs/heads/main\x00" +
				"detached\x00",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseWorktreeList([]byte(test.input)); err == nil {
				t.Fatal("parseWorktreeList() error = nil, want malformed input error")
			}
		})
	}
}

func TestParseWorktreeListEmptyInput(t *testing.T) {
	worktrees, err := parseWorktreeList(nil)
	if err != nil {
		t.Fatalf("parseWorktreeList() error = %v", err)
	}
	if len(worktrees) != 0 {
		t.Fatalf("parseWorktreeList() returned %#v, want empty", worktrees)
	}
}

func TestWorktreeListInvokesPorcelainZAndParses(t *testing.T) {
	runner, recorder := newHelperRunner(t, "worktree-list")
	runner.Stderr = &bytes.Buffer{}
	dir := t.TempDir()

	worktrees, err := runner.WorktreeList(context.Background(), dir)
	if err != nil {
		t.Fatalf("WorktreeList() error = %v", err)
	}
	if len(worktrees) != 3 || worktrees[0].Branch != "main" || !worktrees[1].Detached {
		t.Fatalf("WorktreeList() returned unexpected values: %#v", worktrees)
	}
	want := []string{"worktree", "list", "--porcelain", "-z"}
	command := recorder.last(t)
	if !slicesEqual(command.args, want) {
		t.Fatalf("args = %#v, want %#v", command.args, want)
	}
	if command.cmd.Dir != dir {
		t.Fatalf("command dir = %q, want %q", command.cmd.Dir, dir)
	}
}

func TestWorktreeListReportsParseAndGitErrors(t *testing.T) {
	t.Run("malformed output", func(t *testing.T) {
		runner, _ := newHelperRunner(t, "worktree-list-malformed")
		runner.Stderr = &bytes.Buffer{}
		_, err := runner.WorktreeList(context.Background(), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "parse git worktree list output") {
			t.Fatalf("WorktreeList() error = %v, want parse context", err)
		}
	})

	t.Run("git failure", func(t *testing.T) {
		runner, _ := newHelperRunner(t, "exit-2")
		runner.Stderr = &bytes.Buffer{}
		_, err := runner.WorktreeList(context.Background(), t.TempDir())
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || commandErr.ExitCode != 2 {
			t.Fatalf("WorktreeList() error = %v, want command exit 2", err)
		}
	})
}

func TestWorktreeAddArgumentOrdering(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	runner.Stdout = &bytes.Buffer{}
	runner.Stderr = &bytes.Buffer{}
	dir := t.TempDir()

	err := runner.WorktreeAdd(context.Background(), dir, WorktreeAddOptions{
		Path:      "/worktrees/feature/x",
		Commitish: "origin/feature/x",
		NewBranch: "feature/x",
		Force:     true,
		Tracking:  TrackingEnabled,
	})
	if err != nil {
		t.Fatalf("WorktreeAdd() error = %v", err)
	}

	want := []string{
		"worktree",
		"add",
		"--force",
		"-b", "feature/x",
		"--track",
		"--",
		"/worktrees/feature/x",
		"origin/feature/x",
	}
	if got := recorder.last(t).args; !slicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestWorktreeAddSupportsResetDetachAndOrphanModes(t *testing.T) {
	tests := []struct {
		name    string
		options WorktreeAddOptions
		want    []string
	}{
		{
			name: "reset branch",
			options: WorktreeAddOptions{
				Path:        "/worktrees/reset",
				Commitish:   "main",
				ResetBranch: "reset",
				Tracking:    TrackingDisabled,
			},
			want: []string{
				"worktree", "add",
				"-B", "reset",
				"--no-track",
				"--", "/worktrees/reset", "main",
			},
		},
		{
			name: "detached",
			options: WorktreeAddOptions{
				Path:      "/worktrees/detached",
				Commitish: "HEAD~1",
				Detach:    true,
			},
			want: []string{
				"worktree", "add",
				"--detach",
				"--", "/worktrees/detached", "HEAD~1",
			},
		},
		{
			name: "orphan",
			options: WorktreeAddOptions{
				Path:      "/worktrees/orphan",
				NewBranch: "orphan",
				Orphan:    true,
			},
			want: []string{
				"worktree", "add",
				"--orphan",
				"-b", "orphan",
				"--", "/worktrees/orphan",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, recorder := newHelperRunner(t, "success")
			runner.Stdout = &bytes.Buffer{}
			runner.Stderr = &bytes.Buffer{}
			if err := runner.WorktreeAdd(context.Background(), t.TempDir(), test.options); err != nil {
				t.Fatalf("WorktreeAdd() error = %v", err)
			}
			if got := recorder.last(t).args; !slicesEqual(got, test.want) {
				t.Fatalf("args = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestWorktreeAddRejectsConflictingOptions(t *testing.T) {
	tests := []WorktreeAddOptions{
		{},
		{Path: "/worktree", NewBranch: "new", ResetBranch: "reset"},
		{Path: "/worktree", Detach: true, NewBranch: "new"},
		{Path: "/worktree", Detach: true, Orphan: true},
		{Path: "/worktree", Detach: true, Tracking: TrackingEnabled},
		{Path: "/worktree", Orphan: true, Commitish: "main"},
		{Path: "/worktree", Tracking: TrackingMode(99)},
	}
	for _, options := range tests {
		runner, recorder := newHelperRunner(t, "success")
		if err := runner.WorktreeAdd(context.Background(), "/repo", options); err == nil {
			t.Fatalf("WorktreeAdd(%#v) error = nil, want validation failure", options)
		}
		if len(recorder.commands) != 0 {
			t.Fatalf("WorktreeAdd(%#v) executed a command", options)
		}
	}
}

func TestWorktreeRemovePruneAndRepairArguments(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		run  func(*Runner) error
		want []string
	}{
		{
			name: "remove",
			run: func(r *Runner) error {
				return r.WorktreeRemove(context.Background(), dir, WorktreeRemoveOptions{
					Path:  "/worktrees/-feature",
					Force: true,
				})
			},
			want: []string{"worktree", "remove", "--force", "--", "/worktrees/-feature"},
		},
		{
			name: "prune",
			run: func(r *Runner) error {
				return r.WorktreePrune(context.Background(), dir, WorktreePruneOptions{
					DryRun:  true,
					Verbose: true,
					Expire:  "2.weeks.ago",
				})
			},
			want: []string{
				"worktree", "prune",
				"--dry-run",
				"--verbose",
				"--expire", "2.weeks.ago",
			},
		},
		{
			name: "repair paths",
			run: func(r *Runner) error {
				return r.WorktreeRepair(
					context.Background(),
					dir,
					"/worktrees/one",
					"/worktrees/-two",
				)
			},
			want: []string{
				"worktree", "repair",
				"--",
				"/worktrees/one",
				"/worktrees/-two",
			},
		},
		{
			name: "repair all",
			run: func(r *Runner) error {
				return r.WorktreeRepair(context.Background(), dir)
			},
			want: []string{"worktree", "repair"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, recorder := newHelperRunner(t, "success")
			runner.Stdout = &bytes.Buffer{}
			runner.Stderr = &bytes.Buffer{}
			if err := test.run(runner); err != nil {
				t.Fatalf("helper error = %v", err)
			}
			if got := recorder.last(t).args; !slicesEqual(got, test.want) {
				t.Fatalf("args = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestWorktreeRemoveAndRepairValidatePaths(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	if err := runner.WorktreeRemove(context.Background(), "/repo", WorktreeRemoveOptions{}); err == nil {
		t.Fatal("WorktreeRemove() error = nil, want missing path error")
	}
	if err := runner.WorktreeRepair(context.Background(), "/repo", ""); err == nil {
		t.Fatal("WorktreeRepair() error = nil, want empty path error")
	}
	if len(recorder.commands) != 0 {
		t.Fatalf("validation failures executed %d commands", len(recorder.commands))
	}
}

// TestListWorktreesRealSHA256RepositoryPreservesObjectFormatWidth exercises
// GIT-OBJFMT-01 end to end: it drives a real `git init --object-format=sha256`
// repository through the actual Runner.WorktreeList subprocess call (not just
// parseWorktreeList against a hand-built fixture) and confirms the resulting
// Worktree.HEAD is relayed verbatim at its native 64-hex-digit width, exactly
// matching a ground-truth `git rev-parse HEAD` obtained independently of the
// code under test. This complements the synthetic-fixture width coverage in
// internal/cmd/worktree_list_test.go's
// TestWorktreeListPorcelainPreservesUnbornObjectWidth, which never shells out
// to a real git binary.
func TestListWorktreesRealSHA256RepositoryPreservesObjectFormatWidth(t *testing.T) {
	t.Parallel()

	probeDir := t.TempDir()
	if _, err := gitcmdTryRealGit("", "init", "--object-format=sha256", "-q", probeDir); err != nil {
		t.Skipf("installed git does not support --object-format=sha256: %v", err)
	}

	dir := t.TempDir()
	gitcmdRunRealGit(t, "", "init", "--object-format=sha256", "-q", dir)

	readmePath := filepath.Join(dir, "README")
	if err := os.WriteFile(readmePath, []byte("gh-qw sha256 worktree test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", readmePath, err)
	}
	gitcmdRunRealGit(t, dir, "add", "README")
	gitcmdRunRealGit(t, dir, "commit", "-q", "-m", "initial")

	// Ground truth obtained independently of the code under test.
	wantHEAD := strings.TrimSpace(gitcmdRunRealGit(t, dir, "rev-parse", "HEAD"))
	if len(wantHEAD) != 64 {
		t.Fatalf("ground-truth HEAD length = %d, want 64: %q", len(wantHEAD), wantHEAD)
	}

	runner := &Runner{Executable: "git"}
	worktrees, err := runner.WorktreeList(context.Background(), dir)
	if err != nil {
		t.Fatalf("WorktreeList() error = %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("WorktreeList() returned %d worktrees, want 1: %#v", len(worktrees), worktrees)
	}

	got := worktrees[0].HEAD
	if len(got) != 64 {
		t.Fatalf("Worktree.HEAD length = %d, want 64: %q", len(got), got)
	}
	if got != wantHEAD {
		t.Fatalf("Worktree.HEAD = %q, want %q", got, wantHEAD)
	}
}

// gitcmdRunRealGit runs a real git subprocess in dir (or the current
// directory when dir is empty), failing the test on error. It isolates the
// subprocess from ambient user/system Git configuration, mirroring the
// hermetic env used by internal/local's runGit and
// internal/migrate/backpointer_test.go's migrateRunGit test helpers.
func gitcmdRunRealGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := gitcmdTryRealGit(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %q error = %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return string(output)
}

// gitcmdTryRealGit runs a real git subprocess in dir (or the current
// directory when dir is empty) and returns its combined output without
// failing the test, so callers can use it as a capability probe.
func gitcmdTryRealGit(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=gh-qw tests",
		"GIT_AUTHOR_EMAIL=gh-qw@example.invalid",
		"GIT_COMMITTER_NAME=gh-qw tests",
		"GIT_COMMITTER_EMAIL=gh-qw@example.invalid",
	)
	return cmd.CombinedOutput()
}
