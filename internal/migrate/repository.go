package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

var (
	// ErrNotRepository identifies a directory that is not an ordinary Git
	// repository main worktree.
	ErrNotRepository = errors.New("not an ordinary Git repository")
	// ErrBareRepository identifies a bare Git repository.
	ErrBareRepository = errors.New("bare Git repository")
	// ErrLinkedRepository identifies a linked worktree or submodule.
	ErrLinkedRepository = errors.New("linked Git worktree or submodule")
)

// RepositoryGit is the read-only Git seam used to inspect one repository.
type RepositoryGit interface {
	OutputDir(context.Context, string, ...string) ([]byte, error)
}

// RepositorySnapshot records the physical repository objects checked during
// planning so they can be compared again immediately before movement.
type RepositorySnapshot struct {
	Path   string
	GitDir string

	pathInfo fs.FileInfo
	gitInfo  fs.FileInfo
}

// InspectRepository verifies that path is an ordinary main Git worktree.
func InspectRepository(
	ctx context.Context,
	path string,
	git RepositoryGit,
	options Filesystem,
) (RepositorySnapshot, error) {
	if git == nil {
		return RepositorySnapshot{}, errors.New("inspect repository: nil Git runner")
	}
	filesystem := newFilesystem(options)

	physicalPath, err := filesystem.physicalizeAbsolute(path)
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("inspect repository %q: %w", path, err)
	}
	pathInfo, err := filesystem.lstat(physicalPath)
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("inspect repository %q: %w", physicalPath, err)
	}
	if pathInfo.Mode()&fs.ModeSymlink != 0 || !pathInfo.IsDir() {
		return RepositorySnapshot{}, fmt.Errorf("%w: %q is not a physical directory", ErrNotRepository, physicalPath)
	}
	if err := fsidentity.Prime(pathInfo, filesystem.sameFile); err != nil {
		return RepositorySnapshot{}, fmt.Errorf("capture repository identity at %q: %w", physicalPath, err)
	}

	gitPath := filepath.Join(physicalPath, ".git")
	gitInfo, err := filesystem.lstat(gitPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			bareOutput, bareErr := git.OutputDir(
				ctx,
				physicalPath,
				"--git-dir=.",
				"rev-parse",
				"--is-bare-repository",
			)
			if bareErr == nil {
				bare, parseErr := parseGitBoolean(bareOutput, "bare-repository state")
				if parseErr != nil {
					return RepositorySnapshot{}, fmt.Errorf("inspect repository %q: %w", physicalPath, parseErr)
				}
				if bare {
					return RepositorySnapshot{}, fmt.Errorf("%w: %q", ErrBareRepository, physicalPath)
				}
			}
			return RepositorySnapshot{}, fmt.Errorf("%w: %q has no .git directory", ErrNotRepository, physicalPath)
		}
		return RepositorySnapshot{}, fmt.Errorf("inspect .git at %q: %w", gitPath, err)
	}
	if gitInfo.Mode()&fs.ModeSymlink != 0 {
		return RepositorySnapshot{}, fmt.Errorf("%w: %q is a symbolic link", ErrLinkedRepository, gitPath)
	}
	if !gitInfo.IsDir() {
		return RepositorySnapshot{}, fmt.Errorf("%w: %q is a .git pointer", ErrLinkedRepository, physicalPath)
	}
	if err := fsidentity.Prime(gitInfo, filesystem.sameFile); err != nil {
		return RepositorySnapshot{}, fmt.Errorf("capture Git directory identity at %q: %w", gitPath, err)
	}

	bareOutput, err := git.OutputDir(ctx, physicalPath, "rev-parse", "--is-bare-repository")
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("%w at %q: %v", ErrNotRepository, physicalPath, err)
	}
	bare, err := parseGitBoolean(bareOutput, "bare-repository state")
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("inspect repository %q: %w", physicalPath, err)
	}
	if bare {
		return RepositorySnapshot{}, fmt.Errorf("%w: %q", ErrBareRepository, physicalPath)
	}

	topOutput, err := git.OutputDir(ctx, physicalPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("find Git top-level for %q: %w", physicalPath, err)
	}
	topLevel, err := parseGitPath(topOutput, physicalPath, "top-level")
	if err != nil {
		return RepositorySnapshot{}, err
	}
	commonOutput, err := git.OutputDir(ctx, physicalPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("find common Git directory for %q: %w", physicalPath, err)
	}
	commonDir, err := parseGitPath(commonOutput, physicalPath, "common Git directory")
	if err != nil {
		return RepositorySnapshot{}, err
	}

	topMatches, err := filesystem.samePhysicalPath(topLevel, physicalPath)
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("compare Git top-level: %w", err)
	}
	commonMatches, err := filesystem.samePhysicalPath(commonDir, gitPath)
	if err != nil {
		return RepositorySnapshot{}, fmt.Errorf("compare common Git directory: %w", err)
	}
	if !topMatches || !commonMatches {
		return RepositorySnapshot{}, fmt.Errorf(
			"%w: Git top-level or common directory does not match %q",
			ErrLinkedRepository,
			physicalPath,
		)
	}

	return RepositorySnapshot{
		Path:     physicalPath,
		GitDir:   gitPath,
		pathInfo: pathInfo,
		gitInfo:  gitInfo,
	}, nil
}

// Revalidate repeats repository inspection and verifies that the planned
// source and .git directory are still the same filesystem objects.
func (snapshot RepositorySnapshot) Revalidate(
	ctx context.Context,
	git RepositoryGit,
	options Filesystem,
) error {
	if snapshot.Path == "" || snapshot.GitDir == "" ||
		snapshot.pathInfo == nil || snapshot.gitInfo == nil {
		return errors.New("revalidate repository: incomplete snapshot")
	}
	current, err := InspectRepository(ctx, snapshot.Path, git, options)
	if err != nil {
		return fmt.Errorf("revalidate repository %q: %w", snapshot.Path, err)
	}
	filesystem := newFilesystem(options)
	if current.Path != snapshot.Path ||
		!filesystem.sameFile(snapshot.pathInfo, current.pathInfo) ||
		!filesystem.sameFile(snapshot.gitInfo, current.gitInfo) {
		return fmt.Errorf("revalidate repository %q: source changed after planning", snapshot.Path)
	}
	return nil
}

// ValidateTree rejects filesystem objects that cannot be copied losslessly by
// the cross-device fallback.
func ValidateTree(path string, options Filesystem) error {
	filesystem := newFilesystem(options)
	return validateTree(filesystem, filepath.Clean(path))
}

func validateTree(filesystem filesystem, path string) error {
	info, err := filesystem.lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		return nil
	case mode.IsRegular():
		return nil
	case mode.IsDir():
		entries, err := filesystem.readDir(path)
		if err != nil {
			return fmt.Errorf("read directory %q: %w", path, err)
		}
		sort.Slice(entries, func(left, right int) bool {
			return entries[left].Name() < entries[right].Name()
		})
		for _, entry := range entries {
			if err := validateTree(filesystem, filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("refuse special filesystem object %q with mode %s", path, mode)
	}
}

// DestinationPath derives and physicalizes a canonical main-repository path
// beneath primaryRoot.
func DestinationPath(
	primaryRoot, host, owner, repo string,
	options Filesystem,
) (string, error) {
	if _, err := local.CanonicalIdentity(host, owner, repo); err != nil {
		return "", err
	}
	target := filepath.Join(primaryRoot, host, owner, repo)
	filesystem := newFilesystem(options)
	if _, err := filesystem.lstat(target); err == nil {
		return filepath.Clean(target), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect migration destination %q: %w", target, err)
	}
	physical, err := rootpkg.NewResolverWithOptions(rootOptions(filesystem)).
		PhysicalizeTarget(primaryRoot, target, rootpkg.StrictlyUnder)
	if err != nil {
		return "", fmt.Errorf("derive migration destination: %w", err)
	}
	return physical, nil
}

// CheckDestination re-physicalizes destination against its cached root and
// reports whether any filesystem object already occupies it.
func CheckDestination(
	primaryRoot, destination string,
	options Filesystem,
) (physical string, exists bool, err error) {
	filesystem := newFilesystem(options)
	primaryRoot = filepath.Clean(primaryRoot)
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(primaryRoot) ||
		!filepath.IsAbs(destination) ||
		!pathStrictlyWithin(primaryRoot, destination) {
		return "", false, fmt.Errorf(
			"validate migration destination: %q is not strictly below root %q",
			destination,
			primaryRoot,
		)
	}
	_, err = filesystem.lstat(destination)
	switch {
	case err == nil:
		return destination, true, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("inspect migration destination %q: %w", destination, err)
	}
	physical, err = rootpkg.NewResolverWithOptions(rootOptions(filesystem)).
		PhysicalizeTarget(primaryRoot, destination, rootpkg.StrictlyUnder)
	if err != nil {
		return "", false, fmt.Errorf("validate migration destination: %w", err)
	}
	_, err = filesystem.lstat(physical)
	switch {
	case err == nil:
		return physical, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return physical, false, nil
	default:
		return "", false, fmt.Errorf("inspect migration destination %q: %w", physical, err)
	}
}

// ValidateSourceContainment verifies that a bulk source remains below its
// physical legacy root.
func ValidateSourceContainment(
	sourceRoot, source string,
	options Filesystem,
) error {
	if sourceRoot == "" {
		return nil
	}
	filesystem := newFilesystem(options)
	physical, err := rootpkg.NewResolverWithOptions(rootOptions(filesystem)).
		PhysicalizeTarget(sourceRoot, source, rootpkg.StrictlyUnder)
	if err != nil {
		return fmt.Errorf("validate migration source containment: %w", err)
	}
	if physical != filepath.Clean(source) {
		return fmt.Errorf(
			"validate migration source containment: physical source changed from %q to %q",
			source,
			physical,
		)
	}
	return nil
}

// ValidateDisjoint rejects moves whose source and destination overlap.
func ValidateDisjoint(source, destination string) error {
	if pathsOverlap(source, destination) {
		return fmt.Errorf(
			"unsafe migration paths: source %q and destination %q overlap",
			source,
			destination,
		)
	}
	return nil
}

func parseGitBoolean(output []byte, description string) (bool, error) {
	value, err := parseGitLine(output, description)
	if err != nil {
		return false, err
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("Git returned invalid %s %q", description, value)
	}
}

func parseGitPath(output []byte, relativeTo, description string) (string, error) {
	value, err := parseGitLine(output, description)
	if err != nil {
		return "", err
	}
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(relativeTo, path)
	}
	return filepath.Clean(path), nil
}

func parseGitLine(output []byte, description string) (string, error) {
	output = bytes.TrimSuffix(output, []byte{'\n'})
	output = bytes.TrimSuffix(output, []byte{'\r'})
	if len(output) == 0 {
		return "", fmt.Errorf("Git returned an empty %s", description)
	}
	if bytes.IndexByte(output, 0) >= 0 ||
		bytes.IndexByte(output, '\n') >= 0 ||
		bytes.IndexByte(output, '\r') >= 0 {
		return "", fmt.Errorf("Git returned a non-single-line %s", description)
	}
	return string(output), nil
}

func normalizePath(path string) string {
	return strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
}
