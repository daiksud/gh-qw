package removal

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

// Observer holds read-only dependencies for a single observation phase.
type Observer struct {
	Git                 local.Git
	Filesystem          Filesystem
	LocalFilesystem     local.FilesystemOptions
	ResolveManaged      func(context.Context, local.Repository, string, string, ...local.ManagedWorktreeOptions) (local.Worktree, error)
	ValidateAssociation func(context.Context, local.Repository, local.Worktree, string, ...local.AssociationOptions) error
}

// RepositoryObservation captures managed repository boundaries, without selection policy.
type RepositoryObservation struct {
	Roots          rootpkg.Result
	Repository     local.Repository
	RepositoryRoot Node
	MainDirectory  Node
	MainGit        Node
	WorktreeRoot   Node
	WorktreeBase   string
}

func pathsOverlap(first, second string) bool {
	return physicalpath.SamePath(first, second) || pathStrictlyWithin(first, second) || pathStrictlyWithin(second, first)
}

// ObserveRepository captures repository layout and main Git association.
func (observer Observer) ObserveRepository(
	ctx context.Context,
	roots rootpkg.Result,
	repository local.Repository,
) (RepositoryObservation, error) {
	if err := local.ValidateRepository(repository); err != nil {
		return RepositoryObservation{}, fmt.Errorf("%w: validate selected repository: %v", ErrSafety, err)
	}
	external := repository.RootIndex == len(roots.RepositoryRoots)
	if repository.RootIndex > len(roots.RepositoryRoots) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: repository %q has root index %d outside %d configured roots",
			ErrSafety,
			repository.Identity,
			repository.RootIndex,
			len(roots.RepositoryRoots),
		)
	}
	configuredRoot := repository.Root
	if !external {
		configuredRoot = roots.RepositoryRoots[repository.RootIndex]
	}
	if !physicalpath.SamePath(repository.Root, configuredRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: repository root changed from %q to %q",
			ErrSafety,
			repository.Root,
			configuredRoot,
		)
	}
	if roots.WorktreeRoot == "" || !filepath.IsAbs(roots.WorktreeRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: worktree root %q is not an absolute path",
			ErrSafety,
			roots.WorktreeRoot,
		)
	}
	for index, repositoryRoot := range roots.RepositoryRoots {
		if index == 0 && physicalpath.SamePath(roots.WorktreeRoot, filepath.Join(repositoryRoot, "worktrees")) {
			continue
		}
		if pathsOverlap(repositoryRoot, roots.WorktreeRoot) {
			return RepositoryObservation{}, fmt.Errorf(
				"%w: repository root %q overlaps worktree root %q",
				ErrSafety,
				repositoryRoot,
				roots.WorktreeRoot,
			)
		}
	}
	if external && pathsOverlap(repository.Path, roots.WorktreeRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: registered repository %q overlaps worktree root %q",
			ErrSafety,
			repository.Path,
			roots.WorktreeRoot,
		)
	}

	repositoryRoot, err := CaptureNode(observer.Filesystem,
		"",
		configuredRoot,
		Directory,
		false,
	)
	if err != nil {
		return RepositoryObservation{}, fmt.Errorf("inspect repository root: %w", err)
	}
	worktreeRoot, err := CaptureNode(observer.Filesystem,
		"",
		roots.WorktreeRoot,
		Directory,
		false,
	)
	if err != nil {
		return RepositoryObservation{}, fmt.Errorf("inspect worktree root: %w", err)
	}

	expectedMain := repository.Path
	if !external {
		expectedMain = filepath.Join(
			configuredRoot,
			repository.Host,
			repository.Owner,
			repository.Repo,
		)
	}
	if !physicalpath.SamePath(repository.Path, expectedMain) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: repository path %q is not the exact expected path %q",
			ErrSafety,
			repository.Path,
			expectedMain,
		)
	}
	if !external {
		derivedMain, err := local.MainPath(
			configuredRoot,
			repository.Host,
			repository.Owner,
			repository.Repo,
		)
		if err != nil {
			return RepositoryObservation{}, fmt.Errorf("derive main repository path: %w", err)
		}
		if !physicalpath.SamePath(derivedMain, expectedMain) {
			return RepositoryObservation{}, fmt.Errorf(
				"%w: expected repository path %q resolves to %q",
				ErrSafety,
				expectedMain,
				derivedMain,
			)
		}
	}

	mainDirectory, err := CaptureNode(observer.Filesystem,
		configuredRoot,
		expectedMain,
		Directory,
		true,
	)
	if err != nil {
		return RepositoryObservation{}, fmt.Errorf("inspect main repository: %w", err)
	}
	mainGit, err := CaptureNode(observer.Filesystem,
		expectedMain,
		filepath.Join(expectedMain, ".git"),
		Directory,
		true,
	)
	if err != nil {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: main repository .git is not a real contained directory: %v",
			ErrSafety,
			err,
		)
	}
	if err := validateMainGitAssociation(
		ctx,
		observer.Git,
		expectedMain,
		mainGit.Path,
	); err != nil {
		return RepositoryObservation{}, fmt.Errorf("%w: %v", ErrSafety, err)
	}

	worktreeBase, err := expectedWorktreeBase(roots.WorktreeRoot, repository)
	if err != nil {
		return RepositoryObservation{}, err
	}

	return RepositoryObservation{
		Roots:          roots,
		Repository:     repository,
		RepositoryRoot: repositoryRoot,
		MainDirectory:  mainDirectory,
		MainGit:        mainGit,
		WorktreeRoot:   worktreeRoot,
		WorktreeBase:   worktreeBase,
	}, nil
}

// validateMainGitAssociation asks Git for the main top-level and common directory.
func validateMainGitAssociation(
	ctx context.Context,
	git local.GitOutputter,
	repositoryPath, gitPath string,
) error {
	topOutput, err := git.OutputDir(ctx, repositoryPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("inspect main Git top-level: %w", err)
	}
	topLevel, err := parseGitPath(topOutput, repositoryPath, "top-level")
	if err != nil {
		return err
	}
	commonOutput, err := git.OutputDir(ctx, repositoryPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("inspect main Git common directory: %w", err)
	}
	commonDir, err := parseGitPath(
		commonOutput,
		repositoryPath,
		"common Git directory",
	)
	if err != nil {
		return err
	}
	if !physicalpath.SamePath(topLevel, repositoryPath) ||
		!physicalpath.SamePath(commonDir, gitPath) {
		return fmt.Errorf(
			"main Git association changed: top-level %q, common directory %q",
			topLevel,
			commonDir,
		)
	}
	return nil
}

func parseGitPath(
	output []byte,
	relativeTo, description string,
) (string, error) {
	path, err := gitcmd.ParseLine(output, description)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path), nil
}

// expectedWorktreeBase validates the deterministic managed worktree layout.
func expectedWorktreeBase(worktreeRoot string, repository local.Repository) (string, error) {
	derived, err := local.WorktreeBasePath(worktreeRoot, repository.Host, repository.Owner, repository.Repo)
	if err != nil {
		return "", fmt.Errorf("derive worktree base: %w", err)
	}
	if !physicalpath.SamePath(derived, worktreeRoot) {
		return "", fmt.Errorf("%w: worktree base %q redirects to %q", ErrSafety, worktreeRoot, derived)
	}
	return worktreeRoot, nil
}

// CompareRepository compares established roots and repository identities.
func CompareRepository(filesystem Filesystem, before, after RepositoryObservation) error {
	if before.Repository.Identity != after.Repository.Identity ||
		before.Repository.RootIndex != after.Repository.RootIndex ||
		!physicalpath.SamePath(before.Repository.Path, after.Repository.Path) ||
		!physicalpath.SamePath(before.Repository.Root, after.Repository.Root) {
		return fmt.Errorf("%w: selected repository or removal mode changed", ErrSafety)
	}
	if len(before.Roots.RepositoryRoots) != len(after.Roots.RepositoryRoots) ||
		!physicalpath.SamePath(before.Roots.WorktreeRoot, after.Roots.WorktreeRoot) {
		return fmt.Errorf("%w: configured roots changed", ErrSafety)
	}
	for index := range before.Roots.RepositoryRoots {
		if !physicalpath.SamePath(
			before.Roots.RepositoryRoots[index],
			after.Roots.RepositoryRoots[index],
		) {
			return fmt.Errorf("%w: configured roots changed", ErrSafety)
		}
	}

	for _, comparison := range []struct {
		name   string
		before Node
		after  Node
	}{
		{"repository root", before.RepositoryRoot, after.RepositoryRoot},
		{"main repository", before.MainDirectory, after.MainDirectory},
		{"main .git", before.MainGit, after.MainGit},
		{"worktree root", before.WorktreeRoot, after.WorktreeRoot},
	} {
		if err := CompareNode(filesystem,
			comparison.before,
			comparison.after,
			comparison.name,
		); err != nil {
			return err
		}
	}

	if !physicalpath.SamePath(before.WorktreeBase, after.WorktreeBase) {
		return fmt.Errorf("%w: worktree base changed", ErrSafety)
	}
	return nil
}

// RevalidateRepository checks the previously established boundary identities and main Git association.
// A captured worktree base is checked only when present in the caller's plan.
func (observer Observer) RevalidateRepository(ctx context.Context, snapshot RepositoryObservation, base *Node) error {
	for _, item := range []struct {
		name string
		node Node
	}{
		{"repository root", snapshot.RepositoryRoot}, {"main repository", snapshot.MainDirectory},
		{"main .git", snapshot.MainGit}, {"worktree root", snapshot.WorktreeRoot},
	} {
		if err := RevalidateNode(observer.Filesystem, item.node, item.name); err != nil {
			return err
		}
	}
	if base != nil {
		if err := RevalidateNode(observer.Filesystem, *base, "worktree base"); err != nil {
			return err
		}
	}
	if err := validateMainGitAssociation(ctx, observer.Git, snapshot.MainDirectory.Path, snapshot.MainGit.Path); err != nil {
		return fmt.Errorf("%w: %v", ErrSafety, err)
	}
	return nil
}

// ResolveWorktree delegates managed association validation to the local Git-backed resolver.
func (observer Observer) ResolveWorktree(ctx context.Context, repository local.Repository, root, slot string) (local.Worktree, error) {
	return observer.ResolveManaged(ctx, repository, root, slot, local.ManagedWorktreeOptions{Git: observer.Git, Filesystem: observer.LocalFilesystem})
}
