// Package herdr runs the external herdr executable so gh-qw commands can
// integrate with a Herdr-managed terminal session, such as opening and
// focusing a workspace for a new linked worktree (`gh qw worktree add
// --herdr`) and closing it again on removal (`gh qw worktree remove
// --herdr`, `gh qw rm --herdr`).
package herdr
