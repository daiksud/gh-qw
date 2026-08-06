package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/daiksud/gh-qw/internal/herdr"
	"github.com/spf13/cobra"
)

// HerdrCreator is the Herdr capability `worktree add --herdr` uses to open
// and focus a workspace for a newly created linked worktree. *herdr.Runner
// satisfies it.
type HerdrCreator interface {
	CreateWorkspace(ctx context.Context, options herdr.CreateOptions) (herdr.Workspace, error)
}

// HerdrCloser is the Herdr capability `worktree remove --herdr` and
// `rm --herdr` use to find and close the workspace open for a linked
// worktree being removed. *herdr.Runner satisfies it.
type HerdrCloser interface {
	FindWorkspaceForPath(ctx context.Context, repoPath, worktreePath string) (string, bool, error)
	CloseWorkspace(ctx context.Context, workspaceID string) error
}

// herdrFlagValues backs the --herdr/--no-herdr flag pair shared by
// worktree add, worktree remove, and rm.
type herdrFlagValues struct {
	herdr   bool
	noHerdr bool
}

// registerHerdrFlags adds --herdr and --no-herdr to command, storing their
// values in values. verb briefly describes what the integration does for
// this command (for example "Open" or "Close"), so each command's own
// --help text reads naturally.
func registerHerdrFlags(command *cobra.Command, values *herdrFlagValues, verb string) {
	command.Flags().BoolVar(
		&values.herdr,
		"herdr",
		false,
		fmt.Sprintf("%s a Herdr workspace for the linked worktree", verb),
	)
	command.Flags().BoolVar(
		&values.noHerdr,
		"no-herdr",
		false,
		"Never integrate with Herdr, overriding GHQW_HERDR or configuration",
	)
}

// herdrIntent captures what --herdr/--no-herdr asked for on the command
// line, resolved once from the parsed flags right after registration (see
// registerHerdrFlags), so the rest of the pipeline never needs the
// *cobra.Command itself.
type herdrIntent struct {
	changedHerdr   bool
	changedNoHerdr bool
}

// newHerdrIntent captures the --herdr/--no-herdr Changed state from
// command immediately after flag parsing.
func newHerdrIntent(command *cobra.Command) herdrIntent {
	return herdrIntent{
		changedHerdr:   command.Flags().Changed("herdr"),
		changedNoHerdr: command.Flags().Changed("no-herdr"),
	}
}

// resolveHerdrIntegration decides whether the Herdr integration should run
// for one invocation of worktree add, worktree remove, or rm, from intent
// (captured from --herdr/--no-herdr; see newHerdrIntent), configDefault
// (the configuration file's herdr key, already combined with GHQW_HERDR's
// own precedence by internal/root.Resolver; see root.Result.Herdr), and
// lookupEnv, used only to check HERDR_ENV through herdr.InSession.
//
// The returned error is always a usage error and is left unwrapped so each
// command can wrap it with its own usage-error type, exactly like its
// other flag validation. --herdr together with --no-herdr, and an explicit
// --herdr outside of a Herdr-managed pane (HERDR_ENV != 1), are both usage
// errors: the second because the person explicitly asked for an
// integration that cannot exist there. An implicit request from
// GHQW_HERDR or the configuration file outside Herdr instead writes one
// warning line to warn and resolves to (false, nil), leaving the calling
// command's own result and exit status unaffected.
func resolveHerdrIntegration(
	intent herdrIntent,
	configDefault bool,
	lookupEnv func(string) (string, bool),
	warn io.Writer,
) (bool, error) {
	if intent.changedHerdr && intent.changedNoHerdr {
		return false, errors.New("--herdr and --no-herdr are mutually exclusive")
	}

	requested := configDefault
	explicit := false
	switch {
	case intent.changedHerdr:
		requested = true
		explicit = true
	case intent.changedNoHerdr:
		requested = false
		explicit = true
	}

	if !requested {
		return false, nil
	}
	if herdr.InSession(lookupEnv) {
		return true, nil
	}
	if explicit {
		return false, fmt.Errorf(
			"--herdr requires running inside a Herdr-managed pane (%s=1)",
			herdr.EnvironmentVariable,
		)
	}

	_, _ = fmt.Fprintf(
		warn,
		"gh-qw: Herdr integration is enabled by GHQW_HERDR or configuration, but this process "+
			"is not running inside a Herdr-managed pane (%s=1); skipping Herdr integration\n",
		herdr.EnvironmentVariable,
	)
	return false, nil
}

// herdrWorkspaceLabel is the Herdr workspace label used for a linked
// worktree: the repository's own short name and the worktree's branch
// slot, joined the same way gh-qw's own linked-worktree identity is
// (<repo>@<branch>; see the CLI reference's canonical identity section).
func herdrWorkspaceLabel(repo, branch string) string {
	return repo + "@" + branch
}
