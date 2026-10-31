package fzfcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperProcessEnv = "GO_WANT_FZFCMD_HELPER"

type helperReport struct {
	Arguments       []string
	Input           string
	DefaultOpts     bool
	DefaultOptsFile bool
	InheritedValue  string
}

type commandRecorder struct {
	executable string
	command    *exec.Cmd
}

func newHelperSelector(t *testing.T, mode string) (*Selector, *commandRecorder) {
	t.Helper()
	t.Setenv(helperProcessEnv, "1")
	recorder := &commandRecorder{}
	selector := &Selector{
		Stderr: io.Discard,
		commandContext: func(ctx context.Context, executable string, args ...string) *exec.Cmd {
			helperArgs := append([]string{"-test.run=^TestFzfSelectorHelperProcess$", "--", mode}, args...)
			command := exec.CommandContext(ctx, os.Args[0], helperArgs...)
			recorder.executable = executable
			recorder.command = command
			return command
		},
	}
	return selector, recorder
}

func TestSelectPassesCandidatesAndControlledOptionsToFzf(t *testing.T) {
	selector, recorder := newHelperSelector(t, "inspect")
	t.Setenv("FZF_DEFAULT_OPTS", "--filter=private --print-query --multi")
	t.Setenv("FZF_DEFAULT_OPTS_FILE", "/private/fzf-defaults")
	t.Setenv("FZFCMD_TEST_VALUE", "inherited")
	var stderr bytes.Buffer
	selector.Stderr = &stderr
	candidates := []string{
		"repository\tgithub.com/acme/repo\t\"/workspace/repo\"",
		"worktree\tgithub.com/acme/repo@feature/new\t" + strconv.Quote("/workspace/space tab\tand newline\n$literal`value`"),
	}

	selected, err := selector.Select(context.Background(), candidates)
	if err != nil || selected != candidates[1] {
		t.Fatalf("Select() = %q, %v; want last offered candidate", selected, err)
	}
	if recorder.executable != "fzf" {
		t.Fatalf("executable = %q, want fzf", recorder.executable)
	}
	var report helperReport
	if err := json.Unmarshal(stderr.Bytes(), &report); err != nil {
		t.Fatalf("decode helper report: %v; stderr = %q", err, stderr.String())
	}
	wantArgs := []string{
		"--read0", "--print0", "--no-multi", "--no-select-1", "--no-exit-0",
		"--prompt=Remove> ",
		"--header=Repositories include all linked worktrees. Enter: select; Esc: cancel.",
	}
	if !slices.Equal(report.Arguments, wantArgs) {
		t.Fatalf("arguments = %#v, want %#v", report.Arguments, wantArgs)
	}
	if report.Input != strings.Join(candidates, "\x00")+"\x00" {
		t.Fatalf("candidate input = %q", report.Input)
	}
	if report.DefaultOpts || report.DefaultOptsFile || report.InheritedValue != "inherited" {
		t.Fatalf("child environment = %#v", report)
	}
}

func TestSelectPreservesDirectStderr(t *testing.T) {
	selector, recorder := newHelperSelector(t, "select")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	selector.Stderr = writer
	selected, selectErr := selector.Select(context.Background(), []string{"offered"})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if selectErr != nil || selected != "offered" || string(output) != "fzf-ui" {
		t.Fatalf("Select() = %q, %v; stderr = %q", selected, selectErr, output)
	}
	if recorder.command.Stderr != writer {
		t.Fatal("selector wrapped the terminal descriptor in a pipe")
	}
}

func TestSelectRejectsInvalidSuccessOutput(t *testing.T) {
	for _, mode := range []string{"empty", "empty-record", "unterminated", "unknown", "multiple", "trailing-newline", "query-first"} {
		t.Run(mode, func(t *testing.T) {
			selector, _ := newHelperSelector(t, mode)
			selected, err := selector.Select(context.Background(), []string{"first", "second"})
			if err == nil || selected != "" {
				t.Fatalf("Select() = %q, %v; want no target and an error", selected, err)
			}
			if errors.Is(err, ErrCanceled) || errors.Is(err, ErrNoMatch) {
				t.Fatalf("invalid output reported as ordinary cancellation: %v", err)
			}
		})
	}
}

func TestSelectPreservesProcessFailuresAndDiscardsPartialSelections(t *testing.T) {
	for _, test := range []struct {
		code     int
		sentinel error
	}{
		{code: 1, sentinel: ErrNoMatch},
		{code: 130, sentinel: ErrCanceled},
		{code: 2},
		{code: 23},
	} {
		t.Run(strconv.Itoa(test.code), func(t *testing.T) {
			selector, _ := newHelperSelector(t, "exit-"+strconv.Itoa(test.code))
			var stderr bytes.Buffer
			selector.Stderr = &stderr
			selected, err := selector.Select(context.Background(), []string{"private-target"})
			if err == nil || selected != "" {
				t.Fatalf("Select() = %q, %v; want no target and a process failure", selected, err)
			}
			var failure *CommandError
			var exitErr *exec.ExitError
			if !errors.As(err, &failure) || failure.ExitCode != test.code || !errors.As(err, &exitErr) {
				t.Fatalf("process error chain = %v", err)
			}
			if errors.Is(err, ErrNoMatch) != (test.sentinel == ErrNoMatch) ||
				errors.Is(err, ErrCanceled) != (test.sentinel == ErrCanceled) {
				t.Fatalf("cancellation/no-match classification = %v", err)
			}
			if stderr.String() != "private-diagnostic" || string(failure.StderrOutput()) != stderr.String() {
				t.Fatalf("stderr = %q, retained = %q", stderr.String(), failure.StderrOutput())
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("formatted error exposed candidate or diagnostic: %q", err)
			}
		})
	}
}

func TestSelectReportsMissingFzf(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	selector := &Selector{Stderr: io.Discard}
	selected, err := selector.Select(context.Background(), []string{"offered"})
	var failure *CommandError
	if selected != "" || !errors.Is(err, exec.ErrNotFound) || !errors.As(err, &failure) || failure.ExitCode != -1 {
		t.Fatalf("Select() = %q, %v; want executable failure", selected, err)
	}
	if !strings.Contains(err.Error(), "install fzf") || !strings.Contains(err.Error(), "explicit removal target") {
		t.Fatalf("missing executable error has no next step: %v", err)
	}
}

func TestSelectPreservesContextCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(deadline), func(t *testing.T) {
			selector, _ := newHelperSelector(t, "wait")
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			selected, err := selector.Select(ctx, []string{"offered"})
			var failure *CommandError
			if selected != "" || !errors.Is(err, want) || !errors.As(err, &failure) {
				t.Fatalf("Select() = %q, %v; want context failure %v", selected, err, want)
			}
			if errors.Is(err, ErrCanceled) || errors.Is(err, ErrNoMatch) {
				t.Fatalf("context failure confused with fzf user selection: %v", err)
			}
		})
	}
}

func TestSelectDoesNotStartFzfWithoutValidCandidates(t *testing.T) {
	for _, candidates := range [][]string{nil, {""}, {"first\x00second"}, {"duplicate", "duplicate"}} {
		t.Run(strconv.Itoa(len(candidates))+strings.Join(candidates, "/"), func(t *testing.T) {
			selector := &Selector{commandContext: func(context.Context, string, ...string) *exec.Cmd {
				t.Fatal("invalid candidate set must not launch fzf")
				return nil
			}}
			selected, err := selector.Select(context.Background(), candidates)
			if selected != "" || err == nil {
				t.Fatalf("Select() = %q, %v; want no target", selected, err)
			}
			if len(candidates) == 0 && !errors.Is(err, ErrNoMatch) {
				t.Fatalf("empty candidate set error = %v, want ErrNoMatch", err)
			}
		})
	}
}

func TestFzfSelectorHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(90)
	}
	mode := os.Args[separator+1]
	if mode == "wait" {
		time.Sleep(10 * time.Second)
		os.Exit(91)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(92)
	}
	records := bytes.Split(bytes.TrimSuffix(input, []byte{0}), []byte{0})
	selected := records[len(records)-1]
	output := append(bytes.Clone(selected), 0)
	switch mode {
	case "inspect":
		_, defaultOpts := os.LookupEnv("FZF_DEFAULT_OPTS")
		_, defaultOptsFile := os.LookupEnv("FZF_DEFAULT_OPTS_FILE")
		_ = json.NewEncoder(os.Stderr).Encode(helperReport{
			Arguments:       os.Args[separator+2:],
			Input:           string(input),
			DefaultOpts:     defaultOpts,
			DefaultOptsFile: defaultOptsFile,
			InheritedValue:  os.Getenv("FZFCMD_TEST_VALUE"),
		})
	case "select":
		_, _ = io.WriteString(os.Stderr, "fzf-ui")
	case "empty":
		output = nil
	case "empty-record":
		output = []byte{0}
	case "unterminated":
		output = selected
	case "unknown":
		output = []byte("not-offered\x00")
	case "multiple":
		output = input
	case "trailing-newline":
		output = append(output, '\n')
	case "query-first":
		output = append([]byte("query\x00"), output...)
	default:
		if code, err := strconv.Atoi(strings.TrimPrefix(mode, "exit-")); err == nil {
			_, _ = os.Stdout.Write(output)
			_, _ = io.WriteString(os.Stderr, "private-diagnostic")
			os.Exit(code)
		}
		os.Exit(93)
	}
	_, _ = os.Stdout.Write(output)
	os.Exit(0)
}
