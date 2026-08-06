package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverLegacyRootsUsesEnvironmentExclusively(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	first = legacyTestPhysical(t, first)
	second = legacyTestPhysical(t, second)
	git := &legacyTestGit{
		outputs: map[string][]byte{
			legacyTestKey("config", "--path", "--get-all", "ghq.root"): []byte("/unused\n"),
		},
	}

	result, err := DiscoverLegacyRoots(context.Background(), git, LegacyOptions{
		LookupEnv: func(name string) (string, bool) {
			if name != "GHQ_ROOT" {
				t.Fatalf("LookupEnv(%q), want GHQ_ROOT", name)
			}
			return strings.Join([]string{first, second}, string(os.PathListSeparator)), true
		},
	})
	if err != nil {
		t.Fatalf("DiscoverLegacyRoots() error = %v", err)
	}
	if got, want := result.Roots, []string{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
	if len(git.calls) != 0 {
		t.Fatalf("Git calls = %#v, want none", git.calls)
	}
}

func TestDiscoverLegacyRootsOrdersConfigAndDeduplicatesPhysicalPaths(t *testing.T) {
	t.Parallel()

	base := legacyTestPhysical(t, t.TempDir())
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	third := filepath.Join(base, "third")
	for _, path := range []string{first, second, third} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secondAlias := filepath.Join(base, "second-alias")
	if err := os.Symlink(second, secondAlias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}

	git := &legacyTestGit{
		outputs: map[string][]byte{
			legacyTestKey("config", "--path", "--get-all", "ghq.root"): []byte(first + "\n" + second + "\n"),
			legacyTestKey("config", "--path", "--get-regexp", `^ghq\..+\.root$`): []byte(
				"ghq.https://example.com.root " + secondAlias + "\n" +
					"ghq.ssh://example.net.root " + third + "\n",
			),
		},
	}

	result, err := DiscoverLegacyRoots(context.Background(), git, LegacyOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return base, nil },
	})
	if err != nil {
		t.Fatalf("DiscoverLegacyRoots() error = %v", err)
	}
	if got, want := result.Roots, []string{second, first, third}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestDiscoverLegacyRootsWarnsAndSkipsInvalidValues(t *testing.T) {
	t.Parallel()

	valid := legacyTestPhysical(t, t.TempDir())
	git := &legacyTestGit{
		outputs: map[string][]byte{
			legacyTestKey("config", "--path", "--get-all", "ghq.root"): []byte(valid + "\nrelative\n"),
		},
	}

	result, err := DiscoverLegacyRoots(context.Background(), git, LegacyOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		IsConfigMissing: func(err error) bool {
			return errors.Is(err, legacyTestMissing)
		},
	})
	if err != nil {
		t.Fatalf("DiscoverLegacyRoots() error = %v", err)
	}
	if got, want := result.Roots, []string{valid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Value != "relative" {
		t.Fatalf("warnings = %#v, want one warning for relative", result.Warnings)
	}
}

func TestDiscoverLegacyRootsFallsBackToHomeGhq(t *testing.T) {
	t.Parallel()

	home := legacyTestPhysical(t, t.TempDir())
	git := &legacyTestGit{}
	result, err := DiscoverLegacyRoots(context.Background(), git, LegacyOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
		IsConfigMissing: func(error) bool {
			return true
		},
	})
	if err != nil {
		t.Fatalf("DiscoverLegacyRoots() error = %v", err)
	}
	if got, want := result.Roots, []string{filepath.Join(home, "ghq")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}

func TestDiscoverLegacyRootsDoesNotFallbackForEmptyConfiguredValue(t *testing.T) {
	t.Parallel()

	home := legacyTestPhysical(t, t.TempDir())
	git := &legacyTestGit{
		outputs: map[string][]byte{
			legacyTestKey("config", "--path", "--get-all", "ghq.root"): []byte("\n"),
		},
	}
	_, err := DiscoverLegacyRoots(context.Background(), git, LegacyOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
		IsConfigMissing: func(err error) bool {
			return errors.Is(err, legacyTestMissing)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no usable root") {
		t.Fatalf("DiscoverLegacyRoots() error = %v, want unusable configured root", err)
	}
}

var legacyTestMissing = errors.New("missing config value")

type legacyTestGit struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   [][]string
}

func (git *legacyTestGit) Output(_ context.Context, arguments ...string) ([]byte, error) {
	git.calls = append(git.calls, append([]string(nil), arguments...))
	key := legacyTestKey(arguments...)
	if err, ok := git.errors[key]; ok {
		return nil, err
	}
	if output, ok := git.outputs[key]; ok {
		return append([]byte(nil), output...), nil
	}
	return nil, legacyTestMissing
}

func legacyTestKey(arguments ...string) string {
	return strings.Join(arguments, "\x00")
}

func legacyTestPhysical(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
