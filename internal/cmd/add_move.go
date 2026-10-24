package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	movementpkg "github.com/daiksud/gh-qw/internal/movement"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

// RepositoryMoveGit combines the Git operations used to move an existing repository.
type RepositoryMoveGit interface {
	movementpkg.RepositoryGit
	movementpkg.WorktreeLister
	WorktreeRepair(context.Context, string, ...string) error
}

// RepositoryMoveDependencies supplies injectable repository-move dependencies.
type RepositoryMoveDependencies struct {
	Resolver   RootResolver
	Git        RepositoryMoveGit
	Registry   LocationRegistry
	Filesystem movementpkg.Filesystem
	Getwd      func() (string, error)
	Stdout     io.Writer
	Stderr     io.Writer
}

type repositoryMovePlan struct {
	source      string
	destination string
	snapshot    movementpkg.RepositorySnapshot
	worktrees   movementpkg.WorktreeRepairPlan
	singleRoots []string
	identity    string
}

func repositoryMoveApplyDefaults(dependencies *RepositoryMoveDependencies) {
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
	if dependencies.Getwd == nil {
		dependencies.Getwd = os.Getwd
	}
}

func repositoryMovePlanSingle(
	ctx context.Context,
	dependencies RepositoryMoveDependencies,
	rootResult rootpkg.Result,
	argument string,
) (repositoryMovePlan, error) {
	source, err := repositoryMoveAbsoluteInput(argument, dependencies.Getwd)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	snapshot, err := movementpkg.InspectRepository(
		ctx,
		source,
		dependencies.Git,
		dependencies.Filesystem,
	)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	source = snapshot.Path

	spec, err := repositoryMoveRemoteSpec(ctx, dependencies.Git, source, rootResult.RepositoryRoots)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	destination, err := movementpkg.DestinationPath(
		rootResult.Primary(),
		spec.Host,
		spec.Owner,
		spec.Repo,
		dependencies.Filesystem,
	)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	physicalDestination, exists, err := movementpkg.CheckDestination(
		rootResult.Primary(),
		destination,
		dependencies.Filesystem,
	)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	if physicalDestination != destination {
		return repositoryMovePlan{}, fmt.Errorf(
			"repository destination changed from %q to %q",
			destination,
			physicalDestination,
		)
	}
	if exists {
		return repositoryMovePlan{}, fmt.Errorf(
			"repository destination %q already exists",
			destination,
		)
	}

	item, err := repositoryMovePrepareSnapshot(
		ctx,
		dependencies,
		snapshot,
		destination,
	)
	if err != nil {
		return repositoryMovePlan{}, err
	}
	item.singleRoots = append([]string(nil), rootResult.RepositoryRoots...)
	item.identity = spec.Identity.String()
	return item, nil
}

func repositoryMovePrepareSnapshot(
	ctx context.Context,
	dependencies RepositoryMoveDependencies,
	snapshot movementpkg.RepositorySnapshot,
	destination string,
) (repositoryMovePlan, error) {
	source := snapshot.Path
	if err := movementpkg.ValidateDisjoint(source, destination); err != nil {
		return repositoryMovePlan{}, err
	}
	if err := movementpkg.ValidateSourceContainment(
		"",
		source,
		dependencies.Filesystem,
	); err != nil {
		return repositoryMovePlan{}, err
	}
	if err := movementpkg.ValidateTree(source, dependencies.Filesystem); err != nil {
		return repositoryMovePlan{}, fmt.Errorf("preflight repository tree: %w", err)
	}
	worktrees, err := movementpkg.PlanWorktreeRepair(
		ctx,
		source,
		destination,
		dependencies.Git,
		dependencies.Filesystem,
	)
	if err != nil {
		return repositoryMovePlan{}, fmt.Errorf("preflight linked worktrees: %w", err)
	}
	return repositoryMovePlan{
		source:      source,
		destination: destination,
		snapshot:    snapshot,
		worktrees:   worktrees,
	}, nil
}

func repositoryMoveExecute(
	ctx context.Context,
	dependencies RepositoryMoveDependencies,
	rootResult rootpkg.Result,
	item repositoryMovePlan,
) (bool, error) {
	if err := item.snapshot.Revalidate(ctx, dependencies.Git, dependencies.Filesystem); err != nil {
		return false, err
	}
	if err := movementpkg.ValidateSourceContainment(
		"",
		item.source,
		dependencies.Filesystem,
	); err != nil {
		return false, err
	}
	if err := movementpkg.ValidateTree(item.source, dependencies.Filesystem); err != nil {
		return false, fmt.Errorf("revalidate repository tree: %w", err)
	}
	if err := item.worktrees.Revalidate(ctx, dependencies.Git, dependencies.Filesystem); err != nil {
		return false, fmt.Errorf("revalidate linked worktrees: %w", err)
	}
	spec, err := repositoryMoveRemoteSpec(
		ctx,
		dependencies.Git,
		item.source,
		item.singleRoots,
	)
	if err != nil {
		return false, fmt.Errorf("revalidate repository remote: %w", err)
	}
	if spec.Identity.String() != item.identity {
		return false, fmt.Errorf(
			"revalidate repository remote: identity changed from %q to %q",
			item.identity,
			spec.Identity.String(),
		)
	}
	destination, err := movementpkg.DestinationPath(
		rootResult.Primary(),
		spec.Host,
		spec.Owner,
		spec.Repo,
		dependencies.Filesystem,
	)
	if err != nil {
		return false, fmt.Errorf("revalidate repository destination: %w", err)
	}
	if destination != item.destination {
		return false, fmt.Errorf(
			"revalidate repository destination: changed from %q to %q",
			item.destination,
			destination,
		)
	}

	registered := false
	if dependencies.Registry != nil {
		entries, err := dependencies.Registry.List()
		if err != nil {
			return false, fmt.Errorf("read registered locations before move: %w", err)
		}
		sourcePhysical, err := physicalRegisteredLocation(item.source)
		if err != nil {
			return false, err
		}
		for other, location := range entries {
			if other == item.identity {
				continue
			}
			locationPhysical, resolveErr := physicalRegisteredLocation(location)
			if resolveErr != nil {
				return false, resolveErr
			}
			within, withinErr := physicalRegisteredLocationStrictlyWithin(sourcePhysical, locationPhysical)
			if withinErr != nil {
				return false, withinErr
			}
			if within {
				return false, fmt.Errorf("registered repository %q at %q is nested inside move source %q", other, location, item.source)
			}
			equal, compareErr := (physicalpath.Filesystem{}).Equivalent(location, item.destination)
			if compareErr != nil {
				return false, fmt.Errorf("compare repository destination with registered location: %w", compareErr)
			}
			if equal {
				return false, fmt.Errorf("repository destination %q is already registered as %q", item.destination, other)
			}
		}
		if current, found := entries[item.identity]; found {
			equal, compareErr := (physicalpath.Filesystem{}).Equivalent(current, item.source)
			if compareErr != nil {
				return false, fmt.Errorf("compare registered location before move: %w", compareErr)
			}
			if !equal {
				return false, fmt.Errorf("repository %q is registered at %q instead of move source %q", item.identity, current, item.source)
			}
			registered = true
		}
	}

	err = movementpkg.Move(
		item.source,
		item.destination,
		movementpkg.MoveOptions{
			Filesystem:      dependencies.Filesystem,
			SourceRoot:      "",
			DestinationRoot: rootResult.Primary(),
		},
	)
	if err != nil {
		return false, fmt.Errorf("move %q to %q: %w", item.source, item.destination, err)
	}

	var registryErr error
	if registered {
		if err := dependencies.Registry.Relocate(item.identity, item.source, item.destination); err != nil {
			registryErr = fmt.Errorf("repository moved to %q but update its registered location: %w", item.destination, err)
		}
	}

	repairPaths := item.worktrees.RepairPaths()
	if len(repairPaths) != 0 {
		if err := dependencies.Git.WorktreeRepair(ctx, item.destination, repairPaths...); err != nil {
			return false, errors.Join(registryErr, fmt.Errorf(
				"repository moved to %q, but git worktree repair failed; "+
					"leave the repository there and rerun git worktree repair from that directory with these path arguments: %q: %w",
				item.destination,
				repairPaths,
				err,
			))
		}
	}
	return registryErr == nil, registryErr
}

func repositoryMoveRemoteSpec(
	ctx context.Context,
	git RepositoryMoveGit,
	source string,
	roots []string,
) (repospec.Spec, error) {
	remoteURL, err := repositoryMoveRemoteURL(ctx, git, source)
	if err != nil {
		return repospec.Spec{}, err
	}
	parsed, err := repospec.Parse(remoteURL)
	if err != nil {
		return repospec.Spec{}, fmt.Errorf("parse repository remote: %w", err)
	}
	spec, err := parsed.Resolve(ctx, repospec.Options{
		Roots:      roots,
		WorkingDir: source,
	})
	if err != nil {
		return repospec.Spec{}, fmt.Errorf("parse repository remote: %w", err)
	}
	if spec.Branch != "" {
		return repospec.Spec{}, &repospec.UsageError{
			Input:  remoteURL,
			Reason: "repository remote must contain exactly host, owner, and repository",
		}
	}
	return spec, nil
}

func repositoryMoveRemoteURL(
	ctx context.Context,
	git RepositoryMoveGit,
	source string,
) (string, error) {
	output, err := git.OutputDir(ctx, source, "remote")
	if err != nil {
		return "", fmt.Errorf("list repository remotes: %w", err)
	}
	remotes, err := repositoryMoveOutputLines(output, "remote list")
	if err != nil {
		return "", err
	}
	if len(remotes) == 0 {
		return "", errors.New("repository has no configured remote")
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
	urls, err := repositoryMoveOutputLines(output, "remote URL")
	if err != nil {
		return "", err
	}
	if len(urls) != 1 {
		return "", fmt.Errorf("remote %q returned %d URLs, want exactly one", selected, len(urls))
	}
	return urls[0], nil
}

func repositoryMoveOutputLines(output []byte, description string) ([]string, error) {
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

func repositoryMoveAbsoluteInput(argument string, getwd func() (string, error)) (string, error) {
	if argument == "" {
		return "", errors.New("repository move directory is empty")
	}
	if strings.IndexByte(argument, 0) >= 0 {
		return "", errors.New("repository move directory contains NUL")
	}
	if filepath.IsAbs(argument) {
		return filepath.Clean(argument), nil
	}
	workingDir, err := getwd()
	if err != nil {
		return "", fmt.Errorf("resolve repository move directory: get working directory: %w", err)
	}
	if !filepath.IsAbs(workingDir) {
		return "", fmt.Errorf("resolve repository move directory: working directory %q is not absolute", workingDir)
	}
	return filepath.Clean(filepath.Join(workingDir, argument)), nil
}
