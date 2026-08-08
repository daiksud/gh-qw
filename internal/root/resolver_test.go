package root

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/daiksud/gh-qw/internal/config"
)

type loaderFunc func() (config.Config, error)

func (f loaderFunc) Load() (config.Config, error) {
	return f()
}

func TestResolverPrecedence(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{physicalJoin(t, home, "ghqw")},
			WorktreeRoot:    physicalJoin(t, home, ".local", "share", "ghqw", "worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
		if got.Primary() != want.RepositoryRoots[0] {
			t.Fatalf("Primary() = %q, want %q", got.Primary(), want.RepositoryRoots[0])
		}

		assertDoesNotExist(t, filepath.Join(home, "ghqw"))
		assertDoesNotExist(t, filepath.Join(home, ".local"))
		assertDirectory(t, base)
	})

	t.Run("file configuration", func(t *testing.T) {
		base, home := newLayout(t)
		repositoryOne := filepath.Join(base, "configured-one")
		repositoryTwo := filepath.Join(base, "configured-two")
		worktrees := filepath.Join(base, "configured-worktrees")
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{repositoryOne, repositoryTwo},
			WorktreeRoot: worktrees,
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{
				physicalJoin(t, base, "configured-one"),
				physicalJoin(t, base, "configured-two"),
			},
			WorktreeRoot: physicalJoin(t, base, "configured-worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
	})

	t.Run("repository environment replaces file roots", func(t *testing.T) {
		base, home := newLayout(t)
		environmentOne := filepath.Join(base, "environment-one")
		environmentTwo := filepath.Join(base, "environment-two")
		configuredWorktrees := filepath.Join(base, "configured-worktrees")
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filepath.Join(base, "configured-one"), filepath.Join(base, "configured-two")},
			WorktreeRoot: configuredWorktrees,
		}, map[string]string{
			repositoryRootEnvironment: strings.Join(
				[]string{environmentOne, environmentTwo},
				string(os.PathListSeparator),
			),
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{
				physicalJoin(t, base, "environment-one"),
				physicalJoin(t, base, "environment-two"),
			},
			WorktreeRoot: physicalJoin(t, base, "configured-worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
	})

	t.Run("worktree environment overrides only worktree setting", func(t *testing.T) {
		base, home := newLayout(t)
		configuredRepository := filepath.Join(base, "configured-repository")
		environmentWorktrees := filepath.Join(base, "environment-worktrees")
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{configuredRepository},
			WorktreeRoot: filepath.Join(base, "configured-worktrees"),
		}, map[string]string{
			worktreeRootEnvironment: environmentWorktrees,
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{physicalJoin(t, base, "configured-repository")},
			WorktreeRoot:    physicalJoin(t, base, "environment-worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
	})

	t.Run("empty environment values are ignored", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filepath.Join(base, "configured-repository")},
			WorktreeRoot: filepath.Join(base, "configured-worktrees"),
		}, map[string]string{
			repositoryRootEnvironment: "",
			worktreeRootEnvironment:   "",
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{physicalJoin(t, base, "configured-repository")},
			WorktreeRoot:    physicalJoin(t, base, "configured-worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
	})

	t.Run("worktree environment remains one path", func(t *testing.T) {
		base, home := newLayout(t)
		rawWorktreeRoot := filepath.Join(
			base,
			"worktrees"+string(os.PathListSeparator)+"archive",
		)
		resolver, _ := newTestResolver(home, config.Config{
			Roots: []string{filepath.Join(base, "repositories")},
		}, map[string]string{
			worktreeRootEnvironment: rawWorktreeRoot,
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(
			t,
			base,
			"worktrees"+string(os.PathListSeparator)+"archive",
		)
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want unsplit path %q", got.WorktreeRoot, want)
		}
	})
}

func TestResolverHerdrPrecedence(t *testing.T) {
	t.Run("defaults to false", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if got.Herdr {
			t.Fatal("Herdr = true, want false")
		}
	})

	t.Run("configuration file enables it", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{Herdr: true}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !got.Herdr {
			t.Fatal("Herdr = false, want true")
		}
	})

	t.Run("environment overrides configuration file", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{Herdr: true}, map[string]string{
			herdrEnvironment: "0",
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if got.Herdr {
			t.Fatal("Herdr = true, want false from GHQW_HERDR=0 overriding configuration")
		}
	})

	t.Run("empty environment value is ignored", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{Herdr: true}, map[string]string{
			herdrEnvironment: "",
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !got.Herdr {
			t.Fatal("Herdr = false, want true (empty GHQW_HERDR falls back to configuration)")
		}
	})

	for _, value := range []string{"1", "true", "TRUE", "Yes", "on"} {
		t.Run("environment truthy "+value, func(t *testing.T) {
			_, home := newLayout(t)
			resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
				herdrEnvironment: value,
			})

			got, err := resolver.Resolve()
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if !got.Herdr {
				t.Fatalf("Herdr = false for GHQW_HERDR=%q, want true", value)
			}
		})
	}

	for _, value := range []string{"0", "false", "FALSE", "No", "off"} {
		t.Run("environment falsy "+value, func(t *testing.T) {
			_, home := newLayout(t)
			resolver, _ := newTestResolver(home, config.Config{Herdr: true}, map[string]string{
				herdrEnvironment: value,
			})

			got, err := resolver.Resolve()
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.Herdr {
				t.Fatalf("Herdr = true for GHQW_HERDR=%q, want false", value)
			}
		})
	}

	t.Run("unrecognized environment value is a configuration error", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
			herdrEnvironment: "maybe",
		})

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Herdr)
	})
}

func TestResolverDefaultWorktreeRootHonorsXDGDataHome(t *testing.T) {
	t.Run("absolute XDG_DATA_HOME is adopted", func(t *testing.T) {
		base, home := newLayout(t)
		xdgDataHome := filepath.Join(base, "custom-data")
		resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
			"XDG_DATA_HOME": xdgDataHome,
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(t, base, "custom-data", "ghqw", "worktrees")
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want %q", got.WorktreeRoot, want)
		}
	})

	t.Run("relative XDG_DATA_HOME is ignored", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
			"XDG_DATA_HOME": filepath.Join("relative", "data"),
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(t, home, ".local", "share", "ghqw", "worktrees")
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want fallback %q for a relative XDG_DATA_HOME", got.WorktreeRoot, want)
		}
	})

	t.Run("empty XDG_DATA_HOME is ignored", func(t *testing.T) {
		_, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
			"XDG_DATA_HOME": "",
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(t, home, ".local", "share", "ghqw", "worktrees")
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want fallback %q for an empty XDG_DATA_HOME", got.WorktreeRoot, want)
		}
	})

	t.Run("GHQW_WORKTREE_ROOT still outranks XDG_DATA_HOME", func(t *testing.T) {
		base, home := newLayout(t)
		environmentWorktrees := filepath.Join(base, "environment-worktrees")
		resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
			"XDG_DATA_HOME":         filepath.Join(base, "custom-data"),
			worktreeRootEnvironment: environmentWorktrees,
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(t, base, "environment-worktrees")
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want GHQW_WORKTREE_ROOT %q to outrank XDG_DATA_HOME", got.WorktreeRoot, want)
		}
	})

	t.Run("file configuration worktree_root still outranks XDG_DATA_HOME", func(t *testing.T) {
		base, home := newLayout(t)
		configuredWorktrees := filepath.Join(base, "configured-worktrees")
		resolver, _ := newTestResolver(home, config.Config{
			WorktreeRoot: configuredWorktrees,
		}, map[string]string{
			"XDG_DATA_HOME": filepath.Join(base, "custom-data"),
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := physicalJoin(t, base, "configured-worktrees")
		if got.WorktreeRoot != want {
			t.Fatalf("WorktreeRoot = %q, want configured worktree_root %q to outrank XDG_DATA_HOME", got.WorktreeRoot, want)
		}
	})
}

func TestResolverRejectsEmptyRepositoryEnvironmentEntries(t *testing.T) {
	separator := string(os.PathListSeparator)
	tests := []struct {
		name  string
		value func(string, string) string
	}{
		{
			name: "leading",
			value: func(first, _ string) string {
				return separator + first
			},
		},
		{
			name: "middle",
			value: func(first, second string) string {
				return first + separator + separator + second
			},
		},
		{
			name: "trailing",
			value: func(first, _ string) string {
				return first + separator
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, home := newLayout(t)
			first := filepath.Join(base, "first")
			second := filepath.Join(base, "second")
			resolver, _ := newTestResolver(home, config.Config{}, map[string]string{
				repositoryRootEnvironment: test.value(first, second),
			})

			_, err := resolver.Resolve()
			assertInvalidRoot(t, err, Repository)
			if !strings.Contains(err.Error(), "empty path-list entry") {
				t.Fatalf("Resolve() error = %v, want empty-entry reason", err)
			}
		})
	}
}

func TestResolverTildeExpansionAndAbsoluteValidation(t *testing.T) {
	t.Run("exact home repository root", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{"~"},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if got.Primary() != mustEvalSymlinks(t, home) {
			t.Fatalf("Primary() = %q, want expanded home %q", got.Primary(), mustEvalSymlinks(t, home))
		}
	})

	t.Run("slash home prefixes", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{"~/repositories"},
			WorktreeRoot: "~/.ghqw/worktrees",
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := Result{
			RepositoryRoots: []string{physicalJoin(t, home, "repositories")},
			WorktreeRoot:    physicalJoin(t, home, ".ghqw", "worktrees"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Resolve() = %#v, want %#v", got, want)
		}
		assertDirectory(t, base)
	})

	if runtime.GOOS == "windows" {
		t.Run("native backslash home prefix", func(t *testing.T) {
			base, home := newLayout(t)
			resolver, _ := newTestResolver(home, config.Config{
				Roots:        []string{`~\repositories`},
				WorktreeRoot: filepath.Join(base, "worktrees"),
			}, nil)

			got, err := resolver.Resolve()
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.Primary() != physicalJoin(t, home, "repositories") {
				t.Fatalf("Primary() = %q, want native tilde expansion", got.Primary())
			}
		})
	} else {
		t.Run("non-native backslash home prefix is rejected", func(t *testing.T) {
			base, home := newLayout(t)
			resolver, _ := newTestResolver(home, config.Config{
				Roots:        []string{`~\repositories`},
				WorktreeRoot: filepath.Join(base, "worktrees"),
			}, nil)

			_, err := resolver.Resolve()
			assertInvalidRoot(t, err, Repository)
		})
	}

	tests := []struct {
		name   string
		config func(string) config.Config
		kind   Kind
	}{
		{
			name: "named user",
			config: func(base string) config.Config {
				return config.Config{
					Roots:        []string{"~someone/repositories"},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}
			},
			kind: Repository,
		},
		{
			name: "relative repository root",
			config: func(base string) config.Config {
				return config.Config{
					Roots:        []string{"relative/repositories"},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}
			},
			kind: Repository,
		},
		{
			name: "relative worktree root",
			config: func(base string) config.Config {
				return config.Config{
					Roots:        []string{filepath.Join(base, "repositories")},
					WorktreeRoot: "relative/worktrees",
				}
			},
			kind: Worktree,
		},
		{
			name: "empty repository root",
			config: func(base string) config.Config {
				return config.Config{
					Roots:        []string{""},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}
			},
			kind: Repository,
		},
		{
			name: "whitespace worktree root",
			config: func(base string) config.Config {
				return config.Config{
					Roots:        []string{filepath.Join(base, "repositories")},
					WorktreeRoot: " \t ",
				}
			},
			kind: Worktree,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, home := newLayout(t)
			resolver, _ := newTestResolver(home, test.config(base), nil)
			_, err := resolver.Resolve()
			assertInvalidRoot(t, err, test.kind)
		})
	}

	t.Run("home lookup failure remains runtime error", func(t *testing.T) {
		base := t.TempDir()
		homeError := errors.New("home lookup failed")
		resolver := NewResolverWithOptions(Options{
			ConfigLoader: loaderFunc(func() (config.Config, error) {
				return config.Config{
					Roots:        []string{"~/repositories"},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}, nil
			}),
			LookupEnv: emptyEnvironment,
			HomeDir: func() (string, error) {
				return "", homeError
			},
		})

		_, err := resolver.Resolve()
		if !errors.Is(err, homeError) {
			t.Fatalf("Resolve() error = %v, want home error", err)
		}
		if errors.Is(err, ErrInvalidRoot) {
			t.Fatalf("Resolve() error = %v, must remain a runtime error", err)
		}
	})
}

func TestResolverPhysicalizesMissingSuffixAndSymlinks(t *testing.T) {
	t.Run("cleans and appends a missing suffix", func(t *testing.T) {
		base, home := newLayout(t)
		ancestor := filepath.Join(base, "existing")
		mustMkdirAll(t, ancestor)
		separator := string(filepath.Separator)
		rawRoot := ancestor + separator + "future" + separator + "." +
			separator + "nested" + separator + ".." + separator + "repository"
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{rawRoot},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := filepath.Join(mustEvalSymlinks(t, ancestor), "future", "repository")
		if got.Primary() != want {
			t.Fatalf("Primary() = %q, want %q", got.Primary(), want)
		}
		assertDoesNotExist(t, filepath.Join(ancestor, "future"))
	})

	t.Run("resolves final directory symlink", func(t *testing.T) {
		base, home := newLayout(t)
		realRoot := filepath.Join(base, "real-repository")
		aliasRoot := filepath.Join(base, "repository-alias")
		mustMkdirAll(t, realRoot)
		mustSymlink(t, realRoot, aliasRoot)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{aliasRoot},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if got.Primary() != mustEvalSymlinks(t, realRoot) {
			t.Fatalf("Primary() = %q, want final symlink target %q", got.Primary(), realRoot)
		}
	})

	t.Run("resolves intermediate symlink before missing suffix", func(t *testing.T) {
		base, home := newLayout(t)
		realParent := filepath.Join(base, "real-parent")
		aliasParent := filepath.Join(base, "parent-alias")
		mustMkdirAll(t, realParent)
		mustSymlink(t, realParent, aliasParent)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filepath.Join(aliasParent, "future", "repository")},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := filepath.Join(mustEvalSymlinks(t, realParent), "future", "repository")
		if got.Primary() != want {
			t.Fatalf("Primary() = %q, want %q", got.Primary(), want)
		}
	})
}

func TestResolverRejectsInvalidExistingPathShapes(t *testing.T) {
	t.Run("existing final file", func(t *testing.T) {
		base, home := newLayout(t)
		filePath := filepath.Join(base, "repository-file")
		mustWriteFile(t, filePath)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filePath},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Repository)
	})

	t.Run("existing intermediate file", func(t *testing.T) {
		base, home := newLayout(t)
		filePath := filepath.Join(base, "repository-file")
		mustWriteFile(t, filePath)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filepath.Join(filePath, "child")},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Repository)
	})

	t.Run("final symlink to file", func(t *testing.T) {
		base, home := newLayout(t)
		filePath := filepath.Join(base, "repository-file")
		aliasPath := filepath.Join(base, "repository-alias")
		mustWriteFile(t, filePath)
		mustSymlink(t, filePath, aliasPath)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{aliasPath},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Repository)
	})

	t.Run("dangling symlink", func(t *testing.T) {
		base, home := newLayout(t)
		aliasPath := filepath.Join(base, "dangling")
		mustSymlink(t, filepath.Join(base, "missing-target"), aliasPath)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{aliasPath},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Repository)
		if !strings.Contains(err.Error(), "dangling symbolic link") {
			t.Fatalf("Resolve() error = %v, want dangling-symlink reason", err)
		}
	})

	t.Run("filesystem permission error remains runtime error", func(t *testing.T) {
		base, home := newLayout(t)
		deniedPath := filepath.Join(base, "denied")
		resolver := NewResolverWithOptions(Options{
			ConfigLoader: loaderFunc(func() (config.Config, error) {
				return config.Config{
					Roots:        []string{deniedPath},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}, nil
			}),
			LookupEnv: emptyEnvironment,
			HomeDir: func() (string, error) {
				return home, nil
			},
			Lstat: func(path string) (fs.FileInfo, error) {
				if path == deniedPath {
					return nil, &os.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
				}
				return os.Lstat(path)
			},
		})

		_, err := resolver.Resolve()
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("Resolve() error = %v, want permission error", err)
		}
		if errors.Is(err, ErrInvalidRoot) {
			t.Fatalf("Resolve() error = %v, must remain a runtime error", err)
		}
	})
}

func TestResolverDeduplicatesEquivalentRootsInOrder(t *testing.T) {
	t.Run("cleaned lexical duplicates", func(t *testing.T) {
		base, home := newLayout(t)
		first := filepath.Join(base, "repositories")
		separator := string(filepath.Separator)
		duplicate := first + separator + "child" + separator + ".."
		second := filepath.Join(base, "archive")
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{first, duplicate, second},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := []string{
			physicalJoin(t, base, "repositories"),
			physicalJoin(t, base, "archive"),
		}
		if !reflect.DeepEqual(got.RepositoryRoots, want) {
			t.Fatalf("RepositoryRoots = %#v, want %#v", got.RepositoryRoots, want)
		}
	})

	t.Run("final symlink aliases", func(t *testing.T) {
		base, home := newLayout(t)
		realRoot := filepath.Join(base, "real-repository")
		aliasRoot := filepath.Join(base, "repository-alias")
		otherRoot := filepath.Join(base, "other-repository")
		mustMkdirAll(t, realRoot)
		mustSymlink(t, realRoot, aliasRoot)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{aliasRoot, realRoot, otherRoot},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := []string{
			mustEvalSymlinks(t, realRoot),
			physicalJoin(t, base, "other-repository"),
		}
		if !reflect.DeepEqual(got.RepositoryRoots, want) {
			t.Fatalf("RepositoryRoots = %#v, want %#v", got.RepositoryRoots, want)
		}
	})

	t.Run("intermediate symlink aliases with missing suffix", func(t *testing.T) {
		base, home := newLayout(t)
		realParent := filepath.Join(base, "real-parent")
		aliasParent := filepath.Join(base, "parent-alias")
		mustMkdirAll(t, realParent)
		mustSymlink(t, realParent, aliasParent)
		resolver, _ := newTestResolver(home, config.Config{
			Roots: []string{
				filepath.Join(aliasParent, "future"),
				filepath.Join(realParent, "future"),
			},
			WorktreeRoot: filepath.Join(base, "worktrees"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		want := []string{filepath.Join(mustEvalSymlinks(t, realParent), "future")}
		if !reflect.DeepEqual(got.RepositoryRoots, want) {
			t.Fatalf("RepositoryRoots = %#v, want %#v", got.RepositoryRoots, want)
		}
	})

	t.Run("SameFile seam handles physical aliases", func(t *testing.T) {
		base, home := newLayout(t)
		first := filepath.Join(base, "first")
		second := filepath.Join(base, "second")
		mustMkdirAll(t, first)
		mustMkdirAll(t, second)
		var sameFileCalls atomic.Int32
		resolver := NewResolverWithOptions(Options{
			ConfigLoader: loaderFunc(func() (config.Config, error) {
				return config.Config{
					Roots:        []string{first, second},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}, nil
			}),
			LookupEnv: emptyEnvironment,
			HomeDir: func() (string, error) {
				return home, nil
			},
			SameFile: func(left, right fs.FileInfo) bool {
				sameFileCalls.Add(1)
				if (left.Name() == "first" && right.Name() == "second") ||
					(left.Name() == "second" && right.Name() == "first") {
					return true
				}
				return os.SameFile(left, right)
			},
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if len(got.RepositoryRoots) != 1 {
			t.Fatalf("RepositoryRoots = %#v, want one SameFile-equivalent root", got.RepositoryRoots)
		}
		if sameFileCalls.Load() == 0 {
			t.Fatal("SameFile seam was not used")
		}
	})

	t.Run("SameFile aliases preserve equivalence for missing suffixes", func(t *testing.T) {
		base, home := newLayout(t)
		first := filepath.Join(base, "first")
		second := filepath.Join(base, "second")
		mustMkdirAll(t, first)
		mustMkdirAll(t, second)
		resolver := NewResolverWithOptions(Options{
			ConfigLoader: loaderFunc(func() (config.Config, error) {
				return config.Config{
					Roots: []string{
						filepath.Join(first, "future"),
						filepath.Join(second, "future"),
					},
					WorktreeRoot: filepath.Join(base, "worktrees"),
				}, nil
			}),
			LookupEnv: emptyEnvironment,
			HomeDir: func() (string, error) {
				return home, nil
			},
			SameFile: sameNamedDirectories("first", "second"),
		})

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if len(got.RepositoryRoots) != 1 {
			t.Fatalf("RepositoryRoots = %#v, want one equivalent future root", got.RepositoryRoots)
		}
	})
}

func TestResolverRootContainmentValidation(t *testing.T) {
	tests := []struct {
		name       string
		repository func(string) string
		worktree   func(string) string
	}{
		{
			name: "equal",
			repository: func(base string) string {
				return filepath.Join(base, "shared")
			},
			worktree: func(base string) string {
				return filepath.Join(base, "shared")
			},
		},
		{
			name: "worktree inside repository",
			repository: func(base string) string {
				return filepath.Join(base, "repositories")
			},
			worktree: func(base string) string {
				return filepath.Join(base, "repositories", "worktrees")
			},
		},
		{
			name: "repository inside worktree",
			repository: func(base string) string {
				return filepath.Join(base, "worktrees", "repositories")
			},
			worktree: func(base string) string {
				return filepath.Join(base, "worktrees")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, home := newLayout(t)
			resolver, _ := newTestResolver(home, config.Config{
				Roots:        []string{test.repository(base)},
				WorktreeRoot: test.worktree(base),
			}, nil)

			_, err := resolver.Resolve()
			assertInvalidRoot(t, err, Worktree)
			if !strings.Contains(err.Error(), "must be disjoint") {
				t.Fatalf("Resolve() error = %v, want disjointness reason", err)
			}
		})
	}

	t.Run("overlap with any repository root", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{
			Roots: []string{
				filepath.Join(base, "primary"),
				filepath.Join(base, "secondary"),
			},
			WorktreeRoot: filepath.Join(base, "secondary", "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Worktree)
	})

	t.Run("physical symlink overlap", func(t *testing.T) {
		base, home := newLayout(t)
		repositoryRoot := filepath.Join(base, "repositories")
		repositoryAlias := filepath.Join(base, "repository-alias")
		mustMkdirAll(t, repositoryRoot)
		mustSymlink(t, repositoryRoot, repositoryAlias)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{repositoryRoot},
			WorktreeRoot: filepath.Join(repositoryAlias, "worktrees"),
		}, nil)

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Worktree)
	})

	t.Run("component prefix is not containment", func(t *testing.T) {
		base, home := newLayout(t)
		resolver, _ := newTestResolver(home, config.Config{
			Roots:        []string{filepath.Join(base, "repos")},
			WorktreeRoot: filepath.Join(base, "repos-other"),
		}, nil)

		got, err := resolver.Resolve()
		if err != nil {
			t.Fatalf("Resolve() error = %v for component-prefix siblings", err)
		}
		if got.Primary() == got.WorktreeRoot {
			t.Fatalf("resolved sibling roots unexpectedly equal: %#v", got)
		}
	})

	t.Run("SameFile aliases detect overlap through missing suffixes", func(t *testing.T) {
		base, home := newLayout(t)
		first := filepath.Join(base, "first")
		second := filepath.Join(base, "second")
		mustMkdirAll(t, first)
		mustMkdirAll(t, second)
		resolver := NewResolverWithOptions(Options{
			ConfigLoader: loaderFunc(func() (config.Config, error) {
				return config.Config{
					Roots:        []string{filepath.Join(first, "future")},
					WorktreeRoot: filepath.Join(second, "future", "worktrees"),
				}, nil
			}),
			LookupEnv: emptyEnvironment,
			HomeDir: func() (string, error) {
				return home, nil
			},
			SameFile: sameNamedDirectories("first", "second"),
		})

		_, err := resolver.Resolve()
		assertInvalidRoot(t, err, Worktree)
	})
}

func TestResolverPlatformCaseBehavior(t *testing.T) {
	base, home := newLayout(t)
	canonical := filepath.Join(base, "CaseProbeRepository")
	alternate := filepath.Join(base, "cASEpROBERepository")
	mustMkdirAll(t, canonical)

	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", canonical, err)
	}
	alternateInfo, alternateErr := os.Stat(alternate)
	caseInsensitive := alternateErr == nil && os.SameFile(canonicalInfo, alternateInfo)

	resolver, _ := newTestResolver(home, config.Config{
		Roots:        []string{canonical, alternate},
		WorktreeRoot: filepath.Join(base, "worktrees"),
	}, nil)
	got, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	wantCount := 2
	if runtime.GOOS == "windows" || caseInsensitive {
		wantCount = 1
	}
	if len(got.RepositoryRoots) != wantCount {
		t.Fatalf(
			"RepositoryRoots = %#v, want %d root(s) for filesystem case behavior",
			got.RepositoryRoots,
			wantCount,
		)
	}

	if runtime.GOOS == "windows" {
		relation := resolver.lexicalRelationship(`C:\repos`, `D:\repos\child`)
		if relation != relationDisjoint {
			t.Fatalf("cross-volume relation = %v, want disjoint", relation)
		}
	}
}

func TestResolverCachesConcurrentResolutionAndReturnsDefensiveCopies(t *testing.T) {
	_, home := newLayout(t)
	var loadCalls atomic.Int32
	var lookupCalls atomic.Int32
	var homeCalls atomic.Int32
	resolver := NewResolverWithOptions(Options{
		ConfigLoader: loaderFunc(func() (config.Config, error) {
			loadCalls.Add(1)
			return config.Config{
				Roots:        []string{"~/repositories"},
				WorktreeRoot: "~/.ghqw/worktrees",
			}, nil
		}),
		LookupEnv: func(string) (string, bool) {
			lookupCalls.Add(1)
			return "", false
		},
		HomeDir: func() (string, error) {
			homeCalls.Add(1)
			return home, nil
		},
	})

	const goroutines = 64
	start := make(chan struct{})
	results := make(chan Result, goroutines)
	errs := make(chan error, goroutines)
	var waitGroup sync.WaitGroup
	for range goroutines {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := resolver.Resolve()
			results <- result
			errs <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Resolve() error = %v", err)
		}
	}
	want := Result{
		RepositoryRoots: []string{physicalJoin(t, home, "repositories")},
		WorktreeRoot:    physicalJoin(t, home, ".ghqw", "worktrees"),
	}
	for result := range results {
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("concurrent Resolve() = %#v, want %#v", result, want)
		}
	}
	if loadCalls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", loadCalls.Load())
	}
	if lookupCalls.Load() != 3 {
		t.Fatalf("environment lookups = %d, want 3", lookupCalls.Load())
	}
	if homeCalls.Load() != 1 {
		t.Fatalf("home lookups = %d, want 1", homeCalls.Load())
	}

	first, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	first.RepositoryRoots[0] = "mutated"
	first.RepositoryRoots = append(first.RepositoryRoots, "extra")

	second, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("second Resolve() = %#v after caller mutation, want %#v", second, want)
	}
}

func TestResolverPreservesConfigurationLoaderErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "runtime loader error",
			err:  fs.ErrPermission,
		},
		{
			name: "invalid configuration sentinel",
			err:  config.ErrInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			resolver := NewResolverWithOptions(Options{
				ConfigLoader: loaderFunc(func() (config.Config, error) {
					calls.Add(1)
					return config.Config{}, test.err
				}),
				LookupEnv: emptyEnvironment,
			})

			_, err := resolver.Resolve()
			if !errors.Is(err, test.err) {
				t.Fatalf("Resolve() error = %v, want errors.Is(..., %v)", err, test.err)
			}
			if _, secondErr := resolver.Resolve(); !errors.Is(secondErr, test.err) {
				t.Fatalf("second Resolve() error = %v, want cached loader error", secondErr)
			}
			if calls.Load() != 1 {
				t.Fatalf("loader calls = %d, want 1", calls.Load())
			}
		})
	}
}

func newTestResolver(
	home string,
	fileConfig config.Config,
	environment map[string]string,
) (*Resolver, *atomic.Int32) {
	loadCalls := &atomic.Int32{}
	resolver := NewResolverWithOptions(Options{
		ConfigLoader: loaderFunc(func() (config.Config, error) {
			loadCalls.Add(1)
			result := fileConfig
			result.Roots = append([]string(nil), fileConfig.Roots...)
			return result, nil
		}),
		LookupEnv: func(name string) (string, bool) {
			value, ok := environment[name]
			return value, ok
		},
		HomeDir: func() (string, error) {
			return home, nil
		},
	})
	return resolver, loadCalls
}

func emptyEnvironment(string) (string, bool) {
	return "", false
}

func sameNamedDirectories(first, second string) func(fs.FileInfo, fs.FileInfo) bool {
	return func(left, right fs.FileInfo) bool {
		if (left.Name() == first && right.Name() == second) ||
			(left.Name() == second && right.Name() == first) {
			return true
		}
		return os.SameFile(left, right)
	}
}

func newLayout(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	mustMkdirAll(t, home)
	return base, home
}

func physicalJoin(t *testing.T, existingAncestor string, suffix ...string) string {
	t.Helper()
	parts := append([]string{mustEvalSymlinks(t, existingAncestor)}, suffix...)
	return filepath.Join(parts...)
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	result, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return filepath.Clean(result)
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		t.Fatalf("Symlink(%q, %q) error = %v", target, link, err)
	}
}

func assertDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want not exist", path, err)
	}
}

func assertDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", path)
	}
}

func assertInvalidRoot(t *testing.T, err error, kind Kind) {
	t.Helper()
	if err == nil {
		t.Fatal("Resolve() error = nil, want invalid root")
	}
	if !errors.Is(err, ErrInvalidRoot) {
		t.Fatalf("Resolve() error = %v, want errors.Is(..., ErrInvalidRoot)", err)
	}
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("Resolve() error = %v, want configuration classification", err)
	}
	var invalidError *InvalidError
	if !errors.As(err, &invalidError) {
		t.Fatalf("Resolve() error type = %T, want *InvalidError", err)
	}
	if invalidError.Kind() != kind {
		t.Fatalf("InvalidError.Kind() = %q, want %q", invalidError.Kind(), kind)
	}
	if invalidError.Reason() == "" {
		t.Fatal("InvalidError.Reason() is empty")
	}
}
