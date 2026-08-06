package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// GitOutputter is the read-only Git output seam used for repository and
// current-directory inspection. gitcmd.Runner satisfies this interface.
type GitOutputter interface {
	OutputDir(context.Context, string, ...string) ([]byte, error)
}

func gitTopLevel(ctx context.Context, git GitOutputter, dir string) (string, error) {
	output, err := git.OutputDir(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return parseGitPath(output, dir, "top-level")
}

func gitCommonDir(ctx context.Context, git GitOutputter, topLevel string) (string, error) {
	output, err := git.OutputDir(ctx, topLevel, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return parseGitPath(output, topLevel, "common Git directory")
}

func gitTopLevelAndCommon(
	ctx context.Context,
	git GitOutputter,
	dir string,
) (string, string, error) {
	topLevel, err := gitTopLevel(ctx, git, dir)
	if err != nil {
		return "", "", fmt.Errorf("find Git top-level from %q: %w", dir, err)
	}
	commonDir, err := gitCommonDir(ctx, git, topLevel)
	if err != nil {
		return "", "", fmt.Errorf("find common Git directory from %q: %w", topLevel, err)
	}
	return topLevel, commonDir, nil
}

func parseGitPath(output []byte, relativeTo, description string) (string, error) {
	output = bytes.TrimSuffix(output, []byte{'\n'})
	output = bytes.TrimSuffix(output, []byte{'\r'})
	if len(output) == 0 {
		return "", fmt.Errorf("Git returned an empty %s", description)
	}
	if bytes.IndexByte(output, 0) >= 0 ||
		bytes.IndexByte(output, '\n') >= 0 ||
		bytes.IndexByte(output, '\r') >= 0 {
		return "", fmt.Errorf("Git returned a non-single-line %s", description)
	}

	path := string(output)
	if !filepath.IsAbs(path) {
		if relativeTo == "" {
			return "", errors.New("cannot resolve relative Git path without a base directory")
		}
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path), nil
}
