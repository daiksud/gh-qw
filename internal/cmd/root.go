package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// RootResolver resolves the configured repository root.
type RootResolver interface {
	Resolve() (rootpkg.Result, error)
}

// NewRootCommand returns the command that prints the configured repository root.
func NewRootCommand(resolver RootResolver, output io.Writer) *cobra.Command {
	if resolver == nil {
		resolver = rootpkg.NewResolver()
	}
	if output == nil {
		output = os.Stdout
	}

	command := &cobra.Command{
		Use:           "root",
		Short:         "Print the configured repository root",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			result, err := resolver.Resolve()
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(command.OutOrStdout(), filepath.ToSlash(result.RepositoryRoot))
			return err
		},
	}
	command.SetOut(output)

	return command
}
