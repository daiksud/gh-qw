package migrate

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
}

// PlanWorktreeRepair observes registered paths and validates their movement boundaries.
func PlanWorktreeRepair(ctx context.Context, source, destination string, git WorktreeLister, options Filesystem) (WorktreeRepairPlan, error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return WorktreeRepairPlan{}, errors.New("linked-worktree repair paths must be absolute")
	}
	plan := WorktreeRepairPlan{source: filepath.Clean(source), destination: filepath.Clean(destination)}
	filesystem := newFilesystem(options)
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
