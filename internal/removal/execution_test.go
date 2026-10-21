package removal

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestExecutorVerifiesPathAndRegistrationBeforeCleanup(t *testing.T) {
	gitFailure := errors.New("git removal failed")
	listFailure := errors.New("registration state unavailable")
	pathFailure := errors.New("path state unavailable")
	cleanupFailure := errors.New("parent removal denied")
	for _, test := range []struct {
		name         string
		configure    func(*executionFixture)
		step         Step
		removed      bool
		listCalls    int
		cleanupCalls int
		wantError    error
	}{
		{
			name: "git refusal",
			configure: func(f *executionFixture) {
				f.git.remove = func() error { return gitFailure }
			},
			step: RemoveGit, wantError: gitFailure,
		},
		{
			name: "git fails after deleting target",
			configure: func(f *executionFixture) {
				f.git.remove = func() error { return errors.Join(os.Remove(f.target), gitFailure) }
			},
			step: RemoveGit, removed: true, wantError: gitFailure,
		},
		{
			name: "git leaves target",
			configure: func(f *executionFixture) {
				f.git.remove = func() error { return nil }
			},
			step: VerifyPath,
		},
		{
			name: "unknown target state",
			configure: func(f *executionFixture) {
				f.executor.Filesystem.Lstat = func(path string) (fs.FileInfo, error) {
					if path == f.target {
						return nil, pathFailure
					}
					return os.Lstat(path)
				}
			},
			step: VerifyPath, wantError: pathFailure,
		},
		{
			name: "unknown registrations",
			configure: func(f *executionFixture) {
				f.git.list = func() ([]gitcmd.Worktree, error) { return nil, listFailure }
			},
			step: ReadRegistrations, removed: true, listCalls: 1, wantError: listFailure,
		},
		{
			name: "registration remains",
			configure: func(f *executionFixture) {
				f.git.list = func() ([]gitcmd.Worktree, error) {
					return []gitcmd.Worktree{{Path: f.target}}, nil
				}
			},
			step: VerifyRegistration, removed: true, listCalls: 1,
		},
		{
			name: "cleanup fails after confirmed removal",
			configure: func(f *executionFixture) {
				f.executor.Remove = func(path string) error {
					f.cleaned = append(f.cleaned, path)
					return cleanupFailure
				}
			},
			step: CleanupParents, removed: true, listCalls: 1, cleanupCalls: 1, wantError: cleanupFailure,
		},
		{name: "verified removal", removed: true, listCalls: 1, cleanupCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newExecutionFixture(t)
			if test.configure != nil {
				test.configure(f)
			}
			result := f.execute(context.Background())
			if result.Path != f.target || result.PathRemoved != test.removed || result.FailedStep != test.step {
				t.Fatalf("result=%+v; want removed=%t step=%v", result, test.removed, test.step)
			}
			if (result.Err != nil) != (test.step != 0) {
				t.Fatalf("result error=%v, step=%v", result.Err, test.step)
			}
			if test.wantError != nil && !errors.Is(result.Err, test.wantError) {
				t.Fatalf("result error=%v, want %v", result.Err, test.wantError)
			}
			if f.git.listCalls != test.listCalls || len(f.cleaned) != test.cleanupCalls {
				t.Fatalf("list calls=%d cleanup=%v", f.git.listCalls, f.cleaned)
			}
			if f.git.removeCalls != 1 || f.git.repository != "selected repository" ||
				f.git.options != (gitcmd.WorktreeRemoveOptions{Path: f.target, Force: true}) {
				t.Fatalf("Git invocation=%+v", f.git)
			}
			if _, err := os.Stat(f.boundary.Path); err != nil {
				t.Fatalf("cleanup deleted its boundary: %v", err)
			}
		})
	}
}

func TestExecutorStopsOnCancellationAtEveryMutationBoundary(t *testing.T) {
	for _, stage := range []string{"before Git", "after Git", "after registration", "between parents"} {
		t.Run(stage, func(t *testing.T) {
			f := newExecutionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "before Git":
				cancel()
			case "after Git":
				f.git.remove = func() error { cancel(); return os.Remove(f.target) }
			case "after registration":
				f.git.list = func() ([]gitcmd.Worktree, error) { cancel(); return nil, nil }
			case "between parents":
				f.executor.Remove = func(path string) error {
					f.cleaned = append(f.cleaned, path)
					cancel()
					return os.Remove(path)
				}
			}
			result := f.execute(ctx)
			if !errors.Is(result.Err, context.Canceled) {
				t.Fatalf("result=%+v, want cancellation", result)
			}
			if stage == "before Git" && f.git.removeCalls != 0 {
				t.Fatal("invoked Git after cancellation")
			}
			if stage == "between parents" {
				if len(f.cleaned) != 1 {
					t.Fatalf("cleanup=%v, want only first parent", f.cleaned)
				}
			} else if len(f.cleaned) != 0 {
				t.Fatalf("cleanup=%v after cancellation", f.cleaned)
			}
		})
	}
}

func TestCleanupRejectsReplacedIdentityAndUnsafeParentShape(t *testing.T) {
	for _, change := range []string{"boundary", "parent", "symlink parent", "file parent", "parent during read", "boundary during read"} {
		t.Run(change, func(t *testing.T) {
			f := newExecutionFixture(t)
			parent := filepath.Dir(f.target)
			replace := func(path string) {
				t.Helper()
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(f.target); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "boundary":
				replace(f.boundary.Path)
			case "parent":
				replace(parent)
			case "symlink parent", "file parent":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if change == "symlink parent" {
					if err := os.Symlink(t.TempDir(), parent); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				} else if err := os.WriteFile(parent, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "parent during read":
				f.executor.ReadDir = func(path string) ([]os.DirEntry, error) {
					replace(path)
					return nil, nil
				}
			case "boundary during read":
				f.executor.ReadDir = func(path string) ([]os.DirEntry, error) {
					replace(f.boundary.Path)
					// Preserve the original parent identity under the replaced boundary.
					if err := os.Rename(filepath.Join(f.boundary.Path+"-original", "feature"), filepath.Join(f.boundary.Path, "feature")); err != nil {
						t.Fatal(err)
					}
					return nil, nil
				}
			}
			err := f.executor.CleanupEmptyChain(context.Background(), f.boundary, f.parents)
			want := ErrSafety
			switch change {
			case "boundary", "boundary during read":
				want = ErrCleanupBoundary
			case "symlink parent", "file parent":
				want = ErrCleanupParentShape
			}
			if !errors.Is(err, want) || len(f.cleaned) != 0 {
				t.Fatalf("error=%v cleanup=%v; want %v and no deletion", err, f.cleaned, want)
			}
		})
	}
}

func TestCleanupStopsAtContentAndSkipsMissingOriginalParents(t *testing.T) {
	for _, change := range []string{"nonempty", "missing", "became nonempty"} {
		t.Run(change, func(t *testing.T) {
			f := newExecutionFixture(t)
			if err := os.Remove(f.target); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Dir(f.target)
			switch change {
			case "nonempty":
				if err := os.WriteFile(filepath.Join(parent, "keep"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
			case "became nonempty":
				f.executor.Remove = func(string) error { return syscall.ENOTEMPTY }
			}
			if err := f.executor.CleanupEmptyChain(context.Background(), f.boundary, f.parents); err != nil {
				t.Fatal(err)
			}
			if change == "missing" {
				if want := []string{filepath.Dir(parent)}; !reflect.DeepEqual(f.cleaned, want) {
					t.Fatalf("cleanup=%v, want %v", f.cleaned, want)
				}
			} else if len(f.cleaned) != 0 {
				t.Fatalf("cleanup=%v, want no deletion", f.cleaned)
			}
		})
	}
}

func TestCleanupRevalidatesBoundaryAndCancellationWithNoParents(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "replaced boundary", true: "canceled"}[canceled], func(t *testing.T) {
			f := newExecutionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrCleanupBoundary
			if canceled {
				cancel()
				want = context.Canceled
			} else {
				if err := os.Rename(f.boundary.Path, f.boundary.Path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.boundary.Path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.executor.CleanupEmptyChain(ctx, f.boundary, nil); !errors.Is(err, want) {
				t.Fatalf("empty cleanup error=%v, want %v", err, want)
			}
			if len(f.cleaned) != 0 {
				t.Fatalf("cleanup=%v, want no mutations", f.cleaned)
			}
		})
	}
}

type executionFixture struct {
	executor Executor
	git      *executionGit
	target   string
	boundary Node
	parents  []Node
	cleaned  []string
}

func newExecutionFixture(t *testing.T) *executionFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "worktrees")
	f := &executionFixture{target: filepath.Join(base, "feature", "topic", "leaf")}
	if err := os.MkdirAll(f.target, 0o700); err != nil {
		t.Fatal(err)
	}
	f.git = &executionGit{
		remove: func() error { return os.Remove(f.target) },
		list:   func() ([]gitcmd.Worktree, error) { return nil, nil },
	}
	f.executor = Executor{
		Git:        f.git,
		Filesystem: Filesystem{Lstat: os.Lstat, EvalSymlinks: filepath.EvalSymlinks, SameFile: os.SameFile},
		ReadDir:    os.ReadDir,
		Remove: func(path string) error {
			f.cleaned = append(f.cleaned, path)
			return os.Remove(path)
		},
	}
	f.boundary, err = CaptureNode(f.executor.Filesystem, root, base, Directory, true)
	if err != nil {
		t.Fatal(err)
	}
	f.parents, err = CaptureCleanupChain(f.executor.Filesystem, base, filepath.Dir(f.target))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *executionFixture) execute(ctx context.Context) Result {
	return f.executor.RemoveLinked(ctx, "selected repository", gitcmd.WorktreeRemoveOptions{Path: f.target, Force: true}, f.boundary, f.parents)
}

type executionGit struct {
	remove      func() error
	list        func() ([]gitcmd.Worktree, error)
	removeCalls int
	listCalls   int
	repository  string
	options     gitcmd.WorktreeRemoveOptions
}

func (git *executionGit) WorktreeRemove(_ context.Context, repository string, options gitcmd.WorktreeRemoveOptions) error {
	git.removeCalls++
	git.repository, git.options = repository, options
	return git.remove()
}

func (git *executionGit) WorktreeList(context.Context, string) ([]gitcmd.Worktree, error) {
	git.listCalls++
	return git.list()
}
