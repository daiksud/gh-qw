package gitcmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// TrackingMode controls upstream tracking for a newly created branch.
type TrackingMode uint8

const (
	TrackingDefault TrackingMode = iota
	TrackingEnabled
	TrackingDisabled
)

// WorktreeAddOptions controls git worktree add.
type WorktreeAddOptions struct {
	Path        string
	Commitish   string
	NewBranch   string
	ResetBranch string
	Detach      bool
	Orphan      bool
	Force       bool
	Tracking    TrackingMode
}

// WorktreeAdd creates a linked worktree from the repository in dir.
func (r *Runner) WorktreeAdd(ctx context.Context, dir string, options WorktreeAddOptions) error {
	args, err := worktreeAddArgs(options)
	if err != nil {
		return err
	}
	return r.RunDir(ctx, dir, args...)
}

func worktreeAddArgs(options WorktreeAddOptions) ([]string, error) {
	if options.Path == "" {
		return nil, fmt.Errorf("git worktree add: path is required")
	}
	if options.NewBranch != "" && options.ResetBranch != "" {
		return nil, fmt.Errorf("git worktree add: -b and -B are mutually exclusive")
	}
	if options.Detach && (options.NewBranch != "" || options.ResetBranch != "" || options.Orphan) {
		return nil, fmt.Errorf("git worktree add: detach cannot be combined with branch creation or orphan mode")
	}
	if options.Orphan && options.Commitish != "" {
		return nil, fmt.Errorf("git worktree add: orphan mode cannot use a commit-ish")
	}
	switch options.Tracking {
	case TrackingDefault, TrackingEnabled, TrackingDisabled:
	default:
		return nil, fmt.Errorf("git worktree add: unsupported tracking mode")
	}
	if options.Detach && options.Tracking != TrackingDefault {
		return nil, fmt.Errorf("git worktree add: detached worktrees cannot configure tracking")
	}

	args := []string{"worktree", "add"}
	if options.Force {
		args = append(args, "--force")
	}
	if options.Detach {
		args = append(args, "--detach")
	}
	if options.Orphan {
		args = append(args, "--orphan")
	}
	if options.NewBranch != "" {
		args = append(args, "-b", options.NewBranch)
	}
	if options.ResetBranch != "" {
		args = append(args, "-B", options.ResetBranch)
	}
	switch options.Tracking {
	case TrackingEnabled:
		args = append(args, "--track")
	case TrackingDisabled:
		args = append(args, "--no-track")
	}
	args = append(args, "--", options.Path)
	if options.Commitish != "" {
		args = append(args, options.Commitish)
	}
	return args, nil
}

// Worktree is Git worktree state normalized from porcelain output. Branch is
// the local branch name without the refs/heads/ prefix.
type Worktree struct {
	Path           string
	HEAD           string
	Branch         string
	Detached       bool
	Bare           bool
	Locked         bool
	LockedReason   string
	Prunable       bool
	PrunableReason string
}

// WorktreeList returns all worktrees registered with the repository in dir.
func (r *Runner) WorktreeList(ctx context.Context, dir string) ([]Worktree, error) {
	output, err := r.OutputDir(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}

	worktrees, err := parseWorktreeList(output)
	if err != nil {
		return nil, fmt.Errorf("parse git worktree list output: %w", err)
	}
	return worktrees, nil
}

func parseWorktreeList(output []byte) ([]Worktree, error) {
	var (
		worktrees []Worktree
		current   *Worktree
		record    int
	)

	flush := func() error {
		if current == nil {
			return nil
		}
		if current.Path == "" {
			return fmt.Errorf("record %d has no worktree path", record)
		}
		if current.Detached && current.Branch != "" {
			return fmt.Errorf("record %d is both branched and detached", record)
		}
		worktrees = append(worktrees, *current)
		current = nil
		return nil
	}

	for _, rawField := range bytes.Split(output, []byte{0}) {
		if len(rawField) == 0 {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}

		field := string(rawField)
		key, value, hasValue := strings.Cut(field, " ")
		if key == "worktree" {
			if !hasValue || value == "" {
				return nil, fmt.Errorf("record %d has an empty worktree path", record+1)
			}
			if err := flush(); err != nil {
				return nil, err
			}
			record++
			current = &Worktree{Path: value}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("field before worktree record")
		}

		switch key {
		case "HEAD":
			if !hasValue || value == "" {
				return nil, fmt.Errorf("record %d has an empty HEAD", record)
			}
			current.HEAD = value
		case "branch":
			if !hasValue || value == "" {
				return nil, fmt.Errorf("record %d has an empty branch", record)
			}
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "locked":
			current.Locked = true
			if hasValue {
				current.LockedReason = value
			}
		case "prunable":
			current.Prunable = true
			if hasValue {
				current.PrunableReason = value
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return worktrees, nil
}

// WorktreeRemoveOptions controls git worktree remove.
type WorktreeRemoveOptions struct {
	Path  string
	Force bool
}

// WorktreeRemove removes a linked worktree registered with the repository in
// dir.
func (r *Runner) WorktreeRemove(ctx context.Context, dir string, options WorktreeRemoveOptions) error {
	if options.Path == "" {
		return fmt.Errorf("git worktree remove: path is required")
	}

	args := []string{"worktree", "remove"}
	if options.Force {
		args = append(args, "--force")
	}
	args = append(args, "--", options.Path)
	return r.RunDir(ctx, dir, args...)
}

// WorktreePruneOptions controls git worktree prune.
type WorktreePruneOptions struct {
	DryRun  bool
	Verbose bool
	Expire  string
}

// WorktreePrune removes stale administrative worktree records.
func (r *Runner) WorktreePrune(ctx context.Context, dir string, options WorktreePruneOptions) error {
	args := []string{"worktree", "prune"}
	if options.DryRun {
		args = append(args, "--dry-run")
	}
	if options.Verbose {
		args = append(args, "--verbose")
	}
	if options.Expire != "" {
		args = append(args, "--expire", options.Expire)
	}
	return r.RunDir(ctx, dir, args...)
}

// WorktreeRepair repairs administrative links after worktrees or their main
// repository have moved.
func (r *Runner) WorktreeRepair(ctx context.Context, dir string, paths ...string) error {
	args := []string{"worktree", "repair"}
	if len(paths) != 0 {
		args = append(args, "--")
		for _, path := range paths {
			if path == "" {
				return fmt.Errorf("git worktree repair: paths cannot be empty")
			}
			args = append(args, path)
		}
	}
	return r.RunDir(ctx, dir, args...)
}
