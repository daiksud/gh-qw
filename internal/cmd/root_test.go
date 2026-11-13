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

func TestNewRootCommandPrintsConfiguredRoot(t *testing.T) {
	t.Parallel()

	primary := testAbsolutePath("repositories", "primary")
	resolver := &stubRootResolver{
		result: rootpkg.Result{
			RepositoryRoot: primary,
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

func TestNewRootCommandRejectsAllBeforeResolving(t *testing.T) {
	resolver := &stubRootResolver{}
	var output bytes.Buffer
	command := cmd.NewRootCommand(resolver, &output)
	command.SetArgs([]string{"--all"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("Execute() error = %v, want unknown flag", err)
	}
	if resolver.calls != 0 || output.Len() != 0 {
		t.Fatalf("Resolve() calls = %d, output = %q; want no resolution or output", resolver.calls, output.String())
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
			RepositoryRoot: testAbsolutePath("repositories", "primary"),
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
			RepositoryRoot: testAbsolutePath("repositories", "primary"),
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
		result: rootpkg.Result{RepositoryRoot: nativePath},
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
