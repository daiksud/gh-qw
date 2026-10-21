package cmd

import "testing"

func TestExitCodeReturnsSilentStatusVerbatim(t *testing.T) {
	if got := ExitCode(newSilentStatusError(1)); got != 1 {
		t.Fatalf("ExitCode(newSilentStatusError(1)) = %d, want 1", got)
	}
}

func TestSilentStatusErrorSatisfiesError(t *testing.T) {
	err := newSilentStatusError(1)
	if err == nil {
		t.Fatal("newSilentStatusError() = nil, want a non-nil error")
	}
	if err.Error() == "" {
		t.Fatal("Error() = empty, want non-empty text")
	}
}
