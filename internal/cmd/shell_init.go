package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// shellInitScript defines an opt-in wrapper around the native gh executable.
// Only add --cd is intercepted; command gh always calls the real CLI.
const shellInitScript = `gh() {
  if [ "$#" -ge 2 ] && [ "$1" = "qw" ] && [ "$2" = "add" ]; then
    local ghqw_arg ghqw_cd=0
    for ghqw_arg in "$@"; do
      [ "$ghqw_arg" = "--" ] && break
      if [ "$ghqw_arg" = "--cd" ]; then
        ghqw_cd=1
        break
      fi
    done
    if [ "$ghqw_cd" -eq 1 ]; then
      local ghqw_target
      ghqw_target="$(GHQW_SHELL_CD=1 command gh "$@")" || return $?
      if [ ! -d "$ghqw_target" ]; then
        printf 'gh-qw: add completed but destination is not a directory: %s\n' "$ghqw_target" >&2
        return 1
      fi
      if ! builtin cd -- "$ghqw_target"; then
        printf 'gh-qw: add completed but could not enter %s\n' "$ghqw_target" >&2
        return 1
      fi
      printf '%s\n' "$ghqw_target"
      return 0
    fi
  fi
  command gh "$@"
}
`

// NewShellInitCommand prints shell code for explicitly enabled navigation.
func NewShellInitCommand(output io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:           "shell-init <bash|zsh>",
		Short:         "Print Bash or Zsh integration for add --cd",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, args []string) error {
			if args[0] != "bash" && args[0] != "zsh" {
				return newCommandUsageError(fmt.Errorf("unsupported shell %q (supported: bash, zsh)", args[0]))
			}
			if _, err := io.WriteString(command.OutOrStdout(), shellInitScript); err != nil {
				return fmt.Errorf("write shell integration: %w", err)
			}
			return nil
		},
	}
	command.SetOut(commandStdout(output))
	return command
}
