// Package ghauth resolves which authenticated gh CLI account a network Git
// operation should use for a specific repository owner. gh-qw delegates
// cloning and synchronizing to gh (see internal/ghcmd), so authentication
// follows whichever account gh considers active. In an environment with
// multiple gh-authenticated accounts, the active account is not necessarily
// the one with access to a given repository; this package lets gh-qw select
// a matching account automatically, remember the choice, and ask when it
// cannot decide on its own.
package ghauth
