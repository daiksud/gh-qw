package root_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/root"
)

func TestConfigRawValueFlowsThroughResolver(t *testing.T) {
	base := physicalTestDir(t)
	home := filepath.Join(base, "home")
	writeConfig(t, home, `
root = "~/repositories"
`)

	var reads atomic.Int32
	loader := config.NewLoaderWithOptions(config.LoaderOptions{
		LookupEnv: noEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(path string) ([]byte, error) {
			reads.Add(1)
			return os.ReadFile(path)
		},
	})

	raw, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantRaw := config.Config{
		Root: "~/repositories",
	}
	if !reflect.DeepEqual(raw, wantRaw) {
		t.Fatalf("Load() = %#v, want raw values %#v", raw, wantRaw)
	}

	resolver := root.NewResolverWithOptions(root.Options{
		ConfigLoader: loader,
		LookupEnv:    noEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
	})
	got, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := root.Result{
		RepositoryRoot: filepath.Join(home, "repositories"),
		WorktreeRoot:   filepath.Join(home, "repositories", "worktrees"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}

	again, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("second Resolve() = %#v, want %#v", again, want)
	}
	if reads.Load() != 1 {
		t.Fatalf("configuration reads = %d, want 1", reads.Load())
	}
}

func TestResolverAppliesEnvironmentOverrideBeforeRawPathValidation(t *testing.T) {
	base := physicalTestDir(t)
	home := filepath.Join(base, "home")
	environmentRoot := filepath.Join(base, "environment-root")
	resolver := integratedResolver(t, home, `root = "relative/file-root"`, map[string]string{
		"GHQW_ROOT": environmentRoot,
	})
	got, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := root.Result{
		RepositoryRoot: environmentRoot,
		WorktreeRoot:   filepath.Join(environmentRoot, "worktrees"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func integratedResolver(
	t *testing.T,
	home, data string,
	environment map[string]string,
) *root.Resolver {
	t.Helper()
	writeConfig(t, home, data)
	lookupEnv := func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}
	loader := config.NewLoaderWithOptions(config.LoaderOptions{
		LookupEnv: lookupEnv,
		HomeDir: func() (string, error) {
			return home, nil
		},
	})
	return root.NewResolverWithOptions(root.Options{
		ConfigLoader: loader,
		LookupEnv:    lookupEnv,
		HomeDir: func() (string, error) {
			return home, nil
		},
	})
}

func writeConfig(t *testing.T, home, data string) {
	t.Helper()
	configDirectory := filepath.Join(home, ".config", "ghqw")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", configDirectory, err)
	}
	path := filepath.Join(configDirectory, "config.toml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func physicalTestDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return physical
}

func noEnvironment(string) (string, bool) {
	return "", false
}
