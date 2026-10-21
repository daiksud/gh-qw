// Package fsidentity provides fail-closed filesystem object identity capture.
package fsidentity

import (
	"errors"
	"fmt"
	"io/fs"
)

// Prime forces lazy filesystem identifiers to be captured before a path can
// be replaced. This is required on Windows, where os.FileInfo loads file IDs
// when os.SameFile is first called rather than when Stat or Lstat returns.
func Prime(info fs.FileInfo, sameFile func(fs.FileInfo, fs.FileInfo) bool) error {
	if info == nil {
		return errors.New("filesystem object information is missing")
	}
	if sameFile == nil {
		return errors.New("filesystem identity comparison is unavailable")
	}
	if !sameFile(info, info) {
		return errors.New("filesystem object identity is unavailable")
	}
	return nil
}

// Capture obtains filesystem information and captures its identity before returning it.
func Capture(path string, inspect func(string) (fs.FileInfo, error), sameFile func(fs.FileInfo, fs.FileInfo) bool) (fs.FileInfo, error) {
	info, err := inspect(path)
	if err != nil {
		return nil, err
	}
	if err := Prime(info, sameFile); err != nil {
		return nil, fmt.Errorf("capture filesystem identity: %w", err)
	}
	return info, nil
}
