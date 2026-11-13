// Package ghcmd contains GitHub CLI (gh) command integration for gh-qw.
//
// gh-qw delegates cloning and GitHub API transport to gh so authentication
// and host resolution follow gh's rules. Local worktree, branch, and revision
// operations use Git directly through the gitcmd package.
package ghcmd
