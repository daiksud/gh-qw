package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	migratepkg "github.com/daiksud/gh-qw/internal/migrate"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// MigrateGit combines the Git operations used by migrate.
type MigrateGit interface {
	migratepkg.ConfigGit
	migratepkg.RepositoryGit
	WorktreeRepair(context.Context, string, ...string) error
}

// MigratePrompt asks for confirmation without reading command stdin.
type MigratePrompt func(context.Context, io.Writer, string) (bool, error)

// MigrateDependencies supplies injectable migrate command dependencies.
type MigrateDependencies struct {
	Resolver        RootResolver
	Git             MigrateGit
	Filesystem      migratepkg.Filesystem
	Prompt          MigratePrompt
	LookupEnv       func(string) (string, bool)
	HomeDir         func() (string, error)
	Getwd           func() (string, error)
	IsConfigMissing func(error) bool
	Stdout          io.Writer
	Stderr          io.Writer
}

type migratePlanItem struct {
	source       string
	sourceRoot   string
	destination  string
	snapshot     migratepkg.RepositorySnapshot
	backPointers migratepkg.BackPointerPlan
	bulk         bool
	singleRoots  []string
	identity     string
}

var migrateErrDeclined = errors.New("migration declined")

// NewMigrateCommand returns the command that migrates legacy repositories into
// the primary gh-qw root.
func NewMigrateCommand(dependencies MigrateDependencies) *cobra.Command {
	migrateApplyDefaults(&dependencies)

	var (
		migrateYes    bool
		migrateDryRun bool
	)
	command := &cobra.Command{
		Use:           "migrate [directory]",
		Short:         "Migrate repositories into the primary gh-qw root",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, args []string) error {
			rootResult, err := dependencies.Resolver.Resolve()
			if err != nil {
				return err
			}
			primaryRoot := rootResult.Primary()
			if primaryRoot == "" {
				return errors.New("migrate: primary repository root is empty")
			}

			var plan []migratePlanItem
			if len(args) == 0 {
				plan, err = migratePlanBulk(
					command.Context(),
					dependencies,
					rootResult,
				)
			} else {
				var item migratePlanItem
				item, err = migratePlanSingle(
					command.Context(),
					dependencies,
					rootResult,
					args[0],
				)
				if err == nil {
					plan = []migratePlanItem{item}
				}
			}
			if err != nil {
				return err
			}

			if err := migrateWritePlan(command.ErrOrStderr(), plan); err != nil {
				return err
			}
			if migrateDryRun || len(plan) == 0 {
				return nil
			}

			if !migrateYes {
				confirmed, err := dependencies.Prompt(
					command.Context(),
					command.ErrOrStderr(),
					"Proceed with migration? [y/N] ",
				)
				if err != nil {
					return fmt.Errorf("confirm migration: %w", err)
				}
				if !confirmed {
					return migrateErrDeclined
				}
			}

			for _, item := range plan {
				moved, err := migrateExecuteItem(
					command.Context(),
					dependencies,
					rootResult,
					item,
				)
				if err != nil {
					return err
				}
				if !moved {
					continue
				}
				if _, err := fmt.Fprintln(
					command.OutOrStdout(),
					local.NormalizePathForOutput(item.destination),
				); err != nil {
					return fmt.Errorf(
						"repository migrated to %q but write result: %w",
						item.destination,
						err,
					)
				}
			}
			return nil
		},
	}
	command.Flags().BoolVarP(&migrateYes, "yes", "y", false, "Migrate without confirmation")
	command.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Print the complete plan without changing files")
	command.SetOut(dependencies.Stdout)
	command.SetErr(dependencies.Stderr)
	return command
}

func migrateApplyDefaults(dependencies *MigrateDependencies) {
	if dependencies.Resolver == nil {
		dependencies.Resolver = rootpkg.NewResolver()
	}
	if dependencies.Stdout == nil {
		dependencies.Stdout = os.Stdout
	}
	if dependencies.Stderr == nil {
		dependencies.Stderr = os.Stderr
	}
	if dependencies.Git == nil {
		dependencies.Git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     dependencies.Stderr,
			Stderr:     dependencies.Stderr,
		}
	}
	if dependencies.Prompt == nil {
		dependencies.Prompt = migrateConfirm
	}
	if dependencies.LookupEnv == nil {
		dependencies.LookupEnv = os.LookupEnv
	}
	if dependencies.HomeDir == nil {
		dependencies.HomeDir = os.UserHomeDir
	}
	if dependencies.Getwd == nil {
		dependencies.Getwd = os.Getwd
	}
}

func migratePlanBulk(
	ctx context.Context,
	dependencies MigrateDependencies,
	rootResult rootpkg.Result,
) ([]migratePlanItem, error) {
	legacyResult, err := migratepkg.DiscoverLegacyRoots(
		ctx,
		dependencies.Git,
		migratepkg.LegacyOptions{
			LookupEnv:       dependencies.LookupEnv,
			HomeDir:         dependencies.HomeDir,
			Filesystem:      dependencies.Filesystem,
			IsConfigMissing: dependencies.IsConfigMissing,
		},
	)
	if err != nil {
		return nil, err
	}
	for _, warning := range legacyResult.Warnings {
		if err := migrateWarning(
			dependencies.Stderr,
			"skipping legacy root %q: %v",
			warning.Value,
			warning.Err,
		); err != nil {
			return nil, err
		}
	}

	discovery, err := local.DiscoverRepositories(
		ctx,
		legacyResult.Roots,
		local.DiscoveryOptions{
			Git:        dependencies.Git,
			Filesystem: migratepkg.LocalFilesystemOptions(dependencies.Filesystem),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("discover legacy repositories: %w", err)
	}
	for _, warning := range discovery.Warnings {
		if err := migrateWarning(
			dependencies.Stderr,
			"skipping %q: %v",
			warning.Path,
			warning.Err,
		); err != nil {
			return nil, err
		}
	}

	primaryRoot := rootResult.Primary()
	plannedDestinations := make(map[string]string, len(discovery.Repositories))
	plan := make([]migratePlanItem, 0, len(discovery.Repositories))
	for _, repository := range discovery.Repositories {
		destination, err := migratepkg.DestinationPath(
			primaryRoot,
			repository.Host,
			repository.Owner,
			repository.Repo,
			dependencies.Filesystem,
		)
		if err != nil {
			return nil, fmt.Errorf("plan migration for %q: %w", repository.Path, err)
		}

		physicalDestination, exists, err := migratepkg.CheckDestination(
			primaryRoot,
			destination,
			dependencies.Filesystem,
		)
		if err != nil {
			return nil, fmt.Errorf("plan migration for %q: %w", repository.Path, err)
		}
		if physicalDestination != destination {
			return nil, fmt.Errorf(
				"plan migration for %q: destination changed from %q to %q",
				repository.Path,
				destination,
				physicalDestination,
			)
		}
		if exists {
			if err := migrateCollisionWarning(dependencies.Stderr, repository.Path, destination); err != nil {
				return nil, err
			}
			continue
		}
		if previous, duplicate := plannedDestinations[destination]; duplicate {
			if err := migrateWarning(
				dependencies.Stderr,
				"skipping %q: destination %q is already planned from %q",
				repository.Path,
				destination,
				previous,
			); err != nil {
				return nil, err
			}
			continue
		}

		item, err := migratePrepareItem(
			ctx,
			dependencies,
			repository.Path,
			repository.Root,
			destination,
		)
		if err != nil {
			return nil, fmt.Errorf("plan migration for %q: %w", repository.Path, err)
		}
		item.bulk = true
		item.identity = repository.Identity
		plannedDestinations[destination] = repository.Path
		plan = append(plan, item)
	}
	return plan, nil
}

func migratePlanSingle(
	ctx context.Context,
	dependencies MigrateDependencies,
	rootResult rootpkg.Result,
	argument string,
) (migratePlanItem, error) {
	source, err := migrateAbsoluteInput(argument, dependencies.Getwd)
	if err != nil {
		return migratePlanItem{}, err
	}
	snapshot, err := migratepkg.InspectRepository(
		ctx,
		source,
		dependencies.Git,
		dependencies.Filesystem,
	)
	if err != nil {
		return migratePlanItem{}, err
	}
	source = snapshot.Path

	spec, err := migrateRemoteSpec(ctx, dependencies.Git, source, rootResult.RepositoryRoots)
	if err != nil {
		return migratePlanItem{}, err
	}
	destination, err := migratepkg.DestinationPath(
		rootResult.Primary(),
		spec.Host,
		spec.Owner,
		spec.Repo,
		dependencies.Filesystem,
	)
	if err != nil {
		return migratePlanItem{}, err
	}
	physicalDestination, exists, err := migratepkg.CheckDestination(
		rootResult.Primary(),
		destination,
		dependencies.Filesystem,
	)
	if err != nil {
		return migratePlanItem{}, err
	}
	if physicalDestination != destination {
		return migratePlanItem{}, fmt.Errorf(
			"migration destination changed from %q to %q",
			destination,
			physicalDestination,
		)
	}
	if exists {
		return migratePlanItem{}, fmt.Errorf(
			"migration destination %q already exists",
			destination,
		)
	}

	item, err := migratePrepareSnapshot(
		dependencies,
		snapshot,
		"",
		destination,
	)
	if err != nil {
		return migratePlanItem{}, err
	}
	item.singleRoots = append([]string(nil), rootResult.RepositoryRoots...)
	item.identity = spec.Identity
	return item, nil
}

func migratePrepareItem(
	ctx context.Context,
	dependencies MigrateDependencies,
	source, sourceRoot, destination string,
) (migratePlanItem, error) {
	snapshot, err := migratepkg.InspectRepository(
		ctx,
		source,
		dependencies.Git,
		dependencies.Filesystem,
	)
	if err != nil {
		return migratePlanItem{}, err
	}
	if snapshot.Path != filepath.Clean(source) {
		return migratePlanItem{}, fmt.Errorf(
			"migration source changed from %q to %q",
			source,
			snapshot.Path,
		)
	}
	return migratePrepareSnapshot(dependencies, snapshot, sourceRoot, destination)
}

func migratePrepareSnapshot(
	dependencies MigrateDependencies,
	snapshot migratepkg.RepositorySnapshot,
	sourceRoot, destination string,
) (migratePlanItem, error) {
	source := snapshot.Path
	if err := migratepkg.ValidateDisjoint(source, destination); err != nil {
		return migratePlanItem{}, err
	}
	if err := migratepkg.ValidateSourceContainment(
		sourceRoot,
		source,
		dependencies.Filesystem,
	); err != nil {
		return migratePlanItem{}, err
	}
	if err := migratepkg.ValidateTree(source, dependencies.Filesystem); err != nil {
		return migratePlanItem{}, fmt.Errorf("preflight repository tree: %w", err)
	}
	backPointers, err := migratepkg.PlanBackPointers(
		source,
		destination,
		dependencies.Filesystem,
	)
	if err != nil {
		return migratePlanItem{}, fmt.Errorf("preflight linked worktrees: %w", err)
	}
	return migratePlanItem{
		source:       source,
		sourceRoot:   sourceRoot,
		destination:  destination,
		snapshot:     snapshot,
		backPointers: backPointers,
	}, nil
}

func migrateExecuteItem(
	ctx context.Context,
	dependencies MigrateDependencies,
	rootResult rootpkg.Result,
	item migratePlanItem,
) (bool, error) {
	if err := item.snapshot.Revalidate(ctx, dependencies.Git, dependencies.Filesystem); err != nil {
		return false, err
	}
	if err := migratepkg.ValidateSourceContainment(
		item.sourceRoot,
		item.source,
		dependencies.Filesystem,
	); err != nil {
		return false, err
	}
	if err := migratepkg.ValidateTree(item.source, dependencies.Filesystem); err != nil {
		return false, fmt.Errorf("revalidate repository tree: %w", err)
	}
	if err := item.backPointers.Revalidate(dependencies.Filesystem); err != nil {
		return false, fmt.Errorf("revalidate linked worktrees: %w", err)
	}
	if !item.bulk {
		spec, err := migrateRemoteSpec(
			ctx,
			dependencies.Git,
			item.source,
			item.singleRoots,
		)
		if err != nil {
			return false, fmt.Errorf("revalidate migration remote: %w", err)
		}
		if spec.Identity != item.identity {
			return false, fmt.Errorf(
				"revalidate migration remote: identity changed from %q to %q",
				item.identity,
				spec.Identity,
			)
		}
		destination, err := migratepkg.DestinationPath(
			rootResult.Primary(),
			spec.Host,
			spec.Owner,
			spec.Repo,
			dependencies.Filesystem,
		)
		if err != nil {
			return false, fmt.Errorf("revalidate migration destination: %w", err)
		}
		if destination != item.destination {
			return false, fmt.Errorf(
				"revalidate migration destination: changed from %q to %q",
				item.destination,
				destination,
			)
		}
	}

	err := migratepkg.Move(
		item.source,
		item.destination,
		migratepkg.MoveOptions{
			Filesystem:      dependencies.Filesystem,
			SourceRoot:      item.sourceRoot,
			DestinationRoot: rootResult.Primary(),
		},
	)
	if err != nil {
		if item.bulk && errors.Is(err, migratepkg.ErrDestinationExists) {
			if warningErr := migrateCollisionWarning(
				dependencies.Stderr,
				item.source,
				item.destination,
			); warningErr != nil {
				return false, warningErr
			}
			return false, nil
		}
		return false, fmt.Errorf("migrate %q to %q: %w", item.source, item.destination, err)
	}

	if err := item.backPointers.ApplyBackPointers(dependencies.Filesystem); err != nil {
		return false, fmt.Errorf(
			"repository moved to %q, but linked-worktree back-pointer repair failed; "+
				"leave the repository at its destination and repair it manually: %w",
			item.destination,
			err,
		)
	}
	repairPaths := item.backPointers.RepairPaths()
	if len(repairPaths) != 0 {
		if err := dependencies.Git.WorktreeRepair(ctx, item.destination, repairPaths...); err != nil {
			return false, fmt.Errorf(
				"repository moved to %q, but git worktree repair failed; "+
					"leave the repository there and rerun git worktree repair from that directory: %w",
				item.destination,
				err,
			)
		}
	}
	return true, nil
}

func migrateRemoteSpec(
	ctx context.Context,
	git MigrateGit,
	source string,
	roots []string,
) (repospec.Spec, error) {
	remoteURL, err := migrateRemoteURL(ctx, git, source)
	if err != nil {
		return repospec.Spec{}, err
	}
	spec, err := repospec.Parse(remoteURL, repospec.Options{
		Roots:      roots,
		WorkingDir: source,
	})
	if err != nil {
		return repospec.Spec{}, fmt.Errorf("parse migration remote: %w", err)
	}
	if spec.Branch != "" {
		return repospec.Spec{}, &repospec.UsageError{
			Input:  remoteURL,
			Reason: "migration remote must contain exactly host, owner, and repository",
		}
	}
	return spec, nil
}

func migrateRemoteURL(
	ctx context.Context,
	git MigrateGit,
	source string,
) (string, error) {
	output, err := git.OutputDir(ctx, source, "remote")
	if err != nil {
		return "", fmt.Errorf("list migration remotes: %w", err)
	}
	remotes, err := migrateOutputLines(output, "remote list")
	if err != nil {
		return "", err
	}
	if len(remotes) == 0 {
		return "", errors.New("migration repository has no configured remote")
	}

	selected := remotes[0]
	for _, remote := range remotes {
		if remote == "origin" {
			selected = remote
			break
		}
	}
	output, err = git.OutputDir(ctx, source, "remote", "get-url", selected)
	if err != nil {
		return "", fmt.Errorf("read URL for remote %q: %w", selected, err)
	}
	urls, err := migrateOutputLines(output, "remote URL")
	if err != nil {
		return "", err
	}
	if len(urls) != 1 {
		return "", fmt.Errorf("remote %q returned %d URLs, want exactly one", selected, len(urls))
	}
	return urls[0], nil
}

func migrateOutputLines(output []byte, description string) ([]string, error) {
	text := strings.TrimSuffix(string(output), "\n")
	text = strings.TrimSuffix(text, "\r")
	if text == "" {
		return nil, nil
	}
	if strings.IndexByte(text, 0) >= 0 {
		return nil, fmt.Errorf("Git returned NUL in %s", description)
	}
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
		if lines[index] == "" {
			return nil, fmt.Errorf("Git returned an empty line in %s", description)
		}
	}
	return lines, nil
}

func migrateAbsoluteInput(argument string, getwd func() (string, error)) (string, error) {
	if argument == "" {
		return "", errors.New("migration directory is empty")
	}
	if strings.IndexByte(argument, 0) >= 0 {
		return "", errors.New("migration directory contains NUL")
	}
	if filepath.IsAbs(argument) {
		return filepath.Clean(argument), nil
	}
	workingDir, err := getwd()
	if err != nil {
		return "", fmt.Errorf("resolve migration directory: get working directory: %w", err)
	}
	if !filepath.IsAbs(workingDir) {
		return "", fmt.Errorf("resolve migration directory: working directory %q is not absolute", workingDir)
	}
	return filepath.Clean(filepath.Join(workingDir, argument)), nil
}

func migrateWritePlan(writer io.Writer, plan []migratePlanItem) error {
	if _, err := fmt.Fprintln(writer, "Migration plan:"); err != nil {
		return err
	}
	if len(plan) == 0 {
		_, err := fmt.Fprintln(writer, "  (nothing to migrate)")
		return err
	}
	for _, item := range plan {
		if _, err := fmt.Fprintf(
			writer,
			"  %s -> %s\n",
			local.NormalizePathForOutput(item.source),
			local.NormalizePathForOutput(item.destination),
		); err != nil {
			return err
		}
		paths := item.backPointers.RepairPaths()
		if len(paths) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(writer, "    repair linked worktrees:"); err != nil {
			return err
		}
		for _, path := range paths {
			if _, err := fmt.Fprintf(
				writer,
				"      %s\n",
				local.NormalizePathForOutput(path),
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateCollisionWarning(writer io.Writer, source, destination string) error {
	return migrateWarning(
		writer,
		"skipping %q: destination %q already exists",
		source,
		destination,
	)
}

func migrateWarning(writer io.Writer, format string, arguments ...any) error {
	_, err := fmt.Fprintf(writer, "gh-qw: warning: "+format+"\n", arguments...)
	return err
}

func migrateConfirm(ctx context.Context, output io.Writer, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	terminalPath := "/dev/tty"
	if runtime.GOOS == "windows" {
		terminalPath = "CONIN$"
	}
	terminal, err := os.Open(terminalPath)
	if err != nil {
		return false, fmt.Errorf("open controlling terminal: %w", err)
	}
	defer terminal.Close()

	if _, err := fmt.Fprint(output, prompt); err != nil {
		return false, err
	}
	response, err := bufio.NewReader(terminal).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read controlling terminal: %w", err)
	}
	response = strings.TrimSpace(response)
	return strings.EqualFold(response, "y") || strings.EqualFold(response, "yes"), nil
}
