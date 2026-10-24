package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/daiksud/gh-qw/internal/registry"
)

func TestNewForgetCommandRejectsArgumentCountAsUsage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing"},
		{name: "extra", args: []string{"one", "two"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := NewForgetCommand(nil, io.Discard)
			command.SetArgs(test.args)
			err := command.Execute()
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("Execute() error = %v, exit = %d; want usage exit 2", err, ExitCode(err))
			}
		})
	}
}

func TestNewForgetCommandRemovesDanglingLocation(t *testing.T) {
	base := t.TempDir()
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(base, "missing"), alias); err != nil {
		t.Skipf("create dangling symlink: %v", err)
	}
	store, err := registry.NewStoreAt(filepath.Join(base, "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	identity := "github.com/acme/api"
	if err := store.Register(identity, filepath.Join(alias, "repo")); err != nil {
		t.Fatal(err)
	}

	command := NewForgetCommand(store, io.Discard)
	command.SetArgs([]string{identity})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("List() = %v, %v; want empty", entries, err)
	}
}
