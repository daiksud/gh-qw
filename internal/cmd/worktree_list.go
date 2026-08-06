package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/repospec"
	rootpkg "github.com/daiksud/gh-qw/internal/root"
	"github.com/spf13/cobra"
)

// WorktreeListDependencies supplies the external operations used by
// NewWorktreeListCommand. Nil fields use the production implementations.
type WorktreeListDependencies struct {
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
	Enumerate func(
		context.Context,
		local.Repository,
		string,
		...local.WorktreeOptions,
	) ([]local.Worktree, error)
	Git        local.Git
	Filesystem local.FilesystemOptions
	Getwd      func() (string, error)
	Stdout     io.Writer
	Stderr     io.Writer
}

type worktreeListUsageError struct {
	message string
}

func (e *worktreeListUsageError) Error() string {
	return e.message
}

func (e *worktreeListUsageError) Is(target error) bool {
	return target == repospec.ErrUsage
}

type worktreeListRecord struct {
	identity       string
	path           string
	head           string
	branch         string
	lockedReason   string
	prunableReason string
	main           bool
	detached       bool
	locked         bool
	prunable       bool
}

// NewWorktreeListCommand returns the command that lists one repository's
// registered main and linked worktrees.
func NewWorktreeListCommand(deps WorktreeListDependencies) *cobra.Command {
	worktreeListApplyDefaults(&deps)

	var (
		repositorySelector string
		verbose            bool
		porcelain          bool
		fullPath           bool
	)

	command := &cobra.Command{
		Use:           "list",
		Short:         "List registered worktrees",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			if porcelain && (verbose || fullPath) {
				conflicts := make([]string, 0, 2)
				if verbose {
					conflicts = append(conflicts, "-v/--verbose")
				}
				if fullPath {
					conflicts = append(conflicts, "--full-path")
				}
				return &worktreeListUsageError{
					message: fmt.Sprintf(
						"--porcelain cannot be combined with %s",
						strings.Join(conflicts, " or "),
					),
				}
			}

			ctx := command.Context()
			roots, err := deps.Resolver.Resolve()
			if err != nil {
				return err
			}

			discovery, discoveryErr := deps.Discover(
				ctx,
				roots.RepositoryRoots,
				local.DiscoveryOptions{
					Git:        deps.Git,
					Filesystem: deps.Filesystem,
				},
			)
			if err := worktreeListWriteWarnings(command.ErrOrStderr(), discovery.Warnings); err != nil {
				return err
			}
			if discoveryErr != nil {
				return discoveryErr
			}

			var repository local.Repository
			if command.Flags().Changed("repo") {
				repository, err = local.ResolveEarliestRepository(
					discovery.Repositories,
					repositorySelector,
				)
			} else {
				var cwd string
				cwd, err = deps.Getwd()
				if err == nil {
					var current local.Current
					current, err = deps.Current(
						ctx,
						cwd,
						roots.WorktreeRoot,
						discovery.Repositories,
						local.CurrentOptions{
							Git:        deps.Git,
							Filesystem: deps.Filesystem,
						},
					)
					repository = current.Repository
				}
				if err == nil {
					err = local.ValidateRepository(repository)
				}
			}
			if err != nil {
				return err
			}

			worktrees, err := deps.Enumerate(
				ctx,
				repository,
				roots.WorktreeRoot,
				local.WorktreeOptions{
					Lister:     deps.Git,
					Filesystem: deps.Filesystem,
				},
			)
			if err != nil {
				return err
			}
			records, err := worktreeListPrepareRecords(repository, worktrees)
			if err != nil {
				return err
			}

			var output bytes.Buffer
			if porcelain {
				worktreeListRenderPorcelain(&output, records)
			} else {
				worktreeListRenderHuman(&output, records, verbose, fullPath)
			}
			return worktreeListWriteAll(command.OutOrStdout(), output.Bytes())
		},
	}

	command.Flags().StringVarP(
		&repositorySelector,
		"repo",
		"R",
		"",
		"Select an existing repository",
	)
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show lock and prunable details")
	command.Flags().BoolVar(&porcelain, "porcelain", false, "Use the stable gh-qw format")
	command.Flags().BoolVar(&fullPath, "full-path", false, "Show absolute worktree paths")
	command.SetOut(deps.Stdout)
	command.SetErr(deps.Stderr)

	return command
}

func worktreeListApplyDefaults(deps *WorktreeListDependencies) {
	if deps.Resolver == nil {
		deps.Resolver = rootpkg.NewResolver()
	}
	if deps.Getwd == nil {
		deps.Getwd = os.Getwd
	}
	if deps.Discover == nil {
		deps.Discover = local.DiscoverRepositories
	}
	if deps.Current == nil {
		deps.Current = local.DiscoverCurrent
	}
	if deps.Enumerate == nil {
		deps.Enumerate = local.EnumerateWorktrees
	}
	if deps.Stdout == nil {
		deps.Stdout = os.Stdout
	}
	if deps.Stderr == nil {
		deps.Stderr = os.Stderr
	}
}

func worktreeListWriteWarnings(writer io.Writer, warnings []local.Warning) error {
	if len(warnings) == 0 {
		return nil
	}

	var output bytes.Buffer
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(&output, "gh-qw: warning: %v\n", warning)
	}
	return worktreeListWriteAll(writer, output.Bytes())
}

func worktreeListPrepareRecords(
	repository local.Repository,
	worktrees []local.Worktree,
) ([]worktreeListRecord, error) {
	repositoryIdentity, err := local.NormalizeIdentityForOutput(repository.Identity)
	if err != nil {
		return nil, err
	}
	if len(worktrees) == 0 {
		return nil, &local.WorktreeError{
			Kind:       local.ErrUnsafeWorktree,
			Repository: repositoryIdentity,
			Reason:     "Git reported no registered worktrees",
		}
	}

	records := make([]worktreeListRecord, 0, len(worktrees))
	identities := make(map[string]string, len(worktrees))
	mainCount := 0
	for _, worktree := range worktrees {
		if worktree.Bare {
			return nil, &local.WorktreeError{
				Kind:       local.ErrBareWorktree,
				Repository: repositoryIdentity,
				Path:       worktree.Path,
				Reason:     "cannot represent a bare worktree",
			}
		}
		if worktree.HEAD == "" {
			return nil, worktreeListUnsafeRecord(repositoryIdentity, worktree, "worktree has no HEAD")
		}
		if !filepath.IsAbs(worktree.Path) {
			return nil, worktreeListUnsafeRecord(
				repositoryIdentity,
				worktree,
				"worktree path is not absolute",
			)
		}
		if worktree.Detached == (worktree.Branch != "") {
			return nil, worktreeListUnsafeRecord(
				repositoryIdentity,
				worktree,
				"worktree must have exactly one of branch or detached state",
			)
		}
		if worktree.LockedReason != "" && !worktree.Locked {
			return nil, worktreeListUnsafeRecord(
				repositoryIdentity,
				worktree,
				"worktree has a lock reason without locked state",
			)
		}
		if worktree.PrunableReason != "" && !worktree.Prunable {
			return nil, worktreeListUnsafeRecord(
				repositoryIdentity,
				worktree,
				"worktree has a prunable reason without prunable state",
			)
		}
		if worktree.Repository.Identity != "" &&
			worktree.Repository.Identity != repositoryIdentity {
			return nil, worktreeListUnsafeRecord(
				repositoryIdentity,
				worktree,
				"worktree belongs to a different repository",
			)
		}

		identity := worktree.Identity
		if worktree.Main {
			mainCount++
			if worktree.Slot != "" || identity != repositoryIdentity {
				return nil, worktreeListUnsafeRecord(
					repositoryIdentity,
					worktree,
					"main worktree identity or slot is inconsistent",
				)
			}
		} else {
			if worktree.Slot == "" {
				return nil, worktreeListUnsafeRecord(
					repositoryIdentity,
					worktree,
					"linked worktree has no slot",
				)
			}
			if err := local.ValidateBranch(worktree.Slot); err != nil {
				return nil, worktreeListUnsafeRecord(
					repositoryIdentity,
					worktree,
					"linked worktree has an invalid slot",
				)
			}
			if identity != repositoryIdentity+"@"+worktree.Slot {
				return nil, worktreeListUnsafeRecord(
					repositoryIdentity,
					worktree,
					"linked worktree identity does not match its slot",
				)
			}
		}

		if previousPath, exists := identities[identity]; exists {
			return nil, &local.WorktreeError{
				Kind:       local.ErrWorktreeAmbiguous,
				Repository: repositoryIdentity,
				Path:       previousPath,
				OtherPath:  worktree.Path,
				Reason:     fmt.Sprintf("duplicate worktree identity %q", identity),
			}
		}
		identities[identity] = worktree.Path
		records = append(records, worktreeListRecord{
			identity:       identity,
			path:           local.NormalizePathForOutput(worktree.Path),
			head:           worktree.HEAD,
			branch:         worktree.Branch,
			lockedReason:   worktree.LockedReason,
			prunableReason: worktree.PrunableReason,
			main:           worktree.Main,
			detached:       worktree.Detached,
			locked:         worktree.Locked,
			prunable:       worktree.Prunable,
		})
	}
	if mainCount != 1 {
		return nil, &local.WorktreeError{
			Kind:       local.ErrWorktreeAmbiguous,
			Repository: repositoryIdentity,
			Reason:     fmt.Sprintf("expected one main worktree, found %d", mainCount),
		}
	}

	sort.Slice(records, func(left, right int) bool {
		if records[left].main != records[right].main {
			return records[left].main
		}
		if records[left].identity == records[right].identity {
			return records[left].path < records[right].path
		}
		return records[left].identity < records[right].identity
	})
	return records, nil
}

func worktreeListUnsafeRecord(
	repositoryIdentity string,
	worktree local.Worktree,
	reason string,
) error {
	return &local.WorktreeError{
		Kind:       local.ErrUnsafeWorktree,
		Repository: repositoryIdentity,
		Path:       worktree.Path,
		Reason:     reason,
	}
}

func worktreeListRenderHuman(
	output *bytes.Buffer,
	records []worktreeListRecord,
	verbose, fullPath bool,
) {
	for _, record := range records {
		location := record.identity
		if fullPath {
			location = record.path
		}
		state := "[detached]"
		if !record.detached {
			state = "[" + record.branch + "]"
		}
		_, _ = fmt.Fprintf(output, "%s %s %s", location, record.head, state)
		if verbose {
			worktreeListWriteHumanDiagnostic(output, "locked", record.locked, record.lockedReason)
			worktreeListWriteHumanDiagnostic(
				output,
				"prunable",
				record.prunable,
				record.prunableReason,
			)
		}
		_ = output.WriteByte('\n')
	}
}

func worktreeListWriteHumanDiagnostic(
	output *bytes.Buffer,
	name string,
	present bool,
	reason string,
) {
	if !present {
		return
	}
	_, _ = fmt.Fprintf(output, " [%s", name)
	if reason != "" {
		_, _ = fmt.Fprintf(output, ": %s", worktreeListQuoteToken(reason))
	}
	_ = output.WriteByte(']')
}

func worktreeListRenderPorcelain(output *bytes.Buffer, records []worktreeListRecord) {
	for _, record := range records {
		worktreeListWritePorcelainValue(output, "identity", record.identity)
		worktreeListWritePorcelainValue(output, "path", record.path)
		worktreeListWritePorcelainValue(output, "head", record.head)
		if record.main {
			worktreeListWritePorcelainValue(output, "kind", "main")
		} else {
			worktreeListWritePorcelainValue(output, "kind", "linked")
		}
		if record.detached {
			_, _ = output.WriteString("detached\n")
		} else {
			worktreeListWritePorcelainValue(output, "branch", record.branch)
		}
		if record.locked {
			worktreeListWritePorcelainValue(output, "locked", record.lockedReason)
		}
		if record.prunable {
			worktreeListWritePorcelainValue(output, "prunable", record.prunableReason)
		}
		_ = output.WriteByte('\n')
	}
}

func worktreeListWritePorcelainValue(output *bytes.Buffer, key, value string) {
	_, _ = fmt.Fprintf(output, "%s %s\n", key, worktreeListQuoteToken(value))
}

func worktreeListQuoteToken(value string) string {
	if worktreeListIsPrintableToken(value) {
		return value
	}

	const hexadecimal = "0123456789ABCDEF"
	var output strings.Builder
	output.Grow(len(value) + 2)
	_ = output.WriteByte('"')
	for index := 0; index < len(value); {
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 {
			valueByte := value[index]
			_, _ = output.WriteString(`\x`)
			_ = output.WriteByte(hexadecimal[valueByte>>4])
			_ = output.WriteByte(hexadecimal[valueByte&0x0f])
			index++
			continue
		}

		switch r {
		case '\\':
			_, _ = output.WriteString(`\\`)
		case '"':
			_, _ = output.WriteString(`\"`)
		case '\n':
			_, _ = output.WriteString(`\n`)
		case '\r':
			_, _ = output.WriteString(`\r`)
		case '\t':
			_, _ = output.WriteString(`\t`)
		default:
			if unicode.IsPrint(r) {
				_, _ = output.WriteString(value[index : index+size])
			} else {
				for _, valueByte := range []byte(value[index : index+size]) {
					_, _ = output.WriteString(`\x`)
					_ = output.WriteByte(hexadecimal[valueByte>>4])
					_ = output.WriteByte(hexadecimal[valueByte&0x0f])
				}
			}
		}
		index += size
	}
	_ = output.WriteByte('"')
	return output.String()
}

func worktreeListIsPrintableToken(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) ||
			unicode.IsSpace(r) ||
			r == '\\' ||
			r == '"' {
			return false
		}
	}
	return true
}

func worktreeListWriteAll(writer io.Writer, data []byte) error {
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
