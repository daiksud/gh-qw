// Package locations stores explicitly managed repository locations.
package locations

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/daiksud/gh-qw/internal/xdg"
)

var processLock sync.Mutex

// Store persists canonical repository identities and absolute local paths.
// A zero-value Store uses the standard XDG data directory.
type Store struct {
	Path string
}

// DefaultPath resolves the generated registry's location, independently from
// the user's configuration file.
func DefaultPath(lookupEnv func(string) (string, bool), homeDir func() (string, error)) (string, error) {
	base, err := xdg.BaseDir(lookupEnv, homeDir, xdg.DataHome, ".local", "share")
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ghqw", "repositories.toml"), nil
}

func (s Store) filePath() (string, error) {
	if s.Path != "" {
		if !filepath.IsAbs(s.Path) {
			return "", errors.New("repository registry path must be absolute")
		}
		return filepath.Clean(s.Path), nil
	}
	return DefaultPath(nil, nil)
}

// Read returns an independent identity-to-path snapshot. An absent registry is
// empty, regardless of repositories present on disk; there is no auto-import.
func (s Store) Read() (map[string]string, error) {
	path, err := s.filePath()
	if err != nil {
		return nil, err
	}
	return readFile(path)
}

func readFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository registry %q: %w", path, err)
	}
	var document struct {
		Repositories map[string]string `toml:"repositories"`
	}
	metadata, err := toml.Decode(string(data), &document)
	if err != nil {
		return nil, fmt.Errorf("parse repository registry %q: %w", path, err)
	}
	if unknown := metadata.Undecoded(); len(unknown) > 0 {
		return nil, fmt.Errorf("repository registry %q contains unknown key %q", path, unknown[0].String())
	}
	if document.Repositories == nil {
		document.Repositories = make(map[string]string)
	}
	paths := make(map[string]string, len(document.Repositories))
	for identity, registeredPath := range document.Repositories {
		if err := validateEntry(identity, registeredPath); err != nil {
			return nil, fmt.Errorf("repository registry %q: %w", path, err)
		}
		for previous, previousPath := range paths {
			if samePath(previousPath, registeredPath) {
				return nil, fmt.Errorf("repository registry %q: %q and %q point to the same location", path, previous, identity)
			}
		}
		paths[identity] = registeredPath
	}
	return paths, nil
}

func validateEntry(identity, path string) error {
	if _, err := repospec.ParseIdentity(identity); err != nil {
		return fmt.Errorf("invalid registered repository identity %q: %w", identity, err)
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("repository %q requires a clean absolute location", identity)
	}
	return nil
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b))
}

// Add registers an existing checkout after the caller validates its Git
// identity. Repeating an identical registration is a no-op; moving or stealing
// another registered identity's location is never implicit.
func (s Store) Add(identity, location string) error {
	location = filepath.Clean(location)
	if err := validateEntry(identity, location); err != nil {
		return err
	}
	return s.mutate(func(entries map[string]string) (bool, error) {
		if existing, ok := entries[identity]; ok {
			if samePath(existing, location) {
				return false, nil
			}
			return false, fmt.Errorf("repository %q is already registered at %q; forget it before changing its location", identity, existing)
		}
		for previous, path := range entries {
			if samePath(path, location) {
				return false, fmt.Errorf("location %q is already registered for %q", location, previous)
			}
		}
		entries[identity] = location
		return true, nil
	})
}

// Forget unregisters a repository and never touches its Git metadata or files.
func (s Store) Forget(identity string) error {
	if _, err := repospec.ParseIdentity(identity); err != nil {
		return fmt.Errorf("invalid repository identity %q: %w", identity, err)
	}
	return s.mutate(func(entries map[string]string) (bool, error) {
		if _, found := entries[identity]; !found {
			return false, fmt.Errorf("repository %q is not registered", identity)
		}
		delete(entries, identity)
		return true, nil
	})
}

func (s Store) mutate(update func(map[string]string) (bool, error)) error {
	path, err := s.filePath()
	if err != nil {
		return err
	}
	processLock.Lock()
	defer processLock.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open repository registry lock: %w", err)
	}
	defer lock.Close()
	if err := lockRegistry(lock); err != nil {
		return fmt.Errorf("lock repository registry: %w", err)
	}
	defer unlockRegistry(lock)

	entries, err := readFile(path)
	if err != nil {
		return err
	}
	changed, err := update(entries)
	if err != nil || !changed {
		return err
	}
	return writeAtomic(path, entries)
}

func writeAtomic(path string, entries map[string]string) error {
	var content bytes.Buffer
	document := struct {
		Repositories map[string]string `toml:"repositories"`
	}{Repositories: entries}
	if err := toml.NewEncoder(&content).Encode(document); err != nil {
		return fmt.Errorf("encode repository registry: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".repositories-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary repository registry: %w", err)
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary registry: %w", err)
	}
	if _, err := temp.Write(content.Bytes()); err != nil {
		return fmt.Errorf("write temporary registry: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary registry: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary registry: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replace repository registry: %w", err)
	}
	return nil
}

// Identities returns canonical names in stable order.
func Identities(entries map[string]string) []string {
	result := make([]string, 0, len(entries))
	for identity := range entries {
		result = append(result, identity)
	}
	sort.Strings(result)
	return result
}
