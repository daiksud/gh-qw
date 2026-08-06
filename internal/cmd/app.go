package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghauth"
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

// AccountResolver resolves which gh account a network Git or GitHub API
// operation for a repository owner should authenticate as, so a command
// running with multiple gh-authenticated accounts does not have to rely on
// whichever one happens to be active. *ghauth.Resolver satisfies it.
type AccountResolver interface {
	Resolve(ctx context.Context, host, owner string) (ghauth.Resolution, error)
}

// ApplicationGit combines every local Git operation used by the command
// tree. Network-capable operations are delegated to gh; see ApplicationGh.
type ApplicationGit interface {
	MigrateGit
	WorktreeAddGit
	WorktreeRemoveGit
	WorktreePruneGit
}

// ApplicationGh combines every gh operation used by the command tree.
type ApplicationGh interface {
	GetGitOperations
	WorktreeAddGh
}

// ApplicationAPI combines the GitHub operations used by the command tree.
type ApplicationAPI interface {
	GetIdentityResolver
	WorktreeAddAPI
}

// ApplicationDependencies supplies shared process dependencies.
type ApplicationDependencies struct {
	Resolver        RootResolver
	Git             ApplicationGit
	Gh              ApplicationGh
	GitFactory      GetGitFactory
	AccountResolver AccountResolver
	API             ApplicationAPI
	Discover        func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Version string
}

type applicationRuntime struct {
	resolver        RootResolver
	git             ApplicationGit
	gh              ApplicationGh
	gitFactory      GetGitFactory
	accountResolver AccountResolver
	api             ApplicationAPI
	discover        func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	version string
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
	runtime := prepareApplicationRuntime(dependencies)

	command := &cobra.Command{
		Use:           "qw",
		Short:         "Manage GitHub repositories and Git worktrees",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       runtime.version,
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh qw",
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.CompletionOptions.DisableDefaultCmd = true
	command.SetIn(runtime.stdin)
	command.SetOut(runtime.stdout)
	command.SetErr(runtime.stderr)
	command.SetVersionTemplate("gh qw version {{.Version}}\n")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newCommandUsageError(err)
	})

	discover := func(
		ctx context.Context,
		roots []string,
	) (local.DiscoveryResult, error) {
		return runtime.discover(ctx, roots, local.DiscoveryOptions{Git: runtime.git})
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
			local.WorktreeOptions{Lister: runtime.git},
		)
	}

	getCommand := NewGetCommand(GetDependencies{
		RootResolver:     runtime.resolver,
		GitFactory:       runtime.gitFactory,
		AccountResolver:  runtime.accountResolver,
		Discover:         discover,
		IdentityResolver: runtime.api,
		Stdin:            runtime.stdin,
		Stdout:           runtime.stdout,
		Stderr:           runtime.stderr,
	})
	listCommand := NewListCommand(ListDependencies{
		Resolver:             runtime.resolver,
		DiscoverRepositories: discover,
		EnumerateWorktrees:   enumerate,
		Stdout:               runtime.stdout,
		Stderr:               runtime.stderr,
	})
	rootCommand := NewRootCommand(runtime.resolver, runtime.stdout)
	removeCommand := NewRemoveCommand(RemoveDependencies{
		Resolver: runtime.resolver,
		Discover: runtime.discover,
		Git:      runtime.git,
		Stdout:   runtime.stdout,
		Stderr:   runtime.stderr,
	})
	migrateCommand := NewMigrateCommand(MigrateDependencies{
		Resolver: runtime.resolver,
		Git:      runtime.git,
		Stdout:   runtime.stdout,
		Stderr:   runtime.stderr,
	})

	worktreeCommand := &cobra.Command{
		Use:           "worktree",
		Short:         "Manage linked Git worktrees",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	worktreeCommand.AddCommand(
		NewWorktreeAddCommand(WorktreeAddDependencies{
			Resolver:        runtime.resolver,
			Discover:        runtime.discover,
			Git:             runtime.git,
			Gh:              runtime.gh,
			API:             runtime.api,
			AccountResolver: runtime.accountResolver,
			Stdout:          runtime.stdout,
			Stderr:          runtime.stderr,
		}),
		NewWorktreeListCommand(WorktreeListDependencies{
			Resolver: runtime.resolver,
			Discover: runtime.discover,
			Git:      runtime.git,
			Stdout:   runtime.stdout,
			Stderr:   runtime.stderr,
		}),
		NewWorktreeRemoveCommand(WorktreeRemoveDependencies{
			Resolver: runtime.resolver,
			Discover: runtime.discover,
			Git:      runtime.git,
			Stdout:   runtime.stdout,
			Stderr:   runtime.stderr,
		}),
		NewWorktreePruneCommand(WorktreePruneDependencies{
			Resolver: runtime.resolver,
			Discover: runtime.discover,
			Git:      runtime.git,
			Stdout:   runtime.stdout,
			Stderr:   runtime.stderr,
		}),
	)

	command.AddCommand(
		getCommand,
		listCommand,
		rootCommand,
		removeCommand,
		migrateCommand,
		worktreeCommand,
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
		_, _ = fmt.Fprintf(command.ErrOrStderr(), "gh-qw: %v\n", err)
		return ExitCode(err)
	}
	return 0
}

// ExitCode maps a command failure to the documented process status.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	for _, usageError := range []error{
		ErrCommandUsage,
		ErrGetUsage,
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

func prepareApplicationRuntime(dependencies ApplicationDependencies) applicationRuntime {
	stdin := dependencies.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := dependencies.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	resolver := dependencies.Resolver
	if resolver == nil {
		resolver = rootpkg.NewResolver()
	}
	git := dependencies.Git
	if git == nil {
		git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     stderr,
			Stderr:     stderr,
		}
	}
	gh := dependencies.Gh
	if gh == nil {
		gh = &ghcmd.Runner{
			Stdout: stderr,
			Stderr: stderr,
		}
	}
	gitFactory := dependencies.GitFactory
	if gitFactory == nil {
		gitFactory = applicationGhFactory(gh)
	}
	accountResolver := dependencies.AccountResolver
	if accountResolver == nil {
		accountResolver = ghauth.NewResolver(ghauth.ResolverOptions{
			Runner:     ghcmd.NewRunner(),
			Cache:      ghauth.NewCache(),
			Stdin:      stdin,
			Stderr:     stderr,
			IsTerminal: getIsTerminal,
		})
	}
	apiClient := dependencies.API
	if apiClient == nil {
		apiClient = ghapi.NewClient()
	}
	discover := dependencies.Discover
	if discover == nil {
		discover = local.DiscoverRepositories
	}
	version := dependencies.Version
	if version == "" {
		version = developmentVersion
	}

	return applicationRuntime{
		resolver:        resolver,
		git:             git,
		gh:              gh,
		gitFactory:      gitFactory,
		accountResolver: accountResolver,
		api:             apiClient,
		discover:        discover,
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		version:         version,
	}
}

func applicationGhFactory(gh ApplicationGh) GetGitFactory {
	if runner, ok := gh.(*ghcmd.Runner); ok {
		template := *runner
		return func(stdout, stderr io.Writer, token string) GetGitOperations {
			clone := template
			clone.Stdout = stdout
			clone.Stderr = stderr
			scoped := clone.WithToken(token)
			return &scoped
		}
	}
	return func(_, _ io.Writer, _ string) GetGitOperations {
		return gh
	}
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
