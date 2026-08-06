package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

func TestEnumerateWorktreesRealGitAndCurrentDirectory(t *testing.T) {
	base := physicalTempDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	externalRoot := filepath.Join(base, "external")
	for _, path := range []string{repositoryRoot, worktreeRoot, externalRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}

	repository := testRepository(t, repositoryRoot, "github.com/acme/widget", 0)
	initTestRepository(t, repository.Path)

	managedPath, err := WorktreePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		"feature/x",
	)
	if err != nil {
		t.Fatalf("WorktreePath(feature/x) error = %v", err)
	}
	addTestWorktree(t, repository.Path, managedPath, "-b", "feature/x")
	runGit(
		t,
		repository.Path,
		"worktree", "lock", "--reason", "deployment check", managedPath,
	)

	externalAttached := filepath.Join(externalRoot, "attached")
	addTestWorktree(t, repository.Path, externalAttached, "-b", "external/topic")

	externalDetached := filepath.Join(externalRoot, "review-123")
	addTestWorktree(t, repository.Path, externalDetached, "--detach")

	prunablePath, err := WorktreePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		"stale/slot",
	)
	if err != nil {
		t.Fatalf("WorktreePath(stale/slot) error = %v", err)
	}
	addTestWorktree(t, repository.Path, prunablePath, "-b", "stale/slot")
	if err := os.RemoveAll(prunablePath); err != nil {
		t.Fatalf("RemoveAll(prunable worktree) error = %v", err)
	}

	worktrees, err := EnumerateWorktrees(context.Background(), repository, worktreeRoot)
	if err != nil {
		t.Fatalf("EnumerateWorktrees() error = %v", err)
	}
	if len(worktrees) != 5 {
		t.Fatalf("EnumerateWorktrees() returned %d records: %#v", len(worktrees), worktrees)
	}
	if !worktrees[0].Main || worktrees[0].Identity != repository.Identity {
		t.Fatalf("first worktree = %#v, want main", worktrees[0])
	}

	gotSlots := make([]string, 0, len(worktrees)-1)
	bySlot := make(map[string]Worktree)
	for _, worktree := range worktrees[1:] {
		gotSlots = append(gotSlots, worktree.Slot)
		bySlot[worktree.Slot] = worktree
		if worktree.Repository.Identity != repository.Identity {
			t.Fatalf("worktree parent = %#v", worktree.Repository)
		}
	}
	wantSlots := []string{"external/topic", "feature/x", "review-123", "stale/slot"}
	if !reflect.DeepEqual(gotSlots, wantSlots) {
		t.Fatalf("slots = %#v, want %#v", gotSlots, wantSlots)
	}
	if !bySlot["feature/x"].Locked ||
		bySlot["feature/x"].LockedReason != "deployment check" {
		t.Fatalf("managed locked record = %#v", bySlot["feature/x"])
	}
	if bySlot["review-123"].Branch != "" || !bySlot["review-123"].Detached {
		t.Fatalf("detached external record = %#v", bySlot["review-123"])
	}
	if !bySlot["stale/slot"].Prunable ||
		bySlot["stale/slot"].PrunableReason == "" {
		t.Fatalf("prunable record = %#v", bySlot["stale/slot"])
	}

	managed, err := FindRegisteredLinkedWorktree(worktrees, "feature/x")
	if err != nil {
		t.Fatalf("FindRegisteredLinkedWorktree() error = %v", err)
	}
	if err := ValidateWorktreeAssociation(
		context.Background(),
		repository,
		managed,
		worktreeRoot,
	); err != nil {
		t.Fatalf("ValidateWorktreeAssociation(managed) error = %v", err)
	}
	resolved, err := ResolveManagedWorktree(
		context.Background(),
		repository,
		worktreeRoot,
		"feature/x",
	)
	if err != nil {
		t.Fatalf("ResolveManagedWorktree() error = %v", err)
	}
	if resolved.Path != managedPath {
		t.Fatalf("ResolveManagedWorktree() path = %q, want %q", resolved.Path, managedPath)
	}

	external := bySlot["external/topic"]
	if err := ValidateWorktreeAssociation(
		context.Background(),
		repository,
		external,
		worktreeRoot,
	); !errors.Is(err, ErrUnsafeWorktree) {
		t.Fatalf("ValidateWorktreeAssociation(external) error = %v, want unsafe", err)
	}

	mainDescendant := filepath.Join(repository.Path, "nested", "main")
	linkedDescendant := filepath.Join(managedPath, "nested", "linked")
	for _, path := range []string{mainDescendant, linkedDescendant} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}

	currentMain, err := DiscoverCurrent(
		context.Background(),
		mainDescendant,
		worktreeRoot,
		[]Repository{repository},
	)
	if err != nil {
		t.Fatalf("DiscoverCurrent(main) error = %v", err)
	}
	if !currentMain.Worktree.Main {
		t.Fatalf("DiscoverCurrent(main) = %#v", currentMain)
	}

	currentLinked, err := DiscoverCurrent(
		context.Background(),
		linkedDescendant,
		worktreeRoot,
		[]Repository{repository},
	)
	if err != nil {
		t.Fatalf("DiscoverCurrent(linked) error = %v", err)
	}
	if currentLinked.Worktree.Slot != "feature/x" {
		t.Fatalf("DiscoverCurrent(linked) = %#v", currentLinked)
	}

	foundRepository, err := FindCurrentRepository(
		context.Background(),
		externalAttached,
		[]Repository{repository},
	)
	if err != nil {
		t.Fatalf("FindCurrentRepository(external linked) error = %v", err)
	}
	if foundRepository.Identity != repository.Identity {
		t.Fatalf("FindCurrentRepository(external linked) = %#v", foundRepository)
	}

	unmanaged := filepath.Join(base, "unmanaged")
	initTestRepository(t, unmanaged)
	if _, err := DiscoverCurrent(
		context.Background(),
		unmanaged,
		worktreeRoot,
		[]Repository{repository},
	); !errors.Is(err, ErrCurrentUnmanaged) {
		t.Fatalf("DiscoverCurrent(unmanaged) error = %v, want ErrCurrentUnmanaged", err)
	}
}

func TestEnumerateWorktreesRejectsUnsafeFallbacksDuplicatesAndBare(t *testing.T) {
	base := physicalTempDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	if err := os.MkdirAll(repositoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(repositoryRoot) error = %v", err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(worktreeRoot) error = %v", err)
	}
	repository := testRepository(t, repositoryRoot, "github.com/acme/widget", 0)
	if err := os.MkdirAll(repository.Path, 0o755); err != nil {
		t.Fatalf("MkdirAll(main) error = %v", err)
	}

	t.Run("duplicate external branch fallback", func(t *testing.T) {
		one := filepath.Join(base, "external-one")
		two := filepath.Join(base, "external-two")
		for _, path := range []string{one, two} {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q) error = %v", path, err)
			}
		}
		_, err := EnumerateWorktrees(
			context.Background(),
			repository,
			worktreeRoot,
			WorktreeOptions{Lister: fakeWorktreeLister{worktrees: []gitcmd.Worktree{
				{Path: repository.Path, HEAD: "main", Branch: "main"},
				{Path: one, HEAD: "one", Branch: "topic"},
				{Path: two, HEAD: "two", Branch: "topic"},
			}}},
		)
		if !errors.Is(err, ErrWorktreeAmbiguous) {
			t.Fatalf("EnumerateWorktrees() error = %v, want ambiguous", err)
		}
	})

	t.Run("unsafe external fallback", func(t *testing.T) {
		external := filepath.Join(base, "unsafe-external")
		if err := os.MkdirAll(external, 0o755); err != nil {
			t.Fatalf("MkdirAll(external) error = %v", err)
		}
		_, err := EnumerateWorktrees(
			context.Background(),
			repository,
			worktreeRoot,
			WorktreeOptions{Lister: fakeWorktreeLister{worktrees: []gitcmd.Worktree{
				{Path: repository.Path, HEAD: "main", Branch: "main"},
				{Path: external, HEAD: "external", Branch: "../topic"},
			}}},
		)
		if !errors.Is(err, ErrUnsafeWorktree) {
			t.Fatalf("EnumerateWorktrees() error = %v, want unsafe", err)
		}
	})

	t.Run("bare record", func(t *testing.T) {
		_, err := EnumerateWorktrees(
			context.Background(),
			repository,
			worktreeRoot,
			WorktreeOptions{Lister: fakeWorktreeLister{worktrees: []gitcmd.Worktree{
				{Path: repository.Path, Bare: true},
			}}},
		)
		if !errors.Is(err, ErrBareWorktree) {
			t.Fatalf("EnumerateWorktrees() error = %v, want bare rejection", err)
		}
	})

	t.Run("deterministic symlink escape", func(t *testing.T) {
		basePath, err := WorktreeBasePath(
			worktreeRoot,
			repository.Host,
			repository.Owner,
			repository.Repo,
		)
		if err != nil {
			t.Fatalf("WorktreeBasePath() error = %v", err)
		}
		outside := filepath.Join(base, "outside")
		if err := os.MkdirAll(filepath.Join(outside, "slot"), 0o755); err != nil {
			t.Fatalf("MkdirAll(outside) error = %v", err)
		}
		if err := os.MkdirAll(basePath, 0o755); err != nil {
			t.Fatalf("MkdirAll(basePath) error = %v", err)
		}
		link := filepath.Join(basePath, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}

		_, err = EnumerateWorktrees(
			context.Background(),
			repository,
			worktreeRoot,
			WorktreeOptions{Lister: fakeWorktreeLister{worktrees: []gitcmd.Worktree{
				{Path: repository.Path, HEAD: "main", Branch: "main"},
				{
					Path:   filepath.Join(link, "slot"),
					HEAD:   "linked",
					Branch: "escape/slot",
				},
			}}},
		)
		if !errors.Is(err, ErrUnsafeWorktree) {
			t.Fatalf("EnumerateWorktrees() error = %v, want symlink escape rejection", err)
		}
	})

	t.Run("linked worktree at repository container", func(t *testing.T) {
		basePath, err := WorktreeBasePath(
			worktreeRoot,
			repository.Host,
			repository.Owner,
			repository.Repo,
		)
		if err != nil {
			t.Fatalf("WorktreeBasePath() error = %v", err)
		}
		if err := os.RemoveAll(basePath); err != nil {
			t.Fatalf("RemoveAll(basePath) error = %v", err)
		}
		if err := os.MkdirAll(basePath, 0o755); err != nil {
			t.Fatalf("MkdirAll(basePath) error = %v", err)
		}
		_, err = EnumerateWorktrees(
			context.Background(),
			repository,
			worktreeRoot,
			WorktreeOptions{Lister: fakeWorktreeLister{worktrees: []gitcmd.Worktree{
				{Path: repository.Path, HEAD: "main", Branch: "main"},
				{Path: basePath, HEAD: "linked", Branch: "topic"},
			}}},
		)
		if !errors.Is(err, ErrUnsafeWorktree) {
			t.Fatalf("EnumerateWorktrees() error = %v, want container rejection", err)
		}
	})
}

func TestValidateWorktreeAssociationRejectsForeignCommonDirectory(t *testing.T) {
	base := physicalTempDir(t)
	repositoryRoot := filepath.Join(base, "repositories")
	worktreeRoot := filepath.Join(base, "worktrees")
	foreignRoot := filepath.Join(base, "foreign")
	for _, path := range []string{repositoryRoot, worktreeRoot, foreignRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}

	repository := testRepository(t, repositoryRoot, "github.com/acme/widget", 0)
	initTestRepository(t, repository.Path)
	foreignMain := filepath.Join(foreignRoot, "main")
	initTestRepository(t, foreignMain)

	foreignPath, err := WorktreePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
		"foreign",
	)
	if err != nil {
		t.Fatalf("WorktreePath(foreign) error = %v", err)
	}
	addTestWorktree(t, foreignMain, foreignPath, "-b", "foreign")

	err = ValidateWorktreeAssociation(
		context.Background(),
		repository,
		Worktree{
			Repository: repository,
			Identity:   repository.Identity + "@foreign",
			Slot:       "foreign",
			Path:       foreignPath,
			HEAD:       "unused",
			Branch:     "foreign",
		},
		worktreeRoot,
	)
	if !errors.Is(err, ErrUnsafeWorktree) {
		t.Fatalf("ValidateWorktreeAssociation(foreign) error = %v, want unsafe", err)
	}
}

func TestFindLinkedWorktreeZeroOneMany(t *testing.T) {
	records := []Worktree{
		{Main: true, Identity: "github.com/acme/widget"},
		{Slot: "feature/x", Path: "/one"},
	}
	got, err := FindLinkedWorktree(records, "feature/x")
	if err != nil || got.Path != "/one" {
		t.Fatalf("FindLinkedWorktree(one) = %#v, %v", got, err)
	}
	if _, err := FindLinkedWorktree(records, "missing"); !errors.Is(err, ErrWorktreeNotFound) {
		t.Fatalf("FindLinkedWorktree(zero) error = %v", err)
	}

	records = append(records, Worktree{Slot: "feature/x", Path: "/two"})
	if _, err := FindLinkedWorktree(records, "feature/x"); !errors.Is(err, ErrWorktreeAmbiguous) {
		t.Fatalf("FindLinkedWorktree(many) error = %v", err)
	}
}

func TestLinkedWorktreesRemainSortedByIdentity(t *testing.T) {
	records := []Worktree{
		{Identity: "github.com/acme/widget@z"},
		{Identity: "github.com/acme/widget@a"},
		{Identity: "github.com/acme/widget@m"},
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Identity < records[j].Identity
	})
	got := []string{records[0].Identity, records[1].Identity, records[2].Identity}
	want := []string{
		"github.com/acme/widget@a",
		"github.com/acme/widget@m",
		"github.com/acme/widget@z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted identities = %#v", got)
	}
}
