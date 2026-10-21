package removal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/physicalpath"
)

// LinkedGit delegates linked-worktree mutation and registration observations to Git.
type LinkedGit interface {
	WorktreeRemove(context.Context, string, gitcmd.WorktreeRemoveOptions) error
	WorktreeList(context.Context, string) ([]gitcmd.Worktree, error)
}

// Step identifies the failed operation without choosing a command's stopping policy.
type Step uint8

const (
	RemoveGit Step = iota + 1
	VerifyPath
	ReadRegistrations
	VerifyRegistration
	CleanupParents
)

// Result distinguishes exact-path removal from successful registration and cleanup.
// PathRemoved can be true even when Git failed after deleting the target.
type Result struct {
	Path        string
	PathRemoved bool
	FailedStep  Step
	Err         error
}

// Executor owns the common linked removal postconditions and contained cleanup.
// Callers retain selection, immediate pre-mutation revalidation, and reporting.
type Executor struct {
	Git        LinkedGit
	Filesystem Filesystem
	ReadDir    func(string) ([]os.DirEntry, error)
	Remove     func(string) error
}

// RemoveLinked must follow the caller's revalidation without intervening output,
// discovery, or mutation. It never unlocks a target or increases Git's force level.
func (executor Executor) RemoveLinked(
	ctx context.Context,
	repository string,
	options gitcmd.WorktreeRemoveOptions,
	boundary Node,
	parents []Node,
) Result {
	result := Result{Path: options.Path}
	fail := func(step Step, err error) Result {
		result.FailedStep, result.Err = step, err
		return result
	}
	if err := ctx.Err(); err != nil {
		return fail(RemoveGit, err)
	}
	removeErr := executor.Git.WorktreeRemove(ctx, repository, options)
	missing, err := PathMissing(executor.Filesystem, options.Path)
	result.PathRemoved = missing
	if removeErr != nil {
		if err != nil {
			err = fmt.Errorf("observe exact target state at %q: %w", options.Path, err)
		}
		return fail(RemoveGit, errors.Join(removeErr, err))
	}
	if err != nil {
		return fail(VerifyPath, err)
	}
	if !missing {
		return fail(VerifyPath, fmt.Errorf("Git returned success but exact path %q still exists", outputPath(options.Path)))
	}
	if err := ctx.Err(); err != nil {
		return fail(ReadRegistrations, err)
	}
	// Mutation ends every earlier inventory phase. Always ask Git again.
	registered, err := executor.Git.WorktreeList(ctx, repository)
	if err != nil {
		return fail(ReadRegistrations, err)
	}
	for _, worktree := range registered {
		if physicalpath.SamePath(worktree.Path, options.Path) {
			return fail(VerifyRegistration, fmt.Errorf("Git still registers exact path %q", outputPath(options.Path)))
		}
	}
	if err := executor.CleanupEmptyChain(ctx, boundary, parents); err != nil {
		return fail(CleanupParents, err)
	}
	return result
}

// PathMissing observes the exact path without following a replacement symlink.
func PathMissing(filesystem Filesystem, path string) (bool, error) {
	_, err := filesystem.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

var (
	// ErrCleanupBoundary marks an unsafe shared boundary, never a candidate-only failure.
	ErrCleanupBoundary = errors.New("cleanup boundary changed")
	// ErrCleanupParentShape means cleanup stopped at a symlink or non-directory parent.
	// Single-target removal treats this as a successful stop; bulk commands report it.
	ErrCleanupParentShape = errors.New("cleanup parent is no longer a real directory")
)

// CleanupEmptyChain removes only previously observed empty parents. The boundary
// is never deleted, and each deletion retains both physical-path and identity checks.
func (executor Executor) CleanupEmptyChain(ctx context.Context, boundary Node, chain []Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := RevalidateNode(executor.Filesystem, boundary, "cleanup boundary"); err != nil {
		return errors.Join(ErrCleanupBoundary, err)
	}
	for index, planned := range chain {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index != 0 {
			if err := RevalidateNode(executor.Filesystem, boundary, "cleanup boundary"); err != nil {
				return errors.Join(ErrCleanupBoundary, err)
			}
		}
		if !pathStrictlyWithin(boundary.Path, planned.Path) {
			return fmt.Errorf("cleanup directory %q escaped boundary %q", planned.Path, boundary.Path)
		}
		current, err := executor.captureCleanupParent(boundary, planned)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect cleanup directory %q: %w", planned.Path, err)
		}
		if err := CompareNode(executor.Filesystem, planned, current, "cleanup directory"); err != nil {
			return err
		}
		entries, err := executor.ReadDir(planned.Path)
		if err != nil {
			return fmt.Errorf("read cleanup directory %q: %w", planned.Path, err)
		}
		if len(entries) != 0 {
			return nil
		}
		revalidated, err := executor.captureCleanupParent(boundary, planned)
		if err != nil {
			return fmt.Errorf("reinspect cleanup directory %q: %w", planned.Path, err)
		}
		if err := CompareNode(executor.Filesystem, current, revalidated, "cleanup directory"); err != nil {
			return err
		}
		if err := RevalidateNode(executor.Filesystem, boundary, "cleanup boundary"); err != nil {
			return errors.Join(ErrCleanupBoundary, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := executor.Remove(planned.Path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
				return nil
			}
			return fmt.Errorf("remove empty cleanup directory %q: %w", planned.Path, err)
		}
	}
	return ctx.Err()
}

func (executor Executor) captureCleanupParent(boundary, parent Node) (Node, error) {
	node, err := CaptureNode(executor.Filesystem, boundary.Path, parent.Path, Directory, true)
	if errors.Is(err, errSymlink) || errors.Is(err, errNotDirectory) {
		return Node{}, errors.Join(ErrCleanupParentShape, err)
	}
	return node, err
}
