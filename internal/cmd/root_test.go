package cmd_test

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewRootCommandPrintsPrimaryRoot(t *testing.T) {
	t.Parallel()

	primary := testAbsolutePath("repositories", "primary")
	resolver := &stubRootResolver{
		result: rootpkg.Result{
			RepositoryRoots: []string{
				primary,
				testAbsolutePath("repositories", "secondary"),
			},
		},
	}
	var output bytes.Buffer

	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := output.String(), filepath.ToSlash(primary)+"\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if resolver.calls != 1 {
		t.Fatalf("Resolve() calls = %d, want 1", resolver.calls)
	}
}

func TestNewRootCommandAllPrintsRootsInConfiguredOrder(t *testing.T) {
	t.Parallel()

	roots := []string{
		testAbsolutePath("repositories", "third"),
		testAbsolutePath("repositories", "first"),
		testAbsolutePath("repositories", "second"),
	}
	resolver := &stubRootResolver{
		result: rootpkg.Result{RepositoryRoots: roots},
	}
	var output bytes.Buffer

	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs([]string{"--all"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantLines := make([]string, len(roots))
	for index, root := range roots {
		wantLines[index] = filepath.ToSlash(root)
	}
	if got, want := output.String(), strings.Join(wantLines, "\n")+"\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if resolver.calls != 1 {
		t.Fatalf("Resolve() calls = %d, want 1", resolver.calls)
	}
}

func TestNewRootCommandPropagatesResolutionError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("resolution failed")
	resolver := &stubRootResolver{err: wantErr}
	var output bytes.Buffer

	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs(nil)

	if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want empty", output.String())
	}
	if resolver.calls != 1 {
		t.Fatalf("Resolve() calls = %d, want 1", resolver.calls)
	}
}

func TestNewRootCommandRejectsArgumentsBeforeResolving(t *testing.T) {
	t.Parallel()

	resolver := &stubRootResolver{
		result: rootpkg.Result{
			RepositoryRoots: []string{testAbsolutePath("repositories", "primary")},
		},
	}
	var output bytes.Buffer

	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs([]string{"unexpected"})

	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want an argument error")
	}
	if resolver.calls != 0 {
		t.Fatalf("Resolve() calls = %d, want 0", resolver.calls)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want empty", output.String())
	}
}

func TestNewRootCommandPropagatesWriteError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("write failed")
	resolver := &stubRootResolver{
		result: rootpkg.Result{
			RepositoryRoots: []string{testAbsolutePath("repositories", "primary")},
		},
	}
	command := cmd.NewRootCommand(resolver, failingWriter{err: wantErr})
	command.SetArgs(nil)

	if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
	}
	if resolver.calls != 1 {
		t.Fatalf("Resolve() calls = %d, want 1", resolver.calls)
	}
}

func TestNewRootCommandNormalizesPathSeparators(t *testing.T) {
	t.Parallel()

	if filepath.Separator == '/' {
		t.Skip("native paths already use slash separators")
	}

	nativePath := testAbsolutePath("repositories", "primary")
	resolver := &stubRootResolver{
		result: rootpkg.Result{RepositoryRoots: []string{nativePath}},
	}
	var output bytes.Buffer

	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := output.String(), filepath.ToSlash(nativePath)+"\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if strings.Contains(output.String(), `\`) {
		t.Fatalf("output = %q, want slash separators", output.String())
	}
}

type stubRootResolver struct {
	result rootpkg.Result
	err    error
	calls  int
}

func (r *stubRootResolver) Resolve() (rootpkg.Result, error) {
	r.calls++
	return r.result, r.err
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Writer = failingWriter{}

func testAbsolutePath(parts ...string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(append([]string{`C:\`}, parts...)...)
	}
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}
