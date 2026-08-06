package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestWorktreeListHumanGolden(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	worktrees := []local.Worktree{
		worktreeListLinked(
			repository,
			"zeta",
			worktreeListAbsolute("worktrees", "zeta"),
			worktreeListHash('3'),
			"",
			true,
		),
		worktreeListMain(repository, worktreeListHash('1'), "main"),
		worktreeListLinked(
			repository,
			"feature/a",
			worktreeListAbsolute("worktrees", "feature", "a"),
			worktreeListHash('2'),
			"feature/a",
			false,
		),
	}
	harness := worktreeListNewHarness(repository, worktrees)

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"-R", "acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"github.com/acme/widget " + worktreeListHash('1') + " [main]\n" +
		"github.com/acme/widget@feature/a " + worktreeListHash('2') + " [feature/a]\n" +
		"github.com/acme/widget@zeta " + worktreeListHash('3') + " [detached]\n"
	if got := harness.stdout.String(); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if harness.stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", harness.stderr.String())
	}
	if harness.enumerated.Identity != repository.Identity {
		t.Fatalf("enumerated repository = %q, want %q", harness.enumerated.Identity, repository.Identity)
	}
}

func TestWorktreeListVerboseGolden(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	main := worktreeListMain(repository, worktreeListHash('1'), "main")
	main.Locked = true
	linked := worktreeListLinked(
		repository,
		"feature/a",
		worktreeListAbsolute("worktrees", "feature", "a"),
		worktreeListHash('2'),
		"",
		true,
	)
	linked.Locked = true
	linked.LockedReason = "deployment check"
	linked.Prunable = true
	linked.PrunableReason = "gitdir missing\nsecond line"
	harness := worktreeListNewHarness(repository, []local.Worktree{linked, main})

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"-R", repository.Identity, "-v"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"github.com/acme/widget " + worktreeListHash('1') + " [main] [locked]\n" +
		"github.com/acme/widget@feature/a " + worktreeListHash('2') +
		" [detached] [locked: \"deployment check\"]" +
		" [prunable: \"gitdir missing\\nsecond line\"]\n"
	if got := harness.stdout.String(); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestWorktreeListFullPathGolden(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "main with space")
	linkedPath := worktreeListAbsolute("worktrees", "feature with space")
	worktrees := []local.Worktree{
		worktreeListLinked(
			repository,
			"feature/a",
			linkedPath,
			worktreeListHash('2'),
			"feature/a",
			false,
		),
		worktreeListMain(repository, worktreeListHash('1'), "main"),
	}
	harness := worktreeListNewHarness(repository, worktrees)

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"-R", repository.Identity, "--full-path"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		local.NormalizePathForOutput(repository.Path) + " " +
		worktreeListHash('1') + " [main]\n" +
		local.NormalizePathForOutput(linkedPath) + " " +
		worktreeListHash('2') + " [feature/a]\n"
	if got := harness.stdout.String(); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestWorktreeListPorcelainGolden(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	main := worktreeListMain(repository, strings.Repeat("0", 40), "main")
	alpha := worktreeListLinked(
		repository,
		"feature/a",
		worktreeListAbsolute("worktrees", "feature", "a"),
		worktreeListHash('2'),
		"feature/a",
		false,
	)
	alpha.Locked = true
	zeta := worktreeListLinked(
		repository,
		"zeta",
		worktreeListAbsolute("worktrees", "zeta"),
		worktreeListHash('3'),
		"",
		true,
	)
	zeta.Locked = true
	zeta.LockedReason = "deployment check"
	zeta.Prunable = true
	zeta.PrunableReason = "gitdir missing\nsecond line"
	harness := worktreeListNewHarness(repository, []local.Worktree{zeta, main, alpha})

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"--repo", "widget", "--porcelain"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"identity github.com/acme/widget\n" +
		"path " + local.NormalizePathForOutput(repository.Path) + "\n" +
		"head " + strings.Repeat("0", 40) + "\n" +
		"kind main\n" +
		"branch main\n" +
		"\n" +
		"identity github.com/acme/widget@feature/a\n" +
		"path " + local.NormalizePathForOutput(alpha.Path) + "\n" +
		"head " + worktreeListHash('2') + "\n" +
		"kind linked\n" +
		"branch feature/a\n" +
		"locked \"\"\n" +
		"\n" +
		"identity github.com/acme/widget@zeta\n" +
		"path " + local.NormalizePathForOutput(zeta.Path) + "\n" +
		"head " + worktreeListHash('3') + "\n" +
		"kind linked\n" +
		"detached\n" +
		"locked \"deployment check\"\n" +
		"prunable \"gitdir missing\\nsecond line\"\n" +
		"\n"
	if got := harness.stdout.String(); got != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

func TestWorktreeListPorcelainPreservesUnbornObjectWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{40, 64} {
		width := width
		t.Run(fmt.Sprintf("%d-hex-digits", width), func(t *testing.T) {
			t.Parallel()

			repository := worktreeListRepository("github.com/acme/widget", "primary")
			head := strings.Repeat("0", width)
			harness := worktreeListNewHarness(repository, []local.Worktree{
				worktreeListMain(repository, head, "unborn"),
			})
			command := cmd.NewWorktreeListCommand(harness.dependencies())
			command.SetArgs([]string{"--porcelain", "-R", repository.Identity})

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got, want := harness.stdout.String(), ""+
				"identity github.com/acme/widget\n"+
				"path "+local.NormalizePathForOutput(repository.Path)+"\n"+
				"head "+head+"\n"+
				"kind main\n"+
				"branch unborn\n\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestWorktreeListPorcelainQuotingAndNonUTF8Golden(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "雪")
	main := worktreeListMain(repository, worktreeListHash('1'), "雪")
	main.Locked = true
	main.LockedReason = "déployé"
	main.Prunable = true
	main.PrunableReason = "wide\u00a0space"

	empty := worktreeListLinked(
		repository,
		"feature/empty",
		worktreeListAbsolute("worktrees", "bad-"+string([]byte{0xff})),
		worktreeListHash('2'),
		"feature/empty",
		false,
	)
	empty.Locked = true

	quoted := worktreeListLinked(
		repository,
		"feature/quoted",
		worktreeListAbsolute("worktrees", "line\nquote\""),
		worktreeListHash('3'),
		"",
		true,
	)
	quoted.Prunable = true
	quoted.PrunableReason = "plain雪 white\\quote\"\n\r\t" +
		string([]byte{0x01, 0x7f, 0xff}) +
		"\u200b"
	harness := worktreeListNewHarness(repository, []local.Worktree{quoted, empty, main})

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"--porcelain", "-R", repository.Identity})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "" +
		"identity github.com/acme/widget\n" +
		"path " + local.NormalizePathForOutput(repository.Path) + "\n" +
		"head " + worktreeListHash('1') + "\n" +
		"kind main\n" +
		"branch 雪\n" +
		"locked déployé\n" +
		"prunable \"wide\\xC2\\xA0space\"\n" +
		"\n" +
		"identity github.com/acme/widget@feature/empty\n" +
		"path \"" + local.NormalizePathForOutput(empty.Path[:len(empty.Path)-1]) + "\\xFF\"\n" +
		"head " + worktreeListHash('2') + "\n" +
		"kind linked\n" +
		"branch feature/empty\n" +
		"locked \"\"\n" +
		"\n" +
		"identity github.com/acme/widget@feature/quoted\n" +
		"path \"" + local.NormalizePathForOutput(
		strings.SplitN(quoted.Path, "\n", 2)[0],
	) + "\\nquote\\\"\"\n" +
		"head " + worktreeListHash('3') + "\n" +
		"kind linked\n" +
		"detached\n" +
		"prunable \"plain雪 white\\\\quote\\\"\\n\\r\\t" +
		"\\x01\\x7F\\xFF\\xE2\\x80\\x8B\"\n" +
		"\n"
	if got := harness.stdout.String(); got != want {
		t.Fatalf("stdout bytes = %q\nwant bytes = %q", got, want)
	}
}

func TestWorktreeListSelectsExplicitRepositoryWithoutCurrentDirectory(t *testing.T) {
	t.Parallel()

	alpha := worktreeListRepository("github.com/acme/alpha", "alpha")
	widget := worktreeListRepository("github.com/acme/widget", "widget")
	harness := worktreeListNewHarness(widget, []local.Worktree{
		worktreeListMain(widget, worktreeListHash('1'), "main"),
	})
	harness.repositories = []local.Repository{alpha, widget}
	harness.getcwd = func() (string, error) {
		t.Fatal("Getwd called for explicit -R")
		return "", nil
	}
	harness.current = func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error) {
		t.Fatal("Current called for explicit -R")
		return local.Current{}, nil
	}

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"-R", "acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if harness.enumerated.Identity != widget.Identity {
		t.Fatalf("enumerated = %q, want %q", harness.enumerated.Identity, widget.Identity)
	}
}

func TestWorktreeListTreatsEmptyExplicitRepositoryAsASelector(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	harness := worktreeListNewHarness(repository, []local.Worktree{
		worktreeListMain(repository, worktreeListHash('1'), "main"),
	})
	harness.getcwd = func() (string, error) {
		t.Fatal("Getwd called for explicit --repo=")
		return "", nil
	}

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"--repo="})

	err := command.Execute()
	if !errors.Is(err, local.ErrInvalidSelector) {
		t.Fatalf("Execute() error = %v, want ErrInvalidSelector", err)
	}
	if harness.enumerateCalls != 0 {
		t.Fatalf("Enumerate calls = %d, want 0", harness.enumerateCalls)
	}
	if harness.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", harness.stdout.String())
	}
}

func TestWorktreeListSelectsRepositoryFromLinkedCurrentDirectory(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	harness := worktreeListNewHarness(repository, []local.Worktree{
		worktreeListMain(repository, worktreeListHash('1'), "main"),
	})
	cwd := worktreeListAbsolute("worktrees", "feature", "nested")
	harness.getcwd = func() (string, error) {
		harness.getcwdCalls++
		return cwd, nil
	}
	harness.current = func(
		_ context.Context,
		gotCWD string,
		worktreeRoot string,
		repositories []local.Repository,
		_ ...local.CurrentOptions,
	) (local.Current, error) {
		harness.currentCalls++
		if gotCWD != cwd {
			t.Fatalf("cwd = %q, want %q", gotCWD, cwd)
		}
		if worktreeRoot != harness.roots.WorktreeRoot {
			t.Fatalf("worktree root = %q, want %q", worktreeRoot, harness.roots.WorktreeRoot)
		}
		if len(repositories) != 1 || repositories[0].Identity != repository.Identity {
			t.Fatalf("repositories = %#v", repositories)
		}
		return local.Current{Repository: repository}, nil
	}

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if harness.getcwdCalls != 1 || harness.currentCalls != 1 {
		t.Fatalf(
			"Getwd calls = %d, Current calls = %d, want 1 each",
			harness.getcwdCalls,
			harness.currentCalls,
		)
	}
	if harness.enumerated.Identity != repository.Identity {
		t.Fatalf("enumerated = %q, want %q", harness.enumerated.Identity, repository.Identity)
	}
}

func TestWorktreeListSelectsEarliestDuplicateRepositoryIdentity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "explicit selector", args: []string{"-R", "acme/widget"}},
		{name: "current directory", args: nil},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			first := worktreeListRepository("github.com/acme/widget", "first")
			second := worktreeListRepository("github.com/acme/widget", "second")
			second.RootIndex = 1
			harness := worktreeListNewHarness(first, []local.Worktree{
				worktreeListMain(first, worktreeListHash('1'), "main"),
			})
			harness.repositories = []local.Repository{first, second}
			harness.current = func(
				context.Context,
				string,
				string,
				[]local.Repository,
				...local.CurrentOptions,
			) (local.Current, error) {
				return local.Current{Repository: first}, nil
			}

			command := cmd.NewWorktreeListCommand(harness.dependencies())
			command.SetArgs(test.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if harness.enumerateCalls != 1 {
				t.Fatalf("EnumerateWorktrees calls = %d, want 1", harness.enumerateCalls)
			}
			if harness.enumerated.Path != first.Path {
				t.Fatalf("enumerated path = %q, want earliest %q", harness.enumerated.Path, first.Path)
			}
		})
	}
}

func TestWorktreeListWritesDiscoveryWarningsToStderr(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	harness := worktreeListNewHarness(repository, []local.Worktree{
		worktreeListMain(repository, worktreeListHash('1'), "main"),
	})
	warning := local.Warning{
		Kind:      local.WarningUnsafe,
		Root:      harness.roots.RepositoryRoots[0],
		RootIndex: 0,
		Path:      worktreeListAbsolute("repositories", "unsafe"),
		Operation: "inspect .git",
		Err:       errors.New("permission denied"),
	}
	harness.warnings = []local.Warning{warning}

	command := cmd.NewWorktreeListCommand(harness.dependencies())
	command.SetArgs([]string{"-R", repository.Identity})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := harness.stderr.String(), "gh-qw: warning: "+warning.Error()+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestWorktreeListRejectsArgumentsAndPorcelainConflictsBeforeResolution(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		args      []string
		usage     bool
		wantError string
	}{
		{
			name:      "argument",
			args:      []string{"unexpected"},
			wantError: "unknown command",
		},
		{
			name:      "verbose",
			args:      []string{"--porcelain", "-v"},
			usage:     true,
			wantError: "-v/--verbose",
		},
		{
			name:      "full path",
			args:      []string{"--porcelain", "--full-path"},
			usage:     true,
			wantError: "--full-path",
		},
		{
			name:      "both conflicts",
			args:      []string{"--porcelain", "-v", "--full-path"},
			usage:     true,
			wantError: "-v/--verbose or --full-path",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := &worktreeListRootResolver{}
			var stdout bytes.Buffer
			command := cmd.NewWorktreeListCommand(cmd.WorktreeListDependencies{
				Resolver: resolver,
				Stdout:   &stdout,
				Stderr:   io.Discard,
			})
			command.SetArgs(test.args)

			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Execute() error = %v, want text %q", err, test.wantError)
			}
			if test.usage && !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want usage error", err)
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

func TestWorktreeListPropagatesDependencyErrorsWithoutPartialOutput(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{
		"roots",
		"discovery",
		"getwd",
		"current",
		"enumeration",
	} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			wantErr := errors.New(stage + " failed")
			repository := worktreeListRepository("github.com/acme/widget", "primary")
			harness := worktreeListNewHarness(repository, []local.Worktree{
				worktreeListMain(repository, worktreeListHash('1'), "main"),
			})
			args := []string(nil)
			switch stage {
			case "roots":
				harness.resolver.err = wantErr
			case "discovery":
				harness.discoverErr = wantErr
				args = []string{"-R", repository.Identity}
			case "getwd":
				harness.getcwd = func() (string, error) {
					return "", wantErr
				}
			case "current":
				harness.current = func(
					context.Context,
					string,
					string,
					[]local.Repository,
					...local.CurrentOptions,
				) (local.Current, error) {
					return local.Current{}, wantErr
				}
			case "enumeration":
				harness.enumerateErr = wantErr
				args = []string{"-R", repository.Identity}
			}

			command := cmd.NewWorktreeListCommand(harness.dependencies())
			command.SetArgs(args)

			if err := command.Execute(); !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, wantErr)
			}
			if harness.stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", harness.stdout.String())
			}
		})
	}
}

func TestWorktreeListBuffersBeforeRepresentationErrors(t *testing.T) {
	t.Parallel()

	repository := worktreeListRepository("github.com/acme/widget", "primary")
	validMain := worktreeListMain(repository, worktreeListHash('1'), "main")
	validLinked := worktreeListLinked(
		repository,
		"feature/a",
		worktreeListAbsolute("worktrees", "feature", "a"),
		worktreeListHash('2'),
		"feature/a",
		false,
	)
	for _, test := range []struct {
		name      string
		worktrees []local.Worktree
		wantKind  error
	}{
		{
			name:      "missing main",
			worktrees: []local.Worktree{validLinked},
			wantKind:  local.ErrWorktreeAmbiguous,
		},
		{
			name: "duplicate identity",
			worktrees: []local.Worktree{
				validMain,
				validLinked,
				validLinked,
			},
			wantKind: local.ErrWorktreeAmbiguous,
		},
		{
			name: "branch and detached conflict",
			worktrees: []local.Worktree{
				validMain,
				func() local.Worktree {
					result := validLinked
					result.Detached = true
					return result
				}(),
			},
			wantKind: local.ErrUnsafeWorktree,
		},
		{
			name: "relative path",
			worktrees: []local.Worktree{
				func() local.Worktree {
					result := validMain
					result.Path = "relative"
					return result
				}(),
			},
			wantKind: local.ErrUnsafeWorktree,
		},
		{
			name: "lock reason without lock",
			worktrees: []local.Worktree{
				func() local.Worktree {
					result := validMain
					result.LockedReason = "reason"
					return result
				}(),
			},
			wantKind: local.ErrUnsafeWorktree,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := worktreeListNewHarness(repository, test.worktrees)
			command := cmd.NewWorktreeListCommand(harness.dependencies())
			command.SetArgs([]string{"-R", repository.Identity, "--porcelain"})

			err := command.Execute()
			if !errors.Is(err, test.wantKind) {
				t.Fatalf("Execute() error = %v, want %v", err, test.wantKind)
			}
			if harness.stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", harness.stdout.String())
			}
		})
	}
}

func TestWorktreeListPropagatesWriterErrors(t *testing.T) {
	t.Parallel()

	t.Run("stdout", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("stdout failed")
		repository := worktreeListRepository("github.com/acme/widget", "primary")
		harness := worktreeListNewHarness(repository, []local.Worktree{
			worktreeListMain(repository, worktreeListHash('1'), "main"),
		})
		deps := harness.dependencies()
		deps.Stdout = worktreeListFailingWriter{err: wantErr}
		command := cmd.NewWorktreeListCommand(deps)
		command.SetArgs([]string{"-R", repository.Identity})

		if err := command.Execute(); !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("stderr warning", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("stderr failed")
		repository := worktreeListRepository("github.com/acme/widget", "primary")
		harness := worktreeListNewHarness(repository, []local.Worktree{
			worktreeListMain(repository, worktreeListHash('1'), "main"),
		})
		harness.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      repository.Path,
			Operation: "inspect",
		}}
		deps := harness.dependencies()
		deps.Stderr = worktreeListFailingWriter{err: wantErr}
		command := cmd.NewWorktreeListCommand(deps)
		command.SetArgs([]string{"-R", repository.Identity})

		if err := command.Execute(); !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		if harness.enumerateCalls != 0 {
			t.Fatalf("EnumerateWorktrees calls = %d, want 0", harness.enumerateCalls)
		}
		if harness.stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", harness.stdout.String())
		}
	})

	t.Run("short write", func(t *testing.T) {
		t.Parallel()

		repository := worktreeListRepository("github.com/acme/widget", "primary")
		harness := worktreeListNewHarness(repository, []local.Worktree{
			worktreeListMain(repository, worktreeListHash('1'), "main"),
		})
		deps := harness.dependencies()
		deps.Stdout = worktreeListShortWriter{}
		command := cmd.NewWorktreeListCommand(deps)
		command.SetArgs([]string{"-R", repository.Identity})

		if err := command.Execute(); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("Execute() error = %v, want io.ErrShortWrite", err)
		}
	})
}

type worktreeListHarness struct {
	resolver       *worktreeListRootResolver
	roots          rootpkg.Result
	repositories   []local.Repository
	worktrees      []local.Worktree
	warnings       []local.Warning
	discoverErr    error
	enumerateErr   error
	getcwd         func() (string, error)
	current        func(context.Context, string, string, []local.Repository, ...local.CurrentOptions) (local.Current, error)
	stdout         bytes.Buffer
	stderr         bytes.Buffer
	getcwdCalls    int
	currentCalls   int
	enumerateCalls int
	enumerated     local.Repository
}

func worktreeListNewHarness(
	repository local.Repository,
	worktrees []local.Worktree,
) *worktreeListHarness {
	roots := rootpkg.Result{
		RepositoryRoots: []string{repository.Root},
		WorktreeRoot:    worktreeListAbsolute("worktrees"),
	}
	harness := &worktreeListHarness{
		resolver:     &worktreeListRootResolver{result: roots},
		roots:        roots,
		repositories: []local.Repository{repository},
		worktrees:    worktrees,
	}
	harness.getcwd = func() (string, error) {
		harness.getcwdCalls++
		return repository.Path, nil
	}
	harness.current = func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error) {
		harness.currentCalls++
		return local.Current{Repository: repository}, nil
	}
	return harness
}

func (h *worktreeListHarness) dependencies() cmd.WorktreeListDependencies {
	return cmd.WorktreeListDependencies{
		Resolver: h.resolver,
		Getwd:    h.getcwd,
		Discover: func(
			context.Context,
			[]string,
			...local.DiscoveryOptions,
		) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{
				Repositories: h.repositories,
				Warnings:     h.warnings,
			}, h.discoverErr
		},
		Current: h.current,
		Enumerate: func(
			_ context.Context,
			repository local.Repository,
			_ string,
			_ ...local.WorktreeOptions,
		) ([]local.Worktree, error) {
			h.enumerateCalls++
			h.enumerated = repository
			return append([]local.Worktree(nil), h.worktrees...), h.enumerateErr
		},
		Stdout: &h.stdout,
		Stderr: &h.stderr,
	}
}

type worktreeListRootResolver struct {
	result rootpkg.Result
	err    error
	calls  int
}

func (r *worktreeListRootResolver) Resolve() (rootpkg.Result, error) {
	r.calls++
	return r.result, r.err
}

type worktreeListFailingWriter struct {
	err error
}

func (w worktreeListFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type worktreeListShortWriter struct{}

func (worktreeListShortWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return len(data) - 1, nil
}

func worktreeListRepository(identity, suffix string) local.Repository {
	parts := strings.Split(identity, "/")
	root := worktreeListAbsolute("repositories")
	return local.Repository{
		Identity:  identity,
		Host:      parts[0],
		Owner:     parts[1],
		Repo:      parts[2],
		Path:      filepath.Join(root, parts[0], parts[1], parts[2], suffix),
		Root:      root,
		RootIndex: 0,
	}
}

func worktreeListMain(
	repository local.Repository,
	head, branch string,
) local.Worktree {
	return local.Worktree{
		Repository: repository,
		Identity:   repository.Identity,
		Path:       repository.Path,
		HEAD:       head,
		Branch:     branch,
		Main:       true,
	}
}

func worktreeListLinked(
	repository local.Repository,
	slot, path, head, branch string,
	detached bool,
) local.Worktree {
	return local.Worktree{
		Repository: repository,
		Identity:   repository.Identity + "@" + slot,
		Slot:       slot,
		Path:       path,
		HEAD:       head,
		Branch:     branch,
		Detached:   detached,
	}
}

func worktreeListHash(value byte) string {
	return strings.Repeat(string(value), 40)
}

func worktreeListAbsolute(parts ...string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(append([]string{`C:\`}, parts...)...)
	}
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}
