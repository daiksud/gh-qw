package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/daiksud/gh-qw/internal/local"
	migratepkg "github.com/daiksud/gh-qw/internal/migrate"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// AddDependencies supplies the existing native Git and gh operations for
// registering a checkout in place, moving it, or adding a linked worktree.
type AddDependencies struct {
	Resolver RootResolver
	Git      MigrateGit
	Worktree WorktreeAddDependencies
	Registry LocationRegistry
	Stdout   io.Writer
	Stderr   io.Writer
	Getwd    func() (string, error)
}

// NewAddCommand adds an existing local repository to gh-qw's explicit location
// registry. With --worktree it selects a registered repository and delegates
// creation of its linked worktree to Git.
func NewAddCommand(deps AddDependencies) *cobra.Command {
	deps.Stdout = commandStdout(deps.Stdout)
	deps.Stderr = commandStderr(deps.Stderr)
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	migrate := MigrateDependencies{
		Resolver: deps.Resolver,
		Git:      deps.Git,
		Registry: deps.Registry,
		Getwd:    deps.Getwd,
		Stdout:   deps.Stdout,
		Stderr:   deps.Stderr,
	}
	migrateApplyDefaults(&migrate)

	var move, worktree, makeBranch, resetBranch, detach, orphan, force bool
	var selector string
	command := &cobra.Command{
		Use:           "add [--move|--worktree] <path-or-slot> [start-point]",
		Short:         "Register an existing repository or add a linked worktree",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if worktree {
				if move {
					return newCommandUsageError(errors.New("--move cannot be combined with --worktree"))
				}
				if err := cobra.RangeArgs(1, 2)(command, args); err != nil {
					return newCommandUsageError(err)
				}
				modeCount := 0
				for _, enabled := range []bool{makeBranch, resetBranch, detach, orphan} {
					if enabled {
						modeCount++
					}
				}
				if modeCount > 1 {
					return newCommandUsageError(errors.New("-b, -B, --detach and --orphan are mutually exclusive"))
				}
				if orphan && len(args) == 2 {
					return newCommandUsageError(errors.New("--orphan cannot have a start point"))
				}
				if err := local.ValidateSlot(command.Context(), args[0]); err != nil {
					if errors.Is(err, local.ErrInvalidBranch) {
						return newCommandUsageError(err)
					}
					return err
				}
				return nil
			}
			if err := cobra.ExactArgs(1)(command, args); err != nil {
				return newCommandUsageError(err)
			}
			if command.Flags().Changed("repo") || makeBranch || resetBranch || detach || orphan || force {
				return newCommandUsageError(errors.New("worktree-specific flags require --worktree"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if worktree {
				runtime := worktreeAddPrepareRuntime(deps.Worktree)
				mode := worktreeAddModeAutomatic
				switch {
				case makeBranch:
					mode = worktreeAddModeNew
				case resetBranch:
					mode = worktreeAddModeReset
				case detach:
					mode = worktreeAddModeDetach
				case orphan:
					mode = worktreeAddModeOrphan
				}
				request := worktreeAddRequest{
					selector:    selector,
					selectorSet: command.Flags().Changed("repo"),
					branch:      args[0],
					mode:        mode,
					force:       force,
				}
				if len(args) == 2 {
					request.commitish = args[1]
				}
				return worktreeAddRun(command.Context(), runtime, request)
			}
			store, err := resolveLocationRegistry(deps.Registry)
			if err != nil {
				return err
			}
			migrate.Registry = store
			return addRepository(command.Context(), migrate, store, args[0], move, command.OutOrStdout())
		},
	}
	flags := command.Flags()
	flags.BoolVar(&move, "move", false, "Move an existing repository into the configured primary root")
	flags.BoolVar(&worktree, "worktree", false, "Create a linked Git worktree instead of registering a repository")
	flags.StringVarP(&selector, "repo", "R", "", "Select the repository for a linked worktree")
	flags.BoolVarP(&makeBranch, "b", "b", false, "Create a new worktree branch")
	flags.BoolVarP(&resetBranch, "B", "B", false, "Create or reset a worktree branch")
	flags.BoolVar(&detach, "detach", false, "Create a detached worktree")
	flags.BoolVar(&orphan, "orphan", false, "Create an unborn worktree branch")
	flags.BoolVarP(&force, "force", "f", false, "Pass one force level to Git when adding a worktree")
	command.SetOut(deps.Stdout)
	command.SetErr(deps.Stderr)
	return command
}

func addRepository(ctx context.Context, deps MigrateDependencies, store LocationRegistry, source string, move bool, stdout io.Writer) error {
	roots, err := deps.Resolver.Resolve()
	if err != nil {
		return err
	}
	if move {
		item, err := migratePlanSingle(ctx, deps, roots, source)
		if err != nil {
			return fmt.Errorf("plan repository move: %w", err)
		}
		entries, err := store.List()
		if err != nil {
			return fmt.Errorf("read registered locations before move: %w", err)
		}
		if previous, found := entries[item.identity]; found {
			equal, compareErr := (physicalpath.Filesystem{}).Equivalent(previous, item.source)
			if compareErr != nil {
				return fmt.Errorf("compare registered location before move: %w", compareErr)
			}
			if !equal {
				return fmt.Errorf("repository %q is registered at %q instead of move source %q", item.identity, previous, item.source)
			}
		}
		moved, err := migrateExecuteItem(ctx, deps, roots, item)
		if err != nil {
			return err
		}
		if !moved {
			return fmt.Errorf("repository %q was not moved", item.identity)
		}
		if err := store.Register(item.identity, item.destination); err != nil {
			return fmt.Errorf("repository moved to %q but registration failed: %w", item.destination, err)
		}
		return getWritePath(stdout, item.destination)
	}

	absolute, err := migrateAbsoluteInput(source, deps.Getwd)
	if err != nil {
		return err
	}
	snapshot, err := migratepkg.InspectRepository(ctx, absolute, deps.Git, deps.Filesystem)
	if err != nil {
		return fmt.Errorf("inspect repository to add: %w", err)
	}
	spec, err := migrateRemoteSpec(ctx, deps.Git, snapshot.Path, roots.RepositoryRoots)
	if err != nil {
		return fmt.Errorf("identify repository to add: %w", err)
	}
	if err := store.Register(spec.Identity.String(), snapshot.Path); err != nil {
		return fmt.Errorf("register repository location: %w", err)
	}
	return getWritePath(stdout, snapshot.Path)
}
