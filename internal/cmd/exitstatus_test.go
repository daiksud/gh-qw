package cmd

import "testing"

func TestExitCodeReturnsSilentStatusVerbatim(t *testing.T) {
	for _, status := range []int{1, 130} {
		if got := ExitCode(newSilentStatusError(status)); got != status {
			t.Fatalf("ExitCode(newSilentStatusError(%d)) = %d, want %d", status, got, status)
		}
	}
}

func TestSilentStatusErrorSatisfiesError(t *testing.T) {
	err := newSilentStatusError(130)
	if err == nil {
		t.Fatal("newSilentStatusError() = nil, want a non-nil error")
	}
	if err.Error() == "" {
		t.Fatal("Error() = empty, want non-empty text")
	}
}
