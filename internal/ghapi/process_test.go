package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daiksud/gh-qw/internal/ghcmd"
)

type apiInvocation struct {
	Args       []string
	Env        []string
	Formatting []string
	Stdin      string
}

// The test executable stands in for gh, so these tests never contact a host or
// use the developer's credentials. It accepts gh argv directly, without a shell.
func TestMain(m *testing.M) {
	if mode := os.Getenv("GHAPI_TEST_HELPER"); mode != "" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(90)
		}
		invocation := apiInvocation{Args: os.Args[1:], Stdin: string(input)}
		for _, key := range []string{"GH_HOST", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
			invocation.Env = append(invocation.Env, key+"="+os.Getenv(key))
		}
		for _, key := range []string{"GH_FORCE_TTY", "CLICOLOR_FORCE", "NO_COLOR"} {
			invocation.Formatting = append(invocation.Formatting, key+"="+os.Getenv(key))
		}
		data, err := json.Marshal(invocation)
		if err != nil || os.WriteFile(os.Getenv("GHAPI_TEST_RECORD"), data, 0o600) != nil {
			os.Exit(91)
		}
		if mode == "wait" {
			time.Sleep(10 * time.Second)
		}
		output := os.Getenv("GHAPI_TEST_OUTPUT")
		if mode == "formatting" {
			// gh can color HTTP headers and JSON even when stdout is captured.
			colorEnabled := os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0"
			if os.Getenv("GH_FORCE_TTY") != "" {
				colorEnabled = os.Getenv("NO_COLOR") == "" && os.Getenv("CLICOLOR") != "0"
			}
			if colorEnabled {
				if strings.HasPrefix(output, "HTTP/") {
					output = strings.ReplaceAll(output, "Content-Type:", "\x1b[1;34mContent-Type\x1b[m:")
				} else {
					output = "\x1b[1;34m" + output + "\x1b[m"
				}
			}
		}
		fmt.Fprint(os.Stdout, output)
		if mode == "formatting" && strings.HasPrefix(output, "HTTP/2.0 404 ") {
			fmt.Fprint(os.Stderr, "gh: HTTP 404")
			os.Exit(1)
		}
		if mode == "fail" {
			fmt.Fprint(os.Stderr, "diagnostic contains "+os.Getenv("GH_TOKEN"))
			os.Exit(4)
		}
		fmt.Fprint(os.Stderr, "gh api diagnostic")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func prepareAPIGhProcess(t *testing.T, mode, output string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "invocation.json")
	t.Setenv("GH_PATH", executable)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GHAPI_TEST_HELPER", mode)
	t.Setenv("GHAPI_TEST_RECORD", path)
	t.Setenv("GHAPI_TEST_OUTPUT", output)
	t.Setenv("GH_HOST", "ambient.example.com")
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(key, "dummy-"+key)
	}
	return path
}

func TestClientUsesGHPathAndNativeAuthenticationEnvironment(t *testing.T) {
	record := prepareAPIGhProcess(t, "success", "trunk\n")
	var diagnostics bytes.Buffer
	client := NewClient(&diagnostics)
	branch, err := client.DefaultBranch(context.Background(), "selected.example.com", "acme", "widgets")
	if err != nil || branch != "trunk" {
		t.Fatalf("DefaultBranch() = %q, %v, want trunk", branch, err)
	}
	if diagnostics.String() != "gh api diagnostic" {
		t.Fatalf("diagnostics = %q, want only gh stderr", diagnostics.String())
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var invocation apiInvocation
	if err := json.Unmarshal(data, &invocation); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"api", "--hostname", "selected.example.com", "repos/acme/widgets", "--jq", ".default_branch // empty"}
	if !slices.Equal(invocation.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", invocation.Args, wantArgs)
	}
	wantEnv := []string{
		"GH_HOST=ambient.example.com", "GH_TOKEN=dummy-GH_TOKEN", "GITHUB_TOKEN=dummy-GITHUB_TOKEN",
		"GH_ENTERPRISE_TOKEN=dummy-GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN=dummy-GITHUB_ENTERPRISE_TOKEN",
	}
	if !slices.Equal(invocation.Env, wantEnv) || invocation.Stdin != "" {
		t.Fatalf("invocation = %#v, want unchanged authentication environment and null stdin", invocation)
	}
}

func TestClientCapturesMachineReadableResponsesWithForcedFormatting(t *testing.T) {
	for _, forcing := range []struct {
		name  string
		color string
		tty   string
	}{
		{name: "forced color", color: "1"},
		{name: "forced terminal", tty: "80"},
	} {
		for _, operation := range []string{"exists", "missing", "owner repositories"} {
			t.Run(forcing.name+"/"+operation, func(t *testing.T) {
				output := "HTTP/2.0 200 OK\nContent-Type: application/json\r\n\r\n"
				if operation == "missing" {
					output = "HTTP/2.0 404 Not Found\nContent-Type: application/json\r\n\r\n"
				} else if operation == "owner repositories" {
					output = "[" + ownerRepositoriesTestPage("acme", false, "acme/widgets") + "]"
				}
				record := prepareAPIGhProcess(t, "formatting", output)
				client := NewClient(io.Discard)
				// Read the environment for each request, after client construction.
				t.Setenv("GH_TOKEN", "dummy-late-token")
				t.Setenv("GH_FORCE_TTY", forcing.tty)
				t.Setenv("CLICOLOR_FORCE", forcing.color)
				t.Setenv("CLICOLOR", "1")
				t.Setenv("NO_COLOR", "")
				if operation == "owner repositories" {
					got, err := client.OwnerRepositories(context.Background(), "selected.example.com", "acme")
					if err != nil || !slices.Equal(got, []string{"acme/widgets"}) {
						t.Fatalf("OwnerRepositories() = %v, %v, want uncolored repository data", got, err)
					}
				} else {
					got, err := client.RepositoryExists(context.Background(), "selected.example.com", "acme", "widgets")
					if err != nil || got != (operation == "exists") {
						t.Fatalf("RepositoryExists() = %v, %v, want parsed HTTP status", got, err)
					}
				}
				data, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				var invocation apiInvocation
				if err := json.Unmarshal(data, &invocation); err != nil {
					t.Fatal(err)
				}
				wantEnv := []string{
					"GH_HOST=ambient.example.com", "GH_TOKEN=dummy-late-token", "GITHUB_TOKEN=dummy-GITHUB_TOKEN",
					"GH_ENTERPRISE_TOKEN=dummy-GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN=dummy-GITHUB_ENTERPRISE_TOKEN",
				}
				wantFormatting := []string{"GH_FORCE_TTY=", "CLICOLOR_FORCE=0", "NO_COLOR=1"}
				if !slices.Equal(invocation.Env, wantEnv) || !slices.Equal(invocation.Formatting, wantFormatting) || invocation.Stdin != "" {
					t.Fatalf("invocation = %#v, want inherited auth, plain output, and null stdin", invocation)
				}
				if len(invocation.Args) < 3 || !slices.Equal(invocation.Args[:3], []string{"api", "--hostname", "selected.example.com"}) {
					t.Fatalf("args = %v, want explicit selected host", invocation.Args)
				}
				if os.Getenv("GH_FORCE_TTY") != forcing.tty || os.Getenv("CLICOLOR_FORCE") != forcing.color || os.Getenv("NO_COLOR") != "" {
					t.Fatal("API capture changed the caller's formatting environment")
				}
			})
		}
	}
}

func TestClientErrorsPreserveFailureWithoutIncludingGhDiagnostics(t *testing.T) {
	prepareAPIGhProcess(t, "fail", "partial response\n")
	var diagnostics bytes.Buffer
	client := NewClient(&diagnostics)
	_, err := client.DefaultBranch(context.Background(), "github.com", "acme", "widgets")
	var commandErr *ghcmd.CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode != 4 {
		t.Fatalf("DefaultBranch() error = %v, want preserved gh exit code 4", err)
	}
	if strings.Contains(err.Error(), "dummy-GH_TOKEN") || strings.Contains(err.Error(), "partial response") {
		t.Fatalf("DefaultBranch() error includes gh output: %q", err)
	}
	if !strings.Contains(diagnostics.String(), "diagnostic contains dummy-GH_TOKEN") {
		t.Fatalf("native gh diagnostic was not relayed: %q", diagnostics.String())
	}
}

func TestClientPreservesCancellation(t *testing.T) {
	prepareAPIGhProcess(t, "wait", "unused")
	client := NewClient(io.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.DefaultBranch(ctx, "github.com", "acme", "widgets")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DefaultBranch() error = %v, want context deadline in chain", err)
	}
}

func TestClientReportsMissingExecutableOnlyWhenCalled(t *testing.T) {
	t.Setenv("GH_PATH", "")
	t.Setenv("PATH", t.TempDir())
	client := NewClient(io.Discard)
	_, err := client.DefaultBranch(context.Background(), "github.com", "acme", "widgets")
	var commandErr *ghcmd.CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode != -1 ||
		!strings.Contains(err.Error(), "gh command could not be started") {
		t.Fatalf("error = %v, want sanitized gh start failure", err)
	}
}
