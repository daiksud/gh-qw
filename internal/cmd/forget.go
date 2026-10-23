package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/spf13/cobra"
)

// NewForgetCommand removes only a registered identity-to-location mapping.
// It deliberately does not inspect, move, or delete the Git repository.
func NewForgetCommand(store LocationRegistry, stderr io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use: "forget <repo>",
		Short: "Forget a managed repository without deleting its files",
		SilenceErrors: true,
		SilenceUsage: true,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			selected := store
			if selected == nil {
				var err error
				selected, err = defaultLocationRegistry()
				if err != nil {
					return err
				}
			}
			repositories, err := registeredRepositories(command.Context(), selected, nil)
			if err != nil {
				return err
			}
			if len(repositories) == 0 {
				return fmt.Errorf("forget %q: %w", args[0], errors.New("no registered repositories"))
			}
			repository, err := local.ResolveRepositoryForMutation(repositories, args[0])
			if err != nil {
				return err
			}
			if err := selected.Forget(repository.Identity); err != nil {
				return fmt.Errorf("forget %q: %w", repository.Identity, err)
			}
			return nil
		},
	}
	command.SetErr(commandStderr(stderr))
	return command
}
