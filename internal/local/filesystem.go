package local

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/physicalpath"
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

func equalFoldPath(first, second string) bool { return physicalpath.EqualFoldASCII(first, second) }
