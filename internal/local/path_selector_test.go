package local

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/daiksud/gh-qw/internal/root"
)

func TestDeterministicPathsAreContainedAndDoNotCreateDirectories(t *testing.T) {
	repositoryRoot := physicalTempDir(t)
	worktreeRoot := physicalTempDir(t)

	mainPath, err := MainPath(repositoryRoot, "github.com", "Acme", "Widget")
	if err != nil {
		t.Fatalf("MainPath() error = %v", err)
	}
	wantMain := filepath.Join(repositoryRoot, "github.com", "Acme", "Widget")
	if mainPath != wantMain {
		t.Fatalf("MainPath() = %q, want %q", mainPath, wantMain)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot, "github.com")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MainPath() created a directory or returned unexpected Lstat error: %v", err)
	}

	worktreePath, err := WorktreePath(
		worktreeRoot,
		"github.com",
		"Acme",
		"Widget",
		"feature/login",
	)
	if err != nil {
		t.Fatalf("WorktreePath() error = %v", err)
	}
	wantWorktree := filepath.Join(
		worktreeRoot,
		"github.com",
		"Acme",
		"Widget",
		"feature",
		"login",
	)
	if worktreePath != wantWorktree {
		t.Fatalf("WorktreePath() = %q, want %q", worktreePath, wantWorktree)
	}
	if _, err := os.Lstat(filepath.Join(worktreeRoot, "github.com")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WorktreePath() created a directory or returned unexpected Lstat error: %v", err)
	}
}

func TestDeterministicPathsRejectInvalidValuesAndSymlinkEscapes(t *testing.T) {
	rootPath := physicalTempDir(t)

	identityTests := []struct {
		host  string
		owner string
		repo  string
	}{
		{host: "GitHub.com", owner: "acme", repo: "widget"},
		{host: "github.com:443", owner: "acme", repo: "widget"},
		{host: "github.com", owner: "acme/team", repo: "widget"},
		{host: "github.com", owner: "acme", repo: "widget.git"},
		{host: "github.com", owner: "..", repo: "widget"},
	}
	for _, test := range identityTests {
		if _, err := MainPath(rootPath, test.host, test.owner, test.repo); !errors.Is(err, ErrInvalidIdentity) {
			t.Fatalf(
				"MainPath(%q, %q, %q) error = %v, want ErrInvalidIdentity",
				test.host,
				test.owner,
				test.repo,
				err,
			)
		}
	}

	branches := []string{
		"",
		"/absolute",
		"feature//login",
		"feature/../login",
		`feature\login`,
		"C:/absolute",
		"-option",
		"locked.lock",
		"HEAD",
	}
	for _, branch := range branches {
		if _, err := WorktreePath(
			rootPath,
			"github.com",
			"acme",
			"widget",
			branch,
		); !errors.Is(err, ErrInvalidBranch) {
			t.Fatalf("WorktreePath(branch %q) error = %v, want ErrInvalidBranch", branch, err)
		}
	}

	outside := physicalTempDir(t)
	link := filepath.Join(rootPath, "github.com")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	_, err := MainPath(rootPath, "github.com", "acme", "widget")
	if !errors.Is(err, root.ErrUnsafeTarget) {
		t.Fatalf("MainPath() symlink escape error = %v, want root.ErrUnsafeTarget", err)
	}
}

func TestSlotPrefixCollisionsUseComponentBoundaries(t *testing.T) {
	worktrees := []Worktree{
		{Slot: "feat/x"},
		{Slot: "release"},
	}

	for _, slot := range []string{"feat", "feat/x", "feat/x/y", "release/next"} {
		if err := ValidateSlotAvailable(worktrees, slot); !errors.Is(err, ErrSlotCollision) {
			t.Fatalf("ValidateSlotAvailable(%q) error = %v, want collision", slot, err)
		}
	}
	for _, slot := range []string{"feature", "feat-x", "releases"} {
		if err := ValidateSlotAvailable(worktrees, slot); err != nil {
			t.Fatalf("ValidateSlotAvailable(%q) error = %v", slot, err)
		}
	}
}

func TestValidateWorktreeDestinationChecksFilesystemPrefixes(t *testing.T) {
	base := physicalTempDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	for _, path := range []string{repositoryRoot, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}
	repository := testRepository(t, repositoryRoot, "github.com/acme/widget", 0)

	worktreeBase, err := WorktreeBasePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		t.Fatalf("WorktreeBasePath() error = %v", err)
	}

	descendant := filepath.Join(worktreeBase, "feat", "x")
	if err := os.MkdirAll(descendant, 0o755); err != nil {
		t.Fatalf("MkdirAll(descendant) error = %v", err)
	}
	if _, err := ValidateWorktreeDestination(
		worktreeRoot,
		repository,
		"feat",
		nil,
	); !errors.Is(err, ErrSlotCollision) {
		t.Fatalf("ValidateWorktreeDestination(parent) error = %v, want collision", err)
	}

	if err := os.RemoveAll(filepath.Join(worktreeBase, "feat")); err != nil {
		t.Fatalf("RemoveAll(feat) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktreeBase, "feat"), 0o755); err != nil {
		t.Fatalf("MkdirAll(feat) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeBase, "feat", ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(prefix .git) error = %v", err)
	}
	if _, err := ValidateWorktreeDestination(
		worktreeRoot,
		repository,
		"feat/x",
		nil,
	); !errors.Is(err, ErrSlotCollision) {
		t.Fatalf("ValidateWorktreeDestination(child) error = %v, want collision", err)
	}

	if err := os.RemoveAll(filepath.Join(worktreeBase, "feat")); err != nil {
		t.Fatalf("RemoveAll(feat) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktreeBase, "feature"), 0o755); err != nil {
		t.Fatalf("MkdirAll(feature) error = %v", err)
	}
	got, err := ValidateWorktreeDestination(
		worktreeRoot,
		repository,
		"feature/x",
		nil,
	)
	if err != nil {
		t.Fatalf("ValidateWorktreeDestination(sibling group) error = %v", err)
	}
	want := filepath.Join(worktreeBase, "feature", "x")
	if got != want {
		t.Fatalf("ValidateWorktreeDestination() = %q, want %q", got, want)
	}
	if _, err := os.Lstat(got); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ValidateWorktreeDestination() created destination or Lstat error = %v", err)
	}
}

func TestExactSelectorResolutionAndDuplicateBehavior(t *testing.T) {
	repositories := []Repository{
		{
			Identity:  "github.com/acme/widget",
			Host:      "github.com",
			Owner:     "acme",
			Repo:      "widget",
			Path:      "/root-one/github.com/acme/widget",
			Root:      "/root-one",
			RootIndex: 0,
		},
		{
			Identity:  "git.example.com/team/widget",
			Host:      "git.example.com",
			Owner:     "team",
			Repo:      "widget",
			Path:      "/root-one/git.example.com/team/widget",
			Root:      "/root-one",
			RootIndex: 0,
		},
		{
			Identity:  "github.com/acme/widget",
			Host:      "github.com",
			Owner:     "acme",
			Repo:      "widget",
			Path:      "/root-two/github.com/acme/widget",
			Root:      "/root-two",
			RootIndex: 1,
		},
	}

	if _, err := ResolveRepository(repositories, "missing"); !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("ResolveRepository(missing) error = %v, want not found", err)
	}
	if _, err := ResolveRepository(repositories, "widget"); !errors.Is(err, ErrRepositoryAmbiguous) {
		t.Fatalf("ResolveRepository(widget) error = %v, want ambiguous", err)
	}
	if _, err := ResolveRepositoryForMutation(
		repositories,
		"github.com/acme/widget",
	); !errors.Is(err, ErrRepositoryAmbiguous) {
		t.Fatalf("ResolveRepositoryForMutation(duplicate identity) error = %v, want ambiguous", err)
	}

	got, err := ResolveRepository(repositories, "team/widget")
	if err != nil {
		t.Fatalf("ResolveRepository(team/widget) error = %v", err)
	}
	if got.Identity != "git.example.com/team/widget" {
		t.Fatalf("ResolveRepository(team/widget) = %#v", got)
	}

	earliest := EarliestByIdentity(repositories)
	if len(earliest) != 2 || earliest[0].Path != repositories[0].Path {
		t.Fatalf("EarliestByIdentity() = %#v", earliest)
	}
	got, err = ResolveEarliestRepository(repositories, "github.com/acme/widget")
	if err != nil {
		t.Fatalf("ResolveEarliestRepository() error = %v", err)
	}
	if got.Path != repositories[0].Path {
		t.Fatalf("ResolveEarliestRepository() path = %q, want %q", got.Path, repositories[0].Path)
	}

	invalid := []string{
		"https://github.com/acme/widget",
		"./github.com/acme/widget",
		`github.com\acme\widget`,
		"github.com/acme/widget/extra",
		"github.com/acme/widget.git",
	}
	for _, selector := range invalid {
		if _, err := ResolveRepository(repositories, selector); !errors.Is(err, ErrInvalidSelector) {
			t.Fatalf("ResolveRepository(%q) error = %v, want invalid selector", selector, err)
		}
	}
}

func TestOutputNormalizationAndPathPrefixSafety(t *testing.T) {
	if got := NormalizePathForOutput(`C:\Users\Alice\repo`); got != "C:/Users/Alice/repo" {
		t.Fatalf("NormalizePathForOutput() = %q", got)
	}

	base := filepath.Join(string(filepath.Separator), "roots", "repo")
	if pathStrictlyWithin(base, filepath.Join(string(filepath.Separator), "roots", "repository", "child")) {
		t.Fatal("pathStrictlyWithin() accepted a raw prefix collision")
	}
	if !pathStrictlyWithin(base, filepath.Join(base, "child")) {
		t.Fatal("pathStrictlyWithin() rejected a real descendant")
	}

	if runtime.GOOS == "windows" {
		if !equalFoldPath(`C:\ROOT\Repo`, `c:\root\repo`) {
			t.Fatal("equalFoldPath() rejected a Windows case-only difference")
		}
	}
}

func TestParseIdentityRoundTrip(t *testing.T) {
	got, err := ParseIdentity("git.example.com/Acme/Widget_name.v2")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}
	want := IdentityParts{
		Identity: "git.example.com/Acme/Widget_name.v2",
		Host:     "git.example.com",
		Owner:    "Acme",
		Repo:     "Widget_name.v2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseIdentity() = %#v, want %#v", got, want)
	}
}
