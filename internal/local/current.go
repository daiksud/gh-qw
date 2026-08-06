package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

// CurrentRepositoryOptions supplies the Git output and filesystem seams used
// to identify a repository from a directory.
type CurrentRepositoryOptions struct {
	Git        GitOutputter
	Filesystem FilesystemOptions
}

// FindCurrentRepository identifies the discovered main repository whose real
// .git directory is Git's common directory for cwd.
func FindCurrentRepository(
	ctx context.Context,
	cwd string,
	repositories []Repository,
	options ...CurrentRepositoryOptions,
) (Repository, error) {
	repository, _, err := findCurrentRepository(ctx, cwd, repositories, options...)
	return repository, err
}

// ResolveCurrentRepository is an alias for FindCurrentRepository.
func ResolveCurrentRepository(
	ctx context.Context,
	cwd string,
	repositories []Repository,
	options ...CurrentRepositoryOptions,
) (Repository, error) {
	return FindCurrentRepository(ctx, cwd, repositories, options...)
}

// CurrentOptions supplies the combined Git seam for current-worktree
// discovery.
type CurrentOptions struct {
	Git        Git
	Filesystem FilesystemOptions
}

// DiscoverCurrent identifies both the discovered main repository and the
// registered main or linked worktree containing cwd.
func DiscoverCurrent(
	ctx context.Context,
	cwd, worktreeRoot string,
	repositories []Repository,
	options ...CurrentOptions,
) (Current, error) {
	if len(options) > 1 {
		return Current{}, errors.New("discover current worktree: more than one options value")
	}
	var option CurrentOptions
	if len(options) == 1 {
		option = options[0]
	}
	git := option.Git
	if git == nil {
		git = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}

	repository, topLevel, err := findCurrentRepository(
		ctx,
		cwd,
		repositories,
		CurrentRepositoryOptions{
			Git:        git,
			Filesystem: option.Filesystem,
		},
	)
	if err != nil {
		return Current{}, err
	}
	worktrees, err := EnumerateWorktrees(ctx, repository, worktreeRoot, WorktreeOptions{
		Lister:     git,
		Filesystem: option.Filesystem,
	})
	if err != nil {
		return Current{}, err
	}

	filesystem := newFilesystem(option.Filesystem)
	matches := make([]Worktree, 0, 1)
	for _, worktree := range worktrees {
		match, compareErr := filesystem.samePhysicalPath(topLevel, worktree.Path)
		if compareErr != nil {
			return Current{}, &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       topLevel,
				Reason:     "cannot compare current top-level with registered worktrees",
				Err:        compareErr,
			}
		}
		if match {
			matches = append(matches, worktree)
		}
	}

	switch len(matches) {
	case 0:
		return Current{}, &WorktreeError{
			Kind:       ErrCurrentUnmanaged,
			Repository: repository.Identity,
			Path:       topLevel,
			Reason:     "Git top-level is not in the repository's worktree list",
		}
	case 1:
		return Current{Repository: repository, Worktree: matches[0]}, nil
	default:
		return Current{}, &WorktreeError{
			Kind:       ErrWorktreeAmbiguous,
			Repository: repository.Identity,
			Path:       topLevel,
			Reason:     "Git top-level matches multiple registered worktrees",
		}
	}
}

func findCurrentRepository(
	ctx context.Context,
	cwd string,
	repositories []Repository,
	options ...CurrentRepositoryOptions,
) (Repository, string, error) {
	if len(options) > 1 {
		return Repository{}, "", errors.New("find current repository: more than one options value")
	}
	var option CurrentRepositoryOptions
	if len(options) == 1 {
		option = options[0]
	}
	git := option.Git
	if git == nil {
		git = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}
	filesystem := newFilesystem(option.Filesystem)

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Repository{}, "", fmt.Errorf("get current directory: %w", err)
		}
	}
	if !filepath.IsAbs(cwd) {
		absolute, err := filepath.Abs(cwd)
		if err != nil {
			return Repository{}, "", fmt.Errorf("make current directory absolute: %w", err)
		}
		cwd = absolute
	}

	topLevel, commonDir, err := gitTopLevelAndCommon(ctx, git, cwd)
	if err != nil {
		return Repository{}, "", &WorktreeError{
			Kind:   ErrCurrentUnmanaged,
			Path:   cwd,
			Reason: "Git could not identify a worktree",
			Err:    err,
		}
	}
	physicalTop, err := filesystem.physicalizeAbsolute(topLevel)
	if err != nil {
		return Repository{}, "", &WorktreeError{
			Kind:   ErrUnsafeWorktree,
			Path:   topLevel,
			Reason: "cannot physicalize current Git top-level",
			Err:    err,
		}
	}

	matches := make([]Repository, 0, 1)
	for _, repository := range repositories {
		if err := ValidateRepository(repository); err != nil {
			return Repository{}, "", err
		}
		match, compareErr := filesystem.samePhysicalPath(
			commonDir,
			filepath.Join(repository.Path, ".git"),
		)
		if compareErr != nil {
			return Repository{}, "", &WorktreeError{
				Kind:       ErrUnsafeWorktree,
				Repository: repository.Identity,
				Path:       commonDir,
				Reason:     "cannot compare Git common directories",
				Err:        compareErr,
			}
		}
		if match {
			matches = append(matches, repository)
		}
	}

	switch len(matches) {
	case 0:
		return Repository{}, "", &WorktreeError{
			Kind:   ErrCurrentUnmanaged,
			Path:   physicalTop,
			Reason: "common Git directory does not belong to a discovered main repository",
		}
	case 1:
		return matches[0], physicalTop, nil
	default:
		return Repository{}, "", &SelectorError{
			Selector: "<current-directory>",
			Kind:     SelectorAmbiguous,
			Matches:  matches,
		}
	}
}
