package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
		context.Background(),
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
		"feature-login",
		"Widget",
	)
	if worktreePath != wantWorktree {
		t.Fatalf("WorktreePath() = %q, want %q", worktreePath, wantWorktree)
	}
	if _, err := os.Lstat(filepath.Join(worktreeRoot, "feature-login")); !errors.Is(err, os.ErrNotExist) {
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
			context.Background(),
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

func TestWorktreeSlotsFlattenSlashAndRejectNameCollisions(t *testing.T) {
	worktrees := []Worktree{{Slot: "feat"}, {Slot: "release"}}
	for _, slot := range []string{"feat", "release"} {
		if err := ValidateSlotAvailable(context.Background(), worktrees, slot); !errors.Is(err, ErrSlotCollision) {
			t.Fatalf("ValidateSlotAvailable(%q) = %v, want collision", slot, err)
		}
	}
	for _, slot := range []string{"feat/x", "release/next"} {
		if err := ValidateSlotAvailable(context.Background(), worktrees, slot); err != nil {
			t.Fatalf("ValidateSlotAvailable(%q) = %v, want valid Git branch", slot, err)
		}
	}
	for _, pair := range [][2]string{
		{"feature/login", "feature-login"},
		{"feature-login", "feature-login"},
		{"feature%2Flogin", "feature%2Flogin"},
		{"feat", "feat"},
	} {
		if got := WorktreeSlotDirectoryName(pair[0]); got != pair[1] {
			t.Fatalf("WorktreeSlotDirectoryName(%q) = %q, want %q", pair[0], got, pair[1])
		}
	}
	for _, existing := range []string{"feature/login", "feature-login"} {
		for _, requested := range []string{"feature/login", "feature-login"} {
			if err := ValidateSlotAvailable(
				context.Background(), []Worktree{{Slot: existing}}, requested,
			); !errors.Is(err, ErrSlotCollision) {
				t.Fatalf("existing %q requested %q = %v, want collision", existing, requested, err)
			}
		}
	}
}

func TestValidateWorktreeDestinationReservesGlobalSlot(t *testing.T) {
	base := physicalTempDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	for _, path := range []string{repositoryRoot, worktreeRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repository := testRepository(t, repositoryRoot, "github.com/acme/widget", 0)

	// An existing slot is taken even if the checkout belongs to another repo.
	if err := os.MkdirAll(filepath.Join(worktreeRoot, "occupied", "other-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorktreeDestination(
		context.Background(), worktreeRoot, repository, "occupied", nil,
	); !errors.Is(err, ErrSlotCollision) {
		t.Fatalf("globally occupied slot = %v, want collision", err)
	}
	if err := os.MkdirAll(filepath.Join(worktreeRoot, "feature-login", "other-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorktreeDestination(
		context.Background(), worktreeRoot, repository, "feature/login", nil,
	); !errors.Is(err, ErrSlotCollision) {
		t.Fatalf("flattened slot from another repository = %v, want collision", err)
	}
	got, err := ValidateWorktreeDestination(context.Background(), worktreeRoot, repository, "free", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(worktreeRoot, "free", "widget")
	if got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	if _, err := os.Lstat(filepath.Join(worktreeRoot, "free")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validate unexpectedly created a directory: %v", err)
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

func TestOutputNormalization(t *testing.T) {
	if got := NormalizePathForOutput(`C:\Users\Alice\repo`); got != "C:/Users/Alice/repo" {
		t.Fatalf("NormalizePathForOutput() = %q", got)
	}
}

func TestParseIdentityRoundTrip(t *testing.T) {
	got, err := ParseIdentity("git.example.com/Acme/Widget_name.v2")
	if err != nil {
		t.Fatalf("ParseIdentity() error = %v", err)
	}
	want := IdentityParts{
		Host:  "git.example.com",
		Owner: "Acme",
		Repo:  "Widget_name.v2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseIdentity() = %#v, want %#v", got, want)
	}
}

func TestBranchAndSlotValidationKeepSeparateContracts(t *testing.T) {
	if err := ValidateBranch(context.Background(), "topic./next"); err != nil {
		t.Fatalf("Git-valid branch rejected: %v", err)
	}
	if err := ValidateSlot(context.Background(), "topic./next"); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("dot-ended slot component error = %v, want portability rejection", err)
	}
	for _, value := range []string{"HEAD", "@", "@{-1}", "-topic"} {
		if err := ValidateBranch(context.Background(), value); !errors.Is(err, ErrInvalidBranch) {
			t.Fatalf("ValidateBranch(%q) error = %v", value, err)
		}
		if err := ValidateSlot(context.Background(), value); !errors.Is(err, ErrInvalidBranch) {
			t.Fatalf("ValidateSlot(%q) error = %v", value, err)
		}
	}
	for _, slot := range []string{"feature-next", "topic\u00a0name"} {
		if err := ValidateSlot(context.Background(), slot); err != nil {
			t.Fatalf("ValidateSlot(%q) error = %v", slot, err)
		}
	}
}

func TestCanonicalIdentityAndSelectorsDoNotRunExternalCommands(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	parts, err := ParseIdentity("github.com/Owner/Repository")
	if err != nil || parts.String() != "github.com/Owner/Repository" {
		t.Fatalf("ParseIdentity() = %#v, %v", parts, err)
	}
	for _, value := range []string{"Repository", "Owner/Repository", parts.String()} {
		if _, err := ParseSelector(value); err != nil {
			t.Fatalf("ParseSelector(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"Repository.git", "Owner/Repository.git", "github.com/Owner/Repository.git", "Repository@branch"} {
		if _, err := ParseSelector(value); !errors.Is(err, ErrInvalidSelector) {
			t.Fatalf("ParseSelector(%q) error = %v, want exact canonical component rejection", value, err)
		}
	}
}
