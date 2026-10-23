//go:build !windows

package locations

import (
	"os"
	"syscall"
)

func lockRegistry(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func unlockRegistry(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
