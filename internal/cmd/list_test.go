package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewListCommandMatchesQueries(t *testing.T) {
	t.Parallel()

	repositories := []local.Repository{
		listRepository("github.com/motemen/gore", listAbsolutePath("roots", "github.com", "motemen", "gore"), 0),
		listRepository("golang.org/x/image", listAbsolutePath("roots", "golang.org", "x", "image"), 0),
		listRepository("github.com/test/Awesome", listAbsolutePath("roots", "github.com", "test", "Awesome"), 0),
		listRepository("github.com/motemen/ghq", listAbsolutePath("roots", "github.com", "motemen", "ghq"), 0),
		listRepository("github.com/Songmu/gobump", listAbsolutePath("roots", "github.com", "Songmu", "gobump"), 0),
		listRepository("golang.org/x/crypt", listAbsolutePath("roots", "golang.org", "x", "crypt"), 0),
		listRepository("github.com/motemen/gobump", listAbsolutePath("roots", "github.com", "motemen", "gobump"), 0),
	}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "all sorted",
			want: []string{
				"github.com/Songmu/gobump",
				"github.com/motemen/ghq",
				"github.com/motemen/gobump",
				"github.com/motemen/gore",
				"github.com/test/Awesome",
				"golang.org/x/crypt",
				"golang.org/x/image",
			},
		},
		{name: "repository substring", args: []string{"ghq"}, want: []string{"github.com/motemen/ghq"}},
		{
			name: "owner repository substring",
			args: []string{"men/go"},
			want: []string{"github.com/motemen/gobump", "github.com/motemen/gore"},
		},
		{name: "host alone is non host query", args: []string{"github.com"}},
		{
			name: "host slash narrows",
			args: []string{"golang.org/"},
			want: []string{"golang.org/x/crypt", "golang.org/x/image"},
		},
		{
			name: "host and owner narrow",
			args: []string{"github.com/Songmu"},
			want: []string{"github.com/Songmu/gobump"},
		},
		{name: "uppercase host remains exact", args: []string{"GitHub.com/motemen"}},
		{
			name: "URL canonicalizes",
			args: []string{"https://github.com/motemen/ghq.git"},
			want: []string{"github.com/motemen/ghq"},
		},
		{
			name: "SCP URL canonicalizes",
			args: []string{"git@github.com:motemen/ghq.git"},
			want: []string{"github.com/motemen/ghq"},
		},
		{
			name: "lowercase is case insensitive",
			args: []string{"awesome"},
			want: []string{"github.com/test/Awesome"},
		},
		{
			name: "matching uppercase is case sensitive",
			args: []string{"Awesome"},
			want: []string{"github.com/test/Awesome"},
		},
		{name: "mixed case mismatch", args: []string{"aWesome"}},
		{name: "invalid URL is a plain no match", args: []string{"https://invalid"}},
		{name: "no match succeeds", args: []string{"not-present"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &listRootResolver{
				result: rootpkg.Result{
					RepositoryRoots: []string{listAbsolutePath("roots")},
					WorktreeRoot:    listAbsolutePath("worktrees"),
				},
			}
			discovery := &listDiscoveryRecorder{
				result: local.DiscoveryResult{Repositories: repositories},
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			command := cmd.NewListCommand(cmd.ListDependencies{
				Resolver:             resolver,
				DiscoverRepositories: discovery.listDiscover,
				Stdout:               &stdout,
				Stderr:               &stderr,
			})
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := stdout.String(), listSortedLines(test.want...); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if resolver.calls != 1 {
				t.Fatalf("Resolve() calls = %d, want 1", resolver.calls)
			}
			if discovery.calls != 1 {
				t.Fatalf("discovery calls = %d, want 1", discovery.calls)
			}
		})
	}
}

func TestNewListCommandExactMatching(t *testing.T) {
	t.Parallel()

	repositories := []local.Repository{
		listRepository("github.com/acme/widget", listAbsolutePath("roots", "github.com", "acme", "widget"), 0),
		listRepository("git.example.com/acme/widget", listAbsolutePath("roots", "git.example.com", "acme", "widget"), 0),
		listRepository("github.com/other/Widget", listAbsolutePath("roots", "github.com", "other", "Widget"), 0),
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "repository shorthand",
			args: []string{"-e", "widget"},
			want: []string{"git.example.com/acme/widget", "github.com/acme/widget"},
		},
		{
			name: "owner repository",
			args: []string{"--exact", "acme/widget"},
			want: []string{"git.example.com/acme/widget", "github.com/acme/widget"},
		},
		{
			name: "full identity",
			args: []string{"-e", "github.com/acme/widget"},
			want: []string{"github.com/acme/widget"},
		},
		{
			name: "URL identity",
			args: []string{"--exact", "https://github.com/acme/widget.git"},
			want: []string{"github.com/acme/widget"},
		},
		{name: "partial suffix rejected", args: []string{"-e", "me/widget"}},
		{name: "exact is case sensitive", args: []string{"-e", "Widget"}, want: []string{"github.com/other/Widget"}},
		{name: "lowercase does not fold", args: []string{"-e", "other/widget"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			command := cmd.NewListCommand(cmd.ListDependencies{
				Resolver: &listRootResolver{result: rootpkg.Result{
					RepositoryRoots: []string{listAbsolutePath("roots")},
					WorktreeRoot:    listAbsolutePath("worktrees"),
				}},
				DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
					return local.DiscoveryResult{Repositories: repositories}, nil
				},
				Stdout: &stdout,
				Stderr: io.Discard,
			})
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := stdout.String(), listSortedLines(test.want...); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestNewListCommandDeduplicatesByEarliestConfiguredRootAndPrintsPaths(t *testing.T) {
	t.Parallel()

	firstRoot := listAbsolutePath("roots", "first")
	secondRoot := listAbsolutePath("roots", "second")
	firstPath := filepath.Join(firstRoot, "github.com", "acme", "widget")
	firstDirtyPath := filepath.Join(firstRoot, "github.com", "acme", "nested") +
		string(filepath.Separator) + ".." + string(filepath.Separator) + "widget"
	secondPath := filepath.Join(secondRoot, "github.com", "acme", "widget")
	otherPath := filepath.Join(secondRoot, "github.com", "other", "api")
	repositories := []local.Repository{
		listRepository("github.com/acme/widget", secondPath, 1),
		listRepository("github.com/other/api", otherPath, 1),
		listRepository("github.com/acme/widget", firstDirtyPath, 0),
	}

	resolver := &listRootResolver{result: rootpkg.Result{
		RepositoryRoots: []string{firstRoot, secondRoot},
		WorktreeRoot:    listAbsolutePath("worktrees"),
	}}
	discovery := &listDiscoveryRecorder{
		result: local.DiscoveryResult{Repositories: repositories},
	}
	var stdout bytes.Buffer
	command := cmd.NewListCommand(cmd.ListDependencies{
		Resolver:             resolver,
		DiscoverRepositories: discovery.listDiscover,
		Stdout:               &stdout,
		Stderr:               io.Discard,
	})
	command.SetArgs([]string{"-p"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := stdout.String(), listSortedLines(
		local.NormalizePathForOutput(firstPath),
		local.NormalizePathForOutput(otherPath),
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := discovery.roots; len(got) != 2 || got[0] != firstRoot || got[1] != secondRoot {
		t.Fatalf("discovery roots = %#v, want [%q %q]", got, firstRoot, secondRoot)
	}
}

func TestNewListCommandUniqueSuffixesUseSelectedEntries(t *testing.T) {
	t.Parallel()

	repositories := []local.Repository{
		listRepository("github.com/acme/widget", listAbsolutePath("roots", "github.com", "acme", "widget"), 0),
		listRepository("gitlab.com/acme/widget", listAbsolutePath("roots", "gitlab.com", "acme", "widget"), 0),
		listRepository("github.com/other/widget", listAbsolutePath("roots", "github.com", "other", "widget"), 0),
		listRepository("github.com/acme/gadget", listAbsolutePath("roots", "github.com", "acme", "gadget"), 0),
		listRepository("gitlab.com/else/api", listAbsolutePath("roots", "gitlab.com", "else", "api"), 0),
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "all collisions",
			args: []string{"--unique"},
			want: []string{
				"api",
				"gadget",
				"github.com/acme/widget",
				"gitlab.com/acme/widget",
				"other/widget",
			},
		},
		{
			name: "host selected subset",
			args: []string{"--unique", "github.com/"},
			want: []string{"acme/widget", "gadget", "other/widget"},
		},
		{
			name: "single selected entry",
			args: []string{"--unique", "gadget"},
			want: []string{"gadget"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			command := cmd.NewListCommand(cmd.ListDependencies{
				Resolver: &listRootResolver{result: rootpkg.Result{
					RepositoryRoots: []string{listAbsolutePath("roots")},
					WorktreeRoot:    listAbsolutePath("worktrees"),
				}},
				DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
					return local.DiscoveryResult{Repositories: repositories}, nil
				},
				Stdout: &stdout,
				Stderr: io.Discard,
			})
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := stdout.String(), listSortedLines(test.want...); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestNewListCommandWorktreesAndWorktreeQueries(t *testing.T) {
	t.Parallel()

	repository := listRepository(
		"github.com/acme/widget",
		listAbsolutePath("roots", "github.com", "acme", "widget"),
		0,
	)
	mainPath := repository.Path
	featurePath := listAbsolutePath("worktrees", "github.com", "acme", "widget", "feature", "x")
	reviewPath := listAbsolutePath("worktrees", "github.com", "acme", "widget", "review")
	worktrees := []local.Worktree{
		{Identity: "not-canonical", Main: true, Path: mainPath},
		{Identity: "ignored", Slot: "review", Path: reviewPath},
		{Identity: "ignored", Slot: "feature/x", Path: featurePath},
		{Identity: "ignored", Slot: "feature/x", Path: listAbsolutePath("duplicate")},
	}

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "all registered",
			args: []string{"--worktree"},
			want: []string{
				"github.com/acme/widget",
				"github.com/acme/widget@feature/x",
				"github.com/acme/widget@review",
			},
		},
		{
			name: "slot substring",
			args: []string{"--worktree", "feature"},
			want: []string{"github.com/acme/widget@feature/x"},
		},
		{
			name: "exact repository includes main and linked",
			args: []string{"--worktree", "-e", "widget"},
			want: []string{
				"github.com/acme/widget",
				"github.com/acme/widget@feature/x",
				"github.com/acme/widget@review",
			},
		},
		{
			name: "exact slot narrows",
			args: []string{"--worktree", "--exact", "acme/widget@feature/x"},
			want: []string{"github.com/acme/widget@feature/x"},
		},
		{
			name: "exact URL slot canonicalizes",
			args: []string{"--worktree", "-e", "https://github.com/acme/widget.git@feature/x"},
			want: []string{"github.com/acme/widget@feature/x"},
		},
		{
			name: "exact slot is case sensitive",
			args: []string{"--worktree", "-e", "widget@Feature/x"},
		},
		{
			name: "paths",
			args: []string{"--worktree", "--full-path"},
			want: []string{
				local.NormalizePathForOutput(mainPath),
				local.NormalizePathForOutput(featurePath),
				local.NormalizePathForOutput(reviewPath),
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			enumeration := &listEnumerationRecorder{
				byIdentity: map[string][]local.Worktree{repository.Identity: worktrees},
			}
			var stdout bytes.Buffer
			worktreeRoot := listAbsolutePath("worktrees")
			command := cmd.NewListCommand(cmd.ListDependencies{
				Resolver: &listRootResolver{result: rootpkg.Result{
					RepositoryRoots: []string{listAbsolutePath("roots")},
					WorktreeRoot:    worktreeRoot,
				}},
				DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
					return local.DiscoveryResult{Repositories: []local.Repository{repository}}, nil
				},
				EnumerateWorktrees: enumeration.listEnumerate,
				Stdout:             &stdout,
				Stderr:             io.Discard,
			})
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := stdout.String(), listSortedLines(test.want...); got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if enumeration.calls != 1 {
				t.Fatalf("EnumerateWorktrees() calls = %d, want 1", enumeration.calls)
			}
			if enumeration.worktreeRoots[0] != worktreeRoot {
				t.Fatalf("worktree root = %q, want %q", enumeration.worktreeRoots[0], worktreeRoot)
			}
		})
	}
}

func TestNewListCommandUniqueWorktreeSuffixCollisions(t *testing.T) {
	t.Parallel()

	repositories := []local.Repository{
		listRepository("github.com/acme/widget", listAbsolutePath("roots", "github.com", "acme", "widget"), 0),
		listRepository("gitlab.com/acme/widget", listAbsolutePath("roots", "gitlab.com", "acme", "widget"), 0),
		listRepository("github.com/other/widget", listAbsolutePath("roots", "github.com", "other", "widget"), 0),
	}
	enumeration := &listEnumerationRecorder{byIdentity: make(map[string][]local.Worktree)}
	for _, repository := range repositories {
		enumeration.byIdentity[repository.Identity] = []local.Worktree{
			{Main: true, Path: repository.Path},
			{
				Slot: "feature/x",
				Path: listAbsolutePath(
					"worktrees",
					repository.Host,
					repository.Owner,
					repository.Repo,
					"feature",
					"x",
				),
			},
		}
	}

	var stdout bytes.Buffer
	command := cmd.NewListCommand(cmd.ListDependencies{
		Resolver: &listRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{listAbsolutePath("roots")},
			WorktreeRoot:    listAbsolutePath("worktrees"),
		}},
		DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{Repositories: repositories}, nil
		},
		EnumerateWorktrees: enumeration.listEnumerate,
		Stdout:             &stdout,
		Stderr:             io.Discard,
	})
	command.SetArgs([]string{"--worktree", "--unique", "@feature"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := stdout.String(), listSortedLines(
		"github.com/acme/widget@feature/x",
		"gitlab.com/acme/widget@feature/x",
		"other/widget@feature/x",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestNewListCommandEmitsWarningsAndContinues(t *testing.T) {
	t.Parallel()

	repository := listRepository(
		"github.com/acme/widget",
		listAbsolutePath("roots", "github.com", "acme", "widget"),
		0,
	)
	warning := local.Warning{
		Kind:      local.WarningPermission,
		Root:      listAbsolutePath("roots"),
		RootIndex: 0,
		Path:      listAbsolutePath("roots", "blocked"),
		Operation: "read owner",
		Err:       errors.New("permission denied"),
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := cmd.NewListCommand(cmd.ListDependencies{
		Resolver: &listRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{listAbsolutePath("roots")},
			WorktreeRoot:    listAbsolutePath("worktrees"),
		}},
		DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{
				Repositories: []local.Repository{repository},
				Warnings:     []local.Warning{warning},
			}, nil
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := stdout.String(), repository.Identity+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "gh-qw: warning: "+warning.Error()+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestNewListCommandSurfacesErrorsWithoutPartialOutput(t *testing.T) {
	t.Parallel()

	t.Run("resolution", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("resolve failed")
		resolver := &listRootResolver{err: wantErr}
		discovery := &listDiscoveryRecorder{}
		var stdout bytes.Buffer
		command := cmd.NewListCommand(cmd.ListDependencies{
			Resolver:             resolver,
			DiscoverRepositories: discovery.listDiscover,
			Stdout:               &stdout,
			Stderr:               io.Discard,
		})

		if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
		}
		if discovery.calls != 0 {
			t.Fatalf("discovery calls = %d, want 0", discovery.calls)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("discovery with warning", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("discovery failed")
		warning := local.Warning{
			Kind:      local.WarningInspection,
			Path:      listAbsolutePath("roots", "bad"),
			Operation: "inspect Git repository",
			Err:       errors.New("bad repository"),
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		command := cmd.NewListCommand(cmd.ListDependencies{
			Resolver: &listRootResolver{result: rootpkg.Result{
				RepositoryRoots: []string{listAbsolutePath("roots")},
			}},
			DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
				return local.DiscoveryResult{Warnings: []local.Warning{warning}}, wantErr
			},
			Stdout: &stdout,
			Stderr: &stderr,
		})

		if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		if got, want := stderr.String(), "gh-qw: warning: "+warning.Error()+"\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("worktree enumeration", func(t *testing.T) {
		t.Parallel()

		first := listRepository("github.com/acme/first", listAbsolutePath("roots", "github.com", "acme", "first"), 0)
		second := listRepository("github.com/acme/second", listAbsolutePath("roots", "github.com", "acme", "second"), 0)
		wantErr := errors.New("git worktree list failed")
		enumeration := &listEnumerationRecorder{
			byIdentity: map[string][]local.Worktree{
				first.Identity: {{Main: true, Path: first.Path}},
			},
			errByIdentity: map[string]error{second.Identity: wantErr},
		}
		var stdout bytes.Buffer
		command := cmd.NewListCommand(cmd.ListDependencies{
			Resolver: &listRootResolver{result: rootpkg.Result{
				RepositoryRoots: []string{listAbsolutePath("roots")},
				WorktreeRoot:    listAbsolutePath("worktrees"),
			}},
			DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
				return local.DiscoveryResult{Repositories: []local.Repository{first, second}}, nil
			},
			EnumerateWorktrees: enumeration.listEnumerate,
			Stdout:             &stdout,
			Stderr:             io.Discard,
		})
		command.SetArgs([]string{"--worktree", "does-not-match"})

		if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		if enumeration.calls != 2 {
			t.Fatalf("EnumerateWorktrees() calls = %d, want 2", enumeration.calls)
		}
	})
}

func TestNewListCommandRejectsInvalidUsageBeforeResolving(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		wantUsage bool
	}{
		{name: "too many queries", args: []string{"one", "two"}},
		{name: "path and unique", args: []string{"--full-path", "--unique"}, wantUsage: true},
		{name: "short path and unique", args: []string{"-p", "--unique"}, wantUsage: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &listRootResolver{}
			var stdout bytes.Buffer
			command := cmd.NewListCommand(cmd.ListDependencies{
				Resolver: resolver,
				Stdout:   &stdout,
				Stderr:   io.Discard,
			})
			command.SetArgs(test.args)

			err := command.Execute()
			if err == nil {
				t.Fatal("Execute() error = nil, want usage error")
			}
			if test.wantUsage && !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want repospec.ErrUsage", err)
			}
			if resolver.calls != 0 {
				t.Fatalf("Resolve() calls = %d, want 0", resolver.calls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestNewListCommandHandlesWriterAndPathErrors(t *testing.T) {
	t.Parallel()

	repository := listRepository(
		"github.com/acme/widget",
		listAbsolutePath("roots", "github.com", "acme", "widget"),
		0,
	)
	listDeps := func(stdout, stderr io.Writer, warnings []local.Warning) cmd.ListDependencies {
		return cmd.ListDependencies{
			Resolver: &listRootResolver{result: rootpkg.Result{
				RepositoryRoots: []string{listAbsolutePath("roots")},
				WorktreeRoot:    listAbsolutePath("worktrees"),
			}},
			DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
				return local.DiscoveryResult{
					Repositories: []local.Repository{repository},
					Warnings:     warnings,
				}, nil
			},
			Stdout: stdout,
			Stderr: stderr,
		}
	}

	t.Run("stdout", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("stdout failed")
		command := cmd.NewListCommand(listDeps(listErrorWriter{err: wantErr}, io.Discard, nil))

		if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
		}
	})

	t.Run("stderr warning", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("stderr failed")
		warnings := []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      listAbsolutePath("roots", "bad"),
			Operation: "inspect",
			Err:       errors.New("bad"),
		}}
		var stdout bytes.Buffer
		command := cmd.NewListCommand(listDeps(&stdout, listErrorWriter{err: wantErr}, warnings))

		if gotErr := command.Execute(); !errors.Is(gotErr, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", gotErr, wantErr)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("non absolute full path", func(t *testing.T) {
		t.Parallel()

		relative := repository
		relative.Path = filepath.Join("relative", "widget")
		var stdout bytes.Buffer
		command := cmd.NewListCommand(cmd.ListDependencies{
			Resolver: &listRootResolver{result: rootpkg.Result{
				RepositoryRoots: []string{listAbsolutePath("roots")},
			}},
			DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
				return local.DiscoveryResult{Repositories: []local.Repository{relative}}, nil
			},
			Stdout: &stdout,
			Stderr: io.Discard,
		})
		command.SetArgs([]string{"--full-path"})

		if err := command.Execute(); err == nil {
			t.Fatal("Execute() error = nil, want path error")
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})
}

type listRootResolver struct {
	result rootpkg.Result
	err    error
	calls  int
}

func (resolver *listRootResolver) Resolve() (rootpkg.Result, error) {
	resolver.calls++
	return resolver.result, resolver.err
}

type listDiscoveryRecorder struct {
	result local.DiscoveryResult
	err    error
	calls  int
	roots  []string
}

func (recorder *listDiscoveryRecorder) listDiscover(
	_ context.Context,
	roots []string,
) (local.DiscoveryResult, error) {
	recorder.calls++
	recorder.roots = append([]string(nil), roots...)
	return recorder.result, recorder.err
}

type listEnumerationRecorder struct {
	byIdentity    map[string][]local.Worktree
	errByIdentity map[string]error
	calls         int
	repositories  []local.Repository
	worktreeRoots []string
}

func (recorder *listEnumerationRecorder) listEnumerate(
	_ context.Context,
	repository local.Repository,
	worktreeRoot string,
) ([]local.Worktree, error) {
	recorder.calls++
	recorder.repositories = append(recorder.repositories, repository)
	recorder.worktreeRoots = append(recorder.worktreeRoots, worktreeRoot)
	if err := recorder.errByIdentity[repository.Identity]; err != nil {
		return nil, err
	}
	return append([]local.Worktree(nil), recorder.byIdentity[repository.Identity]...), nil
}

type listErrorWriter struct {
	err error
}

func (writer listErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func listRepository(identity, path string, rootIndex int) local.Repository {
	parts := strings.Split(identity, "/")
	if len(parts) != 3 {
		panic("test repository identity must have three components")
	}
	return local.Repository{
		Identity:  identity,
		Host:      parts[0],
		Owner:     parts[1],
		Repo:      parts[2],
		Path:      path,
		Root:      listAbsolutePath("roots"),
		RootIndex: rootIndex,
	}
}

func listSortedLines(lines ...string) string {
	if len(lines) == 0 {
		return ""
	}
	lines = append([]string(nil), lines...)
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func listAbsolutePath(parts ...string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(append([]string{`C:\`}, parts...)...)
	}
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}
