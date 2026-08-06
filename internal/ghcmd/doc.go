// Package ghcmd contains GitHub CLI (gh) command integration for gh-qw.
//
// gh-qw delegates every network-capable Git operation (cloning and
// synchronizing a repository) to the gh CLI so that GitHub authentication,
// host resolution, and API access follow the same rules as any other gh
// command. Purely local operations (worktrees, branch and revision
// inspection) continue to use Git directly through the gitcmd package.
package ghcmd
