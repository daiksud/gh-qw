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
	if roots.RepositoryRoot == "" || !filepath.IsAbs(roots.RepositoryRoot) {
		return RepositoryObservation{}, fmt.Errorf("%w: repository root %q is not an absolute path", ErrSafety, roots.RepositoryRoot)
	}
	expectedMain := filepath.Join(roots.RepositoryRoot, repository.Host, repository.Owner, repository.Repo)
	canonical := physicalpath.SamePath(repository.Path, expectedMain)
	boundary := filepath.Dir(repository.Path)
	if canonical {
		boundary = roots.RepositoryRoot
	}
	if !physicalpath.SamePath(repository.Root, boundary) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: repository root changed from %q to %q", ErrSafety, repository.Root, boundary,
		)
	}
	if roots.WorktreeRoot == "" || !filepath.IsAbs(roots.WorktreeRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: worktree root %q is not an absolute path",
			ErrSafety,
			roots.WorktreeRoot,
		)
	}
	if !physicalpath.SamePath(roots.WorktreeRoot, filepath.Join(roots.RepositoryRoot, "worktrees")) &&
		pathsOverlap(roots.RepositoryRoot, roots.WorktreeRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: repository root %q overlaps worktree root %q", ErrSafety, roots.RepositoryRoot, roots.WorktreeRoot,
		)
	}
	if !canonical && pathsOverlap(repository.Path, roots.WorktreeRoot) {
		return RepositoryObservation{}, fmt.Errorf(
			"%w: registered repository %q overlaps worktree root %q", ErrSafety, repository.Path, roots.WorktreeRoot,
		)
	}

	repositoryRoot, err := CaptureNode(observer.Filesystem,
		"",
		boundary,
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

	if !canonical {
		expectedMain = repository.Path
	}
	if canonical {
		derivedMain, err := local.MainPath(
			boundary,
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
		boundary,
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
	git gitcmd.Outputter,
	repositoryPath, gitPath string,
) error {
	topLevel, commonDir, err := gitcmd.RepositoryPaths(ctx, git, repositoryPath)
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
		!physicalpath.SamePath(before.Repository.Path, after.Repository.Path) ||
		!physicalpath.SamePath(before.Repository.Root, after.Repository.Root) {
		return fmt.Errorf("%w: selected repository or removal mode changed", ErrSafety)
	}
	if !physicalpath.SamePath(before.Roots.RepositoryRoot, after.Roots.RepositoryRoot) ||
		!physicalpath.SamePath(before.Roots.WorktreeRoot, after.Roots.WorktreeRoot) {
		return fmt.Errorf("%w: configured roots changed", ErrSafety)
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
