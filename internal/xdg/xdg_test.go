package xdg

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestBaseDirAdoptsAnAbsoluteValue(t *testing.T) {
	xdgValue := t.TempDir()
	got, err := BaseDir(
		lookupEnvMap(map[string]string{"XDG_DATA_HOME": xdgValue}),
		failingHomeDir,
		DataHome,
		".local", "share",
	)
	if err != nil {
		t.Fatalf("BaseDir() error = %v", err)
	}
	if want := filepath.Clean(xdgValue); got != want {
		t.Fatalf("BaseDir() = %q, want %q", got, want)
	}
}

func TestBaseDirIgnoresARelativeValue(t *testing.T) {
	home := t.TempDir()
	got, err := BaseDir(
		lookupEnvMap(map[string]string{"XDG_DATA_HOME": filepath.Join("relative", "data")}),
		fixedHomeDir(home),
		DataHome,
		".local", "share",
	)
	if err != nil {
		t.Fatalf("BaseDir() error = %v", err)
	}
	if want := filepath.Join(home, ".local", "share"); got != want {
		t.Fatalf("BaseDir() = %q, want fallback %q", got, want)
	}
}

func TestBaseDirIgnoresAnEmptyValue(t *testing.T) {
	home := t.TempDir()
	got, err := BaseDir(
		lookupEnvMap(map[string]string{"XDG_DATA_HOME": ""}),
		fixedHomeDir(home),
		DataHome,
		".local", "share",
	)
	if err != nil {
		t.Fatalf("BaseDir() error = %v", err)
	}
	if want := filepath.Join(home, ".local", "share"); got != want {
		t.Fatalf("BaseDir() = %q, want fallback %q", got, want)
	}
}

func TestBaseDirFallsBackWhenUnset(t *testing.T) {
	home := t.TempDir()
	got, err := BaseDir(
		lookupEnvMap(nil),
		fixedHomeDir(home),
		DataHome,
		".local", "share",
	)
	if err != nil {
		t.Fatalf("BaseDir() error = %v", err)
	}
	if want := filepath.Join(home, ".local", "share"); got != want {
		t.Fatalf("BaseDir() = %q, want fallback %q", got, want)
	}
}

func TestBaseDirPropagatesHomeDirFailure(t *testing.T) {
	wantErr := errors.New("home lookup failed")
	_, err := BaseDir(
		lookupEnvMap(nil),
		func() (string, error) { return "", wantErr },
		ConfigHome,
		".config",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BaseDir() error = %v, want wrapping %v", err, wantErr)
	}
}

func TestBaseDirRejectsAnEmptyHome(t *testing.T) {
	_, err := BaseDir(
		lookupEnvMap(nil),
		fixedHomeDir(""),
		ConfigHome,
		".config",
	)
	if err == nil {
		t.Fatal("BaseDir() error = nil, want failure for an empty home directory")
	}
}

func TestBaseDirRejectsANonAbsoluteHome(t *testing.T) {
	_, err := BaseDir(
		lookupEnvMap(nil),
		fixedHomeDir(filepath.Join("relative", "home")),
		ConfigHome,
		".config",
	)
	if err == nil {
		t.Fatal("BaseDir() error = nil, want failure for a non-absolute home directory")
	}
}

func TestBaseDirJoinsMultipleFallbackComponents(t *testing.T) {
	home := t.TempDir()
	got, err := BaseDir(
		lookupEnvMap(nil),
		fixedHomeDir(home),
		DataHome,
		".local", "share",
	)
	if err != nil {
		t.Fatalf("BaseDir() error = %v", err)
	}
	if want := filepath.Join(home, ".local", "share"); got != want {
		t.Fatalf("BaseDir() = %q, want %q", got, want)
	}
}

func TestBaseDirDefaultsNilLookupEnvAndHomeDir(t *testing.T) {
	// A smoke test only: this exercises the os.LookupEnv/os.UserHomeDir
	// defaults without asserting a specific path, since the real
	// environment and home directory vary by machine.
	if _, err := BaseDir(nil, nil, ConfigHome, ".config"); err != nil {
		t.Fatalf("BaseDir() error = %v, want the OS defaults to resolve without error", err)
	}
}

func lookupEnvMap(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func fixedHomeDir(home string) func() (string, error) {
	return func() (string, error) {
		return home, nil
	}
}

func failingHomeDir() (string, error) {
	return "", errors.New("home directory must not be consulted when the XDG variable is absolute")
}
