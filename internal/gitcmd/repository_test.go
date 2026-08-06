package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

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
