package cmd

import "fmt"

// silentStatusError reports a specific process exit status without
// Execute's standard "gh-qw: <error>" diagnostic line. Bulk --gone removal
// and fzf cancellation/no-match use it to avoid a redundant error.
type silentStatusError struct {
	status int
}

// newSilentStatusError returns an error that ExitCode maps to status and
// that Execute reports without printing its diagnostic line.
func newSilentStatusError(status int) error {
	return &silentStatusError{status: status}
}

// Error implements error. Its text is never printed by Execute, since
// Execute checks for *silentStatusError first and skips the diagnostic
// line entirely; the text exists only so this type satisfies error and
// stays informative if ever surfaced unexpectedly, such as in a test
// failure message.
func (e *silentStatusError) Error() string {
	return fmt.Sprintf("exit status %d", e.status)
}
