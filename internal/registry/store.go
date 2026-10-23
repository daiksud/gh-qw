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

	"github.com/BurntSushi/toml"
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
	result, err := s.read()
	if err != nil {
		return nil, err
	}
	return result, nil
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
	return s.modify(func(entries map[string]string) error {
		if previous, exists := entries[identity]; exists {
			if samePath(previous, path) {
				return nil
			}
			return fmt.Errorf("%w: %q is registered at %q", ErrConflict, identity, previous)
		}
		for other, existing := range entries {
			if samePath(existing, path) {
				return fmt.Errorf("%w: %q is already registered as %q", ErrConflict, path, other)
			}
		}
		entries[identity] = path
		return nil
	})
}

// Forget removes an explicit mapping only; it never mutates the filesystem
// repository, Git branch, or worktree registrations.
func (s *Store) Forget(identity string) error {
	if _, err := repospec.ParseIdentity(identity); err != nil {
		return fmt.Errorf("forget repository: %w: invalid identity: %v", ErrInvalid, err)
	}
	return s.modify(func(entries map[string]string) error {
		if _, exists := entries[identity]; !exists {
			return fmt.Errorf("%w: %q", ErrNotFound, identity)
		}
		delete(entries, identity)
		return nil
	})
}

func (s *Store) modify(change func(map[string]string) error) error {
	if s == nil {
		return errors.New("nil registry store")
	}
	parent := filepath.Dir(s.path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}
	// The exclusive directory prevents concurrent writers from losing updates.
	// If a process dies while holding it, intervention is required instead of
	// risking a second writer clobbering data.
	lock := s.path + ".lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("acquire repository registry lock %q: %w", lock, err)
	}
	defer os.Remove(lock)

	entries, err := s.read()
	if err != nil {
		return err
	}
	if err := change(entries); err != nil {
		return err
	}
	return s.write(entries)
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
	for first, path := range document.Repositories {
		for second, other := range document.Repositories {
			if first < second && samePath(path, other) {
				return nil, fmt.Errorf("%w: %q and %q refer to the same location", ErrInvalid, first, second)
			}
		}
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
	return nil
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	return left == right || (runtime.GOOS == "windows" && strings.EqualFold(left, right))
}
