package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/herdr"
	"github.com/spf13/cobra"
)

func newHerdrTestIntent(t *testing.T, args ...string) herdrIntent {
	t.Helper()
	values := &herdrFlagValues{}
	command := &cobra.Command{Use: "test"}
	registerHerdrFlags(command, values, "Open")
	if err := command.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v) error = %v", args, err)
	}
	return newHerdrIntent(command)
}

func alwaysInSession(string) (string, bool) {
	return "1", true
}

func neverInSession(string) (string, bool) {
	return "", false
}

func TestResolveHerdrIntegrationDisabledByDefault(t *testing.T) {
	intent := newHerdrTestIntent(t)
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, false, alwaysInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty", warn.String())
	}
}

func TestResolveHerdrIntegrationConfigDefaultInsideSession(t *testing.T) {
	intent := newHerdrTestIntent(t)
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, true, alwaysInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if !enabled {
		t.Fatal("enabled = false, want true")
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty", warn.String())
	}
}

func TestResolveHerdrIntegrationConfigDefaultOutsideSessionWarnsAndSkips(t *testing.T) {
	intent := newHerdrTestIntent(t)
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, true, neverInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if !strings.Contains(warn.String(), "HERDR_ENV") {
		t.Fatalf("warn = %q, want it to mention HERDR_ENV", warn.String())
	}
}

func TestResolveHerdrIntegrationExplicitFlagInsideSession(t *testing.T) {
	intent := newHerdrTestIntent(t, "--herdr")
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, false, alwaysInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if !enabled {
		t.Fatal("enabled = false, want true")
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty", warn.String())
	}
}

func TestResolveHerdrIntegrationExplicitFlagOutsideSessionIsUsageError(t *testing.T) {
	intent := newHerdrTestIntent(t, "--herdr")
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, false, neverInSession, &warn)
	if err == nil {
		t.Fatal("resolveHerdrIntegration() error = nil, want a usage error")
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if !strings.Contains(err.Error(), "HERDR_ENV") {
		t.Fatalf("error = %q, want it to mention HERDR_ENV", err.Error())
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty: an error, not a warning, reports this case", warn.String())
	}
}

func TestResolveHerdrIntegrationNoHerdrOverridesConfigDefault(t *testing.T) {
	intent := newHerdrTestIntent(t, "--no-herdr")
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, true, alwaysInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty", warn.String())
	}
}

func TestResolveHerdrIntegrationNoHerdrNeverRequiresASession(t *testing.T) {
	intent := newHerdrTestIntent(t, "--no-herdr")
	var warn bytes.Buffer

	enabled, err := resolveHerdrIntegration(intent, true, neverInSession, &warn)
	if err != nil {
		t.Fatalf("resolveHerdrIntegration() error = %v", err)
	}
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if warn.Len() != 0 {
		t.Fatalf("warn = %q, want empty", warn.String())
	}
}

func TestResolveHerdrIntegrationRejectsBothFlags(t *testing.T) {
	intent := newHerdrTestIntent(t, "--herdr", "--no-herdr")
	var warn bytes.Buffer

	_, err := resolveHerdrIntegration(intent, false, alwaysInSession, &warn)
	if err == nil {
		t.Fatal("resolveHerdrIntegration() error = nil, want mutual-exclusivity failure")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %q, want it to mention mutual exclusivity", err.Error())
	}
}

func TestHerdrWorkspaceLabelJoinsRepoAndBranch(t *testing.T) {
	if got, want := herdrWorkspaceLabel("gh-qw", "feature/login"), "gh-qw@feature/login"; got != want {
		t.Fatalf("herdrWorkspaceLabel() = %q, want %q", got, want)
	}
}

// TestHerdrCreatorAndCloserSatisfiedByRunner guards that a minimal stand-in
// implementing both capability interfaces keeps compiling: gh-qw's own
// *herdr.Runner satisfies both HerdrCreator and HerdrCloser the same way,
// so a real command's default dependency wiring keeps type-checking.
func TestHerdrCreatorAndCloserSatisfiedByRunner(t *testing.T) {
	var _ HerdrCreator = herdrRunnerStub{}
	var _ HerdrCloser = herdrRunnerStub{}
}

type herdrRunnerStub struct{}

func (herdrRunnerStub) CreateWorkspace(
	context.Context,
	herdr.CreateOptions,
) (herdr.Workspace, error) {
	return herdr.Workspace{}, errors.New("unused")
}

func (herdrRunnerStub) FindWorkspaceForPath(
	context.Context,
	string,
	string,
) (string, bool, error) {
	return "", false, errors.New("unused")
}

func (herdrRunnerStub) CloseWorkspace(context.Context, string) error {
	return errors.New("unused")
}
