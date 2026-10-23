package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/locations"
	migratepkg "github.com/daiksud/gh-qw/internal/migrate"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// AddDependencies contains the location registry and the native Git operations
// required to register a repository or add a linked worktree.
type AddDependencies struct {
	Resolver RootResolver
	Git      MigrateGit
	Store    locations.Store
	Worktree WorktreeAddDependencies
	Stdout   io.Writer
	Stderr   io.Writer
}

// NewAddCommand adds a local repository to the location registry, optionally
// relocating it, or delegates linked-worktree creation to Git.
func NewAddCommand(deps AddDependencies) *cobra.Command {
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	deps.Stdout = commandStdout(deps.Stdout)
	deps.Stderr = commandStderr(deps.Stderr)
	if deps.Git == nil {
		deps.Git = &gitcmd.Runner{Executable: "git", Stdout: io.Discard, Stderr: deps.Stderr}
	}
	if deps.Worktree.Resolver == nil {
		deps.Worktree.Resolver = deps.Resolver
	}
	if deps.Worktree.Stdout == nil {
		deps.Worktree.Stdout = deps.Stdout
	}
	if deps.Worktree.Stderr == nil {
		deps.Worktree.Stderr = deps.Stderr
	}
	worktreeRuntime := worktreeAddPrepareRuntime(deps.Worktree)

	var (
		worktree bool
		move bool
		selector string
		newMode bool
		resetMode bool
		detachMode bool
		orphanMode bool
		force bool
	)
	command := &cobra.Command{
		Use: "add [--move|--worktree] <path-or-slot> [commit-ish]",
		Short: "Register a repository or add a linked worktree",
		SilenceErrors: true,
		SilenceUsage: true,
		Args: func(command *cobra.Command, args []string) error {
			if !worktree {
				if err := cobra.ExactArgs(1)(command, args); err != nil {
					return newCommandUsageError(err)
				}
				if selector != "" || command.Flags().Changed("repo") || newMode || resetMode || detachMode || orphanMode || force {
					return newCommandUsageError(errors.New("worktree options require --worktree"))
				}
				return nil
			}
			if move {
				return newCommandUsageError(errors.New("--move and --worktree are mutually exclusive"))
			}
			if err := cobra.RangeArgs(1, 2)(command, args); err != nil {
				return newCommandUsageError(err)
			}
			count := 0
			for _, mode := range []bool{newMode, resetMode, detachMode, orphanMode} {
				if mode { count++ }
			}
			if count > 1 || (orphanMode && len(args) == 2) {
				return newCommandUsageError(errors.New("conflicting worktree creation options"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if !worktree {
				return addManagedRepository(command.Context(), deps, args[0], move, command.OutOrStdout())
			}
			if err := local.ValidateSlot(command.Context(), args[0]); err != nil {
				return err
			}
			mode := worktreeAddModeAutomatic
			switch {
			case newMode: mode = worktreeAddModeNew
			case resetMode: mode = worktreeAddModeReset
			case detachMode: mode = worktreeAddModeDetach
			case orphanMode: mode = worktreeAddModeOrphan
			}
			request := worktreeAddRequest{
				selector: selector,
				selectorSet: command.Flags().Changed("repo"),
				branch: args[0],
				mode: mode,
				force: force,
			}
			if len(args) == 2 {
				request.commitish = args[1]
			}
			return worktreeAddRun(command.Context(), worktreeRuntime, request)
		},
	}
	flags := command.Flags()
	flags.BoolVar(&worktree, "worktree", false, "Add a linked Git worktree")
	flags.BoolVar(&move, "move", false, "Move an existing repository to the primary managed root")
	flags.StringVarP(&selector, "repo", "R", "", "Select the parent repository for a worktree")
	flags.BoolVarP(&newMode, "b", "b", false, "Create a new worktree branch")
	flags.BoolVarP(&resetMode, "B", "B", false, "Create or reset a worktree branch")
	flags.BoolVar(&detachMode, "detach", false, "Create a detached worktree")
	flags.BoolVar(&orphanMode, "orphan", false, "Create an unborn worktree branch")
	flags.BoolVarP(&force, "force", "f", false, "Pass one level of Git force to worktree add")
	command.SetOut(deps.Stdout)
	command.SetErr(deps.Stderr)
	return command
}

func addManagedRepository(ctx context.Context, deps AddDependencies, rawPath string, move bool, output io.Writer) error {
	roots, err := deps.Resolver.Resolve()
	if err != nil {
		return err
	}
	migrateDeps := MigrateDependencies{
		Resolver: deps.Resolver,
		Git: deps.Git,
		Stdout: deps.Stdout,
		Stderr: deps.Stderr,
	}
	migrateApplyDefaults(&migrateDeps)

	var identity, path string
	if move {
		plan, err := migratePlanSingle(ctx, migrateDeps, roots, rawPath)
		if err != nil {
			return err
		}
		identity, path = plan.identity, plan.destination
		if err := addCheckRegistry(deps.Store, identity, path); err != nil {
			return err
		}
		moved, err := migrateExecuteItem(ctx, migrateDeps, roots, plan)
		if err != nil {
			return fmt.Errorf("move repository from %q to %q: %w; inspect the destination and use gh qw add to register any completed move", plan.source, plan.destination, err)
		}
		if !moved {
			return errors.New("repository was not moved")
		}
	} else {
		source, err := migrateAbsoluteInput(rawPath, os.Getwd)
		if err != nil {
			return err
		}
		snapshot, err := migratepkg.InspectRepository(ctx, source, deps.Git, migrateDeps.Filesystem)
		if err != nil {
			return err
		}
		spec, err := migrateRemoteSpec(ctx, deps.Git, snapshot.Path, roots.RepositoryRoots)
		if err != nil {
			return err
		}
		if err := snapshot.Revalidate(ctx, deps.Git, migrateDeps.Filesystem); err != nil {
			return err
		}
		identity, path = spec.Identity.String(), snapshot.Path
	}
	if err := deps.Store.Add(identity, path); err != nil {
		if move {
			return fmt.Errorf("repository moved to %q but registration failed (run gh qw add %q): %w", path, path, err)
		}
		return err
	}
	return commandWriteAll(output, []byte(local.NormalizePathForOutput(filepath.Clean(path))+"\n"))
}

func addCheckRegistry(store locations.Store, identity, path string) error {
	entries, err := store.Read()
	if err != nil {
		return err
	}
	if existing, found := entries[identity]; found {
		return fmt.Errorf("repository %q is already registered at %q; forget it before moving", identity, existing)
	}
	for registered, existing := range entries {
		if filepath.Clean(path) == filepath.Clean(existing) {
			return fmt.Errorf("destination %q is already registered for %q", path, registered)
		}
	}
	return nil
}
