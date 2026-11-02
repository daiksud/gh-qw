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
	"github.com/daiksud/gh-qw/internal/fzfcmd"
	"github.com/daiksud/gh-qw/internal/ghcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
)

func TestCloneOwnerListsWithoutLocalConfigurationOrMutation(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			var stdout bytes.Buffer
			deps := cloneDependencies(cloneTempRoot(t), &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
				t.Fatal("listing must not clone")
				return nil
			}}, &stdout, io.Discard)
			deps.RootResolver = &cloneRootResolver{err: errors.New("listing must not resolve local roots")}
			deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
				t.Fatal("listing must not discover local repositories")
				return local.DiscoveryResult{}, nil
			}
			deps.RepositoryAPI = &cloneRepositoryAPI{repositories: func(_ context.Context, host, owner string) ([]string, error) {
				if host != "github.com" || owner != "acme" {
					t.Fatalf("scope = %s/%s", host, owner)
				}
				return []string{"acme/one", "acme/two"}, nil
			}}
			deps.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			command := cmd.NewCloneCommand(deps)
			args := []string{"acme"}
			if parallel {
				args = append([]string{"--parallel"}, args...)
			}
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != "acme/one\nacme/two\n" {
				t.Fatalf("listing = %q", stdout.String())
			}
		})
	}
}

func TestCloneMissingRepositoryListsLiteralNameMatches(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"acme/widget", "acme/my-widget\nacme/widget-tools\n"},
		{"acme/widget.git", "acme/my-widget\nacme/widget-tools\n"},
		{"acme/acme", ""},
		{"acme/WIDGET", ""},
		{"acme/widget.tools", "acme/widget.tools\n"},
	} {
		t.Run(test.input, func(t *testing.T) {
			root := cloneTempRoot(t)
			var stdout bytes.Buffer
			deps := cloneDependencies(root, &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
				t.Fatal("candidate output must not clone")
				return nil
			}}, &stdout, io.Discard)
			deps.RepositoryAPI = &cloneRepositoryAPI{
				exists: func(context.Context, string, string, string) (bool, error) { return false, nil },
				repositories: func(context.Context, string, string) ([]string, error) {
					return []string{"acme/my-widget", "acme/other", "acme/widget-tools", "acme/widget.tools"}, nil
				},
			}
			deps.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			command := cmd.NewCloneCommand(deps)
			command.SetArgs([]string{test.input})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			want := test.want
			if strings.HasSuffix(test.input, "/widget") || strings.HasSuffix(test.input, "/widget.git") {
				want += "acme/widget.tools\n"
			}
			if stdout.String() != want {
				t.Fatalf("listing = %q, want %q", stdout.String(), want)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("listing changed root: %v, %v", entries, err)
			}
		})
	}
}

func TestCloneSelectorReceivesEveryRepositoryAndPreservesOptions(t *testing.T) {
	for _, input := range []string{"acme", "acme/wid@feature/topic"} {
		for _, parallel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parallel=%v", input, parallel), func(t *testing.T) {
				root := cloneTempRoot(t)
				var stdout bytes.Buffer
				var options ghcmd.CloneOptions
				var registered string
				deps := cloneDependencies(root, &cloneGitOperations{clone: func(_ context.Context, got ghcmd.CloneOptions) error {
					options = got
					return nil
				}}, &stdout, io.Discard)
				candidates := []string{"acme/completely-different", "acme/widget"}
				deps.RepositoryAPI = &cloneRepositoryAPI{
					exists:       func(context.Context, string, string, string) (bool, error) { return false, nil },
					repositories: func(context.Context, string, string) ([]string, error) { return candidates, nil },
				}
				deps.LookPath = func(string) (string, error) { return "/fzf", nil }
				deps.Select = func(_ context.Context, got []string, query string) (string, error) {
					wantQuery := ""
					if input != "acme" {
						wantQuery = "wid"
					}
					if !slices.Equal(got, candidates) || query != wantQuery {
						t.Fatalf("selector got %v, query %q; want all %v and %q", got, query, candidates, wantQuery)
					}
					return candidates[0], nil
				}
				deps.OnAcquired = func(identity, path string) error {
					registered = identity
					if path != filepath.Join(root, "github.com", "acme", "completely-different") {
						t.Fatalf("registered path = %q", path)
					}
					return nil
				}
				command := cmd.NewCloneCommand(deps)
				args := []string{"-p", "--shallow", "--branch", "flag-branch", "--no-recursive", "--partial", "blobless", input}
				if parallel {
					args = append([]string{"--parallel"}, args...)
				}
				command.SetArgs(args)
				if err := command.Execute(); err != nil {
					t.Fatal(err)
				}
				branch := "flag-branch"
				if input != "acme" {
					branch = "feature/topic"
				}
				if options.URL != "ssh://git@github.com/acme/completely-different" || options.Branch != branch ||
					!options.Shallow || options.Submodules != ghcmd.SubmodulesDisabled || options.Filter != ghcmd.PartialFilterBlobless {
					t.Fatalf("clone options = %+v", options)
				}
				if registered != "github.com/acme/completely-different" || stdout.String() != filepath.ToSlash(options.Destination)+"\n" {
					t.Fatalf("registration %q, stdout %q", registered, stdout.String())
				}
			})
		}
	}
}

func TestCloneExactRepositoryDoesNotListOrSelect(t *testing.T) {
	deps := cloneDependencies(cloneTempRoot(t), &cloneGitOperations{}, io.Discard, io.Discard)
	deps.RepositoryAPI = &cloneRepositoryAPI{
		exists: func(_ context.Context, host, owner, repo string) (bool, error) {
			if host != "github.com" || owner != "acme" || repo != "widget" {
				t.Fatalf("lookup = %s/%s/%s", host, owner, repo)
			}
			return true, nil
		},
		repositories: func(context.Context, string, string) ([]string, error) {
			t.Fatal("exact existing repository must not list candidates")
			return nil, nil
		},
	}
	deps.LookPath = func(string) (string, error) {
		t.Fatal("exact existing repository must not inspect fzf")
		return "", nil
	}
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"acme/widget"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCloneSelectedRepositoryPreservesGHHostPort(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.example.com:8443")
	var options ghcmd.CloneOptions
	deps := cloneDependencies(cloneTempRoot(t), &cloneGitOperations{clone: func(_ context.Context, got ghcmd.CloneOptions) error {
		options = got
		return nil
	}}, io.Discard, io.Discard)
	deps.RepositoryAPI = &cloneRepositoryAPI{
		exists: func(_ context.Context, host, _, _ string) (bool, error) {
			if host != "ghe.example.com:8443" {
				t.Fatalf("lookup host = %q", host)
			}
			return false, nil
		},
		repositories: func(_ context.Context, host, _ string) ([]string, error) {
			if host != "ghe.example.com:8443" {
				t.Fatalf("listing host = %q", host)
			}
			return []string{"acme/widget"}, nil
		},
	}
	deps.LookPath = func(string) (string, error) { return "/fzf", nil }
	deps.Select = func(context.Context, []string, string) (string, error) { return "acme/widget", nil }
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"acme/wid"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if options.URL != "https://ghe.example.com:8443/acme/widget" {
		t.Fatalf("selected clone URL = %q", options.URL)
	}
}

func TestCloneDiscoveryFailuresNeverAcquire(t *testing.T) {
	wantFailure := errors.New("API unavailable")
	for _, test := range []struct {
		name                          string
		input                         string
		existsErr, listErr, selectErr error
		selection                     string
		status                        int
	}{
		{name: "lookup failure", input: "acme/widget", existsErr: wantFailure, status: 1},
		{name: "list failure", input: "acme", listErr: wantFailure, status: 1},
		{name: "cancel", input: "acme", selectErr: fzfcmd.ErrCanceled, status: 130},
		{name: "no match", input: "acme/widget", selectErr: fzfcmd.ErrNoMatch, status: 1},
		{name: "selector failure", input: "acme", selectErr: wantFailure, status: 1},
		{name: "unknown selection", input: "acme", selection: "other/repository", status: 1},
	} {
		for _, parallel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", test.name, parallel), func(t *testing.T) {
				root := cloneTempRoot(t)
				var stdout bytes.Buffer
				deps := cloneDependencies(root, &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
					t.Fatal("failed discovery must not clone")
					return nil
				}}, &stdout, io.Discard)
				deps.RepositoryAPI = &cloneRepositoryAPI{
					exists: func(context.Context, string, string, string) (bool, error) { return false, test.existsErr },
					repositories: func(context.Context, string, string) ([]string, error) {
						if test.existsErr != nil {
							t.Fatal("lookup error must not trigger listing")
						}
						return []string{"acme/widget"}, test.listErr
					},
				}
				deps.LookPath = func(string) (string, error) { return "/fzf", nil }
				deps.Select = func(context.Context, []string, string) (string, error) { return test.selection, test.selectErr }
				deps.OnAcquired = func(string, string) error { t.Fatal("failure must not register"); return nil }
				command := cmd.NewCloneCommand(deps)
				args := []string{test.input}
				if parallel {
					args = append([]string{"--parallel"}, args...)
				}
				command.SetArgs(args)
				err := command.Execute()
				if cmd.ExitCode(err) != test.status || stdout.Len() != 0 {
					t.Fatalf("error = %v, status = %d, stdout = %q", err, cmd.ExitCode(err), stdout.String())
				}
				if (test.existsErr == wantFailure || test.listErr == wantFailure || test.selectErr == wantFailure) && !errors.Is(err, wantFailure) {
					t.Fatalf("lost failure cause: %v", err)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("failure changed root: %v %v", entries, err)
				}
			})
		}
	}
}

func TestCloneParallelSelectionCompletesBeforeCloning(t *testing.T) {
	selected := 0
	cloned := 0
	deps := cloneDependencies(cloneTempRoot(t), &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
		if selected != 2 {
			t.Errorf("clone started before both selections: %d", selected)
		}
		cloned++
		return nil
	}}, io.Discard, io.Discard)
	deps.RepositoryAPI = &cloneRepositoryAPI{repositories: func(_ context.Context, _, owner string) ([]string, error) {
		return []string{owner + "/widget"}, nil
	}}
	deps.LookPath = func(string) (string, error) { return "/fzf", nil }
	deps.Select = func(_ context.Context, candidates []string, _ string) (string, error) {
		selected++
		if selected == 2 {
			return "", fzfcmd.ErrCanceled
		}
		return candidates[0], nil
	}
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"--parallel", "one", "two"})
	if err := command.Execute(); cmd.ExitCode(err) != 130 || cloned != 0 || selected != 2 {
		t.Fatalf("canceled preparation = %v, cloned=%d, selected=%d", err, cloned, selected)
	}
}

func TestCloneParallelNoMatchPreservesKnownUsageFailure(t *testing.T) {
	deps := cloneDependencies(cloneTempRoot(t), &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
		t.Fatal("no match must stop before parallel clones")
		return nil
	}}, io.Discard, io.Discard)
	deps.RepositoryAPI = &cloneRepositoryAPI{repositories: func(context.Context, string, string) ([]string, error) {
		return []string{"acme/widget"}, nil
	}}
	deps.LookPath = func(string) (string, error) { return "/fzf", nil }
	deps.Select = func(context.Context, []string, string) (string, error) { return "", fzfcmd.ErrNoMatch }
	command := cmd.NewCloneCommand(deps)
	command.SetArgs([]string{"--parallel", "other/queued", "invalid?repository", "acme"})
	err := command.Execute()
	if cmd.ExitCode(err) != 2 || !errors.Is(err, repospec.ErrUsage) || !errors.Is(err, fzfcmd.ErrNoMatch) {
		t.Fatalf("parallel result = %v, status=%d, want both failures with usage precedence", err, cmd.ExitCode(err))
	}
}

func TestCloneParallelDoesNotRetryFailedGlobalPreparation(t *testing.T) {
	for _, operation := range []string{"discovery", "warning"} {
		t.Run(operation, func(t *testing.T) {
			root := cloneTempRoot(t)
			failure := errors.New("first preparation failed")
			writer := &cloneRecoveringWriter{failure: failure}
			deps := cloneDependencies(root, &cloneGitOperations{clone: func(context.Context, ghcmd.CloneOptions) error {
				t.Fatal("global preparation failure must prevent every clone")
				return nil
			}}, io.Discard, writer)
			calls := 0
			deps.Discover = func(context.Context, []string) (local.DiscoveryResult, error) {
				calls++
				if calls == 1 && operation == "discovery" {
					return local.DiscoveryResult{}, failure
				}
				return local.DiscoveryResult{Warnings: []local.Warning{{Path: root}}}, nil
			}
			command := cmd.NewCloneCommand(deps)
			command.SetArgs([]string{"--parallel", "acme/one", "acme/two"})
			err := command.Execute()
			if !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("preparation error=%v, discovery calls=%d; want original fatal failure", err, calls)
			}
		})
	}
}

type cloneRecoveringWriter struct{ failure error }

func (w *cloneRecoveringWriter) Write(data []byte) (int, error) {
	if w.failure != nil {
		err := w.failure
		w.failure = nil
		return 0, err
	}
	return len(data), nil
}
