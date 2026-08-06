package ghauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daiksud/gh-qw/internal/xdg"
)

const (
	cacheFileName      = "accounts.json"
	cacheSchemaVersion = 1
)

// CacheOptions supplies filesystem seams for isolated callers and tests. Nil
// functions use the operating-system defaults.
type CacheOptions struct {
	LookupEnv func(string) (string, bool)
	HomeDir   func() (string, error)
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte, fs.FileMode) error
	MkdirAll  func(string, fs.FileMode) error
}

// Cache persists which gh login to use for a given host and repository
// owner, so automatic account selection only pays gh auth status's
// roughly one-second latency once per owner (see ListAccounts).
type Cache struct {
	mu sync.Mutex

	lookupEnv func(string) (string, bool)
	homeDir   func() (string, error)
	readFile  func(string) ([]byte, error)
	writeFile func(string, []byte, fs.FileMode) error
	mkdirAll  func(string, fs.FileMode) error

	loaded   bool
	mappings map[string]string
}

type cacheFile struct {
	Version  int               `json:"version"`
	Mappings map[string]string `json:"mappings"`
}

// NewCache returns a Cache backed by the operating system's environment,
// home directory, and filesystem.
func NewCache() *Cache {
	return NewCacheWithOptions(CacheOptions{})
}

// NewCacheWithOptions returns a Cache with optional injected filesystem
// operations.
func NewCacheWithOptions(options CacheOptions) *Cache {
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	homeDir := options.HomeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	readFile := options.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	writeFile := options.WriteFile
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	mkdirAll := options.MkdirAll
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}

	return &Cache{
		lookupEnv: lookupEnv,
		homeDir:   homeDir,
		readFile:  readFile,
		writeFile: writeFile,
		mkdirAll:  mkdirAll,
	}
}

// Path returns the account cache file location.
func (c *Cache) Path() (string, error) {
	return DefaultCachePath(c.lookupEnv, c.homeDir)
}

// DefaultCachePath computes the account cache file location without
// requiring a Cache instance, so callers such as error hints can mention the
// resolved path even when they have not constructed a Cache. It uses
// $XDG_CACHE_HOME/ghqw/accounts.json when XDG_CACHE_HOME is set to an
// absolute path, otherwise ~/.cache/ghqw/accounts.json (see internal/xdg).
// This intentionally differs from gh-qw's configuration file (also XDG-based,
// but under XDG_CONFIG_HOME rather than XDG_CACHE_HOME): the cache is
// machine-written operational state, not user configuration.
func DefaultCachePath(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
) (string, error) {
	base, err := xdg.BaseDir(lookupEnv, homeDir, xdg.CacheHome, ".cache")
	if err != nil {
		return "", fmt.Errorf("resolve account cache path: %w", err)
	}
	return filepath.Join(base, "ghqw", cacheFileName), nil
}

// Lookup returns the cached login for host/owner, if any. Host and owner are
// compared case-insensitively.
func (c *Cache) Lookup(host, owner string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLoaded(); err != nil {
		return "", false, err
	}
	login, ok := c.mappings[cacheKey(host, owner)]
	return login, ok, nil
}

// Store remembers login as the account to use for host/owner and persists
// the mapping to disk.
func (c *Cache) Store(host, owner, login string) error {
	if strings.TrimSpace(host) == "" {
		return errors.New("store gh account mapping: host is required")
	}
	if strings.TrimSpace(owner) == "" {
		return errors.New("store gh account mapping: owner is required")
	}
	if strings.TrimSpace(login) == "" {
		return errors.New("store gh account mapping: login is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLoaded(); err != nil {
		return err
	}
	mappings := cloneMappings(c.mappings)
	mappings[cacheKey(host, owner)] = login
	if err := c.persist(mappings); err != nil {
		return err
	}
	c.mappings = mappings
	return nil
}

// Delete removes any cached mapping for host/owner. A missing mapping is not
// an error.
func (c *Cache) Delete(host, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLoaded(); err != nil {
		return err
	}
	key := cacheKey(host, owner)
	if _, ok := c.mappings[key]; !ok {
		return nil
	}
	mappings := cloneMappings(c.mappings)
	delete(mappings, key)
	if err := c.persist(mappings); err != nil {
		return err
	}
	c.mappings = mappings
	return nil
}

func (c *Cache) ensureLoaded() error {
	if c.loaded {
		return nil
	}

	path, err := c.Path()
	if err != nil {
		return err
	}
	data, err := c.readFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.mappings = make(map[string]string)
			c.loaded = true
			return nil
		}
		return fmt.Errorf("read account cache %q: %w", path, err)
	}

	var parsed cacheFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("parse account cache %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("parse account cache %q: unexpected trailing JSON", path)
		}
		return fmt.Errorf("parse account cache %q: %w", path, err)
	}
	if parsed.Version != cacheSchemaVersion {
		return fmt.Errorf(
			"validate account cache %q: unsupported schema version %d (want %d)",
			path,
			parsed.Version,
			cacheSchemaVersion,
		)
	}
	if parsed.Mappings == nil {
		return fmt.Errorf("validate account cache %q: mappings must be an object", path)
	}
	mappings := make(map[string]string, len(parsed.Mappings))
	for key, login := range parsed.Mappings {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("validate account cache %q: mapping key is empty", path)
		}
		if strings.TrimSpace(login) == "" {
			return fmt.Errorf("validate account cache %q: mapping %q has an empty login", path, key)
		}
		normalized := strings.ToLower(key)
		if _, duplicate := mappings[normalized]; duplicate {
			return fmt.Errorf(
				"validate account cache %q: duplicate case-insensitive mapping key %q",
				path,
				key,
			)
		}
		mappings[normalized] = login
	}
	c.mappings = mappings
	c.loaded = true
	return nil
}

func (c *Cache) persist(mappings map[string]string) error {
	path, err := c.Path()
	if err != nil {
		return err
	}
	if err := c.mkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create account cache directory: %w", err)
	}

	data, err := json.MarshalIndent(
		cacheFile{Version: cacheSchemaVersion, Mappings: mappings},
		"",
		"  ",
	)
	if err != nil {
		return fmt.Errorf("encode account cache: %w", err)
	}
	if err := c.writeFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write account cache %q: %w", path, err)
	}
	return nil
}

func cacheKey(host, owner string) string {
	return strings.ToLower(host) + "/" + strings.ToLower(owner)
}

func cloneMappings(mappings map[string]string) map[string]string {
	cloned := make(map[string]string, len(mappings))
	for key, login := range mappings {
		cloned[key] = login
	}
	return cloned
}
