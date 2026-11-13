package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/local"
)

func prepareRemoveFilesystem(options local.FilesystemOptions, remove, removeAll func(string) error) removeFilesystem {
	filesystem := removeFilesystem{
		readDir:      options.ReadDir,
		lstat:        options.Lstat,
		evalSymlinks: options.EvalSymlinks,
		sameFile:     options.SameFile,
		remove:       remove,
		removeAll:    removeAll,
	}
	if filesystem.readDir == nil {
		filesystem.readDir = os.ReadDir
	}
	if filesystem.lstat == nil {
		filesystem.lstat = os.Lstat
	}
	if filesystem.evalSymlinks == nil {
		filesystem.evalSymlinks = filepath.EvalSymlinks
	}
	if filesystem.sameFile == nil {
		filesystem.sameFile = os.SameFile
	}
	if filesystem.remove == nil {
		filesystem.remove = os.Remove
	}
	if filesystem.removeAll == nil {
		filesystem.removeAll = os.RemoveAll
	}
	return filesystem
}

func prepareRemovePrompt(prompt RemovePrompt, openTerminal func() (io.ReadCloser, error)) RemovePrompt {
	if prompt != nil {
		return prompt
	}
	if openTerminal == nil {
		openTerminal = removeOpenControllingTerminal
	}
	return func(ctx context.Context, writer io.Writer, message string) (bool, error) {
		return removeConfirm(ctx, writer, message, openTerminal)
	}
}
