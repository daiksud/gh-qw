package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLoaderValidConfigurations(t *testing.T) {
	tests := []struct {
		name string
		data string
		want Config
	}{
		{
			name: "empty file",
			want: Config{},
		},
		{
			name: "scalar root",
			data: `root = "~/ghqw"`,
			want: Config{Roots: []string{"~/ghqw"}},
		},
		{
			name: "ordered root array",
			data: `root = ["/second-volume/repos", "~/ghqw", "relative/repos"]`,
			want: Config{Roots: []string{"/second-volume/repos", "~/ghqw", "relative/repos"}},
		},
		{
			name: "worktree root only",
			data: `worktree_root = "~/.ghqw/worktrees"`,
			want: Config{WorktreeRoot: "~/.ghqw/worktrees"},
		},
		{
			name: "both keys",
			data: `
root = ["/repos/primary", "/repos/archive"]
worktree_root = "/worktrees"
`,
			want: Config{
				Roots:        []string{"/repos/primary", "/repos/archive"},
				WorktreeRoot: "/worktrees",
			},
		},
		{
			name: "comments and escapes",
			data: `
# Escapes in basic strings are decoded by TOML. Literal strings retain slashes.
root = ["~/repo\u0020one", 'C:\repos'] # ordered roots
worktree_root = "~/.ghqw/\u0077orktrees"
`,
			want: Config{
				Roots:        []string{"~/repo one", `C:\repos`},
				WorktreeRoot: "~/.ghqw/worktrees",
			},
		},
		{
			name: "duplicates and surrounding spaces remain raw",
			data: `root = ["relative/repo", " /path with boundary spaces ", "relative/repo"]`,
			want: Config{Roots: []string{"relative/repo", " /path with boundary spaces ", "relative/repo"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			var reads atomic.Int32
			var gotPath string
			loader := NewLoaderWithOptions(LoaderOptions{
				LookupEnv: emptyEnvironment,
				HomeDir: func() (string, error) {
					return home, nil
				},
				ReadFile: func(path string) ([]byte, error) {
					reads.Add(1)
					gotPath = path
					return []byte(test.data), nil
				},
			})

			got, err := loader.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Load() = %#v, want %#v", got, test.want)
			}
			if gotPath != filepath.Join(home, ".config", "ghqw", "config.toml") {
				t.Fatalf("read path = %q, want fixed home path", gotPath)
			}

			again, err := loader.Load()
			if err != nil {
				t.Fatalf("second Load() error = %v", err)
			}
			if !reflect.DeepEqual(again, test.want) {
				t.Fatalf("second Load() = %#v, want %#v", again, test.want)
			}
			if reads.Load() != 1 {
				t.Fatalf("read count = %d, want 1", reads.Load())
			}
		})
	}
}

func TestLoaderMissingFileUsesZeroConfiguration(t *testing.T) {
	home := t.TempDir()
	var reads atomic.Int32
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(path string) ([]byte, error) {
			reads.Add(1)
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		},
	})

	got, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("Load() = %#v, want zero Config", got)
	}
	if _, err := loader.Load(); err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if reads.Load() != 1 {
		t.Fatalf("read count = %d, want 1", reads.Load())
	}
}

func TestLoaderRejectsInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name           string
		data           string
		reasonContains string
	}{
		{
			name:           "unknown key",
			data:           `roots = "super-secret-value"`,
			reasonContains: "unknown top-level key or table",
		},
		{
			name: "unknown table",
			data: `
[unexpected]
value = "hidden"
`,
			reasonContains: "unknown top-level key or table",
		},
		{
			name: "case-sensitive unknown key",
			data: `Root = "/repos"`,
		},
		{
			name: "duplicate root",
			data: `
root = "/one"
root = "/two"
`,
			reasonContains: "invalid TOML",
		},
		{
			name: "duplicate worktree root",
			data: `
worktree_root = "/one"
worktree_root = "/two"
`,
			reasonContains: "invalid TOML",
		},
		{
			name:           "malformed TOML",
			data:           `root = ["/one"`,
			reasonContains: "invalid TOML",
		},
		{
			name:           "empty scalar root",
			data:           `root = ""`,
			reasonContains: "must not be empty",
		},
		{
			name:           "whitespace scalar root",
			data:           `root = " \t "`,
			reasonContains: "only whitespace",
		},
		{
			name:           "empty root array",
			data:           `root = []`,
			reasonContains: "must not be empty",
		},
		{
			name:           "empty root array item",
			data:           `root = ["/one", ""]`,
			reasonContains: "item 1 must not be empty",
		},
		{
			name:           "whitespace root array item",
			data:           `root = ["/one", " \t"]`,
			reasonContains: "item 1 must not contain only whitespace",
		},
		{
			name:           "mixed root array",
			data:           `root = ["/one", 2]`,
			reasonContains: "item 1 must be a string",
		},
		{
			name: "root integer",
			data: `root = 1`,
		},
		{
			name: "root float",
			data: `root = 1.5`,
		},
		{
			name: "root boolean",
			data: `root = true`,
		},
		{
			name: "root datetime",
			data: `root = 1979-05-27T07:32:00Z`,
		},
		{
			name: "root inline table",
			data: `root = { path = "/one" }`,
		},
		{
			name: "root table",
			data: `
[root]
path = "/one"
`,
		},
		{
			name: "root nested array",
			data: `root = [["/one"]]`,
		},
		{
			name:           "empty worktree root",
			data:           `worktree_root = ""`,
			reasonContains: "must not be empty",
		},
		{
			name:           "whitespace worktree root",
			data:           `worktree_root = " \t "`,
			reasonContains: "only whitespace",
		},
		{
			name: "worktree root array",
			data: `worktree_root = ["/worktrees"]`,
		},
		{
			name: "worktree root integer",
			data: `worktree_root = 1`,
		},
		{
			name: "worktree root boolean",
			data: `worktree_root = false`,
		},
		{
			name: "worktree root table",
			data: `
[worktree_root]
path = "/worktrees"
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".config", "ghqw", "config.toml")
			var reads atomic.Int32
			loader := NewLoaderWithOptions(LoaderOptions{
				LookupEnv: emptyEnvironment,
				HomeDir: func() (string, error) {
					return home, nil
				},
				ReadFile: func(string) ([]byte, error) {
					reads.Add(1)
					return []byte(test.data), nil
				},
			})

			_, err := loader.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want invalid configuration")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("errors.Is(error, ErrInvalid) = false: %v", err)
			}

			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("error type = %T, want *InvalidError", err)
			}
			if invalid.Path() != path {
				t.Fatalf("InvalidError.Path() = %q, want %q", invalid.Path(), path)
			}
			if invalid.Reason() == "" {
				t.Fatal("InvalidError.Reason() is empty")
			}
			if test.reasonContains != "" && !strings.Contains(invalid.Reason(), test.reasonContains) {
				t.Fatalf("reason = %q, want substring %q", invalid.Reason(), test.reasonContains)
			}
			if !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Fatalf("error does not name path %q: %v", path, err)
			}
			if strings.Contains(err.Error(), "super-secret-value") {
				t.Fatalf("error exposed configuration value: %v", err)
			}

			_, secondErr := loader.Load()
			if !errors.Is(secondErr, ErrInvalid) {
				t.Fatalf("second Load() error = %v, want cached invalid error", secondErr)
			}
			if reads.Load() != 1 {
				t.Fatalf("read count = %d, want 1", reads.Load())
			}
		})
	}
}

func TestLoaderPreservesTOMLParseError(t *testing.T) {
	home := t.TempDir()
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(string) ([]byte, error) {
			return []byte("root = ["), nil
		},
	})

	_, err := loader.Load()
	var parseError toml.ParseError
	if !errors.As(err, &parseError) {
		t.Fatalf("error does not preserve toml.ParseError: %v", err)
	}
}

func TestLoaderKeepsReadErrorsAsRuntimeErrors(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "ghqw", "config.toml")
	readError := &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	var reads atomic.Int32
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(string) ([]byte, error) {
			reads.Add(1)
			return nil, readError
		},
	})

	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want read failure")
	}
	if errors.Is(err, ErrInvalid) {
		t.Fatalf("read error was classified as invalid configuration: %v", err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error does not preserve permission failure: %v", err)
	}
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("error does not preserve *os.PathError: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error does not name path %q: %v", path, err)
	}

	if _, secondErr := loader.Load(); !errors.Is(secondErr, fs.ErrPermission) {
		t.Fatalf("second Load() error = %v, want cached read failure", secondErr)
	}
	if reads.Load() != 1 {
		t.Fatalf("read count = %d, want 1", reads.Load())
	}
}

func TestLoaderRejectsInvalidHomeDirectories(t *testing.T) {
	unavailable := errors.New("home lookup failed")
	tests := []struct {
		name       string
		home       string
		homeError  error
		wantReason string
	}{
		{
			name:       "unavailable",
			homeError:  unavailable,
			wantReason: "home directory unavailable",
		},
		{
			name:       "empty",
			wantReason: "home directory is empty",
		},
		{
			name:       "relative",
			home:       filepath.Join("relative", "home"),
			wantReason: "is not absolute",
		},
		{
			name:       "tilde is not expanded",
			home:       "~",
			wantReason: "is not absolute",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var homeCalls atomic.Int32
			var reads atomic.Int32
			loader := NewLoaderWithOptions(LoaderOptions{
				LookupEnv: emptyEnvironment,
				HomeDir: func() (string, error) {
					homeCalls.Add(1)
					return test.home, test.homeError
				},
				ReadFile: func(string) ([]byte, error) {
					reads.Add(1)
					return nil, nil
				},
			})

			_, err := loader.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want home failure")
			}
			if errors.Is(err, ErrInvalid) {
				t.Fatalf("home error was classified as invalid configuration: %v", err)
			}
			if !strings.Contains(err.Error(), configDisplayPath) {
				t.Fatalf("error does not name %q: %v", configDisplayPath, err)
			}
			if !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("error = %q, want reason %q", err, test.wantReason)
			}
			if test.homeError != nil && !errors.Is(err, test.homeError) {
				t.Fatalf("error does not preserve home failure: %v", err)
			}

			if _, secondErr := loader.Load(); secondErr == nil {
				t.Fatal("second Load() error = nil, want cached home failure")
			}
			if homeCalls.Load() != 1 {
				t.Fatalf("home resolver calls = %d, want 1", homeCalls.Load())
			}
			if reads.Load() != 0 {
				t.Fatalf("read calls = %d, want 0", reads.Load())
			}
		})
	}
}

func TestLoaderReadsOnceUnderConcurrency(t *testing.T) {
	home := t.TempDir()
	var homeCalls atomic.Int32
	var reads atomic.Int32
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			homeCalls.Add(1)
			return home, nil
		},
		ReadFile: func(string) ([]byte, error) {
			if reads.Add(1) == 1 {
				close(readStarted)
			}
			<-releaseRead
			return []byte(`root = ["/one", "/two"]`), nil
		},
	})

	const callers = 64
	type result struct {
		config Config
		err    error
	}
	results := make(chan result, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			config, err := loader.Load()
			results <- result{config: config, err: err}
		}()
	}

	<-readStarted
	close(releaseRead)
	wait.Wait()
	close(results)

	want := Config{Roots: []string{"/one", "/two"}}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Load() error = %v", result.err)
		}
		if !reflect.DeepEqual(result.config, want) {
			t.Fatalf("concurrent Load() = %#v, want %#v", result.config, want)
		}
	}
	if homeCalls.Load() != 1 {
		t.Fatalf("home resolver calls = %d, want 1", homeCalls.Load())
	}
	if reads.Load() != 1 {
		t.Fatalf("read calls = %d, want 1", reads.Load())
	}
}

func TestLoaderReturnsDefensiveRootCopies(t *testing.T) {
	home := t.TempDir()
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(string) ([]byte, error) {
			return []byte(`root = ["/one", "/two"]`), nil
		},
	})

	first, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	first.Roots[0] = "/mutated"
	first.Roots = append(first.Roots, "/three")
	second.Roots[1] = "/also-mutated"

	third, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/one", "/two"}
	if !reflect.DeepEqual(third.Roots, want) {
		t.Fatalf("Load() after caller mutations returned %#v, want %#v", third.Roots, want)
	}
}

func TestLoaderUsesXDGConfigHomeWhenAbsolute(t *testing.T) {
	xdgConfigHome := t.TempDir()
	lookupEnv := func(name string) (string, bool) {
		if name == "XDG_CONFIG_HOME" {
			return xdgConfigHome, true
		}
		return "", false
	}
	failHomeDir := func() (string, error) {
		t.Fatal("HomeDir() called despite an absolute XDG_CONFIG_HOME")
		return "", nil
	}
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: lookupEnv,
		HomeDir:   failHomeDir,
		ReadFile: func(string) ([]byte, error) {
			return nil, &os.PathError{Op: "open", Err: fs.ErrNotExist}
		},
	})

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	path, err := DefaultPath(lookupEnv, failHomeDir)
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	want := filepath.Join(xdgConfigHome, "ghqw", "config.toml")
	if path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}
}

func TestLoaderTreatsARelativeXDGConfigHomeAsUnset(t *testing.T) {
	home := t.TempDir()
	var gotPath string
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: func(name string) (string, bool) {
			if name == "XDG_CONFIG_HOME" {
				return "relative/config", true
			}
			return "", false
		},
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(path string) ([]byte, error) {
			gotPath = path
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		},
	})

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := filepath.Join(home, ".config", "ghqw", "config.toml")
	if gotPath != want {
		t.Fatalf("read path = %q, want fallback %q for a relative XDG_CONFIG_HOME", gotPath, want)
	}
}

func TestLoaderTreatsAnEmptyXDGConfigHomeAsUnset(t *testing.T) {
	home := t.TempDir()
	var gotPath string
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: func(name string) (string, bool) {
			if name == "XDG_CONFIG_HOME" {
				return "", true
			}
			return "", false
		},
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(path string) ([]byte, error) {
			gotPath = path
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		},
	})

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := filepath.Join(home, ".config", "ghqw", "config.toml")
	if gotPath != want {
		t.Fatalf("read path = %q, want fallback %q for an empty XDG_CONFIG_HOME", gotPath, want)
	}
}

func TestLoaderFallsBackToHomeConfigDirWhenXDGConfigHomeIsUnset(t *testing.T) {
	home := t.TempDir()
	var gotPath string
	loader := NewLoaderWithOptions(LoaderOptions{
		LookupEnv: emptyEnvironment,
		HomeDir: func() (string, error) {
			return home, nil
		},
		ReadFile: func(path string) ([]byte, error) {
			gotPath = path
			return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		},
	})

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := filepath.Join(home, ".config", "ghqw", "config.toml")
	if gotPath != want {
		t.Fatalf("read path = %q, want fallback %q", gotPath, want)
	}
}

func emptyEnvironment(string) (string, bool) {
	return "", false
}
