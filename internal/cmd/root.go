package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// RootResolver resolves configured repository roots.
type RootResolver interface {
	Resolve() (rootpkg.Result, error)
}

// NewRootCommand returns the command that prints configured repository roots.
func NewRootCommand(resolver RootResolver, output io.Writer) *cobra.Command {
	if resolver == nil {
		resolver = rootpkg.NewResolver()
	}
	if output == nil {
		output = os.Stdout
	}

	var all bool
	command := &cobra.Command{
		Use:           "root",
		Short:         "Print configured repository roots",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := resolver.Resolve()
			if err != nil {
				return err
			}

			roots := result.RepositoryRoots
			if !all {
				roots = []string{result.Primary()}
			}
			for _, root := range roots {
				if _, err := fmt.Fprintln(command.OutOrStdout(), filepath.ToSlash(root)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Print all configured repository roots")
	command.SetOut(output)

	return command
}
