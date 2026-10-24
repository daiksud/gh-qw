package movement

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/physicalpath"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

// Filesystem supplies the filesystem seams used by repository movement. Nil functions
// use their operating-system equivalents.
type Filesystem struct {
	ReadDir      func(string) ([]os.DirEntry, error)
	Lstat        func(string) (fs.FileInfo, error)
	Stat         func(string) (fs.FileInfo, error)
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

func rootOptions(filesystem filesystem) rootpkg.Options {
	return rootpkg.Options{
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	}
}

func (f filesystem) physicalPaths() physicalpath.Filesystem {
	return physicalpath.Filesystem{Lstat: f.lstat, Stat: f.stat, EvalSymlinks: f.evalSymlinks, SameFile: f.sameFile}
}

func (f filesystem) physicalizeAbsolute(path string) (string, error) {
	return f.physicalPaths().ResolveAbsolute(path)
}
func (f filesystem) samePhysicalPath(first, second string) (bool, error) {
	return f.physicalPaths().Equivalent(first, second)
}
func pathStrictlyWithin(base, candidate string) bool {
	return physicalpath.StrictlyWithin(base, candidate)
}

func pathsOverlap(first, second string) bool {
	return filepath.Clean(first) == filepath.Clean(second) || pathStrictlyWithin(first, second) || pathStrictlyWithin(second, first)
}

func preservedMode(mode fs.FileMode) fs.FileMode {
	return mode & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
}
