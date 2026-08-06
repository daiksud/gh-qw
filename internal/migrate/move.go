package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/daiksud/gh-qw/internal/fsidentity"
)

// ErrDestinationExists identifies a destination collision detected
// immediately before movement.
var ErrDestinationExists = errors.New("migration destination already exists")

// MoveOptions supplies boundaries and filesystem seams for Move.
type MoveOptions struct {
	Filesystem      Filesystem
	SourceRoot      string
	DestinationRoot string
}

// Move renames source to destination, falling back to a lossless
// copy-and-remove operation for a cross-device rename.
func Move(source, destination string, options MoveOptions) error {
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return errors.New("move repository: source and destination must be absolute")
	}
	if options.DestinationRoot == "" {
		return errors.New("move repository: destination root is required")
	}
	if err := ValidateDisjoint(source, destination); err != nil {
		return err
	}
	if err := ValidateSourceContainment(options.SourceRoot, source, options.Filesystem); err != nil {
		return err
	}
	if err := ValidateTree(source, options.Filesystem); err != nil {
		return fmt.Errorf("preflight repository tree: %w", err)
	}

	filesystem := newFilesystem(options.Filesystem)
	sourceInfo, err := filesystem.lstat(source)
	if err != nil {
		return fmt.Errorf("inspect migration source %q: %w", source, err)
	}
	if sourceInfo.Mode()&fs.ModeSymlink != 0 || !sourceInfo.IsDir() {
		return fmt.Errorf("migration source %q is not a physical directory", source)
	}
	if err := fsidentity.Prime(sourceInfo, filesystem.sameFile); err != nil {
		return fmt.Errorf("capture migration source identity %q: %w", source, err)
	}

	physicalDestination, exists, err := CheckDestination(
		options.DestinationRoot,
		destination,
		options.Filesystem,
	)
	if err != nil {
		return err
	}
	if physicalDestination != destination {
		return fmt.Errorf(
			"migration destination changed from %q to %q",
			destination,
			physicalDestination,
		)
	}
	if exists {
		return fmt.Errorf("%w: %q", ErrDestinationExists, destination)
	}

	if err := filesystem.mkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create migration destination parent: %w", err)
	}

	currentSource, err := filesystem.lstat(source)
	if err != nil {
		return fmt.Errorf("revalidate migration source %q: %w", source, err)
	}
	if currentSource.Mode()&fs.ModeSymlink != 0 ||
		!currentSource.IsDir() ||
		!filesystem.sameFile(sourceInfo, currentSource) {
		return fmt.Errorf("revalidate migration source %q: source changed", source)
	}
	physicalDestination, exists, err = CheckDestination(
		options.DestinationRoot,
		destination,
		options.Filesystem,
	)
	if err != nil {
		return err
	}
	if physicalDestination != destination {
		return fmt.Errorf(
			"migration destination changed from %q to %q",
			destination,
			physicalDestination,
		)
	}
	if exists {
		return fmt.Errorf("%w: %q", ErrDestinationExists, destination)
	}

	destinationParent := filepath.Dir(destination)
	parentInfo, err := filesystem.lstat(destinationParent)
	if err != nil {
		return fmt.Errorf("inspect migration destination parent %q: %w", destinationParent, err)
	}
	if parentInfo.Mode()&fs.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("migration destination parent %q is not a physical directory", destinationParent)
	}
	if err := fsidentity.Prime(parentInfo, filesystem.sameFile); err != nil {
		return fmt.Errorf("capture migration destination parent identity %q: %w", destinationParent, err)
	}

	renameErr := filesystem.rename(source, destination)
	if renameErr == nil {
		return nil
	}
	if !isCrossDevice(renameErr) {
		return fmt.Errorf("rename repository: %w", renameErr)
	}

	if err := copyTree(
		filesystem,
		source,
		destination,
		copyDirectoryGuard{path: destinationParent, info: parentInfo},
		true,
	); err != nil {
		if errors.Is(err, ErrDestinationExists) {
			return err
		}
		cleanupErr := cleanupPartialDestination(filesystem, destination)
		if cleanupErr != nil {
			return fmt.Errorf("copy repository: %w; cleanup %q also failed: %v", err, destination, cleanupErr)
		}
		return fmt.Errorf("copy repository: %w", err)
	}

	currentSource, err = filesystem.lstat(source)
	if err != nil {
		return fmt.Errorf(
			"repository copied to %q but source could not be revalidated before removal: %w",
			destination,
			err,
		)
	}
	if currentSource.Mode()&fs.ModeSymlink != 0 ||
		!currentSource.IsDir() ||
		!filesystem.sameFile(sourceInfo, currentSource) {
		return fmt.Errorf(
			"repository copied to %q but source changed before removal; source was retained",
			destination,
		)
	}
	if err := filesystem.removeAll(source); err != nil {
		return fmt.Errorf(
			"repository copied to %q but remove exact source %q: %w",
			destination,
			source,
			err,
		)
	}
	return nil
}

type copyDirectoryGuard struct {
	path string
	info fs.FileInfo
}

func (guard copyDirectoryGuard) validate(filesystem filesystem) error {
	info, err := filesystem.lstat(guard.path)
	if err != nil {
		return fmt.Errorf("revalidate copy directory %q: %w", guard.path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 ||
		!info.IsDir() ||
		!filesystem.sameFile(guard.info, info) {
		return fmt.Errorf("revalidate copy directory %q: directory changed", guard.path)
	}
	return nil
}

func copyTree(
	filesystem filesystem,
	source, destination string,
	parent copyDirectoryGuard,
	root bool,
) error {
	if err := parent.validate(filesystem); err != nil {
		return err
	}
	if _, err := filesystem.lstat(destination); err == nil {
		if root {
			return fmt.Errorf("%w during copy: %q", ErrDestinationExists, destination)
		}
		return fmt.Errorf("copy destination unexpectedly exists: %q", destination)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect copy destination %q: %w", destination, err)
	}

	info, err := filesystem.lstat(source)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", source, err)
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		target, err := filesystem.readlink(source)
		if err != nil {
			return fmt.Errorf("read symbolic link %q: %w", source, err)
		}
		if err := filesystem.symlink(target, destination); err != nil {
			return fmt.Errorf("create symbolic link %q: %w", destination, err)
		}
		return nil
	case mode.IsRegular():
		if filesystem.copyFile != nil {
			if err := filesystem.copyFile(source, destination, preservedMode(mode)); err != nil {
				return err
			}
			copiedInfo, err := filesystem.lstat(destination)
			if err != nil {
				return fmt.Errorf("inspect copied file %q: %w", destination, err)
			}
			if copiedInfo.Mode()&fs.ModeSymlink != 0 || !copiedInfo.Mode().IsRegular() {
				return fmt.Errorf("copy hook did not create a regular file at %q", destination)
			}
			return nil
		}
		return copyRegularFile(filesystem, source, destination, preservedMode(mode))
	case mode.IsDir():
		createMode := preservedMode(mode) | 0o700
		if err := filesystem.mkdir(destination, createMode); err != nil {
			if root && errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%w during copy: %q", ErrDestinationExists, destination)
			}
			return fmt.Errorf("create directory %q: %w", destination, err)
		}
		entries, err := filesystem.readDir(source)
		if err != nil {
			return fmt.Errorf("read directory %q: %w", source, err)
		}
		destinationInfo, err := filesystem.lstat(destination)
		if err != nil {
			return fmt.Errorf("inspect copied directory %q: %w", destination, err)
		}
		if destinationInfo.Mode()&fs.ModeSymlink != 0 || !destinationInfo.IsDir() {
			return fmt.Errorf("copied directory %q is not a physical directory", destination)
		}
		if err := fsidentity.Prime(destinationInfo, filesystem.sameFile); err != nil {
			return fmt.Errorf("capture copied directory identity %q: %w", destination, err)
		}
		destinationGuard := copyDirectoryGuard{path: destination, info: destinationInfo}
		sort.Slice(entries, func(left, right int) bool {
			return entries[left].Name() < entries[right].Name()
		})
		for _, entry := range entries {
			name := entry.Name()
			if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
				return fmt.Errorf("unsafe directory entry name %q below %q", name, source)
			}
			if err := copyTree(
				filesystem,
				filepath.Join(source, name),
				filepath.Join(destination, name),
				destinationGuard,
				false,
			); err != nil {
				return err
			}
		}
		if err := destinationGuard.validate(filesystem); err != nil {
			return err
		}
		if err := filesystem.chmod(destination, preservedMode(mode)); err != nil {
			return fmt.Errorf("preserve directory mode for %q: %w", destination, err)
		}
		return nil
	default:
		return fmt.Errorf("refuse special filesystem object %q with mode %s", source, mode)
	}
}

func copyRegularFile(
	filesystem filesystem,
	source, destination string,
	mode fs.FileMode,
) (returnErr error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", source, err)
	}
	defer func() {
		if err := sourceFile.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close source file %q: %w", source, err)
		}
	}()

	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", destination, err)
	}
	defer func() {
		if err := destinationFile.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close destination file %q: %w", destination, err)
		}
	}()

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err := destinationFile.Sync(); err != nil {
		return fmt.Errorf("sync destination file %q: %w", destination, err)
	}
	if err := filesystem.chmod(destination, mode); err != nil {
		return fmt.Errorf("preserve file mode for %q: %w", destination, err)
	}
	return nil
}

func cleanupPartialDestination(filesystem filesystem, destination string) error {
	_, err := filesystem.lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return filesystem.removeAll(destination)
}

func isCrossDevice(err error) bool {
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	var linkError *os.LinkError
	return errors.As(err, &linkError) && errors.Is(linkError.Err, syscall.EXDEV)
}
