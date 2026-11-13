package cmd

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/repospec"
)

type commandTestWriter func([]byte) (int, error)

func (w commandTestWriter) Write(p []byte) (int, error) { return w(p) }

func TestRepositoryUsageErrorPreservesUsageAndCause(t *testing.T) {
	cause := errors.New("invalid selected branch")
	err := &repositoryUsageError{err: cause}
	if !errors.Is(err, repospec.ErrUsage) || !errors.Is(err, cause) {
		t.Fatalf("error %v does not preserve usage/cause", err)
	}
	if got := err.Error(); got != cause.Error() {
		t.Fatalf("error text = %q", got)
	}
	message := &repositoryUsageError{message: "bad options"}
	if !errors.Is(message, repospec.ErrUsage) || message.Error() != "bad options" {
		t.Fatalf("message usage = %v", message)
	}
}

func TestCommandWriteAllDetectsShortWritesAndFailures(t *testing.T) {
	var out bytes.Buffer
	if err := commandWriteAll(&out, []byte("result")); err != nil || out.String() != "result" {
		t.Fatalf("normal write = (%q, %v)", out.String(), err)
	}
	if err := commandWriteAll(commandTestWriter(func([]byte) (int, error) {
		return 0, nil
	}), []byte("data")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write = %v", err)
	}
	closed := errors.New("output closed")
	if err := commandWriteAll(commandTestWriter(func([]byte) (int, error) {
		return 0, closed
	}), []byte("data")); !errors.Is(err, closed) {
		t.Fatalf("output error lost: %v", err)
	}
}

func TestApplicationCompositionUsesStreamAwareCloneFactory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := composeApplicationDependencies(ApplicationDependencies{Stdout: &stdout, Stderr: &stderr})
	if deps.Resolver == nil || deps.Git == nil || deps.Gh == nil || deps.API == nil || deps.Registry == nil {
		t.Fatal("composition left a required dependency nil")
	}
	if deps.Stdin == nil || deps.Version == "" {
		t.Fatal("composition failed to supply input or version")
	}
	runner, ok := deps.GitFactory(&stdout, &stderr).(*ghcmd.Runner)
	if !ok || runner.Stdout != &stdout || runner.Stderr != &stderr {
		t.Fatalf("stream-aware clone factory returned %#v", runner)
	}
}
