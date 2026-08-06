package ghauth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCachePathPrefersXDGCacheHome(t *testing.T) {
	xdg := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(key string) (string, bool) {
			if key == "XDG_CACHE_HOME" {
				return xdg, true
			}
			return "", false
		},
		HomeDir: func() (string, error) {
			t.Fatal("HomeDir() called despite XDG_CACHE_HOME being set")
			return "", nil
		},
	})

	path, err := cache.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	want := filepath.Join(xdg, "ghqw", "accounts.json")
	if path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}
}

func TestCachePathFallsBackToHomeCacheDir(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	path, err := cache.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	want := filepath.Join(home, ".cache", "ghqw", "accounts.json")
	if path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}
}

func TestCachePathTreatsRelativeXDGCacheHomeAsUnset(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(key string) (string, bool) {
			if key == "XDG_CACHE_HOME" {
				return "relative/cache", true
			}
			return "", false
		},
		HomeDir: func() (string, error) { return home, nil },
	})

	path, err := cache.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	want := filepath.Join(home, ".cache", "ghqw", "accounts.json")
	if path != want {
		t.Fatalf("Path() = %q, want fallback %q for a relative XDG_CACHE_HOME", path, want)
	}
}

func TestCachePathTreatsEmptyXDGCacheHomeAsUnset(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(key string) (string, bool) {
			if key == "XDG_CACHE_HOME" {
				return "", true
			}
			return "", false
		},
		HomeDir: func() (string, error) { return home, nil },
	})

	path, err := cache.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	want := filepath.Join(home, ".cache", "ghqw", "accounts.json")
	if path != want {
		t.Fatalf("Path() = %q, want fallback %q for an empty XDG_CACHE_HOME", path, want)
	}
}

func TestCacheStoreThenLookupRoundTrips(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if err := cache.Store("github.com", "daiksud", "daiksud"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	login, ok, err := cache.Lookup("github.com", "daiksud")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok || login != "daiksud" {
		t.Fatalf("Lookup() = (%q, %v), want (%q, true)", login, ok, "daiksud")
	}
}

func TestCacheLookupIsCaseInsensitiveForHostAndOwner(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if err := cache.Store("GitHub.COM", "Acme", "TE-DaikiSudo"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	login, ok, err := cache.Lookup("github.com", "acme")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok || login != "TE-DaikiSudo" {
		t.Fatalf("Lookup() = (%q, %v), want (%q, true)", login, ok, "TE-DaikiSudo")
	}
}

func TestCacheLookupMissesReturnFalse(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if _, ok, err := cache.Lookup("github.com", "nobody"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if ok {
		t.Fatal("Lookup() = true, want false for an unseeded cache")
	}
}

func TestCacheLookupPropagatesCorruptFile(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "ghqw")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "accounts.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if _, _, err := cache.Lookup("github.com", "daiksud"); err == nil {
		t.Fatal("Lookup() error = nil, want corrupt cache failure")
	}
}

func TestCacheLookupRejectsUnsupportedSchemaVersion(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, ".cache", "ghqw")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(cacheDir, "accounts.json"),
		[]byte(`{"version":2,"mappings":{}}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if _, _, err := cache.Lookup("github.com", "daiksud"); err == nil {
		t.Fatal("Lookup() error = nil, want schema version failure")
	}
}

func TestCacheLookupPropagatesReadFailure(t *testing.T) {
	wantErr := errors.New("permission denied")
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return t.TempDir(), nil },
		ReadFile:  func(string) ([]byte, error) { return nil, wantErr },
	})

	if _, _, err := cache.Lookup("github.com", "daiksud"); !errors.Is(err, wantErr) {
		t.Fatalf("Lookup() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestCacheOperationsPropagatePathFailure(t *testing.T) {
	wantErr := errors.New("home directory unavailable")
	newCache := func() *Cache {
		return NewCacheWithOptions(CacheOptions{
			LookupEnv: func(string) (string, bool) { return "", false },
			HomeDir:   func() (string, error) { return "", wantErr },
		})
	}

	if _, _, err := newCache().Lookup("github.com", "acme"); !errors.Is(err, wantErr) {
		t.Fatalf("Lookup() error = %v, want it to wrap %v", err, wantErr)
	}
	if err := newCache().Store("github.com", "acme", "acme-bot"); !errors.Is(err, wantErr) {
		t.Fatalf("Store() error = %v, want it to wrap %v", err, wantErr)
	}
	if err := newCache().Delete("github.com", "acme"); !errors.Is(err, wantErr) {
		t.Fatalf("Delete() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestCacheDeleteRemovesMapping(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})
	if err := cache.Store("github.com", "daiksud", "daiksud"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	if err := cache.Delete("github.com", "daiksud"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, ok, err := cache.Lookup("github.com", "daiksud"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if ok {
		t.Fatal("Lookup() = true after Delete(), want false")
	}
}

func TestCacheStorePersistenceFailureDoesNotChangeMemory(t *testing.T) {
	wantErr := errors.New("disk full")
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return t.TempDir(), nil },
		ReadFile:  func(string) ([]byte, error) { return nil, os.ErrNotExist },
		MkdirAll:  func(string, os.FileMode) error { return nil },
		WriteFile: func(string, []byte, os.FileMode) error { return wantErr },
	})

	if err := cache.Store("github.com", "acme", "acme-bot"); !errors.Is(err, wantErr) {
		t.Fatalf("Store() error = %v, want it to wrap %v", err, wantErr)
	}
	if _, ok, err := cache.Lookup("github.com", "acme"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	} else if ok {
		t.Fatal("Lookup() found mapping after failed Store()")
	}
}

func TestCacheDeletePersistenceFailureRetainsMapping(t *testing.T) {
	home := t.TempDir()
	writeCalls := 0
	wantErr := errors.New("disk full")
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
		WriteFile: func(path string, data []byte, mode os.FileMode) error {
			writeCalls++
			if writeCalls > 1 {
				return wantErr
			}
			return os.WriteFile(path, data, mode)
		},
	})
	if err := cache.Store("github.com", "acme", "acme-bot"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	if err := cache.Delete("github.com", "acme"); !errors.Is(err, wantErr) {
		t.Fatalf("Delete() error = %v, want it to wrap %v", err, wantErr)
	}
	login, ok, err := cache.Lookup("github.com", "acme")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok || login != "acme-bot" {
		t.Fatalf("Lookup() = (%q, %v), want retained mapping", login, ok)
	}
}

func TestCacheStoreCreatesRestrictedDirectoryAndFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	if err := cache.Store("github.com", "daiksud", "daiksud"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	path, err := cache.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file permissions = %o, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("directory permissions = %o, want 0700", perm)
	}
}

func TestCacheStorePersistsAcrossNewCacheInstances(t *testing.T) {
	home := t.TempDir()
	options := CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	}
	first := NewCacheWithOptions(options)
	if err := first.Store("github.com", "daiksud", "daiksud"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	second := NewCacheWithOptions(options)
	login, ok, err := second.Lookup("github.com", "daiksud")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !ok || login != "daiksud" {
		t.Fatalf("Lookup() on a fresh Cache = (%q, %v), want (%q, true)", login, ok, "daiksud")
	}
}

func TestCacheRejectsEmptyHostOrOwnerOrLogin(t *testing.T) {
	home := t.TempDir()
	cache := NewCacheWithOptions(CacheOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
	})

	tests := []struct {
		name  string
		host  string
		owner string
		login string
	}{
		{name: "empty host", host: "", owner: "acme", login: "daiksud"},
		{name: "empty owner", host: "github.com", owner: "", login: "daiksud"},
		{name: "empty login", host: "github.com", owner: "acme", login: ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if err := cache.Store(test.host, test.owner, test.login); err == nil {
				t.Fatal("Store() error = nil, want validation failure")
			}
		})
	}
}
