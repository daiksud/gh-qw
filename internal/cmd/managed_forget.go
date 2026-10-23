package cmd

import (
	"fmt"
	"io"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/locations"
	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/spf13/cobra"
)

// NewForgetCommand removes only an explicit repository-location registration.
func NewForgetCommand(store locations.Store, output io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use: "forget <repository>",
		Short: "Stop managing a repository location without deleting files",
		Args: cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			registered, err := store.Read()
			if err != nil {
				return err
			}
			var repositories []local.Repository
			for _, identity := range locations.Identities(registered) {
				parts, err := repospec.ParseIdentity(identity)
				if err != nil {
					return err
				}
				repositories = append(repositories, local.Repository{
					Identity: identity,
					Host: parts.Host,
					Owner: parts.Owner,
					Repo: parts.Repo,
					Path: registered[identity],
				})
			}
			repository, err := local.ResolveRepositoryForMutation(repositories, args[0])
			if err != nil {
				return &repositoryUsageError{err: fmt.Errorf("select repository to forget: %w", err)}
			}
			return store.Forget(repository.Identity)
		},
	}
	command.SetOut(commandStdout(output))
	return command
}
