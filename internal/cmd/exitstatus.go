package cmd

import "fmt"

// silentStatusError reports a specific process exit status without
// Execute's standard "gh-qw: <error>" diagnostic line. It exists for
// conditions that already communicated themselves to the person another
// way — for example, canceling an interactive `list --fzf` selection with
// Esc or Ctrl-C, where fzf's own screen already cleared without a message,
// or fzf reporting that no candidate matched the typed query — so gh-qw's
// own diagnostic line would be redundant or misleading noise. Every other
// command failure keeps using an ordinary error and the 0/1/2 convention
// documented on ExitCode.
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
