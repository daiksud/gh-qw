package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/fzfcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/daiksud/gh-qw/internal/testregistry"
)

func listSelectionDependencies(t *testing.T, repositories ...local.Repository) (cmd.ListDependencies, *bytes.Buffer) {
	t.Helper()
	stdout := new(bytes.Buffer)
	return cmd.ListDependencies{
		Resolver: &listRootResolver{result: rootpkg.Result{
			RepositoryRoot: listAbsolutePath("roots"), WorktreeRoot: listAbsolutePath("worktrees"),
		}},
		Registry: testregistry.From(func() (map[string]string, error) {
			return testregistry.Locations(repositories...), nil
		}),
		Stdout: stdout, Stderr: io.Discard,
		IsTerminal: func(io.Writer) bool { return true },
		LookPath:   func(string) (string, error) { return "/fzf", nil },
		Select: func(_ context.Context, rows []string, _ string) (string, error) {
			return rows[0], nil
		},
	}, stdout
}

func TestListSelectionModeAndFallback(t *testing.T) {
	t.Parallel()
	first := listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget"))
	other := listRepository("github.com/other/another", listAbsolutePath("roots", "another"))
	for _, test := range []struct {
		name      string
		flags     []string
		terminal  bool
		lookupErr error
		lookups   int
		selects   int
		status    int
		want      string
	}{
		{name: "automatic terminal", terminal: true, lookups: 1, selects: 1, want: other.Identity + "\n"},
		{name: "automatic pipe", want: first.Identity + "\n"},
		{name: "automatic missing", terminal: true, lookupErr: exec.ErrNotFound, lookups: 1, want: first.Identity + "\n"},
		{name: "forced pipe", flags: []string{"--fzf"}, lookups: 1, selects: 1, want: other.Identity + "\n"},
		{name: "disabled terminal", flags: []string{"--fzf=false"}, terminal: true, want: first.Identity + "\n"},
		{name: "forced missing", flags: []string{"--fzf"}, lookupErr: exec.ErrNotFound, lookups: 1, status: 1},
		{name: "path error", terminal: true, lookupErr: exec.ErrDot, lookups: 1, status: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps, stdout := listSelectionDependencies(t, first, other)
			terminalCalls, lookupCalls, selectCalls := 0, 0, 0
			deps.IsTerminal = func(output io.Writer) bool {
				terminalCalls++
				if output != stdout {
					t.Fatal("terminal detector did not receive the command output")
				}
				return test.terminal
			}
			deps.LookPath = func(name string) (string, error) {
				lookupCalls++
				if name != "fzf" {
					t.Fatalf("lookup = %q", name)
				}
				return "/fzf", test.lookupErr
			}
			deps.Select = func(_ context.Context, rows []string, query string) (string, error) {
				selectCalls++
				if len(rows) != 2 || query != "widget" || !slices.IsSorted(rows) {
					t.Fatalf("rows=%q, query=%q", rows, query)
				}
				return rows[1], nil
			}
			command := cmd.NewListCommand(deps)
			command.SetArgs(append(slices.Clone(test.flags), "widget"))
			err := command.Execute()
			if cmd.ExitCode(err) != test.status || stdout.String() != test.want {
				t.Fatalf("error=%v, stdout=%q; want status=%d stdout=%q", err, stdout.String(), test.status, test.want)
			}
			if lookupCalls != test.lookups || selectCalls != test.selects {
				t.Fatalf("lookup/select calls = %d/%d, want %d/%d", lookupCalls, selectCalls, test.lookups, test.selects)
			}
			if len(test.flags) != 0 && terminalCalls != 0 {
				t.Fatal("explicit mode consulted terminal state")
			}
			if test.status != 0 && !errors.Is(err, test.lookupErr) {
				t.Fatalf("lost lookup cause: %v", err)
			}
		})
	}
}

func TestListSelectionUsesCurrentOutputWriter(t *testing.T) {
	t.Parallel()
	repository := listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget"))
	deps, original := listSelectionDependencies(t, repository)
	var replacement bytes.Buffer
	deps.IsTerminal = func(output io.Writer) bool {
		if output != &replacement {
			t.Fatal("terminal detection used the original output")
		}
		return true
	}
	command := cmd.NewListCommand(deps)
	command.SetOut(&replacement)
	if err := command.Execute(); err != nil || original.Len() != 0 || replacement.String() != repository.Identity+"\n" {
		t.Fatalf("error=%v, original=%q replacement=%q", err, original.String(), replacement.String())
	}
}

func TestListDefaultTerminalDetectionSkipsNonTerminals(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	for name, output := range map[string]io.Writer{"buffer": new(bytes.Buffer), "file": file, "pipe": writer} {
		t.Run(name, func(t *testing.T) {
			deps, _ := listSelectionDependencies(t, listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget")))
			deps.Stdout, deps.IsTerminal = output, nil
			deps.LookPath = func(string) (string, error) { t.Fatal("nonterminal output inspected fzf"); return "", nil }
			if err := cmd.NewListCommand(deps).Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestListSelectionQueryAndResultFormats(t *testing.T) {
	t.Parallel()
	name := "space tab\tand newline\nwidget"
	if runtime.GOOS == "windows" {
		name = "space and widget"
	}
	first := listRepository("github.com/acme/widget", listAbsolutePath("roots", name))
	other := listRepository("github.com/other/widget", listAbsolutePath("roots", "other"))
	for _, test := range []struct {
		name, query, seed, want string
		flags                   []string
		count                   int
	}{
		{name: "default", query: "not-a-match", seed: "not-a-match", want: first.Identity, count: 2},
		{name: "canonical URL", query: "https://github.com/acme/widget.git", seed: first.Identity, want: first.Identity, count: 2},
		{name: "path", flags: []string{"--full-path"}, want: local.NormalizePathForOutput(first.Path), count: 2},
		{name: "unique across offered set", flags: []string{"--unique"}, want: "acme/widget", count: 2},
		{name: "exact prefilter", flags: []string{"--exact", "--unique"}, query: "acme/widget", want: "widget", count: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps, stdout := listSelectionDependencies(t, other, first)
			deps.Select = func(_ context.Context, rows []string, query string) (string, error) {
				wantRow := fmt.Sprintf("repository\t%s\t%q", first.Identity, local.NormalizePathForOutput(first.Path))
				if len(rows) != test.count || rows[0] != wantRow || query != test.seed {
					t.Fatalf("rows=%q query=%q; want first=%q count=%d seed=%q", rows, query, wantRow, test.count, test.seed)
				}
				return rows[0], nil
			}
			command := cmd.NewListCommand(deps)
			args := slices.Clone(test.flags)
			if test.query != "" {
				args = append(args, test.query)
			}
			command.SetArgs(args)
			if err := command.Execute(); err != nil || stdout.String() != test.want+"\n" {
				t.Fatalf("error=%v stdout=%q want=%q", err, stdout.String(), test.want+"\n")
			}
		})
	}
}

func TestListSelectionWorktreeAliasesAndModes(t *testing.T) {
	t.Parallel()
	repository := listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget"))
	worktrees := []local.Worktree{
		{Main: true, Path: repository.Path},
		{Slot: "one", Branch: "shared", Path: listAbsolutePath("worktrees", "one")},
		{Slot: "two", Branch: "shared", Path: listAbsolutePath("worktrees", "two")},
		{Slot: "three", Branch: "feature/topic", Path: listAbsolutePath("external", "three")},
		{Slot: "four", Detached: true, Path: listAbsolutePath("external", "four")},
	}
	for _, test := range []struct {
		name  string
		flags []string
		count int
	}{
		{name: "all", flags: []string{"--all"}, count: 5},
		{name: "worktrees", flags: []string{"--worktree"}, count: 4},
		{name: "gone", flags: []string{"--worktree", "--gone"}, count: 1},
		{name: "exact slot", flags: []string{"--worktree", "--exact", "widget@three"}, count: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps, stdout := listSelectionDependencies(t, repository)
			deps.EnumerateWorktrees = (&listEnumerationRecorder{byIdentity: map[string][]local.Worktree{repository.Identity: worktrees}}).listEnumerate
			deps.Git = &listGit{upstreams: []gitcmd.BranchUpstream{{Branch: "feature/topic", Upstream: "refs/remotes/origin/topic"}}}
			deps.Select = func(_ context.Context, rows []string, query string) (string, error) {
				if len(rows) != test.count || query != "" {
					t.Fatalf("rows=%q query=%q", rows, query)
				}
				selected := ""
				for _, row := range rows {
					fields := strings.Split(row, "\t")
					switch fields[1] {
					case repository.Identity + "@one", repository.Identity + "@two":
						if len(fields) != 4 || fields[3] != repository.Identity+"@shared" {
							t.Fatalf("collision lost branch search alias: %q", row)
						}
					case repository.Identity + "@feature/topic":
						if len(fields) != 4 || fields[3] != repository.Identity+"@three" {
							t.Fatalf("branch label lost stable slot alias: %q", row)
						}
						selected = row
					case repository.Identity + "@four":
						if len(fields) != 3 {
							t.Fatalf("detached row has redundant aliases: %q", row)
						}
					}
				}
				return selected, nil
			}
			command := cmd.NewListCommand(deps)
			command.SetArgs(test.flags)
			if err := command.Execute(); err != nil || stdout.String() != repository.Identity+"@feature/topic\n" {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestListSelectionFailuresDoNotPrintResults(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("selector failed")
	for _, test := range []struct {
		name      string
		selectErr error
		unknown   bool
		cancel    bool
		status    int
	}{
		{name: "cancel", selectErr: fzfcmd.ErrCanceled, status: 130},
		{name: "no match", selectErr: fzfcmd.ErrNoMatch, status: 1},
		{name: "failure", selectErr: wantErr, status: 1},
		{name: "unknown", unknown: true, status: 1},
		{name: "context", cancel: true, status: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deps, stdout := listSelectionDependencies(t, listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget")))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deps.Select = func(_ context.Context, rows []string, _ string) (string, error) {
				if test.cancel {
					cancel()
				}
				if test.unknown {
					return "unknown", nil
				}
				return rows[0], test.selectErr
			}
			command := cmd.NewListCommand(deps)
			err := command.ExecuteContext(ctx)
			if cmd.ExitCode(err) != test.status || stdout.Len() != 0 {
				t.Fatalf("error=%v status=%d stdout=%q", err, cmd.ExitCode(err), stdout.String())
			}
			if test.selectErr == wantErr && !errors.Is(err, wantErr) {
				t.Fatalf("lost selector cause: %v", err)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost context cause: %v", err)
			}
		})
	}
}

func TestListSelectionDiscoveryFailuresDoNotOpenPicker(t *testing.T) {
	t.Parallel()
	repository := listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget"))
	wantErr := errors.New("discovery failed")
	for _, phase := range []string{"registry", "worktrees", "path"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			deps, stdout := listSelectionDependencies(t, repository)
			deps.Select = func(context.Context, []string, string) (string, error) {
				t.Fatal("failed discovery opened the picker")
				return "", nil
			}
			args := []string{"--fzf"}
			switch phase {
			case "registry":
				deps.Registry = testregistry.From(func() (map[string]string, error) { return nil, wantErr })
			case "worktrees":
				args = append(args, "--worktree")
				deps.EnumerateWorktrees = func(context.Context, local.Repository, string) ([]local.Worktree, error) { return nil, wantErr }
			case "path":
				args = append(args, "--worktree")
				deps.EnumerateWorktrees = func(context.Context, local.Repository, string) ([]local.Worktree, error) {
					return []local.Worktree{{Main: true, Path: repository.Path}, {Slot: "topic", Path: "relative"}}, nil
				}
			}
			command := cmd.NewListCommand(deps)
			command.SetArgs(args)
			err := command.Execute()
			if cmd.ExitCode(err) != 1 || stdout.Len() != 0 || (phase != "path" && !errors.Is(err, wantErr)) {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestListSelectionPropagatesResultWriteFailure(t *testing.T) {
	t.Parallel()
	deps, _ := listSelectionDependencies(t, listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget")))
	wantErr := errors.New("result write failed")
	deps.Stdout = listErrorWriter{err: wantErr}
	if err := cmd.NewListCommand(deps).Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("error=%v, want %v", err, wantErr)
	}
}

func TestListCDAndEmptySelection(t *testing.T) {
	t.Setenv("GHQW_SHELL_CD", "1")
	repository := listRepository("github.com/acme/widget", listAbsolutePath("roots", "widget"))
	for _, test := range []struct {
		name   string
		args   []string
		empty  bool
		status int
	}{
		{name: "cd selects path", args: []string{"--cd"}},
		{name: "empty cd", args: []string{"--cd"}, empty: true, status: 1},
		{name: "empty list", args: []string{"--fzf"}, empty: true},
		{name: "exact empty cd", args: []string{"--cd", "--exact", "not-found"}, status: 1},
		{name: "exact empty list", args: []string{"--fzf", "--exact", "not-found"}},
		{name: "cd disabled", args: []string{"--cd=false", "--fzf=false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repositories := []local.Repository{repository}
			if test.empty {
				repositories = nil
			}
			deps, stdout := listSelectionDependencies(t, repositories...)
			deps.IsTerminal = func(io.Writer) bool { return false }
			lookupCalls, selectCalls := 0, 0
			deps.LookPath = func(string) (string, error) { lookupCalls++; return "/fzf", nil }
			deps.Select = func(_ context.Context, rows []string, query string) (string, error) {
				selectCalls++
				return rows[0], nil
			}
			command := cmd.NewListCommand(deps)
			command.SetArgs(test.args)
			err := command.Execute()
			want := ""
			if test.name == "cd selects path" {
				want = local.NormalizePathForOutput(repository.Path) + "\n"
				if lookupCalls != 1 || selectCalls != 1 {
					t.Fatalf("cd did not force selection: lookup=%d select=%d", lookupCalls, selectCalls)
				}
			} else {
				if lookupCalls != 0 || selectCalls != 0 {
					t.Fatalf("empty/disabled selection consulted fzf: lookup=%d select=%d", lookupCalls, selectCalls)
				}
				if test.name == "cd disabled" {
					want = repository.Identity + "\n"
				}
			}
			if cmd.ExitCode(err) != test.status || stdout.String() != want {
				t.Fatalf("error=%v stdout=%q; want status=%d stdout=%q", err, stdout.String(), test.status, want)
			}
		})
	}
}

func TestListCDInvalidUsageStopsBeforeDiscovery(t *testing.T) {
	for _, test := range []struct {
		name, integration string
		args              []string
	}{
		{name: "integration missing", args: []string{"--cd"}},
		{name: "unique", integration: "1", args: []string{"--cd", "--unique"}},
		{name: "fzf disabled", integration: "1", args: []string{"--cd", "--fzf=false"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GHQW_SHELL_CD", test.integration)
			deps, stdout := listSelectionDependencies(t)
			resolver := deps.Resolver.(*listRootResolver)
			command := cmd.NewListCommand(deps)
			command.SetArgs(test.args)
			err := command.Execute()
			if cmd.ExitCode(err) != 2 || resolver.calls != 0 || stdout.Len() != 0 {
				t.Fatalf("error=%v calls=%d stdout=%q", err, resolver.calls, stdout.String())
			}
		})
	}
}
