package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	runtimepkg "runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

const worktreePrunePointerLimit = 64 * 1024

// WorktreePruneGit is the Git capability required by worktree prune.
type WorktreePruneGit interface {
	local.Git
	WorktreePrune(context.Context, string, gitcmd.WorktreePruneOptions) error
}

// WorktreePruneDependencies supplies command integration and filesystem seams.
type WorktreePruneDependencies struct {
	Resolver RootResolver
	Discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	Current func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)

	Git        WorktreePruneGit
	Filesystem local.FilesystemOptions
	Getwd      func() (string, error)

	WorktreeBasePath func(string, string, string, string) (string, error)
	ReadFile         func(string) ([]byte, error)
	RemoveAll        func(string) error
	Remove           func(string) error
	ExpiryThreshold  func(context.Context, string, string) (uint64, error)

	Stdout io.Writer
	Stderr io.Writer
}

type worktreePruneRuntime struct {
	resolver RootResolver
	discover func(
		context.Context,
		[]string,
		...local.DiscoveryOptions,
	) (local.DiscoveryResult, error)
	current func(
		context.Context,
		string,
		string,
		[]local.Repository,
		...local.CurrentOptions,
	) (local.Current, error)

	git               WorktreePruneGit
	filesystem        worktreePruneFilesystem
	getwd             func() (string, error)
	worktreeBasePath  func(string, string, string, string) (string, error)
	readFile          func(string) ([]byte, error)
	removeAll         func(string) error
	remove            func(string) error
	expiryThreshold   func(context.Context, string, string) (uint64, error)
	physicalizeTarget func(string, string, rootpkg.ContainmentMode) (string, error)
	stdout            io.Writer
	stderr            io.Writer
}

type worktreePruneFilesystem struct {
	readDir      func(string) ([]os.DirEntry, error)
	lstat        func(string) (fs.FileInfo, error)
	stat         func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool
}

type worktreePruneRequest struct {
	selector    string
	selectorSet bool
	dryRun      bool
	verbose     bool
	expire      string
}

type worktreePruneUsageError struct {
	err error
}

func (err *worktreePruneUsageError) Error() string {
	if err == nil || err.err == nil {
		return repospec.ErrUsage.Error()
	}
	return err.err.Error()
}

func (err *worktreePruneUsageError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *worktreePruneUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

type worktreePruneSnapshot struct {
	existing map[string]struct{}
}

type worktreePruneRegistered struct {
	byPath     map[string][]gitcmd.Worktree
	entries    []worktreePruneRegisteredEntry
	unreliable bool
}

type worktreePruneRegisteredEntry struct {
	path     string
	worktree gitcmd.Worktree
	info     fs.FileInfo
}

type worktreePruneProof uint8

const (
	worktreePruneProofMetadata worktreePruneProof = iota + 1
	worktreePruneProofAge
)

type worktreePruneActionKind uint8

const (
	worktreePruneRemoveCandidate worktreePruneActionKind = iota + 1
	worktreePruneRemoveEmpty
)

type worktreePruneAction struct {
	kind      worktreePruneActionKind
	path      string
	target    string
	proof     worktreePruneProof
	threshold uint64
}

type worktreePrunePlanner struct {
	ctx        context.Context
	runtime    worktreePruneRuntime
	repository local.Repository
	base       string
	adminBase  string
	request    worktreePruneRequest
	snapshot   worktreePruneSnapshot
	registered worktreePruneRegistered

	thresholdLoaded bool
	threshold       uint64
	thresholdErr    error
	actions         []worktreePruneAction
	diagnostics     bytes.Buffer
}

// NewWorktreePruneCommand returns the command that prunes Git metadata and
// safely removes proven orphaned deterministic worktree slots.
func NewWorktreePruneCommand(deps WorktreePruneDependencies) *cobra.Command {
	runtime := worktreePrunePrepareRuntime(deps)

	var (
		selector string
		dryRun   bool
		verbose  bool
		expire   string
	)

	command := &cobra.Command{
		Use:           "prune [-R|--repo selector] [-n|--dry-run] [-v|--verbose] [--expire value]",
		Short:         "Prune stale worktrees",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(command *cobra.Command, args []string) error {
			if err := cobra.NoArgs(command, args); err != nil {
				return &worktreePruneUsageError{err: err}
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return worktreePruneRun(command.Context(), runtime, worktreePruneRequest{
				selector:    selector,
				selectorSet: command.Flags().Changed("repo"),
				dryRun:      dryRun,
				verbose:     verbose,
				expire:      expire,
			})
		},
	}

	flags := command.Flags()
	flags.StringVarP(&selector, "repo", "R", "", "Select an existing repository")
	flags.BoolVarP(&dryRun, "dry-run", "n", false, "Report without removing")
	flags.BoolVarP(&verbose, "verbose", "v", false, "Report pruning decisions")
	flags.StringVar(&expire, "expire", "", "Expire worktrees older than this value")
	command.SetOut(runtime.stdout)
	command.SetErr(runtime.stderr)

	return command
}

func worktreePrunePrepareRuntime(deps WorktreePruneDependencies) worktreePruneRuntime {
	stdout := deps.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := deps.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.Discover == nil {
		deps.Discover = local.DiscoverRepositories
	}
	if deps.Current == nil {
		deps.Current = local.DiscoverCurrent
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.WorktreeBasePath == nil {
		deps.WorktreeBasePath = local.WorktreeBasePath
	}
	if deps.ReadFile == nil {
		deps.ReadFile = os.ReadFile
	}
	if deps.RemoveAll == nil {
		deps.RemoveAll = os.RemoveAll
	}
	if deps.Remove == nil {
		deps.Remove = os.Remove
	}

	filesystem := worktreePruneFilesystem{
		readDir:      deps.Filesystem.ReadDir,
		lstat:        deps.Filesystem.Lstat,
		stat:         deps.Filesystem.Stat,
		evalSymlinks: deps.Filesystem.EvalSymlinks,
		sameFile:     deps.Filesystem.SameFile,
	}
	if filesystem.readDir == nil {
		filesystem.readDir = os.ReadDir
	}
	if filesystem.lstat == nil {
		filesystem.lstat = os.Lstat
	}
	if filesystem.stat == nil {
		filesystem.stat = os.Stat
	}
	if filesystem.evalSymlinks == nil {
		filesystem.evalSymlinks = filepath.EvalSymlinks
	}
	if filesystem.sameFile == nil {
		filesystem.sameFile = os.SameFile
	}

	safetyResolver := rootpkg.NewResolverWithOptions(rootpkg.Options{
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	})

	git := deps.Git
	if git == nil {
		git = &gitcmd.Runner{
			Executable: "git",
			Stdout:     io.Discard,
			Stderr:     stderr,
		}
	}
	if deps.ExpiryThreshold == nil {
		deps.ExpiryThreshold = func(
			ctx context.Context,
			repositoryPath, expire string,
		) (uint64, error) {
			return worktreePruneResolveExpiry(ctx, git, repositoryPath, expire)
		}
	}

	return worktreePruneRuntime{
		resolver:          deps.Resolver,
		discover:          deps.Discover,
		current:           deps.Current,
		git:               git,
		filesystem:        filesystem,
		getwd:             deps.Getwd,
		worktreeBasePath:  deps.WorktreeBasePath,
		readFile:          deps.ReadFile,
		removeAll:         deps.RemoveAll,
		remove:            deps.Remove,
		expiryThreshold:   deps.ExpiryThreshold,
		physicalizeTarget: safetyResolver.PhysicalizeTarget,
		stdout:            stdout,
		stderr:            stderr,
	}
}

func worktreePruneRun(
	ctx context.Context,
	runtime worktreePruneRuntime,
	request worktreePruneRequest,
) error {
	roots, err := runtime.resolver.Resolve()
	if err != nil {
		return fmt.Errorf("resolve roots: %w", err)
	}

	discovery, discoveryErr := runtime.discover(
		ctx,
		roots.RepositoryRoots,
		local.DiscoveryOptions{
			Git:        runtime.git,
			Filesystem: worktreePruneLocalFilesystem(runtime.filesystem),
		},
	)
	warningErr := worktreePruneWriteDiscoveryWarnings(runtime.stderr, discovery.Warnings)
	if discoveryErr != nil || warningErr != nil {
		return errors.Join(
			worktreePruneWrapError("discover repositories", discoveryErr),
			worktreePruneWrapError("write discovery warnings", warningErr),
		)
	}

	repository, err := worktreePruneSelectRepository(
		ctx,
		runtime,
		roots.WorktreeRoot,
		discovery.Repositories,
		request.selector,
		request.selectorSet,
	)
	if err != nil {
		return err
	}

	base, baseWarning := worktreePrunePrepareBase(runtime, roots.WorktreeRoot, repository)
	adminBase, adminWarning := worktreePrunePrepareAdminBase(runtime, repository)
	snapshot := worktreePruneSnapshot{existing: make(map[string]struct{})}
	var setupDiagnostics bytes.Buffer
	if baseWarning != "" {
		worktreePruneWarning(&setupDiagnostics, baseWarning)
	}
	if adminWarning != "" {
		worktreePruneWarning(&setupDiagnostics, adminWarning)
	}
	if !request.dryRun && adminWarning == "" {
		var snapshotWarning string
		snapshot, snapshotWarning = worktreePruneSnapshotAdmin(runtime, adminBase)
		if snapshotWarning != "" {
			worktreePruneWarning(&setupDiagnostics, snapshotWarning)
		}
	}

	if err := runtime.git.WorktreePrune(ctx, repository.Path, gitcmd.WorktreePruneOptions{
		DryRun:  request.dryRun,
		Verbose: request.verbose,
		Expire:  request.expire,
	}); err != nil {
		return fmt.Errorf("prune Git worktrees for %q: %w", repository.Identity, err)
	}

	if err := worktreePruneWriteAll(runtime.stderr, setupDiagnostics.Bytes()); err != nil {
		return fmt.Errorf("write prune warnings: %w", err)
	}
	if baseWarning != "" || adminWarning != "" {
		return nil
	}

	worktrees, err := runtime.git.WorktreeList(ctx, repository.Path)
	if err != nil {
		return fmt.Errorf("list worktrees after pruning %q: %w", repository.Identity, err)
	}
	registered, registeredDiagnostics := worktreePruneIndexRegistered(runtime, worktrees)

	planner := &worktreePrunePlanner{
		ctx:        ctx,
		runtime:    runtime,
		repository: repository,
		base:       base,
		adminBase:  adminBase,
		request:    request,
		snapshot:   snapshot,
		registered: registered,
	}
	planner.diagnostics.Write(registeredDiagnostics)
	if _, err := planner.scanDirectory(base, true); err != nil {
		return err
	}

	if err := worktreePruneWriteAll(runtime.stderr, planner.diagnostics.Bytes()); err != nil {
		return fmt.Errorf("write prune diagnostics: %w", err)
	}
	if request.dryRun || len(planner.actions) == 0 {
		return nil
	}

	latest := registered
	if planner.hasCandidateAction() {
		latestWorktrees, err := runtime.git.WorktreeList(ctx, repository.Path)
		if err != nil {
			return fmt.Errorf("revalidate worktrees before cleanup for %q: %w", repository.Identity, err)
		}
		var latestDiagnostics []byte
		latest, latestDiagnostics = worktreePruneIndexRegistered(runtime, latestWorktrees)
		if err := worktreePruneWriteAll(runtime.stderr, latestDiagnostics); err != nil {
			return fmt.Errorf("write worktree revalidation warnings: %w", err)
		}
	}

	for _, action := range planner.actions {
		switch action.kind {
		case worktreePruneRemoveCandidate:
			if err := worktreePruneApplyCandidate(
				runtime,
				base,
				adminBase,
				action,
				snapshot,
				latest,
			); err != nil {
				return err
			}
		case worktreePruneRemoveEmpty:
			if err := worktreePruneApplyEmpty(runtime, base, action.path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("apply worktree prune plan: unknown action for %q", action.path)
		}
	}
	return nil
}

func worktreePruneSelectRepository(
	ctx context.Context,
	runtime worktreePruneRuntime,
	worktreeRoot string,
	repositories []local.Repository,
	selector string,
	selectorSet bool,
) (local.Repository, error) {
	if selectorSet {
		repository, err := local.ResolveRepositoryForMutation(repositories, selector)
		if err != nil {
			return local.Repository{}, &worktreePruneUsageError{
				err: fmt.Errorf("resolve repository %q: %w", selector, err),
			}
		}
		return repository, nil
	}

	cwd, err := runtime.getwd()
	if err != nil {
		return local.Repository{}, fmt.Errorf("get current directory: %w", err)
	}
	current, err := runtime.current(
		ctx,
		cwd,
		worktreeRoot,
		repositories,
		local.CurrentOptions{
			Git:        runtime.git,
			Filesystem: worktreePruneLocalFilesystem(runtime.filesystem),
		},
	)
	if err != nil {
		return local.Repository{}, &worktreePruneUsageError{
			err: fmt.Errorf(
				"resolve repository from current directory (use -R outside a discovered repository): %w",
				err,
			),
		}
	}
	if err := local.ValidateRepository(current.Repository); err != nil {
		return local.Repository{}, &worktreePruneUsageError{
			err: fmt.Errorf("validate current repository for mutation: %w", err),
		}
	}
	return current.Repository, nil
}

func worktreePrunePrepareBase(
	runtime worktreePruneRuntime,
	worktreeRoot string,
	repository local.Repository,
) (string, string) {
	base, err := runtime.worktreeBasePath(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	)
	if err != nil {
		return "", fmt.Sprintf(
			"leaving deterministic worktree directory for %q: cannot derive path: %v",
			repository.Identity,
			err,
		)
	}
	expected := filepath.Clean(filepath.Join(
		worktreeRoot,
		repository.Host,
		repository.Owner,
		repository.Repo,
	))
	if !worktreePruneSamePath(base, expected) {
		return "", fmt.Sprintf(
			"leaving deterministic worktree directory for %q: derived path %q does not equal %q",
			repository.Identity,
			base,
			expected,
		)
	}
	physical, err := runtime.physicalizeTarget(worktreeRoot, base, rootpkg.StrictlyUnder)
	if err != nil {
		return "", fmt.Sprintf(
			"leaving deterministic worktree directory %q: containment validation failed: %v",
			base,
			err,
		)
	}
	if !worktreePruneSamePath(physical, base) {
		return "", fmt.Sprintf(
			"leaving deterministic worktree directory %q: path contains a symbolic link",
			base,
		)
	}
	return filepath.Clean(base), ""
}

func worktreePrunePrepareAdminBase(
	runtime worktreePruneRuntime,
	repository local.Repository,
) (string, string) {
	gitDirectory := filepath.Join(repository.Path, ".git")
	info, err := runtime.filesystem.lstat(gitDirectory)
	if err != nil {
		return "", fmt.Sprintf(
			"leaving orphaned worktree directories for %q: cannot inspect main .git directory: %v",
			repository.Identity,
			err,
		)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Sprintf(
			"leaving orphaned worktree directories for %q: main .git path is not a real directory",
			repository.Identity,
		)
	}
	physicalGit, err := runtime.physicalizeTarget(
		repository.Path,
		gitDirectory,
		rootpkg.StrictlyUnder,
	)
	if err != nil || !worktreePruneSamePath(physicalGit, gitDirectory) {
		return "", fmt.Sprintf(
			"leaving orphaned worktree directories for %q: main .git containment is unsafe",
			repository.Identity,
		)
	}

	adminBase := filepath.Join(gitDirectory, "worktrees")
	physicalAdmin, err := runtime.physicalizeTarget(
		gitDirectory,
		adminBase,
		rootpkg.StrictlyUnder,
	)
	if err != nil {
		return "", fmt.Sprintf(
			"leaving orphaned worktree directories for %q: administrative path is unsafe: %v",
			repository.Identity,
			err,
		)
	}
	if !worktreePruneSamePath(physicalAdmin, adminBase) {
		return "", fmt.Sprintf(
			"leaving orphaned worktree directories for %q: administrative path contains a symbolic link",
			repository.Identity,
		)
	}
	return filepath.Clean(adminBase), ""
}

func worktreePruneSnapshotAdmin(
	runtime worktreePruneRuntime,
	adminBase string,
) (worktreePruneSnapshot, string) {
	snapshot := worktreePruneSnapshot{existing: make(map[string]struct{})}
	info, err := runtime.filesystem.lstat(adminBase)
	if errors.Is(err, fs.ErrNotExist) {
		return snapshot, ""
	}
	if err != nil {
		return snapshot, fmt.Sprintf(
			"administrative worktree state could not be recorded before Git prune: %v",
			err,
		)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return snapshot, "administrative worktree path is not a real directory"
	}

	entries, err := runtime.filesystem.readDir(adminBase)
	if err != nil {
		return snapshot, fmt.Sprintf(
			"administrative worktree state could not be read before Git prune: %v",
			err,
		)
	}
	for _, entry := range entries {
		path := filepath.Join(adminBase, entry.Name())
		info, err := runtime.filesystem.lstat(path)
		if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		backpointerInfo, err := runtime.filesystem.lstat(filepath.Join(path, "gitdir"))
		if err != nil ||
			backpointerInfo.Mode()&fs.ModeSymlink != 0 ||
			!backpointerInfo.Mode().IsRegular() {
			continue
		}
		snapshot.existing[worktreePrunePathKey(path)] = struct{}{}
	}
	return snapshot, ""
}

func worktreePruneIndexRegistered(
	runtime worktreePruneRuntime,
	worktrees []gitcmd.Worktree,
) (worktreePruneRegistered, []byte) {
	index := worktreePruneRegistered{
		byPath: make(map[string][]gitcmd.Worktree, len(worktrees)),
	}
	var diagnostics bytes.Buffer
	for _, worktree := range worktrees {
		if worktree.Path == "" || !filepath.IsAbs(worktree.Path) {
			index.unreliable = true
			worktreePruneWarning(
				&diagnostics,
				fmt.Sprintf("Git reported an unsafe worktree path %q; orphan cleanup is disabled", worktree.Path),
			)
			continue
		}
		physical, err := worktreePrunePhysicalizeAbsolute(runtime.filesystem, worktree.Path)
		if err != nil {
			index.unreliable = true
			worktreePruneWarning(
				&diagnostics,
				fmt.Sprintf(
					"Git worktree path %q could not be physicalized; orphan cleanup is disabled: %v",
					worktree.Path,
					err,
				),
			)
			continue
		}
		key := worktreePrunePathKey(physical)
		index.byPath[key] = append(index.byPath[key], worktree)
		entry := worktreePruneRegisteredEntry{
			path:     physical,
			worktree: worktree,
		}
		if info, err := runtime.filesystem.stat(physical); err == nil {
			if err := fsidentity.Prime(info, runtime.filesystem.sameFile); err != nil {
				index.unreliable = true
				worktreePruneWarning(
					&diagnostics,
					fmt.Sprintf(
						"Git worktree path %q has no stable filesystem identity; orphan cleanup is disabled: %v",
						worktree.Path,
						err,
					),
				)
			} else {
				entry.info = info
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			index.unreliable = true
			worktreePruneWarning(
				&diagnostics,
				fmt.Sprintf(
					"Git worktree path %q could not be inspected; orphan cleanup is disabled: %v",
					worktree.Path,
					err,
				),
			)
		}
		index.entries = append(index.entries, entry)
	}
	return index, diagnostics.Bytes()
}

func (planner *worktreePrunePlanner) scanDirectory(path string, base bool) (bool, error) {
	if err := planner.ctx.Err(); err != nil {
		return false, err
	}
	info, err := planner.runtime.filesystem.lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if base && planner.request.verbose {
			planner.report("worktree directory %q does not exist", path)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect worktree directory %q: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		planner.warn("leaving symbolic link %q under the worktree directory", path)
		return false, nil
	}
	if !info.IsDir() {
		planner.warn("leaving non-directory path %q at the deterministic worktree location", path)
		return false, nil
	}
	if !base {
		if err := worktreePruneValidateRealDirectory(
			planner.runtime,
			planner.base,
			path,
		); err != nil {
			planner.warn("leaving directory %q: containment validation failed: %v", path, err)
			return false, nil
		}
	}

	entries, err := planner.runtime.filesystem.readDir(path)
	if err != nil {
		planner.warn("leaving unreadable directory %q: %v", path, err)
		return false, nil
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	for _, entry := range entries {
		if !base && entry.Name() == ".git" {
			return planner.scanCandidate(path)
		}
	}

	allGone := true
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		if base && entry.Name() == ".git" {
			planner.warn("leaving unknown non-worktree .git path %q", child)
			allGone = false
			continue
		}
		childInfo, err := planner.runtime.filesystem.lstat(child)
		if err != nil {
			planner.warn("leaving path %q that could not be inspected: %v", child, err)
			allGone = false
			continue
		}
		if childInfo.Mode()&fs.ModeSymlink != 0 {
			planner.warn("leaving unknown symbolic link %q", child)
			allGone = false
			continue
		}
		if !childInfo.IsDir() {
			planner.warn("leaving unknown non-worktree file %q", child)
			allGone = false
			continue
		}
		gone, err := planner.scanDirectory(child, false)
		if err != nil {
			return false, err
		}
		if !gone {
			allGone = false
		}
	}

	if base || !allGone {
		return false, nil
	}
	planner.actions = append(planner.actions, worktreePruneAction{
		kind: worktreePruneRemoveEmpty,
		path: path,
	})
	if planner.request.dryRun {
		planner.report("would remove empty directory %q", path)
	} else if planner.request.verbose {
		planner.report("remove empty directory %q", path)
	}
	return true, nil
}

func (planner *worktreePrunePlanner) scanCandidate(path string) (bool, error) {
	target, err := worktreePruneReadCandidatePointer(
		planner.runtime,
		planner.adminBase,
		path,
	)
	if err != nil {
		planner.warn("leaving worktree candidate %q: %v", path, err)
		return false, nil
	}

	records, err := worktreePruneRecordsForCandidate(
		planner.runtime,
		planner.registered,
		path,
	)
	if err != nil {
		planner.warn("leaving worktree candidate %q: cannot compare registered paths: %v", path, err)
		return false, nil
	}
	if len(records) > 1 {
		planner.warn("leaving worktree candidate %q: Git registered the path more than once", path)
		return false, nil
	}
	if len(records) == 1 {
		record, associationErr := worktreePruneRecordForAdmin(
			planner.runtime,
			target,
			planner.registered,
		)
		if associationErr != nil {
			planner.warn(
				"leaving registered worktree candidate %q: .git association is unsafe: %v",
				path,
				associationErr,
			)
			return false, nil
		}
		equivalent, associationErr := worktreePrunePathsEquivalent(
			planner.runtime.filesystem,
			path,
			record.Path,
		)
		if associationErr != nil || !equivalent {
			if associationErr != nil {
				planner.warn(
					"leaving registered worktree candidate %q: .git association cannot be compared: %v",
					path,
					associationErr,
				)
			} else {
				planner.warn(
					"leaving registered worktree candidate %q: .git points to metadata registered for %q",
					path,
					record.Path,
				)
			}
			return false, nil
		}
		if planner.request.dryRun && records[0].Prunable {
			return planner.addCandidate(path, target, worktreePruneProofMetadata, 0), nil
		}
		if planner.request.verbose {
			planner.report("keep worktree %q: path remains registered with Git", path)
		}
		return false, nil
	}
	if planner.registered.unreliable {
		planner.warn(
			"leaving worktree candidate %q: the current Git worktree list could not be validated",
			path,
		)
		return false, nil
	}

	targetInfo, targetErr := planner.runtime.filesystem.lstat(target)
	switch {
	case targetErr == nil:
		if targetInfo.Mode()&fs.ModeSymlink != 0 || !targetInfo.IsDir() {
			planner.warn(
				"leaving worktree candidate %q: administrative target %q is not a real directory",
				path,
				target,
			)
			return false, nil
		}
		record, err := worktreePruneRecordForAdmin(
			planner.runtime,
			target,
			planner.registered,
		)
		if err != nil {
			planner.warn("leaving worktree candidate %q: %v", path, err)
			return false, nil
		}
		if planner.request.dryRun && record.Prunable {
			return planner.addCandidate(path, target, worktreePruneProofMetadata, 0), nil
		}
		if record.Prunable {
			if planner.request.verbose {
				planner.report(
					"keep worktree %q: Git has not removed its prunable metadata at the effective expiry",
					path,
				)
			}
			return false, nil
		}
		if worktreePrunePathKey(record.Path) != worktreePrunePathKey(path) {
			planner.warn(
				"leaving worktree candidate %q: .git points to metadata registered for %q",
				path,
				record.Path,
			)
		} else if planner.request.verbose {
			planner.report("keep worktree %q: administrative metadata remains", path)
		}
		return false, nil
	case targetErr != nil && !errors.Is(targetErr, fs.ErrNotExist):
		planner.warn(
			"leaving worktree candidate %q: cannot inspect administrative target %q: %v",
			path,
			target,
			targetErr,
		)
		return false, nil
	}

	if _, existed := planner.snapshot.existing[worktreePrunePathKey(target)]; existed &&
		!planner.request.dryRun {
		return planner.addCandidate(path, target, worktreePruneProofMetadata, 0), nil
	}

	threshold, err := planner.effectiveExpiry()
	if err != nil {
		planner.warn(
			"leaving already-orphaned worktree candidate %q: effective expiry cannot be determined: %v",
			path,
			err,
		)
		return false, nil
	}
	oldEnough, err := worktreePruneTreeOlderThan(
		planner.runtime,
		planner.base,
		path,
		threshold,
	)
	if err != nil {
		planner.warn(
			"leaving already-orphaned worktree candidate %q: age cannot be verified: %v",
			path,
			err,
		)
		return false, nil
	}
	if !oldEnough {
		if planner.request.verbose {
			planner.report("keep worktree %q: age does not satisfy the effective expiry", path)
		}
		return false, nil
	}
	return planner.addCandidate(path, target, worktreePruneProofAge, threshold), nil
}

func (planner *worktreePrunePlanner) addCandidate(
	path, target string,
	proof worktreePruneProof,
	threshold uint64,
) bool {
	planner.actions = append(planner.actions, worktreePruneAction{
		kind:      worktreePruneRemoveCandidate,
		path:      path,
		target:    target,
		proof:     proof,
		threshold: threshold,
	})
	if planner.request.dryRun {
		planner.report("would remove orphaned worktree %q", path)
	} else if planner.request.verbose {
		planner.report("remove orphaned worktree %q", path)
	}
	return true
}

func (planner *worktreePrunePlanner) effectiveExpiry() (uint64, error) {
	if !planner.thresholdLoaded {
		planner.thresholdLoaded = true
		planner.threshold, planner.thresholdErr = planner.runtime.expiryThreshold(
			planner.ctx,
			planner.repository.Path,
			planner.request.expire,
		)
	}
	return planner.threshold, planner.thresholdErr
}

func (planner *worktreePrunePlanner) hasCandidateAction() bool {
	for _, action := range planner.actions {
		if action.kind == worktreePruneRemoveCandidate {
			return true
		}
	}
	return false
}

func (planner *worktreePrunePlanner) warn(format string, arguments ...any) {
	worktreePruneWarning(&planner.diagnostics, fmt.Sprintf(format, arguments...))
}

func (planner *worktreePrunePlanner) report(format string, arguments ...any) {
	fmt.Fprintf(&planner.diagnostics, "gh-qw: %s\n", fmt.Sprintf(format, arguments...))
}

func worktreePruneReadCandidatePointer(
	runtime worktreePruneRuntime,
	adminBase, candidate string,
) (string, error) {
	gitFile := filepath.Join(candidate, ".git")
	info, err := runtime.filesystem.lstat(gitFile)
	if err != nil {
		return "", fmt.Errorf(".git pointer cannot be inspected: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", errors.New(".git pointer is a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New(".git is not a regular pointer file")
	}
	if info.Size() < 0 || info.Size() > worktreePrunePointerLimit {
		return "", errors.New(".git pointer has an unsafe size")
	}
	data, err := runtime.readFile(gitFile)
	if err != nil {
		return "", fmt.Errorf("read .git pointer: %w", err)
	}
	if len(data) > worktreePrunePointerLimit {
		return "", errors.New(".git pointer has an unsafe size")
	}
	targetText, err := worktreePruneParseGitPointer(data)
	if err != nil {
		return "", err
	}
	target := filepath.FromSlash(targetText)
	if !filepath.IsAbs(target) {
		target = filepath.Join(candidate, target)
	}
	target = filepath.Clean(target)
	if err := worktreePruneValidateNoSymlinkPath(runtime, adminBase, target, true); err != nil {
		return "", fmt.Errorf(".git pointer target is unsafe: %w", err)
	}
	physical, err := runtime.physicalizeTarget(adminBase, target, rootpkg.StrictlyUnder)
	if err != nil {
		return "", fmt.Errorf(".git pointer does not identify this repository: %w", err)
	}
	if !worktreePruneSamePath(physical, target) {
		return "", errors.New(".git pointer target contains a symbolic link")
	}
	return filepath.Clean(physical), nil
}

func worktreePruneParseGitPointer(data []byte) (string, error) {
	line, err := worktreePruneSingleLine(data)
	if err != nil {
		return "", fmt.Errorf("malformed .git pointer: %w", err)
	}
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", errors.New("malformed .git pointer: expected \"gitdir: \"")
	}
	target := strings.TrimPrefix(line, prefix)
	if target == "" {
		return "", errors.New("malformed .git pointer: target is empty")
	}
	if strings.IndexByte(target, 0) >= 0 {
		return "", errors.New("malformed .git pointer: target contains NUL")
	}
	return target, nil
}

func worktreePruneRecordForAdmin(
	runtime worktreePruneRuntime,
	target string,
	registered worktreePruneRegistered,
) (gitcmd.Worktree, error) {
	backpointer := filepath.Join(target, "gitdir")
	info, err := runtime.filesystem.lstat(backpointer)
	if err != nil {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q cannot be associated with Git: %w",
			target,
			err,
		)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has no real gitdir backpointer",
			target,
		)
	}
	if info.Size() < 0 || info.Size() > worktreePrunePointerLimit {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has an unsafe gitdir backpointer",
			target,
		)
	}
	data, err := runtime.readFile(backpointer)
	if err != nil {
		return gitcmd.Worktree{}, fmt.Errorf(
			"read administrative gitdir backpointer for %q: %w",
			target,
			err,
		)
	}
	if len(data) > worktreePrunePointerLimit {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has an unsafe gitdir backpointer",
			target,
		)
	}
	backpointerText, err := worktreePruneSingleLine(data)
	if err != nil || backpointerText == "" {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has a malformed gitdir backpointer",
			target,
		)
	}
	gitFile := filepath.FromSlash(backpointerText)
	if !filepath.IsAbs(gitFile) {
		gitFile = filepath.Join(target, gitFile)
	}
	gitFile, err = worktreePrunePhysicalizeAbsolute(runtime.filesystem, gitFile)
	if err != nil {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has an unsafe gitdir backpointer: %w",
			target,
			err,
		)
	}
	gitFileName := filepath.Base(gitFile)
	if gitFileName != ".git" &&
		!(runtimepkg.GOOS == "windows" && strings.EqualFold(gitFileName, ".git")) {
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q has a gitdir backpointer that is not a .git file",
			target,
		)
	}
	worktreePath := filepath.Dir(gitFile)
	records := registered.byPath[worktreePrunePathKey(worktreePath)]
	switch len(records) {
	case 0:
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q is absent from the current Git worktree list",
			target,
		)
	case 1:
		return records[0], nil
	default:
		return gitcmd.Worktree{}, fmt.Errorf(
			"administrative target %q maps to duplicate Git worktree records",
			target,
		)
	}
}

func worktreePruneApplyCandidate(
	runtime worktreePruneRuntime,
	base, adminBase string,
	action worktreePruneAction,
	snapshot worktreePruneSnapshot,
	registered worktreePruneRegistered,
) error {
	if registered.unreliable {
		return fmt.Errorf(
			"refuse to remove worktree %q: current Git worktree list is unsafe",
			action.path,
		)
	}
	records, err := worktreePruneRecordsForCandidate(runtime, registered, action.path)
	if err != nil {
		return fmt.Errorf(
			"revalidate Git registration for worktree %q: %w",
			action.path,
			err,
		)
	}
	if len(records) != 0 {
		return fmt.Errorf(
			"refuse to remove worktree %q: path became registered with Git",
			action.path,
		)
	}
	if err := worktreePruneValidateRealDirectory(runtime, base, action.path); err != nil {
		return fmt.Errorf("revalidate worktree %q before removal: %w", action.path, err)
	}
	candidateInfo, err := runtime.filesystem.lstat(action.path)
	if err != nil {
		return fmt.Errorf("capture worktree %q before removal: %w", action.path, err)
	}
	if err := fsidentity.Prime(candidateInfo, runtime.filesystem.sameFile); err != nil {
		return fmt.Errorf("capture worktree identity %q before removal: %w", action.path, err)
	}
	target, err := worktreePruneReadCandidatePointer(runtime, adminBase, action.path)
	if err != nil {
		return fmt.Errorf("revalidate worktree association for %q: %w", action.path, err)
	}
	if !worktreePruneSamePath(target, action.target) {
		return fmt.Errorf(
			"revalidate worktree association for %q: administrative target changed",
			action.path,
		)
	}
	if _, err := runtime.filesystem.lstat(target); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return fmt.Errorf(
				"refuse to remove worktree %q: administrative target %q exists",
				action.path,
				target,
			)
		}
		return fmt.Errorf(
			"revalidate administrative target %q for worktree %q: %w",
			target,
			action.path,
			err,
		)
	}

	switch action.proof {
	case worktreePruneProofMetadata:
		if _, ok := snapshot.existing[worktreePrunePathKey(target)]; !ok {
			return fmt.Errorf(
				"refuse to remove worktree %q: Git-pruned metadata transition cannot be proven",
				action.path,
			)
		}
	case worktreePruneProofAge:
		oldEnough, err := worktreePruneTreeOlderThan(
			runtime,
			base,
			action.path,
			action.threshold,
		)
		if err != nil {
			return fmt.Errorf("revalidate age of worktree %q: %w", action.path, err)
		}
		if !oldEnough {
			return fmt.Errorf(
				"refuse to remove worktree %q: its age no longer satisfies the effective expiry",
				action.path,
			)
		}
	default:
		return fmt.Errorf("refuse to remove worktree %q: pruning proof is missing", action.path)
	}

	revalidatedInfo, err := runtime.filesystem.lstat(action.path)
	if err != nil {
		return fmt.Errorf("reinspect worktree %q before removal: %w", action.path, err)
	}
	if revalidatedInfo.Mode()&fs.ModeSymlink != 0 ||
		!revalidatedInfo.IsDir() ||
		!runtime.filesystem.sameFile(candidateInfo, revalidatedInfo) {
		return fmt.Errorf("refuse to remove worktree %q: directory changed during validation", action.path)
	}
	if err := runtime.removeAll(action.path); err != nil {
		return fmt.Errorf("remove orphaned worktree %q: %w", action.path, err)
	}
	if _, err := runtime.filesystem.lstat(action.path); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("remove orphaned worktree %q: path still exists", action.path)
		}
		return fmt.Errorf("verify removal of orphaned worktree %q: %w", action.path, err)
	}
	return nil
}

func worktreePruneApplyEmpty(
	runtime worktreePruneRuntime,
	base, path string,
) error {
	if err := worktreePruneValidateRealDirectory(runtime, base, path); err != nil {
		return fmt.Errorf("revalidate empty directory %q before removal: %w", path, err)
	}
	initialInfo, err := runtime.filesystem.lstat(path)
	if err != nil {
		return fmt.Errorf("capture empty directory %q before removal: %w", path, err)
	}
	if err := fsidentity.Prime(initialInfo, runtime.filesystem.sameFile); err != nil {
		return fmt.Errorf("capture empty directory identity %q before removal: %w", path, err)
	}
	entries, err := runtime.filesystem.readDir(path)
	if err != nil {
		return fmt.Errorf("revalidate empty directory %q: %w", path, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("refuse to remove directory %q: it is no longer empty", path)
	}
	revalidatedInfo, err := runtime.filesystem.lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect empty directory %q before removal: %w", path, err)
	}
	if revalidatedInfo.Mode()&fs.ModeSymlink != 0 ||
		!revalidatedInfo.IsDir() ||
		!runtime.filesystem.sameFile(initialInfo, revalidatedInfo) {
		return fmt.Errorf("refuse to remove directory %q: it changed during validation", path)
	}
	if err := runtime.remove(path); err != nil {
		return fmt.Errorf("remove empty worktree directory %q: %w", path, err)
	}
	return nil
}

func worktreePruneValidateRealDirectory(
	runtime worktreePruneRuntime,
	base, path string,
) error {
	if !worktreePruneStrictlyWithin(base, path) {
		return fmt.Errorf("%w: %q is not strictly below %q", rootpkg.ErrUnsafeTarget, path, base)
	}
	if err := worktreePruneValidateNoSymlinkPath(runtime, base, path, false); err != nil {
		return err
	}
	physical, err := runtime.physicalizeTarget(base, path, rootpkg.StrictlyUnder)
	if err != nil {
		return err
	}
	if !worktreePruneSamePath(physical, path) {
		return errors.New("directory path contains a symbolic link")
	}
	info, err := runtime.filesystem.lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a real directory")
	}
	return nil
}

func worktreePruneValidateNoSymlinkPath(
	runtime worktreePruneRuntime,
	base, target string,
	allowMissing bool,
) error {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	if !worktreePruneStrictlyWithin(base, target) {
		return fmt.Errorf("%w: target %q is outside %q", rootpkg.ErrUnsafeTarget, target, base)
	}
	baseInfo, err := runtime.filesystem.lstat(base)
	if errors.Is(err, fs.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if baseInfo.Mode()&fs.ModeSymlink != 0 || !baseInfo.IsDir() {
		return fmt.Errorf("root path %q is not a real directory", base)
	}

	relative, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	current := base
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := runtime.filesystem.lstat(current)
		if errors.Is(err, fs.ErrNotExist) && allowMissing {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symbolic link", current)
		}
		if index != len(components)-1 && !info.IsDir() {
			return fmt.Errorf("path component %q is not a directory", current)
		}
	}
	return nil
}

func worktreePruneTreeOlderThan(
	runtime worktreePruneRuntime,
	base, path string,
	threshold uint64,
) (bool, error) {
	if threshold == 0 {
		return false, nil
	}
	if threshold > math.MaxInt64 {
		return true, nil
	}
	if err := worktreePruneValidateRealDirectory(runtime, base, path); err != nil {
		return false, err
	}

	var visit func(string) (bool, error)
	visit = func(current string) (bool, error) {
		info, err := runtime.filesystem.lstat(current)
		if err != nil {
			return false, err
		}
		if info.ModTime().Unix() > int64(threshold) {
			return false, nil
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return true, nil
		}
		entries, err := runtime.filesystem.readDir(current)
		if err != nil {
			return false, err
		}
		sort.Slice(entries, func(left, right int) bool {
			return entries[left].Name() < entries[right].Name()
		})
		for _, entry := range entries {
			child := filepath.Join(current, entry.Name())
			oldEnough, err := visit(child)
			if err != nil || !oldEnough {
				return oldEnough, err
			}
		}
		return true, nil
	}
	return visit(path)
}

func worktreePruneResolveExpiry(
	ctx context.Context,
	git local.GitOutputter,
	repositoryPath, expire string,
) (uint64, error) {
	var (
		output []byte
		err    error
	)
	if expire != "" {
		const probeKey = "gh-qw.internalPruneExpiry"
		output, err = git.OutputDir(
			ctx,
			repositoryPath,
			"-c",
			probeKey+"="+expire,
			"config",
			"--type=expiry-date",
			"--get",
			probeKey,
		)
	} else {
		output, err = git.OutputDir(
			ctx,
			repositoryPath,
			"config",
			"--type=expiry-date",
			"--default",
			"3.months.ago",
			"--get",
			"gc.worktreePruneExpire",
		)
	}
	if err != nil {
		return 0, err
	}
	line, err := worktreePruneSingleLine(output)
	if err != nil || line == "" {
		return 0, errors.New("Git returned an invalid effective worktree expiry")
	}
	threshold, err := strconv.ParseUint(line, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse Git worktree expiry %q: %w", line, err)
	}
	return threshold, nil
}

func worktreePrunePhysicalizeAbsolute(
	filesystem worktreePruneFilesystem,
	path string,
) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}
	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := filesystem.lstat(current)
		switch {
		case err == nil:
			physical, err := filesystem.evalSymlinks(current)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(physical) {
				return "", fmt.Errorf("resolved path %q is not absolute", physical)
			}
			parts := append([]string{filepath.Clean(physical)}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), nil
		case errors.Is(err, fs.ErrNotExist):
			parent := filepath.Dir(current)
			if parent == current {
				return "", err
			}
			suffix = append([]string{filepath.Base(current)}, suffix...)
			current = parent
		default:
			return "", err
		}
	}
}

func worktreePruneRecordsForCandidate(
	runtime worktreePruneRuntime,
	registered worktreePruneRegistered,
	path string,
) ([]gitcmd.Worktree, error) {
	key := worktreePrunePathKey(path)
	records := append([]gitcmd.Worktree(nil), registered.byPath[key]...)

	candidateInfo, err := runtime.filesystem.stat(path)
	if err != nil {
		return records, err
	}
	if err := fsidentity.Prime(candidateInfo, runtime.filesystem.sameFile); err != nil {
		return records, err
	}
	for _, entry := range registered.entries {
		if worktreePrunePathKey(entry.path) == key || entry.info == nil {
			continue
		}
		if runtime.filesystem.sameFile(candidateInfo, entry.info) {
			records = append(records, entry.worktree)
		}
	}
	return records, nil
}

func worktreePrunePathsEquivalent(
	filesystem worktreePruneFilesystem,
	first, second string,
) (bool, error) {
	firstPhysical, err := worktreePrunePhysicalizeAbsolute(filesystem, first)
	if err != nil {
		return false, err
	}
	secondPhysical, err := worktreePrunePhysicalizeAbsolute(filesystem, second)
	if err != nil {
		return false, err
	}
	if worktreePruneSamePath(firstPhysical, secondPhysical) {
		return true, nil
	}
	firstInfo, firstErr := filesystem.stat(firstPhysical)
	secondInfo, secondErr := filesystem.stat(secondPhysical)
	if firstErr == nil && secondErr == nil {
		return filesystem.sameFile(firstInfo, secondInfo), nil
	}
	if firstErr != nil && !errors.Is(firstErr, fs.ErrNotExist) {
		return false, firstErr
	}
	if secondErr != nil && !errors.Is(secondErr, fs.ErrNotExist) {
		return false, secondErr
	}
	return false, nil
}

func worktreePruneSingleLine(data []byte) (string, error) {
	data = bytes.TrimSuffix(data, []byte{'\n'})
	data = bytes.TrimSuffix(data, []byte{'\r'})
	if len(data) == 0 {
		return "", errors.New("value is empty")
	}
	if bytes.IndexByte(data, 0) >= 0 ||
		bytes.IndexByte(data, '\n') >= 0 ||
		bytes.IndexByte(data, '\r') >= 0 {
		return "", errors.New("value is not a single line")
	}
	return string(data), nil
}

func worktreePruneStrictlyWithin(base, candidate string) bool {
	base = filepath.Clean(base)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(base, candidate)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func worktreePruneSamePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if first == second {
		return true
	}
	return runtimepkg.GOOS == "windows" && strings.EqualFold(first, second)
}

func worktreePrunePathKey(path string) string {
	path = filepath.Clean(path)
	if runtimepkg.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func worktreePruneWarning(writer io.Writer, message string) {
	fmt.Fprintf(writer, "gh-qw: warning: %s\n", message)
}

func worktreePruneWriteDiscoveryWarnings(
	writer io.Writer,
	warnings []local.Warning,
) error {
	var output bytes.Buffer
	for _, warning := range warnings {
		worktreePruneWarning(&output, warning.Error())
	}
	return worktreePruneWriteAll(writer, output.Bytes())
}

func worktreePruneWriteAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written < 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func worktreePruneWrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func worktreePruneLocalFilesystem(
	filesystem worktreePruneFilesystem,
) local.FilesystemOptions {
	return local.FilesystemOptions{
		ReadDir:      filesystem.readDir,
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	}
}
