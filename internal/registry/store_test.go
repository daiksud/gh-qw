package registry

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	absolute := filepath.Join(home, "data")
	for _, tc := range []struct {
		name, env, want string
	}{
		{"absolute", absolute, filepath.Join(absolute, "ghqw", "repositories.toml")},
		{"unset", "", filepath.Join(home, ".local", "share", "ghqw", "repositories.toml")},
		{"relative", "relative", filepath.Join(home, ".local", "share", "ghqw", "repositories.toml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := DefaultPath(func(key string) (string, bool) {
				return tc.env, tc.env != ""
			}, func() (string, error) { return home, nil })
			if err != nil || path != tc.want {
				t.Fatalf("path = %q, err %v; want %q", path, err, tc.want)
			}
		})
	}
}

func TestEmptyRegistryNeverDiscoversDirectories(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "ghqw", "github.com", "acme", "api")
	if err := os.MkdirAll(repoPath, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "data", "ghqw", "repositories.toml")
	store, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("list = %v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created a registry directory: %v", err)
	}
}

func TestRegistrationAndForgetLeaveRepositoryUntouched(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "external", "repo")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(repo, "keep")
	if err := os.WriteFile(sentinel, []byte("do not touch"), 0600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(base, "data", "ghqw", "repositories.toml")
	store, err := NewStoreAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	identity := "github.com/acme/api"
	if err := store.Register(identity, repo); err != nil {
		t.Fatal(err)
	}
	if err := store.Register(identity, repo); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	other, _ := NewStoreAt(registryPath)
	entries, err := other.List()
	if err != nil || entries[identity] != repo || len(entries) != 1 {
		t.Fatalf("readback = %v, %v", entries, err)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil || !strings.Contains(string(data), `"github.com/acme/api"`) {
		t.Fatalf("encoded registry = %q: %v", data, err)
	}
	if err := other.Forget(identity); err != nil {
		t.Fatal(err)
	}
	entries, err = store.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("after forget = %v, %v", entries, err)
	}
	if contents, err := os.ReadFile(sentinel); err != nil || string(contents) != "do not touch" {
		t.Fatalf("forget touched checkout: %q, %v", contents, err)
	}
	if err := other.Forget(identity); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second forget: %v", err)
	}
}

func TestForgetRemovesDanglingRegisteredLocation(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "missing")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(missing, alias); err != nil {
		t.Skipf("create dangling symlink: %v", err)
	}
	registryPath := filepath.Join(base, "repositories.toml")
	location := filepath.Join(alias, "repo")
	data := []byte("[repositories]\n\"github.com/acme/api\" = " + strconv.Quote(location) + "\n")
	if err := os.WriteFile(registryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := store.List()
	if err != nil || entries["github.com/acme/api"] != location {
		t.Fatalf("List() = %v, %v; want dangling registration", entries, err)
	}
	if err := store.Forget("github.com/acme/api"); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}
	entries, err = store.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("List() after Forget = %v, %v; want empty", entries, err)
	}
}

func TestConflictsAndMalformedFileAreFailClosed(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "repositories.toml")
	store, _ := NewStoreAt(path)
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	if err := store.Register("github.com/acme/one", first); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, path string
		want     error
	}{
		{"github.com/acme/one", second, ErrConflict},
		{"github.com/acme/two", first, ErrConflict},
		{"github.com/acme/two", "relative", ErrInvalid},
		{"acme/two", second, ErrInvalid},
	} {
		if err := store.Register(tc.id, tc.path); !errors.Is(err, tc.want) {
			t.Errorf("register %q %q: %v, want %v", tc.id, tc.path, err, tc.want)
		}
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 || entries["github.com/acme/one"] != first {
		t.Fatalf("conflicting update changed registry: %v, %v", entries, err)
	}
	broken := []byte("[repositories]\n\"github.com/acme/one\" = \"relative\"\n")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("github.com/acme/two", second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed file was accepted: %v", err)
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(broken) {
		t.Fatalf("malformed file was overwritten: %q", current)
	}
}

func TestWriterLockPreventsUncoordinatedUpdate(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "repositories.toml")
	store, _ := NewStoreAt(path)
	if err := store.Register("github.com/acme/first", filepath.Join(base, "first")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("github.com/acme/second", filepath.Join(base, "second")); err == nil {
		t.Fatal("registration succeeded while locked")
	}
	// Reads must not bypass the writer lock and see an inconsistent state.
	if _, err := store.List(); err == nil {
		t.Fatal("read succeeded while exclusive lock was held")
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List()
	if err != nil || len(entries) != 1 || entries["github.com/acme/first"] == "" {
		t.Fatalf("lock failure changed data: %v, %v", entries, err)
	}
}

func TestIdempotentRegistrationDoesNotReplaceRegistryFile(t *testing.T) {
	base := t.TempDir()
	registryPath := filepath.Join(base, "repositories.toml")
	store, err := NewStoreAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	id := "github.com/acme/api"
	location := filepath.Join(base, "api")
	if err := store.Register(id, location); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register(id, location); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("idempotent registration replaced the data file")
	}
}

func TestRelocateUpdatesOnlyExpectedRegistration(t *testing.T) {
	base := t.TempDir()
	store, err := NewStoreAt(filepath.Join(base, "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	identity := "github.com/acme/api"
	previous := filepath.Join(base, "previous")
	next := filepath.Join(base, "next")
	other := filepath.Join(base, "other")
	for _, path := range []string{previous, next, other} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Register(identity, previous); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("github.com/acme/other", other); err != nil {
		t.Fatal(err)
	}
	if err := store.Relocate(identity, other, next); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale relocate = %v, want conflict", err)
	}
	if err := store.Relocate(identity, previous, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting relocate = %v, want conflict", err)
	}
	if err := store.Relocate(identity, previous, next); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List()
	if err != nil || entries[identity] != next || entries["github.com/acme/other"] != other {
		t.Fatalf("relocated entries = %v, %v", entries, err)
	}
	if err := store.Relocate(identity, next, next); err != nil {
		t.Fatalf("idempotent relocate = %v", err)
	}
}

func TestForgetAtDoesNotDeleteReplacedRegistration(t *testing.T) {
	base := t.TempDir()
	store, err := NewStoreAt(filepath.Join(base, "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	identity := "github.com/acme/api"
	previous := filepath.Join(base, "previous")
	replacement := filepath.Join(base, "replacement")
	if err := store.Register(identity, replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetAt(identity, previous); !errors.Is(err, ErrConflict) {
		t.Fatalf("ForgetAt replaced mapping = %v, want conflict", err)
	}
	entries, err := store.List()
	if err != nil || entries[identity] != replacement {
		t.Fatalf("replacement registration changed: %v, %v", entries, err)
	}
	if err := store.ForgetAt(identity, replacement); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedKeysAreNotIgnored(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "repositories.toml")
	if err := os.WriteFile(path, []byte("unexpected = 1\n[repositories]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewStoreAt(path)
	if _, err := store.List(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unexpected key accepted: %v", err)
	}
}

func TestSymlinkAliasRegistrationIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	alias := filepath.Join(base, "alias")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreAt(filepath.Join(base, "repositories.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Register("github.com/acme/first", real); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("github.com/acme/second", alias); !errors.Is(err, ErrConflict) {
		t.Fatalf("alias registration: %v", err)
	}
}

func TestSymlinkAliasesInStoredRegistryFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "repositories.toml")
	data := "[repositories]\n\"github.com/acme/first\" = " + strconv.Quote(real) +
		"\n\"github.com/acme/second\" = " + strconv.Quote(alias) + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate physical path accepted: %v", err)
	}
}
