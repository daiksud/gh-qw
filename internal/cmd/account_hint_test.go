package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/ghauth"
)

func TestAccountFailureHintShowsSelectedLogin(t *testing.T) {
	resolution := ghauth.Resolution{Source: ghauth.SourceSelected, Login: "TE-DaikiSudo", Token: "gho_x"}

	hint := accountFailureHint(resolution)

	if !strings.Contains(hint, `"TE-DaikiSudo"`) {
		t.Fatalf("hint = %q, want it to name the selected account", hint)
	}
	if !strings.Contains(hint, "gh auth switch") {
		t.Fatalf("hint = %q, want a gh auth switch suggestion", hint)
	}
}

func TestAccountFailureHintIsEmptyWithoutASelectedLogin(t *testing.T) {
	resolution := ghauth.Resolution{Source: ghauth.SourceSelected}

	if hint := accountFailureHint(resolution); hint != "" {
		t.Fatalf("hint = %q, want empty without a selected login", hint)
	}
}

func TestAccountFailureHintIsEmptyForExplicitEnvironmentToken(t *testing.T) {
	// An explicit GH_TOKEN/GITHUB_TOKEN might not correspond to any
	// gh-known account (e.g., a bare PAT in CI), and automatic selection
	// never inspected it, so no hint should be added.
	resolution := ghauth.Resolution{Source: ghauth.SourceExplicitEnv, Login: "should-be-ignored"}

	if hint := accountFailureHint(resolution); hint != "" {
		t.Fatalf("hint = %q, want empty for an explicit environment token", hint)
	}
}

func TestWrapAccountFailureHintAppendsHintWithoutLosingErrorChain(t *testing.T) {
	wantErr := errors.New("gh command failed with exit code 1")
	resolution := ghauth.Resolution{Source: ghauth.SourceSelected, Login: "TE-DaikiSudo"}

	got := wrapAccountFailureHint(wantErr, resolution)

	if !errors.Is(got, wantErr) {
		t.Fatalf("wrapAccountFailureHint() = %v, want it to wrap %v", got, wantErr)
	}
	if !strings.Contains(got.Error(), "TE-DaikiSudo") {
		t.Fatalf("wrapAccountFailureHint() = %q, want it to mention the account", got.Error())
	}
}

func TestWrapAccountFailureHintReturnsNilForNilError(t *testing.T) {
	if err := wrapAccountFailureHint(nil, ghauth.Resolution{}); err != nil {
		t.Fatalf("wrapAccountFailureHint(nil, ...) = %v, want nil", err)
	}
}

func TestWrapAccountFailureHintLeavesErrorUnchangedWithoutAHint(t *testing.T) {
	wantErr := errors.New("gh command failed with exit code 1")

	got := wrapAccountFailureHint(wantErr, ghauth.Resolution{Source: ghauth.SourceExplicitEnv})

	if got != wantErr {
		t.Fatalf("wrapAccountFailureHint() = %v, want the original error returned unchanged", got)
	}
}

func TestWrapAccountFailureHintPreservesStderrOutputForSilentHandling(t *testing.T) {
	commandErr := &accountHintTestError{message: "gh command failed with exit code 1", stderr: []byte("remote: Repository not found.")}
	resolution := ghauth.Resolution{Source: ghauth.SourceSelected, Login: "TE-DaikiSudo"}

	got := wrapAccountFailureHint(commandErr, resolution)

	var outputter getStderrOutputter
	if !errors.As(got, &outputter) {
		t.Fatalf("wrapAccountFailureHint() = %v, want StderrOutput() still reachable via errors.As", got)
	}
	if string(outputter.StderrOutput()) != "remote: Repository not found." {
		t.Fatalf("StderrOutput() = %q, want the original diagnostic preserved", outputter.StderrOutput())
	}
}

// accountHintTestError mirrors ghcmd.CommandError's StderrOutput capability
// for tests that do not need a real gh subprocess failure.
type accountHintTestError struct {
	message string
	stderr  []byte
}

func (e *accountHintTestError) Error() string        { return e.message }
func (e *accountHintTestError) StderrOutput() []byte { return e.stderr }

func TestDefaultCachePathIsReachableForHintText(t *testing.T) {
	// Sanity check that ghauth.DefaultCachePath (used internally by
	// accountFailureHint) does not error with the real OS environment and
	// home directory, so the hint's cache-path suggestion is normally
	// present rather than silently degraded.
	if _, err := ghauth.DefaultCachePath(os.LookupEnv, os.UserHomeDir); err != nil {
		t.Fatalf("DefaultCachePath() error = %v", err)
	}
}
