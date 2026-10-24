package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/cmd"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestCloneSyntaxErrorHasNoResolutionOrCloneSideEffects(t *testing.T) {
	for _, args := range [][]string{{"invalid?repository"}, {"--parallel", "invalid?repository"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			root := cloneTempRoot(t)
			t.Setenv("PATH", t.TempDir())
			var stdout bytes.Buffer
			deps := cloneDependencies(root, &cloneGitOperations{}, &stdout, io.Discard)
			identity := &cloneIdentityResolver{err: errors.New("authentication must not run")}
			deps.IdentityResolver = identity
			resolver := &cloneRootResolver{err: errors.New("root resolution must not run")}
			deps.RootResolver = resolver
			deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
				t.Fatal("syntax errors must not discover repositories")
				return local.DiscoveryResult{}, nil
			}
			deps.GitFactory = func(io.Writer, io.Writer) cmd.CloneGitOperations {
				t.Fatal("syntax errors must not construct clone operations")
				return nil
			}
			command := cmd.NewCloneCommand(deps)
			command.SetArgs(args)
			if err := command.Execute(); !errors.Is(err, cmd.ErrCloneUsage) || !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("syntax error = %v, want usage classification", err)
			}
			if stdout.Len() != 0 || identity.Calls() != 0 {
				t.Fatalf("syntax error produced stdout %q or authenticated %d times", stdout.String(), identity.Calls())
			}
			if resolver.Calls() != 0 {
				t.Fatalf("syntax error resolved roots %d times", resolver.Calls())
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("syntax error changed the repository root: entries %#v, error %v", entries, err)
			}
		})
	}
}

func TestNativeRefFailuresRemainRuntimeErrors(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"add", "--worktree", "topic"},
		{"remove", "--worktree", "topic"},
		{"clone", "--branch", "topic", "owner/widget"},
		{"clone", "owner/widget@topic"},
		{"clone", "--parallel", "owner/widget@topic"},
		{"list", "https://github.com/owner/widget@topic"},
	} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/canceled=%t", args, canceled), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if canceled {
					cancel()
				}
				var stdout bytes.Buffer
				command := cmd.NewCommand(cmd.ApplicationDependencies{
					Resolver: &cloneRootResolver{result: rootpkg.Result{RepositoryRoots: []string{t.TempDir()}}},
					Discover: func(context.Context, []string, ...local.DiscoveryOptions) (local.DiscoveryResult, error) {
						return local.DiscoveryResult{}, nil
					},
					Stdout: &stdout,
					Stderr: io.Discard,
				})
				command.SetArgs(args)
				err := command.ExecuteContext(ctx)
				if code := cmd.ExitCode(err); code != 1 {
					t.Fatalf("status = %d, want runtime status 1; error: %v", code, err)
				}
				if canceled {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation was lost: %v", err)
					}
				} else {
					var commandErr *gitcmd.CommandError
					if !errors.As(err, &commandErr) || commandErr.ExitCode >= 0 {
						t.Fatalf("Git start failure was lost: %v", err)
					}
				}
				if stdout.Len() != 0 {
					t.Fatalf("failed validation produced stdout %q", stdout.String())
				}
			})
		}
	}
}

func TestCloneOrderedSyntaxFailurePreservesEarlierSuccess(t *testing.T) {
	root := cloneTempRoot(t)
	var cloned []string
	var stdout bytes.Buffer
	deps := cloneDependencies(root, &cloneGitOperations{
		clone: func(_ context.Context, options ghcmd.CloneOptions) error {
			cloned = append(cloned, options.URL)
			return nil
		},
	}, &stdout, io.Discard)
	discoveries := 0
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		discoveries++
		return local.DiscoveryResult{}, nil
	}
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"github.com/owner/first", "invalid?repository", "github.com/owner/last"})
	err := command.Execute()
	if cmd.ExitCode(err) != 2 || !errors.Is(err, repospec.ErrUsage) {
		t.Fatalf("ordered syntax failure = %v, want usage status 2", err)
	}
	if !slices.Equal(cloned, []string{"https://github.com/owner/first"}) || discoveries != 1 {
		t.Fatalf("cloned = %v, discoveries = %d; want only earlier success and one discovery", cloned, discoveries)
	}
	want := filepath.ToSlash(filepath.Join(root, "github.com", "owner", "first")) + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want completed result %q", stdout.String(), want)
	}
}

func TestCloneParallelKeepsNativeRuntimeAndSyntaxErrorsDistinct(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := cloneTempRoot(t)
	var stdout bytes.Buffer
	command := cmd.NewCloneCommand(cloneDependencies(root, &cloneGitOperations{}, &stdout, io.Discard))
	command.SetArgs([]string{"--parallel", "github.com/owner/good", "invalid?repository", "github.com/owner/widget@topic"})
	err := command.Execute()
	var aggregate *cmd.CloneAggregateError
	if cmd.ExitCode(err) != 2 || !errors.As(err, &aggregate) {
		t.Fatalf("parallel result = %v, want aggregate usage status 2", err)
	}
	if len(aggregate.UsageErrors()) != 1 || len(aggregate.RuntimeErrors()) != 1 {
		t.Fatalf("usage errors = %v, runtime errors = %v", aggregate.UsageErrors(), aggregate.RuntimeErrors())
	}
	var commandErr *gitcmd.CommandError
	if !errors.As(aggregate.RuntimeErrors()[0], &commandErr) {
		t.Fatalf("runtime error lost Git start failure: %v", aggregate.RuntimeErrors())
	}
	want := filepath.ToSlash(filepath.Join(root, "github.com", "owner", "good")) + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want successful result %q", stdout.String(), want)
	}
}

func TestCloneParallelKeepsKnownSyntaxFailureWhenRootResolutionFails(t *testing.T) {
	wantErr := errors.New("cannot read root configuration")
	deps := cloneDependencies(t.TempDir(), &cloneGitOperations{}, io.Discard, io.Discard)
	deps.RootResolver = &cloneRootResolver{err: wantErr}
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		t.Fatal("root resolution failure must prevent discovery")
		return local.DiscoveryResult{}, nil
	}
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"--parallel", "invalid?repository", "owner/widget"})
	err := command.Execute()
	if cmd.ExitCode(err) != 2 || !errors.Is(err, repospec.ErrUsage) || !errors.Is(err, wantErr) {
		t.Fatalf("parallel result = %v, want usage precedence and both failure causes", err)
	}
}

func TestClonePassesLiteralGitValidCloneRefs(t *testing.T) {
	for _, branch := range []string{"HEAD", "feature/next", "topic\u00a0name", "topic./next"} {
		t.Run(branch, func(t *testing.T) {
			root := cloneTempRoot(t)
			var cloned ghcmd.CloneOptions
			var stdout bytes.Buffer
			command := cmd.NewCloneCommand(cloneDependencies(root, &cloneGitOperations{
				clone: func(_ context.Context, options ghcmd.CloneOptions) error {
					cloned = options
					return nil
				},
			}, &stdout, io.Discard))
			command.SetArgs([]string{"github.com/owner/widget@" + branch})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if cloned.Branch != branch || cloned.URL != "https://github.com/owner/widget" {
				t.Fatalf("clone ref or transport changed: %#v", cloned)
			}
			want := filepath.ToSlash(filepath.Join(root, "github.com", "owner", "widget")) + "\n"
			if stdout.String() != want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), want)
			}
		})
	}
}

func TestExistingCloneTargetRejectsWithoutExternalCommands(t *testing.T) {
	root := cloneTempRoot(t)
	repository := listRepository("github.com/owner/widget", filepath.Join(root, "github.com", "owner", "widget"), 0)
	if err := os.MkdirAll(repository.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	var stdout bytes.Buffer
	deps := cloneDependencies(root, &cloneGitOperations{}, &stdout, io.Discard)
	deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
		return local.DiscoveryResult{Repositories: []local.Repository{repository}}, nil
	}
	deps.GitFactory = func(io.Writer, io.Writer) cmd.CloneGitOperations {
		t.Fatal("existing-repository lookup must not construct clone operations")
		return nil
	}
	identity := &cloneIdentityResolver{err: errors.New("authentication must not be used")}
	deps.IdentityResolver = identity
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"ssh://git@github.com/owner/widget.git"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("Execute() error = %v, want registered collision", err)
	}
	if stdout.String() != "" || identity.Calls() != 0 {
		t.Fatalf("stdout = %q, identity calls = %d", stdout.String(), identity.Calls())
	}
}

func TestListCanonicalizationNeedsNoExternalCommands(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repository := listRepository("github.com/owner/widget", listAbsolutePath("roots", "github.com", "owner", "widget"), 0)
	var stdout bytes.Buffer
	command := cmd.NewListCommand(cmd.ListDependencies{
		Resolver: &listRootResolver{result: rootpkg.Result{RepositoryRoots: []string{listAbsolutePath("roots")}}},
		DiscoverRepositories: func(context.Context, []string) (local.DiscoveryResult, error) {
			return local.DiscoveryResult{Repositories: []local.Repository{repository}}, nil
		},
		Stdout: &stdout,
		Stderr: io.Discard,
	})
	command.SetArgs([]string{"https://github.com/owner/widget.git"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != repository.Identity+"\n" {
		t.Fatalf("stdout = %q, want canonical local identity", stdout.String())
	}
}

func TestAddMoveRemoteResolutionNeedsNoGh(t *testing.T) {
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	base := moveTestPhysical(t, t.TempDir())
	primary, source := filepath.Join(base, "primary"), filepath.Join(base, "source")
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	moveTestInitRepository(t, source, "ssh://git@github.com/Owner/Widget.git")
	git := moveTestGitRunner(io.Discard)
	git.runner.Executable = gitExecutable
	t.Setenv("PATH", t.TempDir())
	var stdout, stderr bytes.Buffer
	command := newAddMoveCommand(t, cmd.RepositoryMoveDependencies{
		Resolver: &moveRootResolver{result: rootpkg.Result{RepositoryRoots: []string{primary}}},
		Git:      git,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	command.SetArgs([]string{"--move", source})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(primary, "github.com", "Owner", "Widget")
	if stdout.String() != filepath.ToSlash(destination)+"\n" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), destination)
	}
}
