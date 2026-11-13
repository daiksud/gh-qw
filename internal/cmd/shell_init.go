package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// shellInitScript defines an opt-in wrapper around the native gh executable.
// Only add/list --cd is intercepted; command gh always calls the real CLI.
const shellInitScript = `gh() {
  if [ "$#" -ge 2 ] && [ "$1" = "qw" ] && { [ "$2" = "add" ] || [ "$2" = "list" ]; }; then
    local ghqw_arg ghqw_cd=0 ghqw_help=0
    for ghqw_arg in "$@"; do
      [ "$ghqw_arg" = "--" ] && break
      case "$ghqw_arg" in
        --cd|--cd=1|--cd=t|--cd=T|--cd=true|--cd=TRUE|--cd=True) ghqw_cd=1 ;;
        --cd=0|--cd=f|--cd=F|--cd=false|--cd=FALSE|--cd=False) ghqw_cd=0 ;;
        -h|--help|-h=1|-h=t|-h=T|-h=true|-h=TRUE|-h=True|--help=1|--help=t|--help=T|--help=true|--help=TRUE|--help=True) ghqw_help=1 ;;
        -h=0|-h=f|-h=F|-h=false|-h=FALSE|-h=False|--help=0|--help=f|--help=F|--help=false|--help=FALSE|--help=False) ghqw_help=0 ;;
        -[!-]*)
          # All list shorthands are booleans, so help may occur in a cluster.
          if [ "$2" = "list" ]; then
            case "${ghqw_arg%%=*}" in
              *h*)
                case "$ghqw_arg" in
                  *h=0|*h=f|*h=F|*h=false|*h=FALSE|*h=False) ghqw_help=0 ;;
                  *) ghqw_help=1 ;;
                esac
                ;;
            esac
          fi
          ;;
      esac
    done
    if [ "$ghqw_cd" -eq 1 ] && [ "$ghqw_help" -eq 0 ]; then
      local ghqw_target
      # Preserve path newlines, then remove only the result's terminating newline.
      ghqw_target="$(GHQW_SHELL_CD=1 command gh "$@" && printf '.')" || return $?
      ghqw_target="${ghqw_target%.}"
      case "$ghqw_target" in
        *$'\n') ghqw_target="${ghqw_target%$'\n'}" ;;
        *) printf 'gh-qw: missing directory result\n' >&2; return 1 ;;
      esac
      if [ ! -d "$ghqw_target" ]; then
        printf 'gh-qw: %s completed but destination is not a directory: %s\n' "$2" "$ghqw_target" >&2
        return 1
      fi
      if ! builtin cd -- "$ghqw_target"; then
        printf 'gh-qw: %s completed but could not enter %s\n' "$2" "$ghqw_target" >&2
        return 1
      fi
      printf '%s\n' "$ghqw_target"
      return $?
    fi
  fi
  command gh "$@"
}
`

// NewShellInitCommand prints shell code for explicitly enabled navigation.
func NewShellInitCommand(output io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:           "shell-init <bash|zsh>",
		Short:         "Print Bash or Zsh integration for add/list --cd",
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
