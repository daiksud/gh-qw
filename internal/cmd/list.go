package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/daiksud/gh-qw/internal/fzf"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

var (
	listSchemePattern    = regexp.MustCompile(`^[^:]+://`)
	listSCPLikePattern   = regexp.MustCompile(`^([^@]+@)?([^:]+):(/?.+)$`)
	listAuthorityPattern = regexp.MustCompile(`[A-Za-z0-9]\.[A-Za-z]+(?::\d{1,5})?$`)
)

// listFzfPrompt is the fixed fzf prompt used by `list --fzf`. It is a
// product default, not a user-configurable option.
const listFzfPrompt = "gh qw> "

// ListDependencies supplies the read-only seams used by the list command.
// Nil fields use production implementations and process streams.
type ListDependencies struct {
	Resolver             RootResolver
	DiscoverRepositories func(context.Context, []string) (local.DiscoveryResult, error)
	EnumerateWorktrees   func(context.Context, local.Repository, string) ([]local.Worktree, error)
	// Select picks one candidate identity from items for --fzf. Nil uses
	// an fzf.Runner writing its diagnostics to Stderr.
	Select func(ctx context.Context, items []string) (string, error)
	Stdout io.Writer
	Stderr io.Writer
}

type listEntry struct {
	host     string
	owner    string
	repo     string
	identity string
	slot     string
	path     string
}

type listUsageError struct {
	message string
}

func (err *listUsageError) Error() string {
	return err.message
}

func (err *listUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

// NewListCommand returns the command that lists discovered repositories and
// their registered worktrees.
func NewListCommand(deps ListDependencies) *cobra.Command {
	deps = listDependenciesWithDefaults(deps)

	var exact bool
	var fullPath bool
	var unique bool
	var includeWorktrees bool
	var useFzf bool

	command := &cobra.Command{
		Use:           "list [query]",
		Short:         "List local repositories",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if fullPath && unique {
				return &listUsageError{
					message: "--full-path and --unique are mutually exclusive",
				}
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			roots, err := deps.Resolver.Resolve()
			if err != nil {
				return err
			}

			discovery, discoveryErr := deps.DiscoverRepositories(
				command.Context(),
				roots.RepositoryRoots,
			)
			if err := listEmitWarnings(command.ErrOrStderr(), discovery.Warnings); err != nil {
				return fmt.Errorf("write discovery warnings: %w", err)
			}
			if discoveryErr != nil {
				return discoveryErr
			}

			repositories := listEarliestRepositories(discovery.Repositories)
			entries, err := listCollectEntries(
				command.Context(),
				repositories,
				roots.WorktreeRoot,
				includeWorktrees,
				deps.EnumerateWorktrees,
			)
			if err != nil {
				return err
			}

			query := ""
			if len(args) == 1 {
				query = listCanonicalizeQuery(args[0], roots.RepositoryRoots)
			}
			entries = listFilterEntries(entries, query, exact, includeWorktrees)

			if useFzf {
				return listRunFzf(command, entries, deps.Select)
			}

			lines, err := listOutputLines(entries, fullPath, unique)
			if err != nil {
				return err
			}
			sort.Strings(lines)

			if err := listWriteAll(command.OutOrStdout(), listRenderLines(lines)); err != nil {
				return err
			}
			return nil
		},
	}

	command.Flags().BoolVarP(&exact, "exact", "e", false, "Match exact identity suffixes")
	command.Flags().BoolVarP(&fullPath, "full-path", "p", false, "Print absolute paths")
	command.Flags().BoolVar(&unique, "unique", false, "Print shortest unique identity suffixes")
	command.Flags().BoolVar(&includeWorktrees, "worktree", false, "Include registered linked worktrees")
	command.Flags().BoolVar(
		&useFzf,
		"fzf",
		false,
		"Select one entry interactively with fzf and print its absolute path",
	)
	command.SetOut(deps.Stdout)
	command.SetErr(deps.Stderr)

	return command
}

func listDependenciesWithDefaults(deps ListDependencies) ListDependencies {
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.DiscoverRepositories == nil {
		deps.DiscoverRepositories = func(
			ctx context.Context,
			roots []string,
		) (local.DiscoveryResult, error) {
			return local.DiscoverRepositories(ctx, roots)
		}
	}
	if deps.EnumerateWorktrees == nil {
		deps.EnumerateWorktrees = func(
			ctx context.Context,
			repository local.Repository,
			worktreeRoot string,
		) ([]local.Worktree, error) {
			return local.EnumerateWorktrees(ctx, repository, worktreeRoot)
		}
	}
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
	if deps.Select == nil {
		stderr := deps.Stderr
		deps.Select = func(ctx context.Context, items []string) (string, error) {
			runner := fzf.NewRunner()
			runner.Stderr = stderr
			return runner.Select(ctx, items, fzf.Options{Prompt: listFzfPrompt})
		}
	}
	return deps
}

func listEarliestRepositories(repositories []local.Repository) []local.Repository {
	positions := make(map[string]int, len(repositories))
	unique := make([]local.Repository, 0, len(repositories))
	for _, repository := range repositories {
		position, exists := positions[repository.Identity]
		if !exists {
			positions[repository.Identity] = len(unique)
			unique = append(unique, repository)
			continue
		}
		if repository.RootIndex < unique[position].RootIndex {
			unique[position] = repository
		}
	}
	return unique
}

func listCollectEntries(
	ctx context.Context,
	repositories []local.Repository,
	worktreeRoot string,
	includeWorktrees bool,
	enumerate func(context.Context, local.Repository, string) ([]local.Worktree, error),
) ([]listEntry, error) {
	if !includeWorktrees {
		entries := make([]listEntry, 0, len(repositories))
		for _, repository := range repositories {
			entries = append(entries, listRepositoryEntry(repository, repository.Path))
		}
		return entries, nil
	}

	entries := make([]listEntry, 0, len(repositories))
	seen := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		worktrees, err := enumerate(ctx, repository, worktreeRoot)
		if err != nil {
			return nil, fmt.Errorf("enumerate worktrees for %q: %w", repository.Identity, err)
		}

		sawMain := false
		for _, worktree := range worktrees {
			entry, main, err := listWorktreeEntry(repository, worktree)
			if err != nil {
				return nil, err
			}
			sawMain = sawMain || main
			if _, exists := seen[entry.identity]; exists {
				continue
			}
			seen[entry.identity] = struct{}{}
			entries = append(entries, entry)
		}
		if !sawMain {
			return nil, fmt.Errorf(
				"enumerate worktrees for %q: main worktree is missing",
				repository.Identity,
			)
		}
	}
	return entries, nil
}

func listRepositoryEntry(repository local.Repository, path string) listEntry {
	return listEntry{
		host:     repository.Host,
		owner:    repository.Owner,
		repo:     repository.Repo,
		identity: repository.Identity,
		path:     path,
	}
}

func listWorktreeEntry(
	repository local.Repository,
	worktree local.Worktree,
) (listEntry, bool, error) {
	main := worktree.Main ||
		(worktree.Slot == "" &&
			(worktree.Identity == "" || worktree.Identity == repository.Identity))
	if main {
		if worktree.Slot != "" {
			return listEntry{}, false, fmt.Errorf(
				"enumerate worktrees for %q: main worktree has slot %q",
				repository.Identity,
				worktree.Slot,
			)
		}
		return listRepositoryEntry(repository, worktree.Path), true, nil
	}

	slot := worktree.Slot
	if slot == "" {
		prefix := repository.Identity + "@"
		if strings.HasPrefix(worktree.Identity, prefix) {
			slot = strings.TrimPrefix(worktree.Identity, prefix)
		}
	}
	if slot == "" {
		return listEntry{}, false, fmt.Errorf(
			"enumerate worktrees for %q: linked worktree has no slot",
			repository.Identity,
		)
	}
	if err := local.ValidateBranch(slot); err != nil {
		return listEntry{}, false, fmt.Errorf(
			"enumerate worktrees for %q: invalid slot %q: %w",
			repository.Identity,
			slot,
			err,
		)
	}

	entry := listRepositoryEntry(repository, worktree.Path)
	entry.slot = slot
	entry.identity += "@" + slot
	return entry, false, nil
}

func listCanonicalizeQuery(query string, roots []string) string {
	if !listSchemePattern.MatchString(query) && !listSCPLikePattern.MatchString(query) {
		return query
	}

	spec, err := repospec.Parse(query, repospec.Options{Roots: roots})
	if err != nil {
		return query
	}
	query = spec.Identity
	if spec.Branch != "" {
		query += "@" + spec.Branch
	}
	return query
}

func listFilterEntries(
	entries []listEntry,
	query string,
	exact bool,
	includeWorktrees bool,
) []listEntry {
	if query == "" {
		return entries
	}

	filtered := make([]listEntry, 0, len(entries))
	for _, entry := range entries {
		var matches bool
		if exact {
			matches = listMatchesExact(entry, query, includeWorktrees)
		} else {
			matches = listMatchesSubstring(entry, query)
		}
		if matches {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func listMatchesExact(entry listEntry, query string, includeWorktrees bool) bool {
	repositoryQuery := query
	slotQuery := ""
	hasSlot := false
	if index := strings.IndexByte(query, '@'); index >= 0 {
		repositoryQuery = query[:index]
		slotQuery = query[index+1:]
		hasSlot = true
	}

	if !listMatchesRepositorySuffix(entry, repositoryQuery) {
		return false
	}
	if !hasSlot {
		return true
	}
	return includeWorktrees && slotQuery != "" && entry.slot == slotQuery
}

func listMatchesRepositorySuffix(entry listEntry, query string) bool {
	switch query {
	case entry.repo:
		return true
	case entry.owner + "/" + entry.repo:
		return true
	case entry.host + "/" + entry.owner + "/" + entry.repo:
		return true
	default:
		return false
	}
}

func listMatchesSubstring(entry listEntry, query string) bool {
	host := ""
	needle := query
	if slash := strings.IndexByte(query, '/'); slash >= 0 {
		first := query[:slash]
		if listAuthorityPattern.MatchString(first) {
			host = first
			needle = query[slash+1:]
		}
	}
	if host != "" && entry.host != host {
		return false
	}

	nonHost := entry.owner + "/" + entry.repo
	if entry.slot != "" {
		nonHost += "@" + entry.slot
	}
	if strings.ToLower(needle) == needle {
		return strings.Contains(strings.ToLower(nonHost), needle)
	}
	return strings.Contains(nonHost, needle)
}

// listRunFzf lets a person pick one entry from entries with fzf and writes
// its absolute path to command's stdout. entries must already be filtered;
// listRunFzf sorts a copy by identity so fzf's candidate order matches the
// command's ordinary ascending output.
//
// No candidates writes nothing and succeeds (exit 0) without starting fzf.
// Canceling fzf (Esc or Ctrl-C) or fzf finding no match for the typed query
// both produce a silent, non-zero exit status (130 and 1 respectively; see
// silentStatusError) with no output and no diagnostic line, since fzf's own
// screen already communicated the outcome. Any other selection failure
// (including fzf missing from PATH) is an ordinary error reported like any
// other list failure.
func listRunFzf(
	command *cobra.Command,
	entries []listEntry,
	selectFn func(context.Context, []string) (string, error),
) error {
	if len(entries) == 0 {
		return nil
	}

	sorted := append([]listEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].identity < sorted[j].identity
	})

	items := make([]string, len(sorted))
	paths := make(map[string]string, len(sorted))
	for index, entry := range sorted {
		items[index] = entry.identity
		paths[entry.identity] = entry.path
	}

	selected, err := selectFn(command.Context(), items)
	if err != nil {
		switch {
		case fzf.IsCanceled(err):
			return newSilentStatusError(130)
		case fzf.IsNoMatch(err):
			return newSilentStatusError(1)
		default:
			return fmt.Errorf("select entry with fzf: %w", err)
		}
	}
	if selected == "" {
		return nil
	}

	path, ok := paths[selected]
	if !ok {
		return fmt.Errorf("fzf selected unknown entry %q", selected)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("list entry %q has non-absolute path %q", selected, path)
	}

	return listWriteAll(
		command.OutOrStdout(),
		listRenderLines([]string{local.NormalizePathForOutput(filepath.Clean(path))}),
	)
}

func listOutputLines(entries []listEntry, fullPath, unique bool) ([]string, error) {
	if unique {
		return listUniqueIdentities(entries), nil
	}

	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !fullPath {
			lines = append(lines, entry.identity)
			continue
		}
		if !filepath.IsAbs(entry.path) {
			return nil, fmt.Errorf(
				"list entry %q has non-absolute path %q",
				entry.identity,
				entry.path,
			)
		}
		lines = append(
			lines,
			local.NormalizePathForOutput(filepath.Clean(entry.path)),
		)
	}
	return lines, nil
}

func listUniqueIdentities(entries []listEntry) []string {
	allCandidates := make([][]string, len(entries))
	counts := make(map[string]int, len(entries)*3)
	for index, entry := range entries {
		candidates := listIdentityCandidates(entry)
		allCandidates[index] = candidates
		counted := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			if _, exists := counted[candidate]; exists {
				continue
			}
			counted[candidate] = struct{}{}
			counts[candidate]++
		}
	}

	identities := make([]string, 0, len(entries))
	for _, candidates := range allCandidates {
		selected := candidates[len(candidates)-1]
		for _, candidate := range candidates {
			if counts[candidate] == 1 {
				selected = candidate
				break
			}
		}
		identities = append(identities, selected)
	}
	return identities
}

func listIdentityCandidates(entry listEntry) []string {
	suffix := ""
	if entry.slot != "" {
		suffix = "@" + entry.slot
	}
	return []string{
		entry.repo + suffix,
		entry.owner + "/" + entry.repo + suffix,
		entry.host + "/" + entry.owner + "/" + entry.repo + suffix,
	}
}

func listEmitWarnings(writer io.Writer, warnings []local.Warning) error {
	if len(warnings) == 0 {
		return nil
	}

	var builder strings.Builder
	for _, warning := range warnings {
		fmt.Fprintf(&builder, "gh-qw: warning: %s\n", warning.Error())
	}
	return listWriteAll(writer, []byte(builder.String()))
}

func listRenderLines(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func listWriteAll(writer io.Writer, data []byte) error {
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
