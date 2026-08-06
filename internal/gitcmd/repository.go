package gitcmd

import (
	"context"
	"fmt"
)

// RevisionExists reports whether revision resolves to an object in dir.
func (r *Runner) RevisionExists(ctx context.Context, dir, revision string) (bool, error) {
	if revision == "" {
		return false, fmt.Errorf("git rev-parse: revision is required")
	}

	_, err := r.OutputDir(
		ctx,
		dir,
		"rev-parse",
		"--verify",
		"--quiet",
		"--end-of-options",
		revision+"^{object}",
	)
	return existenceResult(err)
}

// RevExists is an alias for RevisionExists.
func (r *Runner) RevExists(ctx context.Context, dir, revision string) (bool, error) {
	return r.RevisionExists(ctx, dir, revision)
}

// RefExists reports whether an exact ref exists in dir.
func (r *Runner) RefExists(ctx context.Context, dir, ref string) (bool, error) {
	if ref == "" {
		return false, fmt.Errorf("git show-ref: ref is required")
	}

	_, err := r.OutputDir(ctx, dir, "show-ref", "--verify", "--quiet", "--", ref)
	return existenceResult(err)
}

// LocalBranchExists reports whether a local branch exists.
func (r *Runner) LocalBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	if branch == "" {
		return false, fmt.Errorf("git show-ref: branch is required")
	}
	return r.RefExists(ctx, dir, "refs/heads/"+branch)
}

// RemoteBranchExists reports whether a remote-tracking branch exists.
func (r *Runner) RemoteBranchExists(ctx context.Context, dir, remote, branch string) (bool, error) {
	if remote == "" {
		return false, fmt.Errorf("git show-ref: remote is required")
	}
	if branch == "" {
		return false, fmt.Errorf("git show-ref: branch is required")
	}
	return r.RefExists(ctx, dir, "refs/remotes/"+remote+"/"+branch)
}

func existenceResult(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if exitCode, ok := CommandExitCode(err); ok && exitCode == 1 {
		return false, nil
	}
	return false, err
}
