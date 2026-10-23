// Package registry persists only gh-qw's repository identity-to-location mapping.
// Git remains authoritative for all repository and worktree metadata.
package registry

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/daiksud/gh-qw/internal/physicalpath"
	"github.com/daiksud/gh-qw/internal/repospec"
	"github.com/daiksud/gh-qw/internal/xdg"
)

var (
	ErrInvalid  = errors.New("invalid repository registry")
	ErrConflict = errors.New("repository registration conflicts with an existing location")
	ErrNotFound = errors.New("repository is not registered")
)

type fileDocument struct {
	Repositories map[string]string `toml:"repositories"`
}

// Store owns one registry file. Do not cache its contents between operations:
// other gh-qw processes or the user may modify the file.
type Store struct{ path string }

// DefaultPath resolves the persistent XDG data file, independently of config.toml.
func DefaultPath(lookupEnv func(string) (string, bool), homeDir func() (string, error)) (string, error) {
	base, err := xdg.BaseDir(lookupEnv, homeDir, xdg.DataHome, ".local", "share")
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ghqw", "repositories.toml"), nil
}

// NewStore uses the process's XDG data directory.
func NewStore() (*Store, error) {
	path, err := DefaultPath(nil, nil)
	if err != nil {
		return nil, err
	}
	return NewStoreAt(path)
}

// NewStoreAt opens a logical store without creating files or directories.
func NewStoreAt(path string) (*Store, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("registry path %q must be absolute", path)
	}
	return &Store{path: filepath.Clean(path)}, nil
}

// List returns a snapshot of the explicitly registered repositories. An absent
// registry represents an empty managed set: no directory discovery is performed.
func (s *Store) List() (map[string]string, error) {
	if s == nil {
		return nil, errors.New("nil registry store")
	}
	// An uninitialized registry must not create its XDG parent on a read.
	if _, err := os.Stat(filepath.Dir(s.path)); errors.Is(err, fs.ErrNotExist) {
		return make(map[string]string), nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect registry directory: %w", err)
	}
	var entries map[string]string
	err := s.withLock(false, func() (err error) {
		entries, err = s.read()
		return err
	})
	return entries, err
}

// Register records an existing main repository after the caller has validated
// its Git association. No directory is moved or created at the target path.
func (s *Store) Register(identity, path string) error {
	if _, err := repospec.ParseIdentity(identity); err != nil {
		return fmt.Errorf("register repository: %w: invalid identity: %v", ErrInvalid, err)
	}
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("register repository: %w: location must be absolute", ErrInvalid)
	}
	path = filepath.Clean(path)
	return s.modify(func(entries map[string]string) (bool, error) {
		if previous, exists := entries[identity]; exists {
			equal, err := equivalentLocation(previous, path)
			if err != nil {
				return false, fmt.Errorf("compare registered repository location: %w", err)
			}
			if equal {
				return false, nil
			}
			return false, fmt.Errorf("%w: %q is registered at %q", ErrConflict, identity, previous)
		}
		for other, existing := range entries {
			equal, err := equivalentLocation(existing, path)
			if err != nil {
				return false, fmt.Errorf("compare registered repository location: %w", err)
			}
			if equal {
				return false, fmt.Errorf("%w: %q is already registered as %q", ErrConflict, path, other)
			}
		}
		entries[identity] = path
		return true, nil
	})
}

// Forget removes an explicit mapping only; it never mutates the filesystem
// repository, Git branch, or worktree registrations.
func (s *Store) Forget(identity string) error {
	if _, err := repospec.ParseIdentity(identity); err != nil {
		return fmt.Errorf("forget repository: %w: invalid identity: %v", ErrInvalid, err)
	}
	return s.modify(func(entries map[string]string) (bool, error) {
		if _, exists := entries[identity]; !exists {
			return false, fmt.Errorf("%w: %q", ErrNotFound, identity)
		}
		delete(entries, identity)
		return true, nil
	})
}

func (s *Store) modify(change func(map[string]string) (bool, error)) error {
	if s == nil {
		return errors.New("nil registry store")
	}
	return s.withLock(true, func() error {
		entries, err := s.read()
		if err != nil {
			return err
		}
		changed, err := change(entries)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return s.write(entries)
	})
}

// Readers and writers coordinate through one cross-process lock. This also
// prevents Windows readers from mistaking a non-atomic replacement for an
// empty registry. A stale lock left by a crashed process is not removed
// automatically: fail closed rather than allowing concurrent writers.
func (s *Store) withLock(createParent bool, action func() error) (result error) {
	parent := filepath.Dir(s.path)
	if createParent {
		if err := os.MkdirAll(parent, 0700); err != nil {
			return fmt.Errorf("create registry directory: %w", err)
		}
	}
	lock := s.path + ".lock"
	for attempt := 0; attempt < 100; attempt++ {
		err := os.Mkdir(lock, 0700)
		if err == nil {
			defer func() {
				if removeErr := os.Remove(lock); removeErr != nil {
					result = errors.Join(result, fmt.Errorf("release repository registry lock %q: %w", lock, removeErr))
				}
			}()
			return action()
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("acquire repository registry lock %q: %w", lock, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("repository registry is busy or has a stale lock at %q", lock)
}

func (s *Store) read() (map[string]string, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect repository registry: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: registry must be a regular file", ErrInvalid)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read repository registry: %w", err)
	}
	var document fileDocument
	metadata, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&document)
	if err != nil {
		return nil, fmt.Errorf("%w: decode registry: %v", ErrInvalid, err)
	}
	if len(metadata.Undecoded()) != 0 {
		return nil, fmt.Errorf("%w: unexpected key %q", ErrInvalid, metadata.Undecoded()[0].String())
	}
	if document.Repositories == nil {
		document.Repositories = make(map[string]string)
	}
	for identity, path := range document.Repositories {
		if _, err := repospec.ParseIdentity(identity); err != nil {
			return nil, fmt.Errorf("%w: invalid identity %q: %v", ErrInvalid, identity, err)
		}
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, fmt.Errorf("%w: %q must have a normalized absolute path", ErrInvalid, identity)
		}
	}
	// Resolve each registered location only once. This avoids O(n²)
	// filesystem lookups under the cross-process registry lock.
	resolved := make(map[string]string, len(document.Repositories))
	type observed struct {
		identity string
		info     fs.FileInfo
	}
	var observedLocations []observed
	for identity, path := range document.Repositories {
		physical, err := (physicalpath.Filesystem{}).ResolveAbsolute(path)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve registered location for %q: %v", ErrInvalid, identity, err)
		}
		key := physical
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if previous, exists := resolved[key]; exists {
			return nil, fmt.Errorf("%w: %q and %q refer to the same location", ErrInvalid, previous, identity)
		}
		resolved[key] = identity

		info, err := os.Stat(physical)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: stat registered location for %q: %v", ErrInvalid, identity, err)
		}
		for _, previous := range observedLocations {
			if os.SameFile(info, previous.info) {
				return nil, fmt.Errorf("%w: %q and %q refer to the same location", ErrInvalid, previous.identity, identity)
			}
		}
		observedLocations = append(observedLocations, observed{identity: identity, info: info})
	}

	return document.Repositories, nil
}

func (s *Store) write(entries map[string]string) error {
	data, err := toml.Marshal(fileDocument{Repositories: entries})
	if err != nil {
		return fmt.Errorf("encode repository registry: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".repositories-*.tmp")
	if err != nil {
		return fmt.Errorf("create registry temporary file: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect registry: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write registry: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync registry: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close registry: %w", err)
	}
	if err := os.Rename(temporary.Name(), s.path); err != nil {
		return fmt.Errorf("replace registry: %w", err)
	}
	// On POSIX, a successful rename does not by itself make the directory
	// entry durable after a crash. Windows directory fsync is unsupported;
	// readers still coordinate with writers using withLock.
	if runtime.GOOS != "windows" {
		parent, err := os.Open(filepath.Dir(s.path))
		if err != nil {
			return fmt.Errorf("open registry directory for sync after replacement: %w", err)
		}
		syncErr := parent.Sync()
		closeErr := parent.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return fmt.Errorf("sync registry directory after replacement: %w", err)
		}
	}
	return nil
}

func equivalentLocation(left, right string) (bool, error) {
	if physicalpath.SamePath(left, right) {
		return true, nil
	}
	return (physicalpath.Filesystem{}).Equivalent(left, right)
}
