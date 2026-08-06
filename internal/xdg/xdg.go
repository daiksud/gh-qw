package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// Base directory variable names defined by the XDG Base Directory
// Specification.
const (
	ConfigHome = "XDG_CONFIG_HOME"
	DataHome   = "XDG_DATA_HOME"
	CacheHome  = "XDG_CACHE_HOME"
)

// BaseDir resolves an XDG base directory: the value of variable (one of
// ConfigHome, DataHome, or CacheHome) when it is set to an absolute path,
// otherwise homeDir joined with fallback's path components. Nil lookupEnv or
// homeDir use the operating-system defaults (os.LookupEnv, os.UserHomeDir).
//
// Per the XDG Base Directory Specification, "if $XDG_CONFIG_HOME ... is
// either not set or empty, a default equal to ... should be used". gh-qw
// extends that same treatment to a relative value: the specification does
// not define what a relative value would even be relative to, so BaseDir
// treats it as unavailable and uses the fallback rather than joining it onto
// an unspecified base or rejecting it as a configuration error. The rule
// applies consistently to every XDG base directory gh-qw resolves.
func BaseDir(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
	variable string,
	fallback ...string,
) (string, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}

	if value, ok := lookupEnv(variable); ok && filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}

	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve %s: home directory unavailable: %w", variable, err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve %s: home directory is empty", variable)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("resolve %s: home directory %q is not absolute", variable, home)
	}

	parts := append([]string{home}, fallback...)
	return filepath.Join(parts...), nil
}
