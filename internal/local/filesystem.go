package local

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// FilesystemOptions supplies read-only filesystem seams.
type FilesystemOptions struct {
	ReadDir      func(string) ([]os.DirEntry, error)
	Lstat        func(string) (fs.FileInfo, error)
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	SameFile     func(fs.FileInfo, fs.FileInfo) bool
}

type filesystem struct {
	readDir      func(string) ([]os.DirEntry, error)
	lstat        func(string) (fs.FileInfo, error)
	stat         func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool
}

func newFilesystem(options FilesystemOptions) filesystem {
	result := filesystem{
		readDir:      options.ReadDir,
		lstat:        options.Lstat,
		stat:         options.Stat,
		evalSymlinks: options.EvalSymlinks,
		sameFile:     options.SameFile,
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
	if result.evalSymlinks == nil {
		result.evalSymlinks = filepath.EvalSymlinks
	}
	if result.sameFile == nil {
		result.sameFile = os.SameFile
	}
	return result
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
	return relative != ".." &&
		!hasPathComponentPrefix(relative, "..")
}

func hasPathComponentPrefix(path, component string) bool {
	return len(path) > len(component) &&
		path[:len(component)] == component &&
		os.IsPathSeparator(path[len(component)])
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
