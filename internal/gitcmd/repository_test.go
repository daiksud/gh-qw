package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBranchUpstreamsUsesQualifiedStructuredRefs(t *testing.T) {
	runner, recorder := newHelperRunner(t, "branch-upstreams")
	runner.Stderr = &bytes.Buffer{}
	dir := t.TempDir()

	got, err := runner.BranchUpstreams(context.Background(), dir)
	if err != nil {
		t.Fatalf("BranchUpstreams() error = %v", err)
	}
	want := []BranchUpstream{
		{Branch: "local", Upstream: "refs/heads/main"},
		{Branch: "main"},
		{Branch: "topic", Upstream: "refs/remotes/custom/review/topic"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BranchUpstreams() = %#v, want %#v", got, want)
	}
	command := recorder.last(t)
	wantArgs := []string{
		"for-each-ref",
		"--format=%(refname)%00%(upstream)%00",
		"refs/heads/",
	}
	if !slicesEqual(command.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", command.args, wantArgs)
	}
	if command.cmd.Dir != dir {
		t.Fatalf("command dir = %q, want %q", command.cmd.Dir, dir)
	}
}

func TestBranchUpstreamsRejectsMalformedOutputAndGitFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		text string
	}{
		{name: "malformed", mode: "branch-upstreams-malformed", text: "parse git for-each-ref output"},
		{name: "git failure", mode: "exit-2", text: "git command failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, _ := newHelperRunner(t, test.mode)
			runner.Stderr = &bytes.Buffer{}
			_, err := runner.BranchUpstreams(context.Background(), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.text) {
				t.Fatalf("BranchUpstreams() error = %v, want text %q", err, test.text)
			}
		})
	}
}

func TestBranchUpstreamsAndExactRefExistenceInRealRepository(t *testing.T) {
	dir := t.TempDir()
	gitcmdRunRealGit(t, "", "init", "-q", "-b", "main", dir)
	gitcmdRunRealGit(t, dir, "commit", "-q", "--allow-empty", "-m", "initial")
	gitcmdRunRealGit(t, dir, "branch", "local", "main")
	gitcmdRunRealGit(t, dir, "config", "branch.local.remote", ".")
	gitcmdRunRealGit(t, dir, "config", "branch.local.merge", "refs/heads/main")
	gitcmdRunRealGit(t, dir, "branch", "topic", "main")
	gitcmdRunRealGit(
		t,
		dir,
		"config",
		"remote.review.fetch",
		"+refs/pull/*:refs/remotes/review/pull/*",
	)
	remoteRef := "refs/remotes/review/pull/42"
	gitcmdRunRealGit(t, dir, "update-ref", remoteRef, "HEAD")
	gitcmdRunRealGit(t, dir, "config", "branch.topic.remote", "review")
	gitcmdRunRealGit(t, dir, "config", "branch.topic.merge", "refs/pull/42")

	runner := &Runner{Executable: "git", Stderr: &bytes.Buffer{}}
	upstreams, err := runner.BranchUpstreams(context.Background(), dir)
	if err != nil {
		t.Fatalf("BranchUpstreams() error = %v", err)
	}
	want := []BranchUpstream{
		{Branch: "local", Upstream: "refs/heads/main"},
		{Branch: "main"},
		{Branch: "topic", Upstream: remoteRef},
	}
	if !reflect.DeepEqual(upstreams, want) {
		t.Fatalf("BranchUpstreams() = %#v, want %#v", upstreams, want)
	}
	if exists, err := runner.RefExists(context.Background(), dir, remoteRef); err != nil || !exists {
		t.Fatalf("RefExists(existing) = (%v, %v), want (true, nil)", exists, err)
	}
	gitcmdRunRealGit(t, dir, "update-ref", "-d", remoteRef)
	if exists, err := runner.RefExists(context.Background(), dir, remoteRef); err != nil || exists {
		t.Fatalf("RefExists(gone) = (%v, %v), want (false, nil)", exists, err)
	}
	upstreams, err = runner.BranchUpstreams(context.Background(), dir)
	if err != nil {
		t.Fatalf("BranchUpstreams() after ref deletion error = %v", err)
	}
	if !reflect.DeepEqual(upstreams, want) {
		t.Fatalf("BranchUpstreams() after ref deletion = %#v, want %#v", upstreams, want)
	}
}

func TestRevisionExists(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		wantExists bool
		wantError  bool
	}{
		{name: "exists", mode: "success", wantExists: true},
		{name: "missing", mode: "exit-1"},
		{name: "git failure", mode: "exit-2", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, recorder := newHelperRunner(t, test.mode)
			runner.Stderr = &bytes.Buffer{}
			exists, err := runner.RevisionExists(context.Background(), t.TempDir(), "main")
			if exists != test.wantExists {
				t.Fatalf("exists = %v, want %v", exists, test.wantExists)
			}
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %v", err, test.wantError)
			}
			if test.wantError {
				var commandErr *CommandError
				if !errors.As(err, &commandErr) || commandErr.ExitCode != 2 {
					t.Fatalf("error = %v, want exit code 2", err)
				}
			}
			want := []string{
				"rev-parse",
				"--verify",
				"--quiet",
				"--end-of-options",
				"main^{object}",
			}
			if got := recorder.last(t).args; !slicesEqual(got, want) {
				t.Fatalf("args = %#v, want %#v", got, want)
			}
		})
	}
}

func TestRefAndBranchExistenceUseExactRefs(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		run  func(*Runner) (bool, error)
		ref  string
	}{
		{
			name: "exact ref",
			run: func(r *Runner) (bool, error) {
				return r.RefExists(context.Background(), dir, "refs/tags/v1")
			},
			ref: "refs/tags/v1",
		},
		{
			name: "local branch",
			run: func(r *Runner) (bool, error) {
				return r.LocalBranchExists(context.Background(), dir, "feature/x")
			},
			ref: "refs/heads/feature/x",
		},
		{
			name: "remote branch",
			run: func(r *Runner) (bool, error) {
				return r.RemoteBranchExists(context.Background(), dir, "upstream", "feature/x")
			},
			ref: "refs/remotes/upstream/feature/x",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, recorder := newHelperRunner(t, "success")
			runner.Stderr = &bytes.Buffer{}
			exists, err := test.run(runner)
			if err != nil || !exists {
				t.Fatalf("existence = (%v, %v), want (true, nil)", exists, err)
			}
			want := []string{"show-ref", "--verify", "--quiet", "--", test.ref}
			if got := recorder.last(t).args; !slicesEqual(got, want) {
				t.Fatalf("args = %#v, want %#v", got, want)
			}
		})
	}
}
