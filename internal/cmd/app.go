package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

const developmentVersion = "dev"

// ErrCommandUsage identifies command-line syntax errors.
var ErrCommandUsage = errors.New("invalid command usage")

// ApplicationGit combines every local Git operation used by the command
// tree. Network-capable operations are delegated to gh; see ApplicationGh.
type ApplicationGit interface {
	RepositoryMoveGit
	WorktreeAddGit
	WorktreeRemoveGit
}

// ApplicationGh combines every gh operation used by the command tree.
type ApplicationGh interface {
	CloneGitOperations
	WorktreeAddGh
}

// ApplicationAPI combines the GitHub operations used by the command tree.
type ApplicationAPI interface {
	CloneRepositoryAPI
	WorktreeAddAPI
}

// ApplicationDependencies supplies shared process dependencies.
type ApplicationDependencies struct {
	Resolver   RootResolver
	Git        ApplicationGit
	Gh         ApplicationGh
	GitFactory CloneGitFactory
	API        ApplicationAPI
	Registry   LocationRegistry
	Discover   func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

type commandUsageError struct {
	err error
}

func (err *commandUsageError) Error() string {
	if err == nil || err.err == nil {
		return ErrCommandUsage.Error()
	}
	return err.err.Error()
}

func (err *commandUsageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *commandUsageError) Is(target error) bool {
	return target == ErrCommandUsage
}

// NewCommand returns the complete gh-qw command tree.
func NewCommand(dependencies ApplicationDependencies) *cobra.Command {
	deps := composeApplicationDependencies(dependencies)

	command := &cobra.Command{
		Use:           "qw",
		Short:         "Manage GitHub repositories and Git worktrees",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       deps.Version,
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh qw",
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.CompletionOptions.DisableDefaultCmd = true
	command.SetIn(deps.Stdin)
	command.SetOut(deps.Stdout)
	command.SetErr(deps.Stderr)
	command.SetVersionTemplate("gh qw version {{.Version}}\n")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newCommandUsageError(err)
	})

	discover := func(
		ctx context.Context,
		roots []string,
	) (local.DiscoveryResult, error) {
		return deps.Discover(ctx, roots, local.DiscoveryOptions{Git: deps.Git})
	}
	enumerate := func(
		ctx context.Context,
		repository local.Repository,
		worktreeRoot string,
	) ([]local.Worktree, error) {
		return local.EnumerateWorktrees(
			ctx,
			repository,
			worktreeRoot,
			local.WorktreeOptions{Lister: deps.Git},
		)
	}

	cloneCommand := NewCloneCommand(CloneDependencies{
		OnAcquired:    deps.Registry.Register,
		RootResolver:  deps.Resolver,
		GitFactory:    deps.GitFactory,
		Discover:      discover,
		RepositoryAPI: deps.API,
		Stdin:         deps.Stdin,
		Stdout:        deps.Stdout,
		Stderr:        deps.Stderr,
	})
	listCommand := NewListCommand(ListDependencies{
		Registry:             deps.Registry,
		Resolver:             deps.Resolver,
		Git:                  deps.Git,
		DiscoverRepositories: discover,
		EnumerateWorktrees:   enumerate,
		Stdout:               deps.Stdout,
		Stderr:               deps.Stderr,
	})
	rootCommand := NewRootCommand(deps.Resolver, deps.Stdout)
	addCommand := NewAddCommand(AddDependencies{
		Resolver: deps.Resolver,
		Git:      deps.Git,
		Registry: deps.Registry,
		Worktree: WorktreeAddDependencies{
			Resolver: deps.Resolver,
			Discover: deps.Discover,
			Git:      deps.Git,
			Gh:       deps.Gh,
			API:      deps.API,
			Stdout:   deps.Stdout,
			Stderr:   deps.Stderr,
		},
		Stdout: deps.Stdout,
		Stderr: deps.Stderr,
	})
	forgetCommand := NewForgetCommand(deps.Registry, deps.Stderr)
	removeCommand := NewRemoveCommand(RemoveDependencies{
		Forget:   deps.Registry.ForgetAt,
		Resolver: deps.Resolver,
		Discover: deps.Discover,
		Git:      deps.Git,
		Stdout:   deps.Stdout,
		Stderr:   deps.Stderr,
		Worktree: WorktreeRemoveDependencies{
			Resolver: deps.Resolver,
			Discover: deps.Discover,
			Git:      deps.Git,
			Stdout:   deps.Stdout,
			Stderr:   deps.Stderr,
		},
	})
	command.AddCommand(
		cloneCommand,
		addCommand,
		forgetCommand,
		listCommand,
		rootCommand,
		removeCommand,
	)
	wrapArgumentErrors(command)
	return command
}

// Execute runs gh-qw and returns its process exit status.
func Execute(
	ctx context.Context,
	args []string,
	dependencies ApplicationDependencies,
) int {
	command := NewCommand(dependencies)
	if ctx != nil {
		command.SetContext(ctx)
	}
	if args == nil {
		args = []string{}
	}
	command.SetArgs(args)

	if err := command.Execute(); err != nil {
		var silent *silentStatusError
		if !errors.As(err, &silent) {
			_, _ = fmt.Fprintf(command.ErrOrStderr(), "gh-qw: %v\n", err)
		}
		return ExitCode(err)
	}
	return 0
}

// ExitCode maps a command failure to the documented process status.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var silent *silentStatusError
	if errors.As(err, &silent) {
		return silent.status
	}
	for _, usageError := range []error{
		ErrCommandUsage,
		ErrCloneUsage,
		repospec.ErrUsage,
		config.ErrInvalid,
		rootpkg.ErrInvalidRoot,
		local.ErrSelector,
	} {
		if errors.Is(err, usageError) {
			return 2
		}
	}
	return 1
}

// composeApplicationDependencies is the sole composition root for the command
// tree. Standalone subcommand constructors still normalize nil test seams,
// but the production tree passes explicit dependencies into each command.
func composeApplicationDependencies(deps ApplicationDependencies) ApplicationDependencies {
	deps.Stdin = commandStdin(deps.Stdin)
	deps.Stdout = commandStdout(deps.Stdout)
	deps.Stderr = commandStderr(deps.Stderr)
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.Git == nil {
		deps.Git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     deps.Stderr,
			Stderr:     deps.Stderr,
		}
	}
	if deps.Gh == nil {
		deps.Gh = &ghcmd.Runner{Stdout: deps.Stderr, Stderr: deps.Stderr}
	}
	if deps.GitFactory == nil {
		// A fake needing clone-specific output streams must supply GitFactory.
		// Do not discard stdout/stderr arguments for non-runner fakes.
		deps.GitFactory = func(stdout, stderr io.Writer) CloneGitOperations {
			return &ghcmd.Runner{Stdout: stdout, Stderr: stderr}
		}
	}
	if deps.API == nil {
		deps.API = ghapi.NewClient(deps.Stderr)
	}
	if deps.Registry == nil {
		deps.Registry = dynamicLocationRegistry{}
	}
	if deps.Discover == nil {
		deps.Discover = registryDiscovery(deps.Registry)
	}
	if deps.Version == "" {
		deps.Version = developmentVersion
	}
	return deps
}

func wrapArgumentErrors(command *cobra.Command) {
	if command.Args != nil {
		validate := command.Args
		command.Args = func(command *cobra.Command, args []string) error {
			return newCommandUsageError(validate(command, args))
		}
	}
	for _, child := range command.Commands() {
		wrapArgumentErrors(child)
	}
}

func newCommandUsageError(err error) error {
	if err == nil {
		return nil
	}
	return &commandUsageError{err: err}
}
