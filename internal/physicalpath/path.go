// Package physicalpath shares filesystem path observations without establishing managed roots.
package physicalpath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Filesystem supplies read-only observation seams. Nil functions use OS defaults.
type Filesystem struct {
	// PathEqual compares resolved path names. Nil uses exact equality, with ASCII
	// case folding on Windows for legacy layout callers.
	PathEqual    func(string, string) bool
	Lstat        func(string) (fs.FileInfo, error)
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	SameFile     func(fs.FileInfo, fs.FileInfo) bool
}

func (f Filesystem) defaults() Filesystem {
	if f.PathEqual == nil {
		f.PathEqual = func(first, second string) bool {
			return first == second || (runtime.GOOS == "windows" && EqualFoldASCII(first, second))
		}
	}
	if f.Lstat == nil {
		f.Lstat = os.Lstat
	}
	if f.Stat == nil {
		f.Stat = os.Stat
	}
	if f.EvalSymlinks == nil {
		f.EvalSymlinks = filepath.EvalSymlinks
	}
	if f.SameFile == nil {
		f.SameFile = os.SameFile
	}
	return f
}

// ResolveAbsolute resolves existing symlinks and appends missing trailing components.
// It does not establish a trusted boundary or require a final directory.
func (f Filesystem) ResolveAbsolute(path string) (string, error) {
	f = f.defaults()
	if path == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}

	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := f.Lstat(current)
		switch {
		case err == nil:
			physical, evalErr := f.EvalSymlinks(current)
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

// Equivalent compares physical paths, including filesystem object identity.
func (f Filesystem) Equivalent(first, second string) (bool, error) {
	f = f.defaults()
	firstPhysical, err := f.ResolveAbsolute(first)
	if err != nil {
		return false, err
	}
	secondPhysical, err := f.ResolveAbsolute(second)
	if err != nil {
		return false, err
	}

	if f.PathEqual(firstPhysical, secondPhysical) {
		return true, nil
	}

	firstInfo, firstErr := f.Stat(firstPhysical)
	secondInfo, secondErr := f.Stat(secondPhysical)
	if firstErr == nil && secondErr == nil {
		return f.SameFile(firstInfo, secondInfo), nil
	}
	if firstErr != nil && !errors.Is(firstErr, fs.ErrNotExist) {
		return false, fmt.Errorf("stat path %q: %w", firstPhysical, firstErr)
	}
	if secondErr != nil && !errors.Is(secondErr, fs.ErrNotExist) {
		return false, fmt.Errorf("stat path %q: %w", secondPhysical, secondErr)
	}
	return false, nil
}

// StrictlyWithin compares cleaned path components without resolving either path.
// Callers must establish physical boundaries separately.
func StrictlyWithin(base, candidate string) bool {
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

// EqualFoldASCII preserves the ASCII case comparison used for legacy layout paths.
func EqualFoldASCII(first, second string) bool {
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

// SamePath compares cleaned names using the Unicode case policy of removal commands.
// It performs no filesystem observation and must not re-establish trusted roots.
func SamePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	return first == second || (runtime.GOOS == "windows" && strings.EqualFold(first, second))
}

// Key preserves removal commands' cleaned, Windows-lowercased registration key.
// It is not a replacement for SamePath or filesystem identity checks.
func Key(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
