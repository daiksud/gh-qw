package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/daiksud/gh-qw/internal/xdg"
)

const configDisplayPath = "~/.config/ghqw/config.toml"

// ErrInvalid identifies configuration contents that do not satisfy the file
// schema. Callers should treat this as a usage error.
var ErrInvalid = errors.New("invalid configuration")

// Config is the unexpanded configuration read from config.toml.
type Config struct {
	Roots        []string
	WorktreeRoot string
}

// InvalidError describes invalid TOML syntax or a schema violation.
type InvalidError struct {
	path   string
	reason string
	err    error
}

// Error implements error without including the configuration contents.
func (e *InvalidError) Error() string {
	if e == nil {
		return ErrInvalid.Error()
	}
	return fmt.Sprintf("%s %q: %s", ErrInvalid, e.path, e.reason)
}

// Is makes every InvalidError discoverable with errors.Is(err, ErrInvalid).
func (e *InvalidError) Is(target error) bool {
	return target == ErrInvalid
}

// Unwrap returns the underlying TOML parser error, when one exists.
func (e *InvalidError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Path returns the configuration file path associated with the error.
func (e *InvalidError) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

// Reason returns the schema or syntax failure without file contents.
func (e *InvalidError) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

// LoaderOptions supplies filesystem seams for isolated callers and tests.
// Nil functions use the operating-system defaults.
type LoaderOptions struct {
	LookupEnv func(string) (string, bool)
	HomeDir   func() (string, error)
	ReadFile  func(string) ([]byte, error)
}

// Loader reads and caches the fixed gh-qw configuration file.
type Loader struct {
	once sync.Once

	lookupEnv func(string) (string, bool)
	homeDir   func() (string, error)
	readFile  func(string) ([]byte, error)

	config Config
	err    error
}

// NewLoader returns a Loader backed by the operating-system home directory and
// filesystem.
func NewLoader() *Loader {
	return NewLoaderWithOptions(LoaderOptions{})
}

// NewLoaderWithOptions returns a Loader with optional injected filesystem
// operations.
func NewLoaderWithOptions(options LoaderOptions) *Loader {
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

	return &Loader{
		lookupEnv: lookupEnv,
		homeDir:   homeDir,
		readFile:  readFile,
	}
}

// DefaultPath computes the gh-qw configuration file location without
// requiring a Loader instance, so a caller can mention the resolved path
// even without constructing one. It uses $XDG_CONFIG_HOME/ghqw/config.toml
// when XDG_CONFIG_HOME is set to an absolute path, otherwise
// ~/.config/ghqw/config.toml (see internal/xdg).
func DefaultPath(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
) (string, error) {
	return configPath(lookupEnv, homeDir)
}

// Load reads config.toml at most once and returns a defensive copy of the
// cached configuration.
func (l *Loader) Load() (Config, error) {
	if l == nil {
		return Config{}, errors.New("load configuration: nil loader")
	}

	l.once.Do(func() {
		l.config, l.err = l.load()
	})

	return cloneConfig(l.config), l.err
}

func (l *Loader) load() (Config, error) {
	lookupEnv := l.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	homeDir := l.homeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	readFile := l.readFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	path, err := configPath(lookupEnv, homeDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration path %q: %w", configDisplayPath, err)
	}

	data, err := readFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}

	return decode(path, data)
}

func configPath(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
) (string, error) {
	base, err := xdg.BaseDir(lookupEnv, homeDir, xdg.ConfigHome, ".config")
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ghqw", "config.toml"), nil
}

func decode(path string, data []byte) (Config, error) {
	document := make(map[string]any)
	metadata, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&document)
	if err != nil {
		return Config{}, newInvalidError(path, tomlFailureReason(err), err)
	}

	for _, key := range metadata.Keys() {
		if len(key) == 0 {
			continue
		}
		switch key[0] {
		case "root", "worktree_root":
		default:
			return Config{}, newInvalidError(
				path,
				fmt.Sprintf("unknown top-level key or table %q", key[0]),
				nil,
			)
		}
	}

	var result Config
	if value, ok := document["root"]; ok {
		roots, reason := decodeRoots(value)
		if reason != "" {
			return Config{}, newInvalidError(path, reason, nil)
		}
		result.Roots = roots
	}

	if value, ok := document["worktree_root"]; ok {
		worktreeRoot, ok := value.(string)
		if !ok {
			return Config{}, newInvalidError(path, "worktree_root must be a string", nil)
		}
		if reason := nonBlankReason("worktree_root", worktreeRoot); reason != "" {
			return Config{}, newInvalidError(path, reason, nil)
		}
		result.WorktreeRoot = worktreeRoot
	}

	return result, nil
}

func decodeRoots(value any) ([]string, string) {
	switch value := value.(type) {
	case string:
		if reason := nonBlankReason("root", value); reason != "" {
			return nil, reason
		}
		return []string{value}, ""
	case []any:
		if len(value) == 0 {
			return nil, "root array must not be empty"
		}

		roots := make([]string, len(value))
		for index, item := range value {
			root, ok := item.(string)
			if !ok {
				return nil, fmt.Sprintf("root array item %d must be a string", index)
			}
			if reason := nonBlankReason(fmt.Sprintf("root array item %d", index), root); reason != "" {
				return nil, reason
			}
			roots[index] = root
		}
		return roots, ""
	default:
		return nil, "root must be a string or a non-empty array of strings"
	}
}

func nonBlankReason(name, value string) string {
	if value == "" {
		return name + " must not be empty"
	}
	if strings.TrimSpace(value) == "" {
		return name + " must not contain only whitespace"
	}
	return ""
}

func tomlFailureReason(err error) string {
	var parseError toml.ParseError
	if errors.As(err, &parseError) {
		if parseError.Position.Line > 0 {
			return fmt.Sprintf("invalid TOML at line %d: %s", parseError.Position.Line, parseError.Message)
		}
		return "invalid TOML: " + parseError.Message
	}
	return "TOML decoding failed"
}

func newInvalidError(path, reason string, err error) *InvalidError {
	return &InvalidError{
		path:   path,
		reason: reason,
		err:    err,
	}
}

func cloneConfig(config Config) Config {
	config.Roots = append([]string(nil), config.Roots...)
	return config
}
