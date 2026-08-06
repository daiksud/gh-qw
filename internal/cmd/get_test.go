package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/ghapi"
	"github.com/daiksud/gh-qw/internal/ghauth"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/procio"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewGetCommandSurface(t *testing.T) {
	t.Parallel()

	command := cmd.NewGetCommand(cmd.GetDependencies{})
	if got, want := command.Name(), "get"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	if len(command.Aliases) != 1 || command.Aliases[0] != "clone" {
		t.Fatalf("Aliases = %v, want [clone]", command.Aliases)
	}
}

func TestNewGetCommandFlagsAndCloneOptions(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var (
		gotClone     ghcmd.CloneOptions
		gotGitStdout io.Writer
		gotGitStderr io.Writer
	)
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			gotClone = options
			return nil
		},
	}
	resolver := &getRootResolver{result: rootpkg.Result{RepositoryRoots: []string{root}}}
	var discoverCalls int
	var output bytes.Buffer
	command := cmd.NewGetCommand(cmd.GetDependencies{
		RootResolver: resolver,
		GitFactory: func(stdout, stderr io.Writer, _ string) cmd.GetGitOperations {
			gotGitStdout = stdout
			gotGitStderr = stderr
			return git
		},
		Discover: func(context.Context, []string) (local.DiscoveryResult, error) {
			discoverCalls++
			return local.DiscoveryResult{}, nil
		},
		AccountResolver: getNoopAccountResolver{},
		Stdout:          &output,
		Stderr:          io.Discard,
		Stdin:           strings.NewReader(""),
		IsTerminal:      func(io.Reader) bool { return false },
	})
	command.SetArgs([]string{
		"--update",
		"-p",
		"--shallow",
		"--branch", "topic",
		"--no-recursive",
		"-P",
		"--partial", "blobless",
		"acme/widget",
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := gotClone.URL, "ssh://git@github.com/acme/widget"; got != want {
		t.Fatalf("Clone URL = %q, want %q", got, want)
	}
	if !gotClone.Shallow {
		t.Fatal("Clone Shallow = false, want true")
	}
	if got, want := gotClone.Branch, "topic"; got != want {
		t.Fatalf("Clone Branch = %q, want %q", got, want)
	}
	if got, want := gotClone.Submodules, ghcmd.SubmodulesDisabled; got != want {
		t.Fatalf("Clone Submodules = %v, want %v", got, want)
	}
	if got, want := gotClone.Filter, ghcmd.PartialFilterBlobless; got != want {
		t.Fatalf("Clone Filter = %q, want %q", got, want)
	}
	if gotGitStdout != io.Discard || gotGitStderr != io.Discard {
		t.Fatalf("parallel Git streams = (%T, %T), want discarded", gotGitStdout, gotGitStderr)
	}
	if resolver.Calls() != 1 {
		t.Fatalf("Resolve() calls = %d, want 1", resolver.Calls())
	}
	if discoverCalls != 1 {
		t.Fatalf("Discover() calls = %d, want 1", discoverCalls)
	}
	if got, want := output.String(), filepath.ToSlash(gotClone.Destination)+"\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNewGetCommandPassesResolvedTokenToGitFactory(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	accountResolver := &getAccountResolver{
		resolution: ghauth.Resolution{Source: ghauth.SourceSelected, Login: "acme-bot", Token: "gho_scoped"},
	}
	var gotToken string
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	baseFactory := deps.GitFactory
	deps.GitFactory = func(stdout, stderr io.Writer, token string) cmd.GetGitOperations {
		gotToken = token
		return baseFactory(stdout, stderr, token)
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotToken != "gho_scoped" {
		t.Fatalf("GitFactory token = %q, want %q", gotToken, "gho_scoped")
	}
	calls := accountResolver.Calls()
	if len(calls) != 1 || calls[0].host != "github.com" || calls[0].owner != "acme" {
		t.Fatalf("Resolve() calls = %#v, want one call for (github.com, acme)", calls)
	}
}

func TestNewGetCommandStopsBeforeCloneWhenAccountResolutionFails(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantErr := errors.New("account cache is unreadable")
	cloneCalls := 0
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			cloneCalls++
			return nil
		},
	}, io.Discard, io.Discard)
	deps.AccountResolver = &getAccountResolver{err: wantErr}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if cloneCalls != 0 {
		t.Fatalf("Clone() calls = %d, want 0 after resolution failure", cloneCalls)
	}
}

func TestNewGetCommandUsesAmbientTokenForExplicitEnvironmentResolution(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	accountResolver := &getAccountResolver{resolution: ghauth.Resolution{Source: ghauth.SourceExplicitEnv}}
	var gotToken string
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	baseFactory := deps.GitFactory
	deps.GitFactory = func(stdout, stderr io.Writer, token string) cmd.GetGitOperations {
		gotToken = token
		return baseFactory(stdout, stderr, token)
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotToken != "" {
		t.Fatalf("GitFactory token = %q, want empty for explicit environment authentication", gotToken)
	}
}

func TestNewGetCommandParallelSkipsOnlyOwnersWhoseResolutionFailed(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantErr := errors.New("cannot choose an acme account")
	accountResolver := &getAccountResolver{
		resolve: func(_ string, owner string) (ghauth.Resolution, error) {
			if owner == "acme" {
				return ghauth.Resolution{}, wantErr
			}
			return ghauth.Resolution{
				Source: ghauth.SourceSelected,
				Login:  "other-bot",
				Token:  "gho_other",
			}, nil
		},
	}
	var cloneMu sync.Mutex
	var cloned []string
	deps := getDependencies(root, &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			cloneMu.Lock()
			defer cloneMu.Unlock()
			cloned = append(cloned, options.URL)
			return nil
		},
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"-P", "acme/one", "acme/two", "other/three"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to include %v", err, wantErr)
	}
	calls := accountResolver.Calls()
	if len(calls) != 2 {
		t.Fatalf("Resolve() calls = %#v, want one call for each unique owner", calls)
	}
	cloneMu.Lock()
	defer cloneMu.Unlock()
	if len(cloned) != 1 || !strings.Contains(cloned[0], "other/three") {
		t.Fatalf("Clone() URLs = %#v, want only the unaffected other/three job", cloned)
	}
}

func TestNewGetCommandParallelResolvesEachUniqueOwnerOnce(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	accountResolver := &getAccountResolver{
		resolution: ghauth.Resolution{Source: ghauth.SourceSelected, Login: "acme-bot", Token: "gho_scoped"},
	}
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"-P", "acme/one", "acme/two", "acme/three"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	calls := accountResolver.Calls()
	if len(calls) != 1 || calls[0].host != "github.com" || calls[0].owner != "acme" {
		t.Fatalf("Resolve() calls = %#v, want a single call for the shared owner (github.com, acme)", calls)
	}
}

func TestNewGetCommandCloneFailureHintsTheSelectedAccount(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantErr := errors.New("gh command failed with exit code 1")
	accountResolver := &getAccountResolver{
		resolution: ghauth.Resolution{Source: ghauth.SourceSelected, Login: "TE-DaikiSudo", Token: "gho_scoped"},
	}
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return wantErr },
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), `"TE-DaikiSudo"`) {
		t.Fatalf("Execute() error = %q, want it to name the selected account", err.Error())
	}
}

func TestNewGetCommandCloneFailureOmitsHintForExplicitEnvironmentToken(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantErr := errors.New("gh command failed with exit code 1")
	accountResolver := &getAccountResolver{
		resolution: ghauth.Resolution{Source: ghauth.SourceExplicitEnv},
	}
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return wantErr },
	}, io.Discard, io.Discard)
	deps.AccountResolver = accountResolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if strings.Contains(err.Error(), "used gh account") {
		t.Fatalf("Execute() error = %q, want no account hint for an explicit environment token", err.Error())
	}
}

func TestNewGetCommandMapsTreelessPartialClone(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var got ghcmd.CloneOptions
	command := cmd.NewGetCommand(getDependencies(root, &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			got = options
			return nil
		},
	}, io.Discard, io.Discard))
	command.SetArgs([]string{"--partial", "treeless", "acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Filter != ghcmd.PartialFilterTreeless {
		t.Fatalf("Clone Filter = %q, want %q", got.Filter, ghcmd.PartialFilterTreeless)
	}
	if got.Submodules != ghcmd.SubmodulesRecursive {
		t.Fatalf("Clone Submodules = %v, want recursive", got.Submodules)
	}
}

func TestNewGetCommandExposesOnlyDocumentedFlags(t *testing.T) {
	t.Parallel()

	command := cmd.NewGetCommand(cmd.GetDependencies{})
	var names []string
	command.Flags().VisitAll(func(flag *pflag.Flag) {
		names = append(names, flag.Name)
	})
	sort.Strings(names)
	want := []string{
		"branch",
		"no-recursive",
		"p",
		"parallel",
		"partial",
		"shallow",
		"silent",
		"update",
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("flags = %v, want %v", names, want)
	}

	shorthands := map[string]string{
		"branch":   "b",
		"p":        "p",
		"parallel": "P",
		"silent":   "s",
		"update":   "u",
	}
	for name, shorthand := range shorthands {
		if got := command.Flags().Lookup(name).Shorthand; got != shorthand {
			t.Fatalf("%s shorthand = %q, want %q", name, got, shorthand)
		}
	}
	for _, name := range []string{"look", "vcs", "bare", "ssh"} {
		if command.Flags().Lookup(name) != nil {
			t.Fatalf("unexpected flag --%s", name)
		}
	}
}

func TestNewGetCommandCloneAliasExecutes(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var cloned atomic.Int32
	var output bytes.Buffer
	getCommand := cmd.NewGetCommand(getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			cloned.Add(1)
			return nil
		},
	}, &output, io.Discard))
	parent := &cobra.Command{Use: "qw", SilenceErrors: true, SilenceUsage: true}
	parent.AddCommand(getCommand)
	parent.SetArgs([]string{"clone", "acme/widget"})

	if err := parent.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cloned.Load() != 1 {
		t.Fatalf("Clone() calls = %d, want 1", cloned.Load())
	}
}

func TestNewGetCommandPositionalArgumentsIgnoreStdin(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var terminalCalls atomic.Int32
	var output bytes.Buffer
	deps := getDependencies(root, &getGitOperations{}, &output, io.Discard)
	deps.Stdin = getPanicReader{}
	deps.IsTerminal = func(io.Reader) bool {
		terminalCalls.Add(1)
		return true
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if terminalCalls.Load() != 0 {
		t.Fatalf("IsTerminal() calls = %d, want 0", terminalCalls.Load())
	}
}

// TestNewGetCommandClonesWithoutTouchingCallerStdin extends the previous
// test's panic-reader guard through the actual clone path. GetGitFactory has
// no stdin parameter (see cmd.GetGitFactory), so it is structurally
// impossible for get to hand a gh subprocess the reader configured here;
// this proves that invariant holds behaviorally too, by confirming a full
// clone completes successfully while deps.Stdin panics on any Read.
func TestNewGetCommandClonesWithoutTouchingCallerStdin(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var cloned atomic.Int32
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			cloned.Add(1)
			return nil
		},
	}
	var output bytes.Buffer
	deps := getDependencies(root, git, &output, io.Discard)
	deps.Stdin = getPanicReader{}
	deps.IsTerminal = func(io.Reader) bool { return false }
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if cloned.Load() != 1 {
		t.Fatalf("Clone() calls = %d, want 1", cloned.Load())
	}
}

func TestNewGetCommandReadsTrimmedNonTerminalStdin(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var (
		mu   sync.Mutex
		urls []string
	)
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			mu.Lock()
			defer mu.Unlock()
			urls = append(urls, options.URL)
			return nil
		},
	}
	var output bytes.Buffer
	deps := getDependencies(root, git, &output, io.Discard)
	deps.Stdin = strings.NewReader("  acme/one  \n\n\tacme/two\r\n")
	deps.IsTerminal = func(io.Reader) bool { return false }
	command := cmd.NewGetCommand(deps)
	command.SetArgs(nil)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantURLs := []string{
		"https://github.com/acme/one",
		"https://github.com/acme/two",
	}
	if fmt.Sprint(urls) != fmt.Sprint(wantURLs) {
		t.Fatalf("Clone URLs = %v, want %v", urls, wantURLs)
	}
	wantOutput := strings.Join([]string{
		filepath.ToSlash(filepath.Join(root, "github.com", "acme", "one")),
		filepath.ToSlash(filepath.Join(root, "github.com", "acme", "two")),
	}, "\n") + "\n"
	if got := output.String(); got != wantOutput {
		t.Fatalf("output = %q, want %q", got, wantOutput)
	}
}

func TestNewGetCommandRejectsTerminalWithoutArguments(t *testing.T) {
	t.Parallel()

	resolver := &getRootResolver{}
	command := cmd.NewGetCommand(cmd.GetDependencies{
		RootResolver: resolver,
		Stdin:        strings.NewReader(""),
		Stdout:       io.Discard,
		Stderr:       io.Discard,
		IsTerminal:   func(io.Reader) bool { return true },
	})
	command.SetArgs(nil)

	err := command.Execute()
	if !errors.Is(err, cmd.ErrGetUsage) {
		t.Fatalf("Execute() error = %v, want ErrGetUsage", err)
	}
	if resolver.Calls() != 0 {
		t.Fatalf("Resolve() calls = %d, want 0", resolver.Calls())
	}
}

func TestNewGetCommandPropagatesStdinReadError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("stdin failed")
	resolver := &getRootResolver{}
	command := cmd.NewGetCommand(cmd.GetDependencies{
		RootResolver: resolver,
		Stdin:        getErrorReader{err: wantErr},
		Stdout:       io.Discard,
		Stderr:       io.Discard,
		IsTerminal:   func(io.Reader) bool { return false },
	})
	command.SetArgs(nil)

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	if resolver.Calls() != 0 {
		t.Fatalf("Resolve() calls = %d, want 0", resolver.Calls())
	}
}

func TestNewGetCommandBareIdentityAndSuffixOverrideBranchFlag(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	identity := &getIdentityResolver{
		identity: ghapi.Identity{Host: "github.example.com", Login: "octocat"},
	}
	var got ghcmd.CloneOptions
	var output bytes.Buffer
	deps := getDependencies(root, &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			got = options
			return nil
		},
	}, &output, io.Discard)
	deps.IdentityResolver = identity
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"--branch", "flag-branch", "widget@feature/topic"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if identity.Calls() != 1 {
		t.Fatalf("ResolveIdentity() calls = %d, want 1", identity.Calls())
	}
	if got, want := got.URL, "https://github.example.com/octocat/widget"; got != want {
		t.Fatalf("Clone URL = %q, want %q", got, want)
	}
	if got, want := got.Branch, "feature/topic"; got != want {
		t.Fatalf("Clone Branch = %q, want suffix %q", got, want)
	}
	wantPath := filepath.ToSlash(filepath.Join(root, "github.example.com", "octocat", "widget"))
	if got := strings.TrimSpace(output.String()); got != wantPath {
		t.Fatalf("output path = %q, want %q", got, wantPath)
	}
}

func TestNewGetCommandMakesParserAndIdentityFailuresUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		identity *getIdentityResolver
		wantErr  error
	}{
		{
			name:    "parser",
			input:   "too/many/repository/path",
			wantErr: repospec.ErrUsage,
		},
		{
			name:  "identity",
			input: "widget",
			identity: &getIdentityResolver{
				err: errors.New("identity failed"),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := getTempRoot(t)
			deps := getDependencies(root, &getGitOperations{}, io.Discard, io.Discard)
			if test.identity != nil {
				deps.IdentityResolver = test.identity
				test.wantErr = test.identity.err
			}
			command := cmd.NewGetCommand(deps)
			command.SetArgs([]string{test.input})

			err := command.Execute()
			if !errors.Is(err, cmd.ErrGetUsage) {
				t.Fatalf("Execute() error = %v, want ErrGetUsage", err)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Execute() error = %v, want cause %v", err, test.wantErr)
			}
		})
	}
}

func TestNewGetCommandRejectsInvalidAndUndocumentedFlagsBeforeResolving(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"--partial", "full", "acme/widget"},
		{"--look", "acme/widget"},
		{"--vcs", "git", "acme/widget"},
		{"--bare", "acme/widget"},
	}
	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()

			resolver := &getRootResolver{}
			command := cmd.NewGetCommand(cmd.GetDependencies{
				RootResolver: resolver,
				Stdin:        strings.NewReader(""),
				Stdout:       io.Discard,
				Stderr:       io.Discard,
				IsTerminal:   func(io.Reader) bool { return false },
			})
			command.SetArgs(args)

			err := command.Execute()
			if !errors.Is(err, cmd.ErrGetUsage) {
				t.Fatalf("Execute() error = %v, want ErrGetUsage", err)
			}
			if resolver.Calls() != 0 {
				t.Fatalf("Resolve() calls = %d, want 0", resolver.Calls())
			}
		})
	}
}

func TestNewGetCommandReusesExistingRepositoryAndUpdatesOnRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantUpdates int
	}{
		{name: "unchanged"},
		{
			name:        "update",
			args:        []string{"--update"},
			wantUpdates: 1,
		},
		{
			// --no-recursive has no update-side effect: `gh repo sync` has no
			// submodule concept, so it only changes clone behavior.
			name:        "update ignores no-recursive",
			args:        []string{"--update", "--no-recursive"},
			wantUpdates: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := getTempRoot(t)
			path := filepath.Join(root, "github.com", "acme", "widget")
			var (
				updateCalls int
				gotDir      string
				gotSync     ghcmd.SyncOptions
				mkdirCalls  int
			)
			git := &getGitOperations{
				clone: func(context.Context, ghcmd.CloneOptions) error {
					t.Fatal("Clone() called for existing repository")
					return nil
				},
				update: func(_ context.Context, dir string, options ghcmd.SyncOptions) error {
					updateCalls++
					gotDir = dir
					gotSync = options
					return nil
				},
			}
			var output bytes.Buffer
			deps := getDependencies(root, git, &output, io.Discard)
			deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
				return local.DiscoveryResult{Repositories: []local.Repository{{
					Identity:  "github.com/acme/widget",
					Host:      "github.com",
					Owner:     "acme",
					Repo:      "widget",
					Path:      path,
					Root:      root,
					RootIndex: 0,
				}}}, nil
			}
			deps.MkdirAll = func(string, fs.FileMode) error {
				mkdirCalls++
				return nil
			}
			command := cmd.NewGetCommand(deps)
			command.SetArgs(append(test.args, "acme/widget"))

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if updateCalls != test.wantUpdates {
				t.Fatalf("Update() calls = %d, want %d", updateCalls, test.wantUpdates)
			}
			if test.wantUpdates != 0 {
				if gotDir != path {
					t.Fatalf("Update() dir = %q, want %q", gotDir, path)
				}
				if got, want := gotSync.Source, "acme/widget"; got != want {
					t.Fatalf("Update() Source = %q, want %q", got, want)
				}
				if gotSync.Branch != "" {
					t.Fatalf("Update() Branch = %q, want empty (gh resolves the default branch)", gotSync.Branch)
				}
			}
			if mkdirCalls != 0 {
				t.Fatalf("MkdirAll() calls = %d, want 0", mkdirCalls)
			}
			if got, want := output.String(), filepath.ToSlash(path)+"\n"; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}

// getExistingRepositoryDiscover mirrors the "repository already exists"
// fixture used by TestNewGetCommandReusesExistingRepositoryAndUpdatesOnRequest,
// so tests that only care about account-resolution call counts don't have to
// duplicate the Discover wiring.
func getExistingRepositoryDiscover(root, path string) func(context.Context, []string) (local.DiscoveryResult, error) {
	return func(context.Context, []string) (local.DiscoveryResult, error) {
		return local.DiscoveryResult{Repositories: []local.Repository{{
			Identity:  "github.com/acme/widget",
			Host:      "github.com",
			Owner:     "acme",
			Repo:      "widget",
			Path:      path,
			Root:      root,
			RootIndex: 0,
		}}}, nil
	}
}

// TestNewGetCommandSkipsAccountResolutionForExistingRepositoryWithoutUpdate
// mirrors TestNewWorktreeAddCommandSkipsAccountResolutionForTheCommonCase:
// unlike TestNewGetCommandReusesExistingRepositoryAndUpdatesOnRequest (which
// only ever plugs in getNoopAccountResolver{}, a resolver with no call
// counter), this test wires in a countable getAccountResolver{} so a
// regression to unconditional account resolution would actually be caught.
func TestNewGetCommandSkipsAccountResolutionForExistingRepositoryWithoutUpdate(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	path := filepath.Join(root, "github.com", "acme", "widget")
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			t.Fatal("Clone() called for existing repository")
			return nil
		},
	}
	var output bytes.Buffer
	resolver := &getAccountResolver{}
	deps := getDependencies(root, git, &output, io.Discard)
	deps.Discover = getExistingRepositoryDiscover(root, path)
	deps.AccountResolver = resolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls := resolver.Calls(); len(calls) != 0 {
		t.Fatalf(
			"Resolve() calls = %#v, want none: reusing an existing repository without --update must never trigger gh account resolution or a prompt",
			calls,
		)
	}
}

// TestNewGetCommandUpdateOfExistingRepositoryDoesResolveAccount is the
// symmetric regression guard for the test above: --update against the same
// existing-repository scenario must still trigger account resolution, so a
// future change can't accidentally make --update skip it too.
func TestNewGetCommandUpdateOfExistingRepositoryDoesResolveAccount(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	path := filepath.Join(root, "github.com", "acme", "widget")
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			t.Fatal("Clone() called for existing repository")
			return nil
		},
		update: func(context.Context, string, ghcmd.SyncOptions) error {
			return nil
		},
	}
	var output bytes.Buffer
	resolver := &getAccountResolver{}
	deps := getDependencies(root, git, &output, io.Discard)
	deps.Discover = getExistingRepositoryDiscover(root, path)
	deps.AccountResolver = resolver
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"--update", "acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls := resolver.Calls(); len(calls) == 0 {
		t.Fatal("Resolve() calls = 0, want at least one: --update must still trigger gh account resolution")
	}
}

func TestNewGetCommandStopsBeforeSyncWhenAccountResolutionFails(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	path := filepath.Join(root, "github.com", "acme", "widget")
	wantErr := errors.New("account selection failed")
	syncCalls := 0
	git := &getGitOperations{
		update: func(context.Context, string, ghcmd.SyncOptions) error {
			syncCalls++
			return nil
		},
	}
	deps := getDependencies(root, git, io.Discard, io.Discard)
	deps.Discover = getExistingRepositoryDiscover(root, path)
	deps.AccountResolver = &getAccountResolver{err: wantErr}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"--update", "acme/widget"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if syncCalls != 0 {
		t.Fatalf("RepoSync() calls = %d, want 0 after resolution failure", syncCalls)
	}
}

func TestNewGetCommandUsesEarliestExistingRoot(t *testing.T) {
	t.Parallel()

	primary := getTempRoot(t)
	secondary := getTempRoot(t)
	primaryPath := filepath.Join(primary, "github.com", "acme", "widget")
	secondaryPath := filepath.Join(secondary, "github.com", "acme", "widget")
	resolver := &getRootResolver{result: rootpkg.Result{
		RepositoryRoots: []string{primary, secondary},
	}}
	var output bytes.Buffer
	command := cmd.NewGetCommand(cmd.GetDependencies{
		RootResolver: resolver,
		GitFactory: func(io.Writer, io.Writer, string) cmd.GetGitOperations {
			t.Fatal("Git factory called for unchanged repository")
			return nil
		},
		MkdirAll: func(string, fs.FileMode) error {
			t.Fatal("MkdirAll() called for unchanged repository")
			return nil
		},
		Discover: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{Repositories: []local.Repository{
				{
					Identity:  "github.com/acme/widget",
					Path:      secondaryPath,
					Root:      secondary,
					RootIndex: 1,
				},
				{
					Identity:  "github.com/acme/widget",
					Path:      primaryPath,
					Root:      primary,
					RootIndex: 0,
				},
			}}, nil
		},
		IdentityResolver: &getIdentityResolver{},
		AccountResolver:  getNoopAccountResolver{},
		Stdin:            strings.NewReader(""),
		Stdout:           &output,
		Stderr:           io.Discard,
		IsTerminal:       func(io.Reader) bool { return false },
	})
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := output.String(), filepath.ToSlash(primaryPath)+"\n"; got != want {
		t.Fatalf("output = %q, want earliest root %q", got, want)
	}
}

func TestNewGetCommandReusesSecondaryRootWhenPrimaryIsMissing(t *testing.T) {
	t.Parallel()

	primary := getTempRoot(t)
	secondary := getTempRoot(t)
	path := filepath.Join(secondary, "github.com", "acme", "widget")
	var output bytes.Buffer
	command := cmd.NewGetCommand(cmd.GetDependencies{
		RootResolver: &getRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{primary, secondary},
		}},
		GitFactory: func(io.Writer, io.Writer, string) cmd.GetGitOperations {
			t.Fatal("Git factory called for secondary repository")
			return nil
		},
		MkdirAll: func(string, fs.FileMode) error {
			t.Fatal("MkdirAll() called for secondary repository")
			return nil
		},
		Discover: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{Repositories: []local.Repository{{
				Identity:  "github.com/acme/widget",
				Path:      path,
				Root:      secondary,
				RootIndex: 1,
			}}}, nil
		},
		IdentityResolver: &getIdentityResolver{},
		AccountResolver:  getNoopAccountResolver{},
		Stdin:            strings.NewReader(""),
		Stdout:           &output,
		Stderr:           io.Discard,
		IsTerminal:       func(io.Reader) bool { return false },
	})
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := output.String(), filepath.ToSlash(path)+"\n"; got != want {
		t.Fatalf("output = %q, want secondary path %q", got, want)
	}
}

func TestNewGetCommandOrderedModeStopsAtFirstFailure(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantErr := errors.New("second clone failed")
	var (
		mu    sync.Mutex
		calls []string
	)
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			repo := filepath.Base(options.Destination)
			mu.Lock()
			calls = append(calls, repo)
			mu.Unlock()
			if repo == "two" {
				return wantErr
			}
			return nil
		},
	}
	var output bytes.Buffer
	command := cmd.NewGetCommand(getDependencies(root, git, &output, io.Discard))
	command.SetArgs([]string{"acme/one", "acme/two", "acme/three"})

	err := command.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want %v", err, wantErr)
	}
	if got, want := fmt.Sprint(calls), "[one two]"; got != want {
		t.Fatalf("Clone order = %s, want %s", got, want)
	}
	wantOutput := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "one")) + "\n"
	if got := output.String(); got != wantOutput {
		t.Fatalf("output = %q, want only first success %q", got, wantOutput)
	}
}

func TestNewGetCommandParallelCapsConcurrencyAtSix(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	started := make(chan string, 7)
	release := make(chan struct{})
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			started <- options.Destination
			<-release
			return nil
		},
	}
	command := cmd.NewGetCommand(getDependencies(root, git, io.Discard, io.Discard))
	args := []string{"--parallel"}
	for index := range 7 {
		args = append(args, fmt.Sprintf("acme/repo-%d", index))
	}
	command.SetArgs(args)

	result := make(chan error, 1)
	go func() {
		result <- command.Execute()
	}()

	for range 6 {
		getReceiveWithin(t, started, "parallel clone start")
	}
	select {
	case path := <-started:
		t.Fatalf("seventh clone started before a slot was released: %s", path)
	case <-time.After(100 * time.Millisecond):
	}

	release <- struct{}{}
	getReceiveWithin(t, started, "seventh clone start")
	for range 6 {
		release <- struct{}{}
	}
	if err := getReceiveWithin(t, result, "parallel command result"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestNewGetCommandParallelWritesCompletionOrder(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	started := make(chan string, 3)
	releases := map[string]chan struct{}{
		"one":   make(chan struct{}),
		"two":   make(chan struct{}),
		"three": make(chan struct{}),
	}
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			repo := filepath.Base(options.Destination)
			started <- repo
			<-releases[repo]
			return nil
		},
	}
	output := &getLineWriter{lines: make(chan string, 3)}
	command := cmd.NewGetCommand(getDependencies(root, git, output, io.Discard))
	command.SetArgs([]string{"-P", "acme/one", "acme/two", "acme/three"})

	result := make(chan error, 1)
	go func() {
		result <- command.Execute()
	}()
	for range 3 {
		getReceiveWithin(t, started, "parallel clone start")
	}

	for _, repo := range []string{"three", "one", "two"} {
		close(releases[repo])
		got := getReceiveWithin(t, output.lines, "result path")
		want := filepath.ToSlash(filepath.Join(root, "github.com", "acme", repo))
		if got != want {
			t.Fatalf("completion output = %q, want %q", got, want)
		}
	}
	if err := getReceiveWithin(t, result, "parallel command result"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestNewGetCommandParallelAggregatesUsageAndRuntimeFailures(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantRuntime := errors.New("clone failed")
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			if filepath.Base(options.Destination) == "fail" {
				return wantRuntime
			}
			return nil
		},
	}
	var output bytes.Buffer
	command := cmd.NewGetCommand(getDependencies(root, git, &output, io.Discard))
	command.SetArgs([]string{
		"--parallel",
		"acme/good",
		"too/many/repository/path",
		"acme/fail",
	})

	err := command.Execute()
	var aggregate *cmd.GetAggregateError
	if !errors.As(err, &aggregate) {
		t.Fatalf("Execute() error = %T %v, want GetAggregateError", err, err)
	}
	if !errors.Is(err, cmd.ErrGetUsage) {
		t.Fatalf("Execute() error = %v, want usage precedence", err)
	}
	if !errors.Is(err, repospec.ErrUsage) {
		t.Fatalf("Execute() error = %v, want parser cause", err)
	}
	if !errors.Is(err, wantRuntime) {
		t.Fatalf("Execute() error = %v, want runtime cause", err)
	}
	if got := len(aggregate.UsageErrors()); got != 1 {
		t.Fatalf("UsageErrors() length = %d, want 1", got)
	}
	if got := len(aggregate.RuntimeErrors()); got != 1 {
		t.Fatalf("RuntimeErrors() length = %d, want 1", got)
	}
	wantPath := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "good")) + "\n"
	if got := output.String(); got != wantPath {
		t.Fatalf("output = %q, want successful path %q", got, wantPath)
	}
}

func TestNewGetCommandParallelSerializesDuplicateDestination(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var cloneCalls atomic.Int32
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			cloneCalls.Add(1)
			started <- struct{}{}
			<-release
			return nil
		},
	}
	var output bytes.Buffer
	command := cmd.NewGetCommand(getDependencies(root, git, &output, io.Discard))
	command.SetArgs([]string{"-P", "acme/widget", "https://github.com/acme/widget.git"})

	result := make(chan error, 1)
	go func() {
		result <- command.Execute()
	}()
	getReceiveWithin(t, started, "first clone start")
	select {
	case <-started:
		t.Fatal("duplicate destination mutated concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := getReceiveWithin(t, result, "parallel command result"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := cloneCalls.Load(); got != 1 {
		t.Fatalf("Clone() calls = %d, want 1", got)
	}
	path := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "widget"))
	if got, want := output.String(), path+"\n"+path+"\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNewGetCommandCreatesOnlyCloneParents(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	destination := filepath.Join(root, "github.com", "acme", "widget")
	var (
		mkdirPath string
		mkdirMode fs.FileMode
	)
	git := &getGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			if options.Destination != destination {
				t.Fatalf("Clone destination = %q, want %q", options.Destination, destination)
			}
			if _, err := os.Lstat(destination); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("destination exists before clone: %v", err)
			}
			return nil
		},
	}
	deps := getDependencies(root, git, io.Discard, io.Discard)
	deps.MkdirAll = func(path string, mode fs.FileMode) error {
		mkdirPath = path
		mkdirMode = mode
		return os.MkdirAll(path, mode)
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := mkdirPath, filepath.Dir(destination); got != want {
		t.Fatalf("MkdirAll() path = %q, want parent %q", got, want)
	}
	if got, want := mkdirMode.Perm(), fs.FileMode(0o755); got != want {
		t.Fatalf("MkdirAll() mode = %v, want %v", got, want)
	}
}

func TestNewGetCommandRevalidatesContainmentAfterMkdir(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	outside := getTempRoot(t)
	var cloneCalls atomic.Int32
	deps := getDependencies(root, &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			cloneCalls.Add(1)
			return nil
		},
	}, io.Discard, io.Discard)
	deps.MkdirAll = func(string, fs.FileMode) error {
		return os.Symlink(outside, filepath.Join(root, "github.com"))
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	err := command.Execute()
	if !errors.Is(err, rootpkg.ErrUnsafeTarget) {
		t.Fatalf("Execute() error = %v, want ErrUnsafeTarget", err)
	}
	if cloneCalls.Load() != 0 {
		t.Fatalf("Clone() calls = %d, want 0", cloneCalls.Load())
	}
}

func TestNewGetCommandPropagatesMkdirAndOutputErrors(t *testing.T) {
	t.Parallel()

	t.Run("mkdir", func(t *testing.T) {
		t.Parallel()

		root := getTempRoot(t)
		wantErr := errors.New("mkdir failed")
		var cloneCalls atomic.Int32
		deps := getDependencies(root, &getGitOperations{
			clone: func(context.Context, ghcmd.CloneOptions) error {
				cloneCalls.Add(1)
				return nil
			},
		}, io.Discard, io.Discard)
		deps.MkdirAll = func(string, fs.FileMode) error { return wantErr }
		command := cmd.NewGetCommand(deps)
		command.SetArgs([]string{"acme/widget"})

		err := command.Execute()
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
		if cloneCalls.Load() != 0 {
			t.Fatalf("Clone() calls = %d, want 0", cloneCalls.Load())
		}
	})

	t.Run("output", func(t *testing.T) {
		t.Parallel()

		root := getTempRoot(t)
		wantErr := errors.New("write failed")
		command := cmd.NewGetCommand(getDependencies(
			root,
			&getGitOperations{},
			getFailingWriter{err: wantErr},
			io.Discard,
		))
		command.SetArgs([]string{"acme/widget"})

		err := command.Execute()
		if !errors.Is(err, wantErr) {
			t.Fatalf("Execute() error = %v, want %v", err, wantErr)
		}
	})
}

func TestNewGetCommandKeepsProgressAndWarningsOffStdout(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var output, diagnostic bytes.Buffer
	warning := local.Warning{
		Kind:      local.WarningInspection,
		Path:      filepath.Join(root, "ignored"),
		Operation: "inspect",
		Err:       errors.New("not a repository"),
	}
	deps := getDependencies(root, &getGitOperations{}, &output, &diagnostic)
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		return local.DiscoveryResult{Warnings: []local.Warning{warning}}, nil
	}
	deps.GitFactory = func(stdout, _ io.Writer, _ string) cmd.GetGitOperations {
		return &getGitOperations{
			clone: func(context.Context, ghcmd.CloneOptions) error {
				_, _ = fmt.Fprintln(stdout, "git progress")
				return nil
			},
		}
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantPath := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "widget")) + "\n"
	if got := output.String(); got != wantPath {
		t.Fatalf("stdout = %q, want only %q", got, wantPath)
	}
	if got := diagnostic.String(); !strings.Contains(got, "warning:") || !strings.Contains(got, "git progress") {
		t.Fatalf("stderr = %q, want warning and progress", got)
	}
}

func TestNewGetCommandSilentSuppressesProgressButKeepsWarningsAndPaths(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	var output, diagnostic bytes.Buffer
	warning := local.Warning{
		Kind:      local.WarningInspection,
		Path:      filepath.Join(root, "ignored"),
		Operation: "inspect",
		Err:       errors.New("not a repository"),
	}
	deps := getDependencies(root, &getGitOperations{}, &output, &diagnostic)
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		return local.DiscoveryResult{Warnings: []local.Warning{warning}}, nil
	}
	deps.GitFactory = func(stdout, _ io.Writer, _ string) cmd.GetGitOperations {
		return &getGitOperations{
			clone: func(context.Context, ghcmd.CloneOptions) error {
				_, _ = fmt.Fprintln(stdout, "git progress")
				return nil
			},
		}
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"--silent", "acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantPath := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "widget")) + "\n"
	if got := output.String(); got != wantPath {
		t.Fatalf("stdout = %q, want only %q", got, wantPath)
	}
	if got := diagnostic.String(); !strings.Contains(got, "warning:") {
		t.Fatalf("stderr = %q, want warning", got)
	} else if strings.Contains(got, "git progress") {
		t.Fatalf("stderr = %q, want progress suppressed", got)
	}
}

// TestNewGetCommandOrderedNonSilentExposesPassthroughFile guards the
// pass-through wiring that lets gh's own clone/sync output regain
// interactive progress and color: when gh-qw's own stderr is a real
// *os.File (as it is in production, whether or not that file is a
// terminal) and get runs in its default ordered, non-silent mode, the
// writers handed to GetGitFactory must resolve through
// procio.PassthroughFile back to that same file.
func TestNewGetCommandOrderedNonSilentExposesPassthroughFile(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	var gotStdout, gotStderr io.Writer
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
	}
	var output bytes.Buffer
	deps := getDependencies(root, git, &output, writer)
	deps.GitFactory = func(stdout, stderr io.Writer, _ string) cmd.GetGitOperations {
		gotStdout = stdout
		gotStderr = stderr
		return git
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := procio.PassthroughFile(gotStdout); got != writer {
		t.Fatalf(
			"PassthroughFile(gotStdout) = %#v, want the pipe file %#v so gh's own stdout can be inherited",
			got,
			writer,
		)
	}
	if got := procio.PassthroughFile(gotStderr); got != writer {
		t.Fatalf(
			"PassthroughFile(gotStderr) = %#v, want the pipe file %#v so gh's own stderr can be inherited",
			got,
			writer,
		)
	}
}

// TestNewGetCommandPassthroughNeverLeaksGhStdoutOntoGhQwStdout guards the
// stdout-purity contract (GH-PROC-IO-02): even in the ordered, non-silent,
// passthrough-eligible scenario exercised by
// TestNewGetCommandOrderedNonSilentExposesPassthroughFile, whatever gh
// itself writes to its own stdout (mimicked here by a fake gh repo sync
// completion message, written from the clone function to the exact stdout
// writer GetGitFactory receives) must never reach gh-qw's own stdout: only
// the destination result path may appear there.
func TestNewGetCommandPassthroughNeverLeaksGhStdoutOntoGhQwStdout(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	const ghMessage = `✓ Synced the "main" branch from acme/widget to local:main`

	// ghStream sanity-checks that the fake gh message really was written to
	// the writer GetGitFactory received (via an io.MultiWriter alongside the
	// real passthrough writer), so the negative assertion on gh-qw's own
	// stdout below cannot pass vacuously.
	var ghStream bytes.Buffer
	var output bytes.Buffer
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
	}
	deps := getDependencies(root, git, &output, writer)
	deps.GitFactory = func(stdout, _ io.Writer, _ string) cmd.GetGitOperations {
		return &getGitOperations{
			clone: func(context.Context, ghcmd.CloneOptions) error {
				_, _ = fmt.Fprintln(io.MultiWriter(stdout, &ghStream), ghMessage)
				return nil
			},
		}
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := ghStream.String(); !strings.Contains(got, ghMessage) {
		t.Fatalf("ghStream = %q, want it to contain gh's message %q (otherwise this test is vacuous)", got, ghMessage)
	}
	wantPath := filepath.ToSlash(filepath.Join(root, "github.com", "acme", "widget")) + "\n"
	if got := output.String(); got != wantPath {
		t.Fatalf("stdout = %q, want only %q", got, wantPath)
	}
	if got := output.String(); strings.Contains(got, ghMessage) {
		t.Fatalf("stdout = %q, must not contain gh's own message %q", got, ghMessage)
	}
}

// TestNewGetCommandSilentAndParallelNeverExposePassthroughFile guards the
// safety invariant behind get's pass-through wiring: --silent discards gh's
// progress entirely, and --parallel can run several gh subprocesses
// concurrently, so neither must ever let a subprocess inherit gh-qw's real
// stderr descriptor directly (bypassing getLockedWriter's mutex).
func TestNewGetCommandSilentAndParallelNeverExposePassthroughFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		args []string
	}{
		{name: "silent", args: []string{"--silent", "acme/widget"}},
		{name: "parallel", args: []string{"--parallel", "acme/widget"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			root := getTempRoot(t)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatalf("Pipe() error = %v", err)
			}
			t.Cleanup(func() {
				_ = reader.Close()
				_ = writer.Close()
			})

			var gotStdout, gotStderr io.Writer
			git := &getGitOperations{
				clone: func(context.Context, ghcmd.CloneOptions) error { return nil },
			}
			var output bytes.Buffer
			deps := getDependencies(root, git, &output, writer)
			deps.GitFactory = func(stdout, stderr io.Writer, _ string) cmd.GetGitOperations {
				gotStdout = stdout
				gotStderr = stderr
				return git
			}
			command := cmd.NewGetCommand(deps)
			command.SetArgs(testCase.args)

			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if got := procio.PassthroughFile(gotStdout); got != nil {
				t.Fatalf(
					"PassthroughFile(gotStdout) = %#v, want nil: suppressed or concurrent progress must never resolve to a file",
					got,
				)
			}
			if got := procio.PassthroughFile(gotStderr); got != nil {
				t.Fatalf(
					"PassthroughFile(gotStderr) = %#v, want nil: suppressed or concurrent progress must never resolve to a file",
					got,
				)
			}
		})
	}
}

func TestNewGetCommandSilentKeepsGitErrors(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	wantDiagnostic := "remote: Repository not found.\nfatal: repository not found"
	git := &getGitOperations{
		clone: func(context.Context, ghcmd.CloneOptions) error {
			return &getStderrError{message: "gh command failed with exit code 1", stderr: []byte(wantDiagnostic)}
		},
	}
	var output, diagnostic bytes.Buffer
	command := cmd.NewGetCommand(getDependencies(root, git, &output, &diagnostic))
	command.SetArgs([]string{"--silent", "acme/widget"})

	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want clone failure")
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
	if got := diagnostic.String(); got != wantDiagnostic+"\n" {
		t.Fatalf("stderr = %q, want retained diagnostic %q", got, wantDiagnostic+"\n")
	}
}

func TestNewGetCommandCustomWarningSink(t *testing.T) {
	t.Parallel()

	root := getTempRoot(t)
	warning := local.Warning{Path: filepath.Join(root, "warning")}
	var got []local.Warning
	deps := getDependencies(root, &getGitOperations{}, io.Discard, io.Discard)
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		return local.DiscoveryResult{Warnings: []local.Warning{warning}}, nil
	}
	deps.WarningSink = func(warning local.Warning) {
		got = append(got, warning)
	}
	command := cmd.NewGetCommand(deps)
	command.SetArgs([]string{"acme/widget"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(got) != 1 || got[0].Path != warning.Path {
		t.Fatalf("warnings = %v, want %v", got, warning)
	}
}

func TestNewGetCommandRejectsFileSchemeInput(t *testing.T) {
	t.Parallel()

	// `get` delegates cloning to `gh repo clone`, which never accepts a
	// file:// URL (local or pseudo-remote authority). Both forms must be
	// rejected as a usage error before any Git operation is attempted.
	tests := []string{
		getFileURL(filepath.Join(getTempRoot(t), "github.com", "acme", "widget")),
		"file://ghe.example.com/acme/widget",
	}
	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			root := getTempRoot(t)
			git := &getGitOperations{
				clone: func(context.Context, ghcmd.CloneOptions) error {
					t.Fatal("Clone() called for rejected file:// input")
					return nil
				},
			}
			command := cmd.NewGetCommand(getDependencies(root, git, io.Discard, io.Discard))
			command.SetArgs([]string{input})

			err := command.Execute()
			if !errors.Is(err, cmd.ErrGetUsage) {
				t.Fatalf("Execute() error = %v, want ErrGetUsage", err)
			}
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want repospec.ErrUsage cause", err)
			}
		})
	}
}

type getRootResolver struct {
	mu     sync.Mutex
	result rootpkg.Result
	err    error
	calls  int
}

func (r *getRootResolver) Resolve() (rootpkg.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.result, r.err
}

func (r *getRootResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type getIdentityResolver struct {
	mu       sync.Mutex
	identity ghapi.Identity
	err      error
	calls    int
}

func (r *getIdentityResolver) ResolveIdentity(context.Context) (ghapi.Identity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.identity, r.err
}

func (r *getIdentityResolver) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type getAccountResolverCall struct {
	host, owner string
}

// getAccountResolver is a cmd.AccountResolver test double that records every
// host/owner it was asked to resolve and always answers with a fixed
// resolution, letting tests assert both the resulting GetGitFactory token
// and how account resolution was invoked (e.g., once per unique owner under
// --parallel).
type getAccountResolver struct {
	mu         sync.Mutex
	resolution ghauth.Resolution
	err        error
	resolve    func(host, owner string) (ghauth.Resolution, error)
	calls      []getAccountResolverCall
}

func (r *getAccountResolver) Resolve(_ context.Context, host, owner string) (ghauth.Resolution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, getAccountResolverCall{host: host, owner: owner})
	if r.resolve != nil {
		return r.resolve(host, owner)
	}
	return r.resolution, r.err
}

func (r *getAccountResolver) Calls() []getAccountResolverCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]getAccountResolverCall(nil), r.calls...)
}

type getGitOperations struct {
	clone  func(context.Context, ghcmd.CloneOptions) error
	update func(context.Context, string, ghcmd.SyncOptions) error
}

func (g *getGitOperations) RepoClone(ctx context.Context, options ghcmd.CloneOptions) error {
	if g.clone == nil {
		return nil
	}
	return g.clone(ctx, options)
}

func (g *getGitOperations) RepoSync(
	ctx context.Context,
	dir string,
	options ghcmd.SyncOptions,
) error {
	if g.update == nil {
		return nil
	}
	return g.update(ctx, dir, options)
}

func getDependencies(
	root string,
	git cmd.GetGitOperations,
	stdout, stderr io.Writer,
) cmd.GetDependencies {
	return cmd.GetDependencies{
		RootResolver: &getRootResolver{result: rootpkg.Result{
			RepositoryRoots: []string{root},
		}},
		GitFactory: func(io.Writer, io.Writer, string) cmd.GetGitOperations {
			return git
		},
		// A safe, hermetic default: tests that do not exercise account
		// resolution directly must never invoke a real gh subprocess, whose
		// availability, authentication, and output vary by environment (see
		// TestNewGetCommandPassesResolvedTokenToGitFactory and its siblings
		// for tests that do exercise it with their own stub).
		AccountResolver: getNoopAccountResolver{},
		Discover: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{}, nil
		},
		IdentityResolver: &getIdentityResolver{
			identity: ghapi.Identity{Host: "github.com", Login: "octocat"},
		},
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		IsTerminal: func(io.Reader) bool { return false },
	}
}

// getNoopAccountResolver represents an explicit environment token without
// ever invoking a real gh subprocess.
type getNoopAccountResolver struct{}

func (getNoopAccountResolver) Resolve(context.Context, string, string) (ghauth.Resolution, error) {
	return ghauth.Resolution{Source: ghauth.SourceExplicitEnv}, nil
}

type getPanicReader struct{}

func (getPanicReader) Read([]byte) (int, error) {
	panic("stdin must not be read")
}

type getErrorReader struct {
	err error
}

func (r getErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type getFailingWriter struct {
	err error
}

func (w getFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type getLineWriter struct {
	lines chan string
}

func (w *getLineWriter) Write(data []byte) (int, error) {
	w.lines <- strings.TrimSuffix(string(data), "\n")
	return len(data), nil
}

func getReceiveWithin[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func getTempRoot(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func getFileURL(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

// getStderrError simulates a gh command failure that retains a diagnostic
// stderr tail, mirroring the capability *ghcmd.CommandError exposes, without
// depending on ghcmd's unexported fields or a real gh subprocess.
type getStderrError struct {
	message string
	stderr  []byte
}

func (e *getStderrError) Error() string { return e.message }

func (e *getStderrError) StderrOutput() []byte { return e.stderr }
