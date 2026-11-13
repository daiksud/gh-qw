package gitcmd

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

type repositoryPathOutput func(context.Context, string, ...string) ([]byte, error)

func (output repositoryPathOutput) OutputDir(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return output(ctx, dir, args...)
}

func TestRepositoryPathsUsesOneNativeAbsoluteQuery(t *testing.T) {
	dir := t.TempDir()
	top := filepath.Join(dir, "main with spaces")
	common := filepath.Join(top, ".git")
	calls := 0
	git := repositoryPathOutput(func(_ context.Context, gotDir string, args ...string) ([]byte, error) {
		calls++
		if gotDir != dir || !reflect.DeepEqual(args, []string{
			"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir",
		}) {
			t.Fatalf("query = %q %q", gotDir, args)
		}
		return []byte(filepath.ToSlash(top) + "\r\n" + filepath.ToSlash(common) + "\r\n"), nil
	})
	gotTop, gotCommon, err := RepositoryPaths(context.Background(), git, dir)
	if err != nil || gotTop != top || gotCommon != common || calls != 1 {
		t.Fatalf("RepositoryPaths = %q, %q, %v; calls = %d", gotTop, gotCommon, err, calls)
	}
}

func TestRepositoryPathsRejectsPartialOrMalformedObservations(t *testing.T) {
	top := t.TempDir()
	common := filepath.Join(top, ".git")
	failure := errors.New("Git failed after producing output")
	for _, test := range []struct {
		name   string
		output string
		err    error
	}{
		{name: "failed with full output", output: top + "\n" + common + "\n", err: failure},
		{name: "failed with partial output", output: top + "\n", err: failure},
		{name: "missing common directory", output: top + "\n"},
		{name: "extra line", output: top + "\n" + common + "\nextra\n"},
		{name: "empty top-level", output: "\n" + common + "\n"},
		{name: "relative common directory", output: top + "\n.git\n"},
		{name: "NUL in path", output: top + "\x00\n" + common + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			git := repositoryPathOutput(func(context.Context, string, ...string) ([]byte, error) {
				return []byte(test.output), test.err
			})
			gotTop, gotCommon, err := RepositoryPaths(context.Background(), git, top)
			if err == nil || gotTop != "" || gotCommon != "" {
				t.Fatalf("unusable observation accepted: %q, %q, %v", gotTop, gotCommon, err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("lost command failure: %v", err)
			}
		})
	}
}
