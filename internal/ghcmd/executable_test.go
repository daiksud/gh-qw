package ghcmd

import (
	"errors"
	"testing"
)

func TestResolveExecutablePrefersNonEmptyGHPath(t *testing.T) {
	var lookPathCalls int
	got, err := ResolveExecutable(
		func(key string) (string, bool) {
			if key != "GH_PATH" {
				t.Fatalf("lookupEnv key = %q, want GH_PATH", key)
			}
			return "/custom/gh", true
		},
		func(string) (string, error) {
			lookPathCalls++
			return "", errors.New("should not be called")
		},
	)
	if err != nil {
		t.Fatalf("ResolveExecutable() error = %v", err)
	}
	if got != "/custom/gh" {
		t.Fatalf("ResolveExecutable() = %q, want %q", got, "/custom/gh")
	}
	if lookPathCalls != 0 {
		t.Fatalf("lookPath calls = %d, want 0", lookPathCalls)
	}
}

func TestResolveExecutableFallsBackToPathLookup(t *testing.T) {
	tests := []struct {
		name  string
		value string
		found bool
	}{
		{name: "unset", found: false},
		{name: "empty", value: "", found: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var lookedUp string
			got, err := ResolveExecutable(
				func(string) (string, bool) { return test.value, test.found },
				func(file string) (string, error) {
					lookedUp = file
					return "/usr/bin/gh", nil
				},
			)
			if err != nil {
				t.Fatalf("ResolveExecutable() error = %v", err)
			}
			if got != "/usr/bin/gh" {
				t.Fatalf("ResolveExecutable() = %q, want %q", got, "/usr/bin/gh")
			}
			if lookedUp != "gh" {
				t.Fatalf("lookPath argument = %q, want %q", lookedUp, "gh")
			}
		})
	}
}

func TestResolveExecutableReportsMissingGh(t *testing.T) {
	_, err := ResolveExecutable(
		func(string) (string, bool) { return "", false },
		func(string) (string, error) { return "", errors.New("executable file not found in $PATH") },
	)
	if err == nil {
		t.Fatal("ResolveExecutable() error = nil, want failure")
	}
}
