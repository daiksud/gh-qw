package movement

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/gitcmd"
)

// WorktreeLister observes registrations through Git's porcelain interface.
type WorktreeLister interface {
	WorktreeList(context.Context, string) ([]gitcmd.Worktree, error)
}

type pathObservation struct {
	physical string
	info     fs.FileInfo
}

type worktreeObservation struct {
	path               string
	directory, pointer pathObservation
	repair             string
}

// WorktreeRepairPlan maps registered linked paths across a main-directory move.
// Git owns all repair of the registration metadata.
type WorktreeRepairPlan struct {
	source, destination string
	entries             []worktreeObservation
	metadata            map[string]pathObservation
}

// PlanWorktreeRepair observes registered paths and validates their movement boundaries.
func PlanWorktreeRepair(ctx context.Context, source, destination string, git WorktreeLister, options Filesystem) (WorktreeRepairPlan, error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return WorktreeRepairPlan{}, errors.New("linked-worktree repair paths must be absolute")
	}
	plan := WorktreeRepairPlan{source: filepath.Clean(source), destination: filepath.Clean(destination)}
	filesystem := newFilesystem(options)
	metadata, err := observeWorktreeMetadata(plan.source, filesystem)
	if err != nil {
		return WorktreeRepairPlan{}, err
	}
	plan.metadata = metadata
	entries, err := git.WorktreeList(ctx, plan.source)
	if err != nil {
		return WorktreeRepairPlan{}, fmt.Errorf("list linked worktrees: %w", err)
	}
	seen := map[string]bool{}
	mainFound := false
	for _, entry := range entries {
		path := filepath.Clean(filepath.FromSlash(entry.Path))
		if !filepath.IsAbs(path) {
			return WorktreeRepairPlan{}, fmt.Errorf("registered worktree path %q is not absolute", entry.Path)
		}
		if seen[path] {
			return WorktreeRepairPlan{}, fmt.Errorf("multiple worktree entries identify %q", path)
		}
		seen[path] = true
		if path == plan.source && !entry.Bare {
			mainFound = true
			continue
		}
		directory, err := observeWorktreePath(path, plan.source, filesystem)
		if err != nil {
			return WorktreeRepairPlan{}, err
		}
		pointer, err := observeWorktreePath(filepath.Join(path, ".git"), plan.source, filesystem)
		if err != nil {
			return WorktreeRepairPlan{}, err
		}
		internal := pathStrictlyWithin(plan.source, path)
		repair := path
		if internal {
			relative, err := filepath.Rel(plan.source, path)
			if err != nil {
				return WorktreeRepairPlan{}, err
			}
			repair = filepath.Join(plan.destination, relative)
		}
		plan.entries = append(plan.entries, worktreeObservation{path: path, directory: directory, pointer: pointer, repair: repair})
	}
	if !mainFound {
		return WorktreeRepairPlan{}, errors.New("main worktree is missing from Git registrations")
	}
	sort.Slice(plan.entries, func(i, j int) bool { return plan.entries[i].path < plan.entries[j].path })
	return plan, nil
}

// RepairPaths returns relocated internal and unchanged external worktree paths.
func (plan WorktreeRepairPlan) RepairPaths() []string {
	paths := make([]string, 0, len(plan.entries))
	for _, entry := range plan.entries {
		paths = append(paths, entry.repair)
	}
	return paths
}

// Revalidate rejects registration or filesystem identity changes since planning.
func (plan WorktreeRepairPlan) Revalidate(ctx context.Context, git WorktreeLister, options Filesystem) error {
	current, err := PlanWorktreeRepair(ctx, plan.source, plan.destination, git, options)
	if err != nil {
		return err
	}
	if len(current.entries) != len(plan.entries) {
		return errors.New("linked-worktree registrations changed after planning")
	}
	filesystem := newFilesystem(options)
	if len(current.metadata) != len(plan.metadata) {
		return errors.New("linked-worktree metadata changed after planning")
	}
	for path, before := range plan.metadata {
		after, exists := current.metadata[path]
		if !exists || !samePathObservation(before, after, filesystem) {
			return errors.New("linked-worktree metadata identity changed after planning")
		}
	}
	for i, before := range plan.entries {
		after := current.entries[i]
		if before.path != after.path || before.repair != after.repair ||
			!samePathObservation(before.directory, after.directory, filesystem) ||
			!samePathObservation(before.pointer, after.pointer, filesystem) {
			return errors.New("linked-worktree paths or identities changed after planning")
		}
	}
	return nil
}

// Missing worktree paths remain observable so Git can report repair failures after movement.
func observeWorktreePath(path, source string, filesystem filesystem) (pathObservation, error) {
	physical, err := filesystem.physicalizeAbsolute(path)
	if err != nil {
		return pathObservation{}, fmt.Errorf("resolve registered worktree path %q: %w", path, err)
	}
	if pathStrictlyWithin(source, path) != pathStrictlyWithin(source, physical) {
		return pathObservation{}, fmt.Errorf("registered worktree path %q crosses the source boundary through a symbolic link", path)
	}
	info, err := filesystem.lstat(physical)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pathObservation{}, fmt.Errorf("inspect registered worktree path %q: %w", path, err)
	}
	if info != nil {
		if err := fsidentity.Prime(info, filesystem.sameFile); err != nil {
			return pathObservation{}, fmt.Errorf("capture worktree path identity %q: %w", path, err)
		}
	}
	return pathObservation{physical: physical, info: info}, nil
}

func samePathObservation(before, after pathObservation, filesystem filesystem) bool {
	return before.physical == after.physical && (before.info == nil) == (after.info == nil) &&
		(before.info == nil || filesystem.sameFile(before.info, after.info))
}

// Observe metadata shape and identity only; Git owns its contents and repair.
func observeWorktreeMetadata(source string, filesystem filesystem) (map[string]pathObservation, error) {
	root := filepath.Join(source, ".git", "worktrees")
	observations := make(map[string]pathObservation)
	capture := func(path string, directory bool) error {
		info, err := filesystem.lstat(path)
		if err != nil {
			return fmt.Errorf("inspect linked-worktree metadata %q: %w", path, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
			return fmt.Errorf("linked-worktree metadata %q has an unsafe file type", path)
		}
		physical, err := filesystem.physicalizeAbsolute(path)
		if err != nil {
			return fmt.Errorf("resolve linked-worktree metadata %q: %w", path, err)
		}
		if physical != path || !pathStrictlyWithin(filepath.Join(source, ".git"), physical) {
			return fmt.Errorf("linked-worktree metadata %q escapes its physical boundary", path)
		}
		if err := fsidentity.Prime(info, filesystem.sameFile); err != nil {
			return fmt.Errorf("capture metadata identity %q: %w", path, err)
		}
		observations[path] = pathObservation{physical: physical, info: info}
		return nil
	}
	if err := capture(root, true); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return observations, nil
		}
		return nil, err
	}
	entries, err := filesystem.readDir(root)
	if err != nil {
		return nil, fmt.Errorf("read linked-worktree metadata %q: %w", root, err)
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := filesystem.lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect worktree metadata entry %q: %w", path, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("worktree metadata entry %q is a symbolic link", path)
		}
		if !info.IsDir() {
			continue
		}
		if err := capture(path, true); err != nil {
			return nil, err
		}
		if err := capture(filepath.Join(path, "gitdir"), false); err != nil {
			return nil, err
		}
	}
	return observations, nil
}
