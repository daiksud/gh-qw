// Package fsidentity provides fail-closed filesystem object identity capture.
package fsidentity

import (
	"errors"
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
