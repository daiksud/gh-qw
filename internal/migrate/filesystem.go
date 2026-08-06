package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

// Filesystem supplies the filesystem seams used by migration. Nil functions
// use their operating-system equivalents.
type Filesystem struct {
	ReadDir      func(string) ([]os.DirEntry, error)
	Lstat        func(string) (fs.FileInfo, error)
	Stat         func(string) (fs.FileInfo, error)
	ReadFile     func(string) ([]byte, error)
	WriteFile    func(string, []byte, fs.FileMode) error
	Readlink     func(string) (string, error)
	Symlink      func(string, string) error
	EvalSymlinks func(string) (string, error)
	SameFile     func(fs.FileInfo, fs.FileInfo) bool
	Rename       func(string, string) error
	Mkdir        func(string, fs.FileMode) error
	MkdirAll     func(string, fs.FileMode) error
	Chmod        func(string, fs.FileMode) error
	RemoveAll    func(string) error

	// CopyFile, when non-nil, copies one regular file and must create dst
	// without overwriting it. It is primarily useful for fault injection.
	CopyFile func(src, dst string, mode fs.FileMode) error
}

type filesystem struct {
	readDir      func(string) ([]os.DirEntry, error)
	lstat        func(string) (fs.FileInfo, error)
	stat         func(string) (fs.FileInfo, error)
	readFile     func(string) ([]byte, error)
	writeFile    func(string, []byte, fs.FileMode) error
	readlink     func(string) (string, error)
	symlink      func(string, string) error
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool
	rename       func(string, string) error
	mkdir        func(string, fs.FileMode) error
	mkdirAll     func(string, fs.FileMode) error
	chmod        func(string, fs.FileMode) error
	removeAll    func(string) error
	copyFile     func(src, dst string, mode fs.FileMode) error
}

func newFilesystem(options Filesystem) filesystem {
	result := filesystem{
		readDir:      options.ReadDir,
		lstat:        options.Lstat,
		stat:         options.Stat,
		readFile:     options.ReadFile,
		writeFile:    options.WriteFile,
		readlink:     options.Readlink,
		symlink:      options.Symlink,
		evalSymlinks: options.EvalSymlinks,
		sameFile:     options.SameFile,
		rename:       options.Rename,
		mkdir:        options.Mkdir,
		mkdirAll:     options.MkdirAll,
		chmod:        options.Chmod,
		removeAll:    options.RemoveAll,
		copyFile:     options.CopyFile,
	}
	if result.readDir == nil {
		result.readDir = os.ReadDir
	}
	if result.lstat == nil {
		result.lstat = os.Lstat
	}
	if result.stat == nil {
		result.stat = os.Stat
	}
	if result.readFile == nil {
		result.readFile = os.ReadFile
	}
	if result.writeFile == nil {
		result.writeFile = os.WriteFile
	}
	if result.readlink == nil {
		result.readlink = os.Readlink
	}
	if result.symlink == nil {
		result.symlink = os.Symlink
	}
	if result.evalSymlinks == nil {
		result.evalSymlinks = filepath.EvalSymlinks
	}
	if result.sameFile == nil {
		result.sameFile = os.SameFile
	}
	if result.rename == nil {
		result.rename = os.Rename
	}
	if result.mkdir == nil {
		result.mkdir = os.Mkdir
	}
	if result.mkdirAll == nil {
		result.mkdirAll = os.MkdirAll
	}
	if result.chmod == nil {
		result.chmod = os.Chmod
	}
	if result.removeAll == nil {
		result.removeAll = os.RemoveAll
	}
	return result
}

// LocalFilesystemOptions converts migration seams for local repository
// discovery.
func LocalFilesystemOptions(options Filesystem) local.FilesystemOptions {
	filesystem := newFilesystem(options)
	return local.FilesystemOptions{
		ReadDir:      filesystem.readDir,
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	}
}

func rootOptions(filesystem filesystem) rootpkg.Options {
	return rootpkg.Options{
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	}
}

func (f filesystem) physicalizeAbsolute(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}

	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := f.lstat(current)
		switch {
		case err == nil:
			physical, evalErr := f.evalSymlinks(current)
			if evalErr != nil {
				return "", fmt.Errorf("resolve symbolic links in %q: %w", current, evalErr)
			}
			if !filepath.IsAbs(physical) {
				return "", fmt.Errorf("resolved path %q is not absolute", physical)
			}
			parts := append([]string{filepath.Clean(physical)}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), nil
		case errors.Is(err, fs.ErrNotExist):
			parent := filepath.Dir(current)
			if parent == current {
				return "", fmt.Errorf("find existing path ancestor for %q: %w", path, err)
			}
			suffix = append([]string{filepath.Base(current)}, suffix...)
			current = parent
		default:
			return "", fmt.Errorf("lstat path %q: %w", current, err)
		}
	}
}

func (f filesystem) samePhysicalPath(first, second string) (bool, error) {
	firstPhysical, err := f.physicalizeAbsolute(first)
	if err != nil {
		return false, err
	}
	secondPhysical, err := f.physicalizeAbsolute(second)
	if err != nil {
		return false, err
	}
	if firstPhysical == secondPhysical ||
		(runtime.GOOS == "windows" && equalFoldPath(firstPhysical, secondPhysical)) {
		return true, nil
	}

	firstInfo, firstErr := f.stat(firstPhysical)
	secondInfo, secondErr := f.stat(secondPhysical)
	if firstErr == nil && secondErr == nil {
		return f.sameFile(firstInfo, secondInfo), nil
	}
	if firstErr != nil && !errors.Is(firstErr, fs.ErrNotExist) {
		return false, fmt.Errorf("stat path %q: %w", firstPhysical, firstErr)
	}
	if secondErr != nil && !errors.Is(secondErr, fs.ErrNotExist) {
		return false, fmt.Errorf("stat path %q: %w", secondPhysical, secondErr)
	}
	return false, nil
}

func pathStrictlyWithin(base, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(base), filepath.Clean(candidate))
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !hasParentPrefix(relative)
}

func pathsOverlap(first, second string) bool {
	return filepath.Clean(first) == filepath.Clean(second) ||
		pathStrictlyWithin(first, second) ||
		pathStrictlyWithin(second, first)
}

func hasParentPrefix(path string) bool {
	return len(path) > 2 &&
		path[:2] == ".." &&
		os.IsPathSeparator(path[2])
}

func equalFoldPath(first, second string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range len(first) {
		left := first[index]
		right := second[index]
		if left == right {
			continue
		}
		if left >= 'A' && left <= 'Z' {
			left += 'a' - 'A'
		}
		if right >= 'A' && right <= 'Z' {
			right += 'a' - 'A'
		}
		if left != right {
			return false
		}
	}
	return true
}

func preservedMode(mode fs.FileMode) fs.FileMode {
	return mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}
