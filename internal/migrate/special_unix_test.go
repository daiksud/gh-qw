//go:build darwin || linux

package migrate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMoveRefusesSpecialFilesBeforeRename(t *testing.T) {
	t.Parallel()

	base := moveTestPhysical(t, t.TempDir())
	source := filepath.Join(base, "source")
	destinationRoot := filepath.Join(base, "destination")
	destination := filepath.Join(destinationRoot, "host", "owner", "repo")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destinationRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	renameCalled := false

	err := Move(source, destination, MoveOptions{
		DestinationRoot: destinationRoot,
		Filesystem: Filesystem{
			Rename: func(string, string) error {
				renameCalled = true
				return nil
			},
		},
	})
	if err == nil {
		t.Fatal("Move() error = nil, want special-file refusal")
	}
	if renameCalled {
		t.Fatal("Rename was called before special-file refusal")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}
