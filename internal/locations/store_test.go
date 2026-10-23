package locations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	data := filepath.Join(t.TempDir(), "data")
	for _, tc := range []struct {
		name string
		env string
		want string
	}{
		{"unset", "", filepath.Join(home, ".local", "share", "ghqw", "repositories.toml")},
		{"absolute", data, filepath.Join(data, "ghqw", "repositories.toml")},
		{"relative", "relative", filepath.Join(home, ".local", "share", "ghqw", "repositories.toml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, err := DefaultPath(func(string) (string, bool) { return tc.env, tc.env != "" }, func() (string, error) { return home, nil })
			if err != nil || path != tc.want {
				t.Fatalf("DefaultPath() = %q, %v, want %q", path, err, tc.want)
			}
		})
	}
}

func TestEmptyMeansNoImplicitImport(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "github.com", "acme", "api")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: filepath.Join(t.TempDir(), "repositories.toml")}
	entries, err := store.Read()
	if err != nil || len(entries) != 0 {
		t.Fatalf("Read without registry = %v, %v", entries, err)
	}
	discovered, err := store.Discover(t.Context(), []string{root})
	if err != nil || len(discovered.Repositories) != 0 {
		t.Fatalf("Discover without registry = %v, %v", discovered, err)
	}
}

func TestAddForgetAndRoundTrip(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "registrations", "repositories.toml")}
	identity := "github.com/acme/api"
	path := filepath.Join(t.TempDir(), "api")
	if err := store.Add(identity, path); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(identity, path); err != nil {
		t.Fatalf("repeat Add: %v", err)
	}
	entries, err := store.Read()
	if err != nil || entries[identity] != path || len(entries) != 1 {
		t.Fatalf("Read() = %#v, %v", entries, err)
	}
	if err := store.Add(identity, filepath.Join(t.TempDir(), "other")); err == nil {
		t.Fatal("Add silently replaced an existing identity")
	}
	if err := store.Add("github.com/acme/other", path); err == nil {
		t.Fatal("Add registered a duplicate location")
	}
	if err := store.Forget(identity); err != nil {
		t.Fatal(err)
	}
	entries, err = store.Read()
	if err != nil || len(entries) != 0 {
		t.Fatalf("Read after Forget = %#v, %v", entries, err)
	}
	if err := store.Forget(identity); err == nil {
		t.Fatal("Forget of missing identity succeeded")
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("registration file was left empty")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Forget touched the repository path: %v", err)
	}
}

func TestConcurrentAddsPreserveEntries(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "repositories.toml")}
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			if err := store.Add("github.com/acme/repo"+string(rune('A'+i)), filepath.Join(t.TempDir(), "repo"+string(rune('A'+i)))); err != nil {
				t.Errorf("Add(%d): %v", i, err)
			}
		}(i)
	}
	workers.Wait()
	entries, err := store.Read()
	if err != nil || len(entries) != 16 {
		t.Fatalf("Read after concurrent additions = %d entries, %v", len(entries), err)
	}
}

func TestRejectCorruptRegistryWithoutClobbering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repositories.toml")
	invalid := "[repositories]\n\"github.com/acme/api\" = 42\n"
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: path}
	if err := store.Add("github.com/acme/new", filepath.Join(t.TempDir(), "new")); err == nil {
		t.Fatal("Add accepted a broken registry")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != invalid {
		t.Fatalf("corrupt registry was rewritten: %q, %v", content, err)
	}
	if err := store.Add("invalid", "/repo"); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if err := store.Add("github.com/acme/new", "relative"); err == nil {
		t.Fatal("relative path accepted")
	}
	if !strings.HasSuffix(path, "repositories.toml") {
		t.Fatal("unexpected test path")
	}
}
