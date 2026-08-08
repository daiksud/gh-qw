package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestRemoveParseSelectionExactFormsAndFirstDelimiter(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		input    string
		selector string
		slot     string
		linked   bool
	}{
		{input: "widget", selector: "widget"},
		{input: "acme/widget", selector: "acme/widget"},
		{
			input:    "github.com/acme/widget",
			selector: "github.com/acme/widget",
		},
		{
			input:    "github.com/acme/widget@feature/team/login",
			selector: "github.com/acme/widget",
			slot:     "feature/team/login",
			linked:   true,
		},
		{
			input:    "acme/widget@feature@tail",
			selector: "acme/widget",
			slot:     "feature@tail",
			linked:   true,
		},
	} {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()

			got, err := removeParseSelection(test.input)
			if err != nil {
				t.Fatalf("removeParseSelection() error = %v", err)
			}
			if got.selector != test.selector ||
				got.slot != test.slot ||
				got.linked != test.linked {
				t.Fatalf("removeParseSelection() = %#v", got)
			}
		})
	}

	for _, input := range []string{
		"",
		".",
		"../widget",
		"/absolute/widget",
		"https://github.com/acme/widget",
		"owner/repo/extra/path",
		"widget@",
		"widget@HEAD",
	} {
		input := input
		t.Run("invalid-"+input, func(t *testing.T) {
			t.Parallel()

			if _, err := removeParseSelection(input); err == nil {
				t.Fatalf("removeParseSelection(%q) error = nil", input)
			}
		})
	}
}

func TestNewRemoveCommandRejectsUsageBeforeResolution(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		nil,
		{"one", "two"},
		{"https://github.com/acme/widget"},
		{"widget@"},
		{"--unknown", "widget"},
	} {
		args := append([]string(nil), args...)
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()

			fixture := newRemoveTestFixture(t, "feature/test")
			err := fixture.execute(args...)
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want usage", err)
			}
			if fixture.resolver.calls != 0 {
				t.Fatalf("Resolve() calls = %d, want 0", fixture.resolver.calls)
			}
			fixture.assertNoMutation(t)
		})
	}
}

func TestNewRemoveCommandAcceptsEveryExactSelectorForm(t *testing.T) {
	t.Parallel()

	for _, selector := range []string{
		"widget",
		"acme/widget",
		"github.com/acme/widget",
	} {
		selector := selector
		t.Run(selector, func(t *testing.T) {
			t.Parallel()

			fixture := newRemoveTestFixture(t)
			if err := fixture.execute("--dry-run", selector); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if fixture.promptCalls != 0 {
				t.Fatalf("Prompt() calls = %d, want 0", fixture.promptCalls)
			}
			fixture.assertNoMutation(t)
			fixture.assertStdoutEmpty(t)
		})
	}
}

func TestNewRemoveCommandDryRunValidatesAndPrintsExactStablePlan(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t, "zeta", "feature/alpha")
	if err := fixture.execute("--dry-run", "acme/widget"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "Removal plan:\n" +
		"  linked worktree: " + removeOutputPath(fixture.linkedPath("feature/alpha")) + "\n" +
		"  linked worktree: " + removeOutputPath(fixture.linkedPath("zeta")) + "\n" +
		"  main repository: " + removeOutputPath(fixture.repository.Path) + "\n"
	if got := fixture.stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if fixture.promptCalls != 0 {
		t.Fatalf("Prompt() calls = %d, want 0", fixture.promptCalls)
	}
	if got := fixture.git.statusCalls; len(got) != 3 {
		t.Fatalf("status calls = %#v, want every target", got)
	}
	fixture.assertNoMutation(t)
	fixture.assertStdoutEmpty(t)
}

func TestNewRemoveCommandDeclineAndNonTTYNeverMutate(t *testing.T) {
	t.Parallel()

	t.Run("decline", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t, "feature/no")
		fixture.prompt = func(context.Context, io.Writer, string) (bool, error) {
			fixture.promptCalls++
			return false, nil
		}
		err := fixture.execute("widget@feature/no")
		if !errors.Is(err, ErrRemoveDeclined) {
			t.Fatalf("Execute() error = %v, want ErrRemoveDeclined", err)
		}
		fixture.assertNoMutation(t)
	})

	t.Run("no controlling terminal", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t, "feature/non-tty")
		wantErr := errors.New("no controlling terminal")
		dependencies := fixture.dependencies()
		dependencies.Prompt = nil
		dependencies.OpenTerminal = func() (io.ReadCloser, error) {
			return nil, wantErr
		}
		command := NewRemoveCommand(dependencies)
		command.SetArgs([]string{"widget@feature/non-tty"})

		err := command.Execute()
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		fixture.assertNoMutation(t)
	})
}

func TestNewRemoveCommandUsesControllingTerminalNotCommandInput(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t, "feature/tty")
	dependencies := fixture.dependencies()
	dependencies.Prompt = nil
	dependencies.OpenTerminal = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("yes\n")), nil
	}
	command := NewRemoveCommand(dependencies)
	command.SetIn(strings.NewReader("widget\nno\n"))
	command.SetArgs([]string{"widget@feature/tty"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Lstat(fixture.linkedPath("feature/tty")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("linked target remains: %v", err)
	}
	if _, err := os.Stat(fixture.repository.Path); err != nil {
		t.Fatalf("main repository changed: %v", err)
	}
	if !strings.Contains(fixture.stderr.String(), "Proceed with removal? [y/N] ") {
		t.Fatalf("stderr has no confirmation prompt: %q", fixture.stderr.String())
	}
	fixture.assertStdoutEmpty(t)
}

func TestRemoveConfirmDefaultsToNo(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		response string
		want     bool
	}{
		{response: "y\n", want: true},
		{response: "YES\n", want: true},
		{response: "\n"},
		{response: "n\n"},
		{response: "anything\n"},
	} {
		test := test
		t.Run(strings.TrimSpace(test.response), func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			got, err := removeConfirm(
				context.Background(),
				&output,
				"Confirm [y/N] ",
				func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(test.response)), nil
				},
			)
			if err != nil {
				t.Fatalf("removeConfirm() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("removeConfirm() = %v, want %v", got, test.want)
			}
			if output.String() != "Confirm [y/N] " {
				t.Fatalf("prompt output = %q", output.String())
			}
		})
	}
}

func TestNewRemoveCommandCompletesAllCleanlinessChecksBeforePrompt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		dirty  string
		output []byte
	}{
		{name: "tracked linked", dirty: "feature/dirty", output: []byte(" M file\x00")},
		{name: "untracked linked", dirty: "feature/dirty", output: []byte("?? new\x00")},
		{name: "dirty main", dirty: "main", output: []byte(" M README\x00")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newRemoveTestFixture(t, "alpha", "feature/dirty")
			path := fixture.linkedPath(test.dirty)
			if test.dirty == "main" {
				path = fixture.repository.Path
			}
			fixture.git.status[path] = test.output

			err := fixture.execute("widget")
			if !errors.Is(err, ErrRemoveSafety) {
				t.Fatalf("Execute() error = %v, want safety refusal", err)
			}
			if fixture.promptCalls != 0 {
				t.Fatalf("Prompt() calls = %d, want 0", fixture.promptCalls)
			}
			fixture.assertNoMutation(t)
			if fixture.stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want no plan before complete preflight", fixture.stderr.String())
			}
		})
	}
}

func TestNewRemoveCommandRejectsUnsafeLinkedTargetsBeforePrompt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		configure func(*removeTestFixture)
	}{
		{
			name: "locked",
			configure: func(fixture *removeTestFixture) {
				fixture.linkedRecord("feature/unsafe").Locked = true
			},
		},
		{
			name: "prunable",
			configure: func(fixture *removeTestFixture) {
				fixture.linkedRecord("feature/unsafe").Prunable = true
			},
		},
		{
			name: "bare",
			configure: func(fixture *removeTestFixture) {
				fixture.linkedRecord("feature/unsafe").Bare = true
			},
		},
		{
			name: "detached ambiguity",
			configure: func(fixture *removeTestFixture) {
				record := fixture.linkedRecord("feature/unsafe")
				record.Detached = true
				record.Branch = ""
			},
		},
		{
			name: "foreign association",
			configure: func(fixture *removeTestFixture) {
				fixture.associationErr = errors.New("foreign common directory")
			},
		},
		{
			name: "foreign repository",
			configure: func(fixture *removeTestFixture) {
				fixture.linkedRecord("feature/unsafe").Repository.Identity =
					"github.com/other/repository"
			},
		},
		{
			name: "external path",
			configure: func(fixture *removeTestFixture) {
				external := filepath.Join(fixture.base, "external")
				if err := os.MkdirAll(external, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(external, ".git"), []byte("gitdir"), 0o600); err != nil {
					t.Fatal(err)
				}
				fixture.linkedRecord("feature/unsafe").Path = external
			},
		},
		{
			name: "missing",
			configure: func(fixture *removeTestFixture) {
				if err := os.RemoveAll(fixture.linkedPath("feature/unsafe")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newRemoveTestFixture(t, "feature/unsafe")
			test.configure(fixture)
			err := fixture.execute("widget@feature/unsafe")
			if err == nil {
				t.Fatal("Execute() error = nil")
			}
			if fixture.promptCalls != 0 {
				t.Fatalf("Prompt() calls = %d, want 0", fixture.promptCalls)
			}
			if len(fixture.git.removals) != 0 {
				t.Fatalf("Git removals = %#v, want none", fixture.git.removals)
			}
			if len(fixture.removeAllCalls) != 0 {
				t.Fatalf("RemoveAll calls = %#v, want none", fixture.removeAllCalls)
			}
		})
	}
}

func TestNewRemoveCommandRejectsDuplicateCanonicalIdentityAsUsage(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t)
	duplicate := fixture.repository
	duplicate.Path = filepath.Join(fixture.base, "duplicate", "github.com", "acme", "widget")
	duplicate.Root = filepath.Join(fixture.base, "duplicate")
	duplicate.RootIndex = 1
	fixture.repositories = append(fixture.repositories, duplicate)

	err := fixture.execute("--dry-run", "github.com/acme/widget")
	if !errors.Is(err, repospec.ErrUsage) ||
		!errors.Is(err, local.ErrRepositoryAmbiguous) {
		t.Fatalf("Execute() error = %v, want ambiguity usage", err)
	}
	fixture.assertNoMutation(t)
}

func TestNewRemoveCommandRejectsSymlinkAndReplacementTOCTOU(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symbolic-link behavior is platform-specific")
	}

	t.Run("main .git symlink", func(t *testing.T) {
		fixture := newRemoveTestFixture(t)
		outside := filepath.Join(fixture.base, "outside-git")
		if err := os.Mkdir(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(fixture.repository.Path, ".git")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(fixture.repository.Path, ".git")); err != nil {
			t.Fatal(err)
		}

		err := fixture.execute("--dry-run", "widget")
		if !errors.Is(err, ErrRemoveSafety) {
			t.Fatalf("Execute() error = %v, want safety refusal", err)
		}
		fixture.assertNoMutation(t)
	})

	t.Run("linked path becomes symlink after prompt", func(t *testing.T) {
		fixture := newRemoveTestFixture(t, "feature/toctou")
		target := fixture.linkedPath("feature/toctou")
		outside := filepath.Join(fixture.base, "outside")
		if err := os.Mkdir(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(outside, "keep")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.prompt = func(context.Context, io.Writer, string) (bool, error) {
			fixture.promptCalls++
			if err := os.RemoveAll(target); err != nil {
				return false, err
			}
			return true, os.Symlink(outside, target)
		}

		err := fixture.execute("widget@feature/toctou")
		if err == nil || !strings.Contains(err.Error(), "revalidate removal plan") {
			t.Fatalf("Execute() error = %v, want revalidation failure", err)
		}
		if len(fixture.git.removals) != 0 {
			t.Fatalf("Git removals = %#v, want none", fixture.git.removals)
		}
		if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "keep" {
			t.Fatalf("outside sentinel changed: %q, %v", got, readErr)
		}
	})

	t.Run("same path replacement after prompt", func(t *testing.T) {
		fixture := newRemoveTestFixture(t, "feature/replaced")
		target := fixture.linkedPath("feature/replaced")
		original := filepath.Join(fixture.base, "original-linked")
		fixture.prompt = func(context.Context, io.Writer, string) (bool, error) {
			fixture.promptCalls++
			if err := os.Rename(target, original); err != nil {
				return false, err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return false, err
			}
			return true, os.WriteFile(filepath.Join(target, ".git"), []byte("replacement"), 0o600)
		}

		err := fixture.execute("widget@feature/replaced")
		if !errors.Is(err, ErrRemoveSafety) ||
			!strings.Contains(err.Error(), "was replaced") {
			t.Fatalf("Execute() error = %v, want same-file refusal", err)
		}
		if len(fixture.git.removals) != 0 {
			t.Fatalf("Git removals = %#v, want none", fixture.git.removals)
		}
	})
}

func TestNewRemoveCommandLinkedOnlyRemovesGitTargetAndNestedParents(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t, "feature/team/login@v2")
	target := fixture.linkedPath("feature/team/login@v2")
	parent := filepath.Dir(target)
	grandparent := filepath.Dir(parent)

	if err := fixture.execute("acme/widget@feature/team/login@v2"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.git.removals) != 1 {
		t.Fatalf("Git removals = %#v", fixture.git.removals)
	}
	if fixture.git.removals[0].options.Force {
		t.Fatal("WorktreeRemove used force")
	}
	if fixture.git.removals[0].dir != fixture.repository.Path ||
		fixture.git.removals[0].options.Path != target {
		t.Fatalf("WorktreeRemove call = %#v", fixture.git.removals[0])
	}
	for _, path := range []string{target, parent, grandparent} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%q remains: %v", path, err)
		}
	}
	if info, err := os.Stat(fixture.worktreeBase); err != nil || !info.IsDir() {
		t.Fatalf("per-repository worktree base changed: %v, %v", info, err)
	}
	if info, err := os.Stat(fixture.repository.Path); err != nil || !info.IsDir() {
		t.Fatalf("main repository changed: %v, %v", info, err)
	}
	if len(fixture.removeAllCalls) != 0 {
		t.Fatalf("RemoveAll calls = %#v, want none", fixture.removeAllCalls)
	}
	wantProgress := "removed linked worktree " + removeOutputPath(target) + "\n"
	if !strings.HasSuffix(fixture.stderr.String(), wantProgress) {
		t.Fatalf("stderr = %q, want suffix %q", fixture.stderr.String(), wantProgress)
	}
	fixture.assertStdoutEmpty(t)
}

func TestNewRemoveCommandHerdrIntegration(t *testing.T) {
	t.Run("explicit flag inside session closes the found workspace", func(t *testing.T) {
		branch := "feature/herdr"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = alwaysInSession
		fixture.herdr.findID = "w2"
		fixture.herdr.findFound = true
		target := fixture.linkedPath(branch)

		if err := fixture.execute("--herdr", "acme/widget@"+branch); err != nil {
			t.Fatalf("Execute() error = %v; stderr = %q", err, fixture.stderr.String())
		}
		if len(fixture.git.removals) != 1 {
			t.Fatalf("Git removals = %#v, want 1", fixture.git.removals)
		}
		if len(fixture.herdr.findCalls) != 1 {
			t.Fatalf("FindWorkspaceForPath() calls = %d, want 1", len(fixture.herdr.findCalls))
		}
		wantFind := worktreeRemoveHerdrFind{repoPath: fixture.repository.Path, worktreePath: target}
		if got := fixture.herdr.findCalls[0]; got != wantFind {
			t.Fatalf("FindWorkspaceForPath() call = %#v, want %#v", got, wantFind)
		}
		if got := fixture.herdr.closeCalls; !reflect.DeepEqual(got, []string{"w2"}) {
			t.Fatalf("CloseWorkspace() calls = %#v, want [w2]", got)
		}
	})

	t.Run("explicit flag outside session is a usage error before any mutation", func(t *testing.T) {
		branch := "feature/herdr-outside"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = neverInSession

		err := fixture.execute("--herdr", "acme/widget@"+branch)
		if err == nil || !strings.Contains(err.Error(), "HERDR_ENV") {
			t.Fatalf("Execute() error = %v, want it to mention HERDR_ENV", err)
		}
		if !errors.Is(err, repospec.ErrUsage) {
			t.Fatalf("Execute() error = %v, want repospec.ErrUsage", err)
		}
		fixture.assertNoMutation(t)
		if len(fixture.herdr.findCalls) != 0 || len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("Herdr calls = find %d, close %d, want 0 and 0",
				len(fixture.herdr.findCalls), len(fixture.herdr.closeCalls))
		}
	})

	t.Run("no workspace found skips close without error", func(t *testing.T) {
		branch := "feature/herdr-no-workspace"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = alwaysInSession
		fixture.herdr.findFound = false

		if err := fixture.execute("--herdr", "acme/widget@"+branch); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(fixture.herdr.findCalls) != 1 {
			t.Fatalf("FindWorkspaceForPath() calls = %d, want 1", len(fixture.herdr.findCalls))
		}
		if len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("CloseWorkspace() calls = %d, want 0", len(fixture.herdr.closeCalls))
		}
		if len(fixture.git.removals) != 1 {
			t.Fatalf("Git removals = %d, want 1", len(fixture.git.removals))
		}
	})

	t.Run("configuration default outside session warns and skips", func(t *testing.T) {
		branch := "feature/herdr-config-outside"
		fixture := newRemoveTestFixture(t, branch)
		fixture.resolver.result.Herdr = true
		fixture.lookupEnv = neverInSession

		if err := fixture.execute("acme/widget@" + branch); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(fixture.herdr.findCalls) != 0 || len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("Herdr calls = find %d, close %d, want 0 and 0",
				len(fixture.herdr.findCalls), len(fixture.herdr.closeCalls))
		}
		if len(fixture.git.removals) != 1 {
			t.Fatalf("Git removals = %d, want 1", len(fixture.git.removals))
		}
		if !strings.Contains(fixture.stderr.String(), "HERDR_ENV") {
			t.Fatalf("stderr = %q, want it to mention HERDR_ENV", fixture.stderr.String())
		}
	})

	t.Run("no-herdr overrides the configuration default", func(t *testing.T) {
		branch := "feature/no-herdr"
		fixture := newRemoveTestFixture(t, branch)
		fixture.resolver.result.Herdr = true
		fixture.lookupEnv = alwaysInSession

		if err := fixture.execute("--no-herdr", "acme/widget@"+branch); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(fixture.herdr.findCalls) != 0 || len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("Herdr calls = find %d, close %d, want 0 and 0",
				len(fixture.herdr.findCalls), len(fixture.herdr.closeCalls))
		}
	})

	t.Run("herdr and no-herdr together is a usage error", func(t *testing.T) {
		branch := "feature/both-flags"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = alwaysInSession

		err := fixture.execute("--herdr", "--no-herdr", "acme/widget@"+branch)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("Execute() error = %v, want it to mention mutual exclusivity", err)
		}
		if !errors.Is(err, repospec.ErrUsage) {
			t.Fatalf("Execute() error = %v, want repospec.ErrUsage", err)
		}
		fixture.assertNoMutation(t)
	})

	t.Run("close failure surfaces after the worktree is already removed", func(t *testing.T) {
		branch := "feature/herdr-close-fails"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = alwaysInSession
		fixture.herdr.findID = "w3"
		fixture.herdr.findFound = true
		wantErr := errors.New("close failed")
		fixture.herdr.closeErr = wantErr
		target := fixture.linkedPath(branch)

		err := fixture.execute("--herdr", "acme/widget@"+branch)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		if errors.Is(err, repospec.ErrUsage) {
			t.Fatalf("Execute() error = %v, want an ordinary error, not a usage error", err)
		}
		if len(fixture.git.removals) != 1 {
			t.Fatalf("Git removals = %d, want 1", len(fixture.git.removals))
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("target remains after Git removal: %v", statErr)
		}
	})

	t.Run("find failure surfaces without blocking removal or attempting close", func(t *testing.T) {
		branch := "feature/herdr-find-fails"
		fixture := newRemoveTestFixture(t, branch)
		fixture.lookupEnv = alwaysInSession
		wantErr := errors.New("find failed")
		fixture.herdr.findErr = wantErr
		fixture.herdr.findFound = true
		fixture.herdr.findID = "w4"
		target := fixture.linkedPath(branch)

		err := fixture.execute("--herdr", "acme/widget@"+branch)
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		if len(fixture.git.removals) != 1 {
			t.Fatalf("Git removals = %d, want 1", len(fixture.git.removals))
		}
		if len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("CloseWorkspace() calls = %d, want 0 (find already failed)", len(fixture.herdr.closeCalls))
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("target remains after Git removal: %v", statErr)
		}
	})

	t.Run("whole-repository removal ignores herdr flags without effect", func(t *testing.T) {
		fixture := newRemoveTestFixture(t, "feature/alpha")
		fixture.lookupEnv = neverInSession

		if err := fixture.execute("--herdr", "github.com/acme/widget"); err != nil {
			t.Fatalf("Execute() error = %v; stderr = %q", err, fixture.stderr.String())
		}
		if len(fixture.herdr.findCalls) != 0 || len(fixture.herdr.closeCalls) != 0 {
			t.Fatalf("Herdr calls = find %d, close %d, want 0 and 0 for a whole-repository removal",
				len(fixture.herdr.findCalls), len(fixture.herdr.closeCalls))
		}
	})
}

func TestNewRemoveCommandWholeRepositoryStableOrderExactBoundaryAndCleanup(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t, "zeta", "feature/alpha")
	mainPath := fixture.repository.Path
	alphaPath := fixture.linkedPath("feature/alpha")
	zetaPath := fixture.linkedPath("zeta")

	if err := fixture.execute("github.com/acme/widget"); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, fixture.stderr.String())
	}
	gotOrder := []string{
		fixture.git.removals[0].options.Path,
		fixture.git.removals[1].options.Path,
	}
	wantOrder := []string{alphaPath, zetaPath}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("Git removal order = %#v, want %#v", gotOrder, wantOrder)
	}
	for _, removal := range fixture.git.removals {
		if removal.options.Force {
			t.Fatalf("WorktreeRemove call used force: %#v", removal)
		}
	}
	if !reflect.DeepEqual(fixture.removeAllCalls, []string{mainPath}) {
		t.Fatalf("RemoveAll calls = %#v, want exact main only", fixture.removeAllCalls)
	}
	for _, path := range []string{alphaPath, zetaPath, mainPath, fixture.worktreeBase} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%q remains: %v", path, err)
		}
	}
	for _, root := range []string{fixture.repositoryRoot, fixture.worktreeRoot} {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			t.Fatalf("root %q changed: %v, %v", root, info, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(fixture.repositoryRoot, "github.com")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty repository host parent remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.worktreeRoot, "github.com")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty worktree host parent remains: %v", err)
	}
	fixture.assertStdoutEmpty(t)
}

func TestNewRemoveCommandNeverDeletesSiblingRepositoryOrRoot(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t)
	sibling := filepath.Join(
		fixture.repositoryRoot,
		"github.com",
		"acme",
		"sibling",
	)
	if err := os.MkdirAll(filepath.Join(sibling, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sibling, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fixture.execute("widget"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !reflect.DeepEqual(fixture.removeAllCalls, []string{fixture.repository.Path}) {
		t.Fatalf("RemoveAll calls = %#v", fixture.removeAllCalls)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("sibling sentinel changed: %q, %v", got, err)
	}
	if info, err := os.Stat(fixture.repositoryRoot); err != nil || !info.IsDir() {
		t.Fatalf("repository root changed: %v, %v", info, err)
	}
}

func TestNewRemoveCommandGitFailureReportsExactPartialState(t *testing.T) {
	t.Parallel()

	fixture := newRemoveTestFixture(t, "alpha", "zeta")
	wantErr := errors.New("second Git removal failed")
	fixture.git.failPath = fixture.linkedPath("zeta")
	fixture.git.failErr = wantErr

	err := fixture.execute("widget")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	alpha := removeOutputPath(fixture.linkedPath("alpha"))
	zeta := removeOutputPath(fixture.linkedPath("zeta"))
	main := removeOutputPath(fixture.repository.Path)
	for _, text := range []string{
		"partial state",
		"removed [" + alpha + "]",
		"remaining [" + zeta + ", " + main + "]",
	} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("Execute() error = %q, want %q", err, text)
		}
	}
	if _, statErr := os.Lstat(fixture.linkedPath("alpha")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("first linked target remains: %v", statErr)
	}
	if _, statErr := os.Stat(fixture.linkedPath("zeta")); statErr != nil {
		t.Fatalf("failed linked target changed: %v", statErr)
	}
	if _, statErr := os.Stat(fixture.repository.Path); statErr != nil {
		t.Fatalf("main repository changed after partial failure: %v", statErr)
	}
	if len(fixture.removeAllCalls) != 0 {
		t.Fatalf("RemoveAll calls = %#v, want none", fixture.removeAllCalls)
	}
}

func TestNewRemoveCommandRevalidatesMainAfterLinkedMutation(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symbolic-link behavior is platform-specific")
	}

	fixture := newRemoveTestFixture(t, "feature/one")
	outside := filepath.Join(fixture.base, "outside-main")
	if err := os.MkdirAll(filepath.Join(outside, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalMain := fixture.repository.Path + "-original"
	fixture.git.afterRemove = func(string) error {
		if err := os.Rename(fixture.repository.Path, originalMain); err != nil {
			return err
		}
		return os.Symlink(outside, fixture.repository.Path)
	}

	err := fixture.execute("widget")
	if err == nil || !strings.Contains(err.Error(), "partial state") {
		t.Fatalf("Execute() error = %v, want partial-state refusal", err)
	}
	if len(fixture.removeAllCalls) != 0 {
		t.Fatalf("RemoveAll calls = %#v, want none", fixture.removeAllCalls)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "keep" {
		t.Fatalf("outside sentinel changed: %q, %v", got, readErr)
	}
}

func TestNewRemoveCommandWarningsAndWriterErrors(t *testing.T) {
	t.Parallel()

	t.Run("warning prefix", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t)
		fixture.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
			Operation: "inspect",
			Err:       errors.New("denied"),
		}}
		if err := fixture.execute("--dry-run", "widget"); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.HasPrefix(fixture.stderr.String(), "gh-qw: warning: ") {
			t.Fatalf("stderr = %q, want warning prefix", fixture.stderr.String())
		}
	})

	t.Run("warning writer failure", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t)
		fixture.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      "entry",
			Operation: "inspect",
		}}
		wantErr := errors.New("warning write failed")
		fixture.stderrWriter = &removeTestFailWriter{err: wantErr}
		err := fixture.execute("--dry-run", "widget")
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		fixture.assertNoMutation(t)
	})

	t.Run("plan short write", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t)
		fixture.stderrWriter = removeTestShortWriter{}
		err := fixture.execute("--dry-run", "widget")
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("Execute() error = %v, want io.ErrShortWrite", err)
		}
		fixture.assertNoMutation(t)
	})

	t.Run("progress writer failure reports completed target", func(t *testing.T) {
		t.Parallel()

		fixture := newRemoveTestFixture(t, "feature/write")
		wantErr := errors.New("progress write failed")
		fixture.stderrWriter = &removeTestNthWriteError{failAt: 2, err: wantErr}
		err := fixture.execute("widget@feature/write")
		if !errors.Is(err, wantErr) ||
			!strings.Contains(err.Error(), "removed ["+removeOutputPath(fixture.linkedPath("feature/write"))+"]") {
			t.Fatalf("Execute() error = %v, want explicit completed target", err)
		}
		if _, statErr := os.Lstat(fixture.linkedPath("feature/write")); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("linked target remains: %v", statErr)
		}
	})
}

func TestNewRemoveCommandRealGitWholeLifecycle(t *testing.T) {
	base := removeTestPhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	for _, path := range []string{repositoryPath, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	removeTestRunGit(t, repositoryPath, "init", "-b", "main")
	removeTestRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	removeTestRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeTestRunGit(t, repositoryPath, "add", "README")
	removeTestRunGit(t, repositoryPath, "commit", "-m", "initial")

	paths := make(map[string]string)
	for _, slot := range []string{"zeta", "feature/alpha"} {
		path := filepath.Join(
			worktreeRoot,
			"github.com",
			"acme",
			"widget",
			filepath.FromSlash(slot),
		)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		removeTestRunGit(t, repositoryPath, "worktree", "add", "-b", slot, path, "HEAD")
		paths[slot] = path
	}

	var stdout, stderr bytes.Buffer
	command := NewRemoveCommand(RemoveDependencies{
		Resolver: &removeTestResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Prompt: func(context.Context, io.Writer, string) (bool, error) {
			return true, nil
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	for _, path := range []string{
		paths["feature/alpha"],
		paths["zeta"],
		repositoryPath,
		filepath.Join(worktreeRoot, "github.com", "acme", "widget"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%q remains after lifecycle: %v", path, err)
		}
	}
	for _, root := range []string{repositoryRoot, worktreeRoot} {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			t.Fatalf("root %q changed: %v, %v", root, info, err)
		}
	}
	alphaIndex := strings.Index(stderr.String(), removeOutputPath(paths["feature/alpha"]))
	zetaIndex := strings.Index(stderr.String(), removeOutputPath(paths["zeta"]))
	if alphaIndex < 0 || zetaIndex < 0 || alphaIndex >= zetaIndex {
		t.Fatalf("stderr does not preserve stable alpha-before-zeta order: %q", stderr.String())
	}
}

type removeTestResolver struct {
	result rootpkg.Result
	err    error
	calls  int
}

func (resolver *removeTestResolver) Resolve() (rootpkg.Result, error) {
	resolver.calls++
	return resolver.result, resolver.err
}

type removeTestRemoval struct {
	dir     string
	options gitcmd.WorktreeRemoveOptions
}

type removeTestGit struct {
	repositoryPath string
	status         map[string][]byte
	statusErr      map[string]error
	statusCalls    []string
	registered     []gitcmd.Worktree
	removals       []removeTestRemoval
	failPath       string
	failErr        error
	afterRemove    func(string) error
}

func (git *removeTestGit) OutputDir(
	_ context.Context,
	dir string,
	args ...string,
) ([]byte, error) {
	switch {
	case reflect.DeepEqual(args, []string{"rev-parse", "--show-toplevel"}):
		return []byte(git.repositoryPath + "\n"), nil
	case reflect.DeepEqual(args, []string{"rev-parse", "--git-common-dir"}):
		return []byte(filepath.Join(git.repositoryPath, ".git") + "\n"), nil
	case len(args) == 5 &&
		args[0] == "status" &&
		args[1] == "--porcelain=v1" &&
		args[2] == "-z" &&
		args[3] == "--untracked-files=all" &&
		args[4] == "--ignore-submodules=none":
		git.statusCalls = append(git.statusCalls, dir)
		if err := git.statusErr[dir]; err != nil {
			return nil, err
		}
		return append([]byte(nil), git.status[dir]...), nil
	default:
		return nil, errors.New("unexpected Git OutputDir call")
	}
}

func (git *removeTestGit) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	return append([]gitcmd.Worktree(nil), git.registered...), nil
}

func (git *removeTestGit) WorktreeRemove(
	_ context.Context,
	dir string,
	options gitcmd.WorktreeRemoveOptions,
) error {
	git.removals = append(git.removals, removeTestRemoval{
		dir:     dir,
		options: options,
	})
	if removeSamePath(options.Path, git.failPath) {
		return git.failErr
	}
	if err := os.RemoveAll(options.Path); err != nil {
		return err
	}
	remaining := git.registered[:0]
	for _, worktree := range git.registered {
		if !removeSamePath(worktree.Path, options.Path) {
			remaining = append(remaining, worktree)
		}
	}
	git.registered = append([]gitcmd.Worktree(nil), remaining...)
	if git.afterRemove != nil {
		return git.afterRemove(options.Path)
	}
	return nil
}

type removeTestFixture struct {
	base           string
	repositoryRoot string
	worktreeRoot   string
	worktreeBase   string
	repository     local.Repository
	repositories   []local.Repository
	worktrees      []*local.Worktree
	resolver       *removeTestResolver
	git            *removeTestGit
	warnings       []local.Warning
	discoveryErr   error
	associationErr error
	prompt         RemovePrompt
	promptCalls    int
	removeAllCalls []string
	parentRemovals []string
	herdr          *worktreeRemoveHerdr
	lookupEnv      func(string) (string, bool)
	stdout         bytes.Buffer
	stderr         bytes.Buffer
	stderrWriter   io.Writer
}

func newRemoveTestFixture(t *testing.T, slots ...string) *removeTestFixture {
	t.Helper()

	base := removeTestPhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	worktreeBase := filepath.Join(worktreeRoot, "github.com", "acme", "widget")
	for _, path := range []string{
		filepath.Join(repositoryPath, ".git"),
		worktreeRoot,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repository := local.Repository{
		Identity:  "github.com/acme/widget",
		Host:      "github.com",
		Owner:     "acme",
		Repo:      "widget",
		Path:      repositoryPath,
		Root:      repositoryRoot,
		RootIndex: 0,
	}
	main := &local.Worktree{
		Repository: repository,
		Identity:   repository.Identity,
		Path:       repository.Path,
		HEAD:       "main-head",
		Branch:     "main",
		Main:       true,
	}

	fixture := &removeTestFixture{
		base:           base,
		repositoryRoot: repositoryRoot,
		worktreeRoot:   worktreeRoot,
		worktreeBase:   worktreeBase,
		repository:     repository,
		repositories:   []local.Repository{repository},
		worktrees:      []*local.Worktree{main},
		herdr:          &worktreeRemoveHerdr{},
		lookupEnv:      func(string) (string, bool) { return "", false },
	}
	fixture.resolver = &removeTestResolver{result: rootpkg.Result{
		RepositoryRoots: []string{repositoryRoot},
		WorktreeRoot:    worktreeRoot,
	}}
	fixture.git = &removeTestGit{
		repositoryPath: repositoryPath,
		status:         make(map[string][]byte),
		statusErr:      make(map[string]error),
		registered: []gitcmd.Worktree{{
			Path:   repositoryPath,
			HEAD:   "main-head",
			Branch: "main",
		}},
	}
	fixture.prompt = func(context.Context, io.Writer, string) (bool, error) {
		fixture.promptCalls++
		return true, nil
	}
	for _, slot := range slots {
		fixture.addLinked(t, slot)
	}
	return fixture
}

func (fixture *removeTestFixture) addLinked(t *testing.T, slot string) {
	t.Helper()

	path := fixture.linkedPath(slot)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := &local.Worktree{
		Repository: fixture.repository,
		Identity:   fixture.repository.Identity + "@" + slot,
		Slot:       slot,
		Path:       path,
		HEAD:       "head-" + slot,
		Branch:     slot,
	}
	fixture.worktrees = append(fixture.worktrees, record)
	fixture.git.registered = append(fixture.git.registered, gitcmd.Worktree{
		Path:   path,
		HEAD:   record.HEAD,
		Branch: slot,
	})
}

func (fixture *removeTestFixture) linkedPath(slot string) string {
	return filepath.Join(fixture.worktreeBase, filepath.FromSlash(slot))
}

func (fixture *removeTestFixture) linkedRecord(slot string) *local.Worktree {
	for _, worktree := range fixture.worktrees {
		if worktree.Slot == slot {
			return worktree
		}
	}
	panic("missing linked test record " + slot)
}

func (fixture *removeTestFixture) registered(path string) bool {
	for _, worktree := range fixture.git.registered {
		if removeSamePath(worktree.Path, path) {
			return true
		}
	}
	return false
}

func (fixture *removeTestFixture) currentWorktrees() []local.Worktree {
	result := make([]local.Worktree, 0, len(fixture.worktrees))
	for _, worktree := range fixture.worktrees {
		if fixture.registered(worktree.Path) {
			result = append(result, *worktree)
		}
	}
	return result
}

func (fixture *removeTestFixture) dependencies() RemoveDependencies {
	stderr := fixture.stderrWriter
	if stderr == nil {
		stderr = &fixture.stderr
	}
	return RemoveDependencies{
		Resolver: fixture.resolver,
		Discover: func(
			context.Context,
			[]string,
			...local.DiscoveryOptions,
		) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{
				Repositories: append([]local.Repository(nil), fixture.repositories...),
				Warnings:     append([]local.Warning(nil), fixture.warnings...),
			}, fixture.discoveryErr
		},
		ResolveManaged: func(
			_ context.Context,
			repository local.Repository,
			_ string,
			slot string,
			_ ...local.ManagedWorktreeOptions,
		) (local.Worktree, error) {
			for _, worktree := range fixture.currentWorktrees() {
				if worktree.Slot == slot {
					return worktree, nil
				}
			}
			return local.Worktree{}, &local.WorktreeError{
				Kind:   local.ErrWorktreeNotFound,
				Slot:   slot,
				Reason: "slot is not registered",
			}
		},
		Enumerate: func(
			context.Context,
			local.Repository,
			string,
			...local.WorktreeOptions,
		) ([]local.Worktree, error) {
			return fixture.currentWorktrees(), nil
		},
		ValidateAssociation: func(
			context.Context,
			local.Repository,
			local.Worktree,
			string,
			...local.AssociationOptions,
		) error {
			return fixture.associationErr
		},
		Git:    fixture.git,
		Prompt: fixture.prompt,
		Remove: func(path string) error {
			fixture.parentRemovals = append(fixture.parentRemovals, path)
			return os.Remove(path)
		},
		RemoveAll: func(path string) error {
			fixture.removeAllCalls = append(fixture.removeAllCalls, path)
			return os.RemoveAll(path)
		},
		Herdr:     fixture.herdr,
		LookupEnv: fixture.lookupEnv,
		Stdout:    &fixture.stdout,
		Stderr:    stderr,
	}
}

func (fixture *removeTestFixture) execute(args ...string) error {
	command := NewRemoveCommand(fixture.dependencies())
	command.SetArgs(args)
	return command.Execute()
}

func (fixture *removeTestFixture) assertNoMutation(t *testing.T) {
	t.Helper()

	if len(fixture.git.removals) != 0 {
		t.Fatalf("Git removals = %#v, want none", fixture.git.removals)
	}
	if len(fixture.removeAllCalls) != 0 {
		t.Fatalf("RemoveAll calls = %#v, want none", fixture.removeAllCalls)
	}
	if _, err := os.Stat(fixture.repository.Path); err != nil {
		t.Fatalf("main repository changed: %v", err)
	}
}

func (fixture *removeTestFixture) assertStdoutEmpty(t *testing.T) {
	t.Helper()
	if fixture.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", fixture.stdout.String())
	}
}

type removeTestFailWriter struct {
	err error
}

func (writer *removeTestFailWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type removeTestShortWriter struct{}

func (removeTestShortWriter) Write([]byte) (int, error) {
	return 0, nil
}

type removeTestNthWriteError struct {
	calls  int
	failAt int
	err    error
}

func (writer *removeTestNthWriteError) Write(data []byte) (int, error) {
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, writer.err
	}
	return len(data), nil
}

func removeTestPhysicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func removeTestRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
}
