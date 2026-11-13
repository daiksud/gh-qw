package gitcmd

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
)

// Outputter supplies native Git output for repository observations.
type Outputter interface {
	OutputDir(context.Context, string, ...string) ([]byte, error)
}

// RepositoryPaths observes the worktree top-level and common Git directory in
// one Git process. Failed commands never produce a usable partial observation.
func RepositoryPaths(ctx context.Context, git Outputter, dir string) (string, string, error) {
	output, err := git.OutputDir(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("inspect Git repository paths from %q: %w", dir, err)
	}
	lines := bytes.Split(bytes.TrimSuffix(output, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2 {
		return "", "", fmt.Errorf("Git returned %d repository paths, want two", len(lines))
	}
	paths := [2]string{}
	for index, description := range []string{"top-level", "common Git directory"} {
		path, err := ParseLine(lines[index], description)
		if err != nil {
			return "", "", err
		}
		path = filepath.FromSlash(path)
		if !filepath.IsAbs(path) {
			return "", "", fmt.Errorf("Git returned a non-absolute %s %q", description, path)
		}
		paths[index] = filepath.Clean(path)
	}
	return paths[0], paths[1], nil
}
