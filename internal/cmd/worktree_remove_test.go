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
	"runtime"
	"strings"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
)

func TestNewWorktreeRemoveCommandSelectsExplicitRepository(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/explicit")
	fixture.current = func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error) {
		t.Fatal("Current() called with explicit -R")
		return local.Current{}, nil
	}

	if err := fixture.worktreeRemoveExecute("-R", "acme/widget", fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.resolvedRepository.Identity != fixture.repository.Identity {
		t.Fatalf(
			"resolved repository = %q, want %q",
			fixture.resolvedRepository.Identity,
			fixture.repository.Identity,
		)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewWorktreeRemoveCommandSelectsCurrentMainOrLinked(t *testing.T) {
	for _, main := range []bool{true, false} {
		name := "linked"
		if main {
			name = "main"
		}
		t.Run(name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature/current-"+name)
			fixture.currentWorktree.Main = main
			fixture.currentWorktree.Slot = fixture.branch
			if main {
				fixture.currentWorktree.Slot = ""
			}

			if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if fixture.currentCalls != 1 {
				t.Fatalf("Current() calls = %d, want 1", fixture.currentCalls)
			}
			if fixture.resolvedRepository.Identity != fixture.repository.Identity {
				t.Fatalf(
					"resolved repository = %q, want %q",
					fixture.resolvedRepository.Identity,
					fixture.repository.Identity,
				)
			}
			worktreeRemoveAssertSuccess(t, fixture)
		})
	}
}

func TestNewWorktreeRemoveCommandUsesCurrentRepositoryWithDuplicateIdentity(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/current-duplicate")
	duplicate := fixture.repository
	duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
	duplicate.RootIndex = 1
	fixture.repositories = append(fixture.repositories, duplicate)

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fixture.resolvedRepository.Path != fixture.repository.Path {
		t.Fatalf(
			"resolved repository path = %q, want current %q",
			fixture.resolvedRepository.Path,
			fixture.repository.Path,
		)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewWorktreeRemoveCommandPassesOneForceLevel(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/forced")

	if err := fixture.worktreeRemoveExecute("--force", fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(fixture.git.removals) != 1 {
		t.Fatalf("WorktreeRemove() calls = %d, want 1", len(fixture.git.removals))
	}
	if got := fixture.git.removals[0].options; !got.Force {
		t.Fatalf("WorktreeRemove options = %#v, want Force true", got)
	}
}

func TestNewWorktreeRemoveCommandRejectsUsageAndAmbiguity(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		configure func(*worktreeRemoveFixture)
		wantIs    error
	}{
		{
			name:   "missing branch",
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "too many branches",
			args:   []string{"one", "two"},
			wantIs: repospec.ErrUsage,
		},
		{
			name:   "invalid branch",
			args:   []string{"HEAD"},
			wantIs: local.ErrInvalidBranch,
		},
		{
			name:   "missing repository",
			args:   []string{"-R", "missing", "feature/test"},
			wantIs: local.ErrRepositoryNotFound,
		},
		{
			name: "duplicate explicit identity",
			args: []string{"-R", "github.com/acme/widget", "feature/test"},
			configure: func(fixture *worktreeRemoveFixture) {
				duplicate := fixture.repository
				duplicate.Path = filepath.Join(fixture.repositoryRoot, "duplicate")
				duplicate.RootIndex = 1
				fixture.repositories = append(fixture.repositories, duplicate)
			},
			wantIs: local.ErrRepositoryAmbiguous,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature/test")
			if test.configure != nil {
				test.configure(fixture)
			}

			err := fixture.worktreeRemoveExecute(test.args...)
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, test.wantIs)
			}
			if !errors.Is(err, repospec.ErrUsage) {
				t.Fatalf("Execute() error = %v, want usage classification", err)
			}
			if len(fixture.git.removals) != 0 {
				t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
			}
		})
	}
}

func TestNewWorktreeRemoveCommandRejectsUnsafeManagedResults(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*worktreeRemoveFixture)
		wantIs    error
	}{
		{
			name: "main worktree",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Main = true
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "bare worktree",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Bare = true
			},
			wantIs: local.ErrBareWorktree,
		},
		{
			name: "external path",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Path = filepath.Join(fixture.root, "external")
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "non-deterministic path",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Path = filepath.Join(fixture.base, "other")
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "foreign repository",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.managed.Repository = local.Repository{
					Identity: "github.com/other/foreign",
				}
			},
			wantIs: local.ErrUnsafeWorktree,
		},
		{
			name: "missing registration",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.resolveManagedErr = &local.WorktreeError{
					Kind:   local.ErrWorktreeNotFound,
					Slot:   fixture.branch,
					Reason: "slot is not registered",
				}
			},
			wantIs: local.ErrWorktreeNotFound,
		},
		{
			name: "foreign Git association",
			configure: func(fixture *worktreeRemoveFixture) {
				fixture.resolveManagedErr = &local.WorktreeError{
					Kind:       local.ErrUnsafeWorktree,
					Repository: fixture.repository.Identity,
					Slot:       fixture.branch,
					Path:       fixture.target,
					Reason:     "Git association does not match the selected main repository",
				}
			},
			wantIs: local.ErrUnsafeWorktree,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature/unsafe")
			test.configure(fixture)

			err := fixture.worktreeRemoveExecute("-R", fixture.repository.Identity, fixture.branch)
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, test.wantIs)
			}
			if len(fixture.git.removals) != 0 {
				t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
			}
			if _, statErr := os.Lstat(fixture.target); statErr != nil {
				t.Fatalf("unsafe target changed: %v", statErr)
			}
		})
	}
}

func TestNewWorktreeRemoveCommandPropagatesGitSafetyFailures(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
	}{
		{name: "dirty", err: errors.New("dirty worktree")},
		{name: "locked", err: errors.New("locked worktree")},
	} {
		t.Run(failure.name, func(t *testing.T) {
			fixture := worktreeRemoveNewFixture(t, "feature/"+failure.name)
			fixture.git.removeErr = failure.err

			err := fixture.worktreeRemoveExecute(fixture.branch)
			if !errors.Is(err, failure.err) {
				t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, failure.err)
			}
			if _, statErr := os.Lstat(fixture.target); statErr != nil {
				t.Fatalf("target changed after Git failure: %v", statErr)
			}
			if fixture.stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want no success diagnostic", fixture.stderr.String())
			}
		})
	}
}

func TestNewWorktreeRemoveCommandCleansNestedEmptyParentsToBoundary(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/team/login")
	firstParent := filepath.Dir(fixture.target)
	secondParent := filepath.Dir(firstParent)

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, path := range []string{firstParent, secondParent} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Lstat(%q) error = %v, want not exist", path, err)
		}
	}
	if info, err := os.Lstat(fixture.base); err != nil || !info.IsDir() {
		t.Fatalf("worktree base was removed or changed: info=%v err=%v", info, err)
	}
	wantRemoved := []string{firstParent, secondParent}
	if !reflect.DeepEqual(fixture.cleanupRemovals, wantRemoved) {
		t.Fatalf("cleanup removals = %#v, want %#v", fixture.cleanupRemovals, wantRemoved)
	}
	worktreeRemoveAssertSuccess(t, fixture)
}

func TestNewWorktreeRemoveCommandStopsAtNonemptyParent(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/nonempty/leaf")
	parent := filepath.Dir(fixture.target)
	sentinel := filepath.Join(parent, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel changed: %v", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
}

func TestNewWorktreeRemoveCommandContinuesPastMissingParent(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/missing/parent/leaf")
	missingParent := filepath.Dir(fixture.target)
	firstExistingParent := filepath.Dir(missingParent)
	secondExistingParent := filepath.Dir(firstExistingParent)
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Remove(missingParent)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	wantRemoved := []string{firstExistingParent, secondExistingParent}
	if !reflect.DeepEqual(fixture.cleanupRemovals, wantRemoved) {
		t.Fatalf("cleanup removals = %#v, want %#v", fixture.cleanupRemovals, wantRemoved)
	}
}

func TestNewWorktreeRemoveCommandStopsAtSymlinkParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link setup is not portable on Windows")
	}

	fixture := worktreeRemoveNewFixture(t, "feature/symlink/leaf")
	parent := filepath.Dir(fixture.target)
	outside := filepath.Join(fixture.root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Remove(parent); err != nil {
			return err
		}
		return os.Symlink(outside, parent)
	}

	if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	info, err := os.Lstat(parent)
	if err != nil {
		t.Fatalf("Lstat(symlink parent) error = %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("parent mode = %v, want symlink", info.Mode())
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if info, err := os.Stat(outside); err != nil || !info.IsDir() {
		t.Fatalf("outside directory changed: info=%v err=%v", info, err)
	}
}

func TestNewWorktreeRemoveCommandErrorsWhenGitLeavesDirectory(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/remains")
	fixture.git.leavePath = true

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "still exists") {
		t.Fatalf("Execute() error = %v, want remaining-directory error", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if fixture.stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no success diagnostic", fixture.stderr.String())
	}
	if _, statErr := os.Lstat(fixture.target); statErr != nil {
		t.Fatalf("remaining target changed: %v", statErr)
	}
}

func TestNewWorktreeRemoveCommandRevalidatesCleanupBoundary(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/replaced/leaf")
	originalBase := fixture.base + "-original"
	fixture.git.removeAction = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Rename(fixture.base, originalBase); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Dir(fixture.target), 0o755)
	}

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if err == nil || !strings.Contains(err.Error(), "worktree base") ||
		!strings.Contains(err.Error(), "changed") {
		t.Fatalf("Execute() error = %v, want changed-boundary error", err)
	}
	if len(fixture.cleanupRemovals) != 0 {
		t.Fatalf("cleanup removals = %#v, want none", fixture.cleanupRemovals)
	}
	if info, statErr := os.Stat(filepath.Dir(fixture.target)); statErr != nil || !info.IsDir() {
		t.Fatalf("replacement parent changed: info=%v err=%v", info, statErr)
	}
}

func TestNewWorktreeRemoveCommandWarningsAndWriterFailures(t *testing.T) {
	t.Run("warning prefix", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature/warning")
		warning := local.Warning{
			Kind:      local.WarningInspection,
			Root:      fixture.repositoryRoot,
			RootIndex: 0,
			Path:      filepath.Join(fixture.repositoryRoot, "unreadable"),
			Operation: "inspect",
			Err:       errors.New("unreadable"),
		}
		fixture.warnings = []local.Warning{warning}

		if err := fixture.worktreeRemoveExecute(fixture.branch); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := "gh-qw: warning: " + warning.Error() + "\n" +
			"removed worktree " + local.NormalizePathForOutput(fixture.target) + "\n"
		if got := fixture.stderr.String(); got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})

	t.Run("warning write failure prevents mutation", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature/warning-write")
		fixture.warnings = []local.Warning{{
			Kind:      local.WarningInspection,
			Path:      "entry",
			Operation: "inspect",
		}}
		writeErr := errors.New("warning writer failed")
		fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

		err := fixture.worktreeRemoveExecute(fixture.branch)
		if !errors.Is(err, writeErr) {
			t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, writeErr)
		}
		if len(fixture.git.removals) != 0 {
			t.Fatalf("WorktreeRemove() calls = %d, want 0", len(fixture.git.removals))
		}
	})

	t.Run("diagnostic write failure reports completed mutation", func(t *testing.T) {
		fixture := worktreeRemoveNewFixture(t, "feature/diagnostic-write")
		writeErr := errors.New("diagnostic writer failed")
		fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

		err := fixture.worktreeRemoveExecute(fixture.branch)
		if !errors.Is(err, writeErr) {
			t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, writeErr)
		}
		if err == nil || !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("Execute() error = %v, want explicit completed-mutation context", err)
		}
		if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("target remains after reported mutation: %v", statErr)
		}
	})
}

func TestNewWorktreeRemoveCommandReportsCleanupFailureAfterMutation(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/cleanup/failure")
	cleanupErr := errors.New("cleanup denied")
	fixture.removeErr = cleanupErr

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Execute() error = %v, want errors.Is(%v)", err, cleanupErr)
	}
	if err == nil || !strings.Contains(err.Error(), "removed by Git") {
		t.Fatalf("Execute() error = %v, want completed-mutation context", err)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("target remains after Git removal: %v", statErr)
	}
}

func TestNewWorktreeRemoveCommandJoinsPostMutationFailures(t *testing.T) {
	fixture := worktreeRemoveNewFixture(t, "feature/joined/failures")
	cleanupErr := errors.New("cleanup failed")
	writeErr := errors.New("diagnostic failed")
	fixture.removeErr = cleanupErr
	fixture.stderrWriter = &worktreeRemoveErrorWriter{err: writeErr}

	err := fixture.worktreeRemoveExecute(fixture.branch)
	if !errors.Is(err, cleanupErr) || !errors.Is(err, writeErr) {
		t.Fatalf(
			"Execute() error = %v, want joined cleanup %v and writer %v",
			err,
			cleanupErr,
			writeErr,
		)
	}
	if _, statErr := os.Lstat(fixture.target); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("target remains after Git removal: %v", statErr)
	}
}

func TestNewWorktreeRemoveCommandRealGitLifecycle(t *testing.T) {
	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(root, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	if err := os.MkdirAll(repositoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	worktreeRemoveRunGit(t, repositoryPath, "init", "-b", "main")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.name", "gh-qw test")
	worktreeRemoveRunGit(t, repositoryPath, "config", "user.email", "gh-qw@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "README"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "add", "README")
	worktreeRemoveRunGit(t, repositoryPath, "commit", "-m", "initial")

	branch := "feature/real"
	target := filepath.Join(worktreeRoot, "github.com", "acme", "widget", "feature", "real")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeRemoveRunGit(t, repositoryPath, "worktree", "add", "-b", branch, target, "HEAD")

	var stdout, stderr bytes.Buffer
	command := NewWorktreeRemoveCommand(WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{repositoryRoot},
			WorktreeRoot:    worktreeRoot,
		}},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	command.SetArgs([]string{"-R", "acme/widget", branch})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(),
		"removed worktree "+local.NormalizePathForOutput(target)+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed worktree Lstat error = %v, want not exist", err)
	}
	if _, err := os.Lstat(filepath.Dir(target)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("empty branch parent Lstat error = %v, want not exist", err)
	}
	if info, err := os.Stat(filepath.Join(worktreeRoot, "github.com", "acme", "widget")); err != nil ||
		!info.IsDir() {
		t.Fatalf("per-repository boundary changed: info=%v err=%v", info, err)
	}
	list := worktreeRemoveGitOutput(t, repositoryPath, "worktree", "list", "--porcelain")
	if strings.Contains(list, target) {
		t.Fatalf("git worktree list still contains %q:\n%s", target, list)
	}
}

type worktreeRemoveStaticResolver struct {
	result rootpkg.Result
	err    error
}

func (resolver worktreeRemoveStaticResolver) Resolve() (rootpkg.Result, error) {
	return resolver.result, resolver.err
}

type worktreeRemoveRemoval struct {
	dir     string
	options gitcmd.WorktreeRemoveOptions
}

type worktreeRemoveFakeGit struct {
	removals     []worktreeRemoveRemoval
	removeErr    error
	removeAction func(string) error
	leavePath    bool
}

func (git *worktreeRemoveFakeGit) OutputDir(
	context.Context,
	string,
	...string,
) ([]byte, error) {
	return nil, errors.New("unexpected OutputDir call")
}

func (git *worktreeRemoveFakeGit) WorktreeList(
	context.Context,
	string,
) ([]gitcmd.Worktree, error) {
	return nil, errors.New("unexpected WorktreeList call")
}

func (git *worktreeRemoveFakeGit) WorktreeRemove(
	_ context.Context,
	dir string,
	options gitcmd.WorktreeRemoveOptions,
) error {
	git.removals = append(git.removals, worktreeRemoveRemoval{
		dir:     dir,
		options: options,
	})
	if git.removeErr != nil {
		return git.removeErr
	}
	if git.removeAction != nil {
		return git.removeAction(options.Path)
	}
	if git.leavePath {
		return nil
	}
	return os.Remove(options.Path)
}

type worktreeRemoveFixture struct {
	root               string
	repositoryRoot     string
	worktreeRoot       string
	base               string
	target             string
	branch             string
	repository         local.Repository
	repositories       []local.Repository
	currentWorktree    local.Worktree
	managed            local.Worktree
	resolveManagedErr  error
	resolvedRepository local.Repository
	currentCalls       int
	warnings           []local.Warning
	discoveryErr       error
	current            func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)
	git             *worktreeRemoveFakeGit
	cleanupRemovals []string
	removeErr       error
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stderrWriter    io.Writer
}

func worktreeRemoveNewFixture(t *testing.T, branch string) *worktreeRemoveFixture {
	t.Helper()

	root := worktreeRemovePhysicalPath(t, t.TempDir())
	repositoryRoot := filepath.Join(root, "repositories")
	worktreeRoot := filepath.Join(root, "worktrees")
	repositoryPath := filepath.Join(repositoryRoot, "github.com", "acme", "widget")
	base := filepath.Join(worktreeRoot, "github.com", "acme", "widget")
	target := filepath.Join(base, filepath.FromSlash(branch))
	for _, path := range []string{repositoryPath, target} {
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
	managed := local.Worktree{
		Repository: repository,
		Identity:   repository.Identity + "@" + branch,
		Slot:       branch,
		Path:       target,
		Branch:     branch,
	}
	return &worktreeRemoveFixture{
		root:            root,
		repositoryRoot:  repositoryRoot,
		worktreeRoot:    worktreeRoot,
		base:            base,
		target:          target,
		branch:          branch,
		repository:      repository,
		repositories:    []local.Repository{repository},
		currentWorktree: local.Worktree{Repository: repository, Main: true},
		managed:         managed,
		git:             &worktreeRemoveFakeGit{},
	}
}

func (fixture *worktreeRemoveFixture) worktreeRemoveDependencies() WorktreeRemoveDependencies {
	stderr := fixture.stderrWriter
	if stderr == nil {
		stderr = &fixture.stderr
	}
	current := fixture.current
	if current == nil {
		current = func(
			context.Context,
			string,
			string,
			[]local.Repository,
			...local.CurrentOptions,
		) (local.Current, error) {
			fixture.currentCalls++
			return local.Current{
				Repository: fixture.repository,
				Worktree:   fixture.currentWorktree,
			}, nil
		}
	}
	return WorktreeRemoveDependencies{
		Resolver: worktreeRemoveStaticResolver{result: rootpkg.Result{
			RepositoryRoots: []string{fixture.repositoryRoot},
			WorktreeRoot:    fixture.worktreeRoot,
		}},
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
		Current: current,
		ResolveManaged: func(
			_ context.Context,
			repository local.Repository,
			_ string,
			_ string,
			_ ...local.ManagedWorktreeOptions,
		) (local.Worktree, error) {
			fixture.resolvedRepository = repository
			return fixture.managed, fixture.resolveManagedErr
		},
		Git:    fixture.git,
		Getwd:  func() (string, error) { return fixture.repository.Path, nil },
		Stdout: &fixture.stdout,
		Stderr: stderr,
		Remove: func(path string) error {
			fixture.cleanupRemovals = append(fixture.cleanupRemovals, path)
			if fixture.removeErr != nil {
				return fixture.removeErr
			}
			return os.Remove(path)
		},
	}
}

func (fixture *worktreeRemoveFixture) worktreeRemoveExecute(args ...string) error {
	command := NewWorktreeRemoveCommand(fixture.worktreeRemoveDependencies())
	command.SetArgs(args)
	return command.Execute()
}

func worktreeRemoveAssertSuccess(t *testing.T, fixture *worktreeRemoveFixture) {
	t.Helper()
	if fixture.stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", fixture.stdout.String())
	}
	if got, want := fixture.stderr.String(),
		"removed worktree "+local.NormalizePathForOutput(fixture.target)+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := os.Lstat(fixture.target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target Lstat error = %v, want not exist", err)
	}
	if len(fixture.git.removals) != 1 {
		t.Fatalf("WorktreeRemove() calls = %d, want 1", len(fixture.git.removals))
	}
	if got := fixture.git.removals[0]; got.dir != fixture.repository.Path ||
		got.options.Path != fixture.target {
		t.Fatalf("WorktreeRemove() call = %#v, want repository %q path %q",
			got, fixture.repository.Path, fixture.target)
	}
}

type worktreeRemoveErrorWriter struct {
	err error
}

func (writer *worktreeRemoveErrorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func worktreeRemovePhysicalPath(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}

func worktreeRemoveRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
}

func worktreeRemoveGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v; output = %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
