package gitcmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// BranchUpstream is one local branch and its fully qualified upstream ref.
// Upstream is empty when the branch has no configured upstream.
type BranchUpstream struct {
	Branch   string
	Upstream string
}

// BranchUpstreams returns every local branch and its fully qualified upstream
// ref without interpreting Git's human-readable tracking status.
func (r *Runner) BranchUpstreams(ctx context.Context, dir string) ([]BranchUpstream, error) {
	output, err := r.OutputDir(
		ctx,
		dir,
		"for-each-ref",
		"--format=%(refname)%00%(upstream)%00",
		"refs/heads/",
	)
	if err != nil {
		return nil, err
	}
	upstreams, err := parseBranchUpstreams(output)
	if err != nil {
		return nil, fmt.Errorf("parse git for-each-ref output: %w", err)
	}
	return upstreams, nil
}

func parseBranchUpstreams(output []byte) ([]BranchUpstream, error) {
	if len(output) == 0 {
		return nil, nil
	}

	lines := bytes.Split(output, []byte{'\n'})
	result := make([]BranchUpstream, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for index, line := range lines {
		if len(line) == 0 && index == len(lines)-1 {
			continue
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		fields := bytes.Split(line, []byte{0})
		if len(fields) != 3 || len(fields[2]) != 0 {
			return nil, fmt.Errorf("record %d does not contain two NUL-terminated fields", index+1)
		}

		ref := string(fields[0])
		if !strings.HasPrefix(ref, "refs/heads/") || len(ref) == len("refs/heads/") {
			return nil, fmt.Errorf("record %d has invalid local branch ref %q", index+1, ref)
		}
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if _, exists := seen[branch]; exists {
			return nil, fmt.Errorf("record %d duplicates local branch %q", index+1, branch)
		}
		seen[branch] = struct{}{}

		upstream := string(fields[1])
		if upstream != "" && !strings.HasPrefix(upstream, "refs/") {
			return nil, fmt.Errorf("record %d has non-qualified upstream ref %q", index+1, upstream)
		}
		result = append(result, BranchUpstream{Branch: branch, Upstream: upstream})
	}
	return result, nil
}

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
