package gitcmd

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRefFormatUsesLiteralOperandAndQuietStreams(t *testing.T) {
	runner, recorder := newHelperRunner(t, "stream")
	var stdout, stderr bytes.Buffer
	runner.Stdout, runner.Stderr = &stdout, &stderr
	const ref = "topic@two/next"
	if err := runner.CheckRefFormat(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	want := []string{"check-ref-format", "--allow-onelevel", ref}
	if got := recorder.last(t).args; !slicesEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("validation wrote stdout %q or stderr %q", stdout.String(), stderr.String())
	}
}

func TestCheckRefFormatRejectsOptionsBeforeExecution(t *testing.T) {
	runner, recorder := newHelperRunner(t, "success")
	for _, ref := range []string{"-topic", "--normalize", "--branch", "--help"} {
		if err := runner.CheckRefFormat(context.Background(), ref); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("CheckRefFormat(%q) error = %v, want ErrInvalidRef", ref, err)
		}
	}
	if len(recorder.commands) != 0 {
		t.Fatal("an option-shaped ref reached Git")
	}
}

func TestCheckRefFormatDistinguishesInvalidRefFromExecutionFailure(t *testing.T) {
	invalid, _ := newHelperRunner(t, "exit-1")
	if err := invalid.CheckRefFormat(context.Background(), "invalid"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("invalid ref error = %v", err)
	}
	failed, _ := newHelperRunner(t, "exit-2")
	err := failed.CheckRefFormat(context.Background(), "topic")
	if code, ok := CommandExitCode(err); !ok || code != 2 || errors.Is(err, ErrInvalidRef) {
		t.Fatalf("execution failure = %v, want Git exit 2", err)
	}
	missing := &Runner{Executable: filepath.Join(t.TempDir(), "missing-git")}
	err = missing.CheckRefFormat(context.Background(), "topic")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.ExitCode != -1 || errors.Is(err, ErrInvalidRef) {
		t.Fatalf("missing executable error = %v, want a start failure", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := failed.CheckRefFormat(ctx, "topic"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation error = %v", err)
	}
}

func TestCheckRefFormatUsesGitWithoutCheckoutHistoryExpansion(t *testing.T) {
	dir := t.TempDir()
	gitcmdRunRealGit(t, "", "init", "-q", "-b", "main", dir)
	gitcmdRunRealGit(t, dir, "commit", "-q", "--allow-empty", "-m", "initial")
	gitcmdRunRealGit(t, dir, "checkout", "-q", "-b", "previous")
	gitcmdRunRealGit(t, dir, "checkout", "-q", "main")
	// Establish the regression condition: --branch would accept this input
	// and replace it with a name determined by this checkout's reflog.
	if got := strings.TrimSpace(gitcmdRunRealGit(t, dir, "check-ref-format", "--branch", "@{-1}")); got != "previous" {
		t.Fatalf("history shorthand = %q, want previous", got)
	}
	t.Chdir(dir)
	runner := &Runner{}
	for _, ref := range []string{"HEAD", "feature/slash", "topic@two", "topic\u00a0name", "topic./next"} {
		if err := runner.CheckRefFormat(context.Background(), ref); err != nil {
			t.Fatalf("CheckRefFormat(%q) error = %v", ref, err)
		}
	}
	for _, ref := range []string{"@", "@{-1}", "-topic", "bad..ref"} {
		if err := runner.CheckRefFormat(context.Background(), ref); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("CheckRefFormat(%q) error = %v, want ErrInvalidRef", ref, err)
		}
	}
}
