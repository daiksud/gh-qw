package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/root"
)

// WarningKind classifies a repository entry skipped during discovery.
type WarningKind string

const (
	WarningPermission WarningKind = "permission"
	WarningUnsafe     WarningKind = "unsafe"
	WarningInspection WarningKind = "inspection"
)

// Warning describes one skipped or unreadable discovery entry.
type Warning struct {
	Kind      WarningKind
	Root      string
	RootIndex int
	Path      string
	Operation string
	Err       error
}

func (w Warning) Error() string {
	if w.Err == nil {
		return fmt.Sprintf("%s warning at %q during %s", w.Kind, w.Path, w.Operation)
	}
	return fmt.Sprintf("%s warning at %q during %s: %v", w.Kind, w.Path, w.Operation, w.Err)
}

// WarningSink receives discovery warnings as they occur.
type WarningSink func(Warning)

// DiscoveryResult preserves every discovered record, including duplicate
// canonical identities, plus structured warnings.
type DiscoveryResult struct {
	Repositories []Repository
	Warnings     []Warning
}

// DiscoveryOptions supplies read-only Git and filesystem seams.
type DiscoveryOptions struct {
	Git        GitOutputter
	Filesystem FilesystemOptions
	Warn       WarningSink
}

// DiscoverRepositories scans every root in configured order at exactly
// <host>/<owner>/<repo> depth.
func DiscoverRepositories(
	ctx context.Context,
	roots []string,
	options ...DiscoveryOptions,
) (DiscoveryResult, error) {
	if len(options) > 1 {
		return DiscoveryResult{}, errors.New("discover repositories: more than one options value")
	}
	var option DiscoveryOptions
	if len(options) == 1 {
		option = options[0]
	}

	filesystem := newFilesystem(option.Filesystem)
	git := option.Git
	if git == nil {
		git = &gitcmd.Runner{Executable: "git", Stderr: io.Discard}
	}
	physicalize := root.NewResolverWithOptions(root.Options{
		Lstat:        filesystem.lstat,
		Stat:         filesystem.stat,
		EvalSymlinks: filesystem.evalSymlinks,
		SameFile:     filesystem.sameFile,
	}).PhysicalizeTarget

	result := DiscoveryResult{}
	warn := func(warning Warning) {
		result.Warnings = append(result.Warnings, warning)
		if option.Warn != nil {
			option.Warn(warning)
		}
	}

	for rootIndex, configuredRoot := range roots {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if configuredRoot == "" || !filepath.IsAbs(configuredRoot) {
			return result, fmt.Errorf(
				"discover repositories: root %d %q must be absolute",
				rootIndex,
				configuredRoot,
			)
		}

		rootPath := filepath.Clean(configuredRoot)
		physicalRoot, err := physicalize(rootPath, rootPath, root.AllowEqual)
		if err != nil {
			warn(discoveryWarning(rootIndex, rootPath, rootPath, "validate root", err, true))
			continue
		}

		hosts, err := filesystem.readDir(physicalRoot)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			warn(discoveryWarning(rootIndex, physicalRoot, physicalRoot, "read root", err, false))
			continue
		}
		sortEntries(hosts)

		for _, hostEntry := range hosts {
			hostPath := filepath.Join(physicalRoot, hostEntry.Name())
			if !discoveryDirectory(
				filesystem,
				physicalize,
				rootIndex,
				physicalRoot,
				hostPath,
				"host",
				func() error {
					_, err := ParseIdentity(hostEntry.Name() + "/gh-qw/repository")
					return err
				},
				warn,
			) {
				continue
			}

			owners, err := filesystem.readDir(hostPath)
			if err != nil {
				warn(discoveryWarning(rootIndex, physicalRoot, hostPath, "read host", err, false))
				continue
			}
			sortEntries(owners)

			for _, ownerEntry := range owners {
				ownerPath := filepath.Join(hostPath, ownerEntry.Name())
				if !discoveryDirectory(
					filesystem,
					physicalize,
					rootIndex,
					physicalRoot,
					ownerPath,
					"owner",
					func() error {
						_, err := ParseIdentity("github.com/" + ownerEntry.Name() + "/repository")
						return err
					},
					warn,
				) {
					continue
				}

				repositories, err := filesystem.readDir(ownerPath)
				if err != nil {
					warn(discoveryWarning(rootIndex, physicalRoot, ownerPath, "read owner", err, false))
					continue
				}
				sortEntries(repositories)

				for _, repositoryEntry := range repositories {
					repositoryPath := filepath.Join(ownerPath, repositoryEntry.Name())
					identity := hostEntry.Name() + "/" + ownerEntry.Name() + "/" + repositoryEntry.Name()
					parts, identityErr := ParseIdentity(identity)
					if !discoveryDirectory(
						filesystem,
						physicalize,
						rootIndex,
						physicalRoot,
						repositoryPath,
						"repository",
						func() error { return identityErr },
						warn,
					) {
						continue
					}

					physicalRepository, err := physicalize(
						physicalRoot,
						repositoryPath,
						root.StrictlyUnder,
					)
					if err != nil {
						warn(discoveryWarning(
							rootIndex,
							physicalRoot,
							repositoryPath,
							"validate repository containment",
							err,
							true,
						))
						continue
					}

					gitPath := filepath.Join(physicalRepository, ".git")
					gitInfo, err := filesystem.lstat(gitPath)
					if err != nil {
						if !errors.Is(err, fs.ErrNotExist) {
							warn(discoveryWarning(
								rootIndex,
								physicalRoot,
								gitPath,
								"inspect .git",
								err,
								false,
							))
						}
						continue
					}
					if gitInfo.Mode()&fs.ModeSymlink != 0 {
						warn(discoveryWarning(
							rootIndex,
							physicalRoot,
							gitPath,
							"inspect .git",
							errors.New(".git is a symbolic link"),
							true,
						))
						continue
					}
					if !gitInfo.IsDir() {
						continue
					}

					physicalGit, err := physicalize(
						physicalRepository,
						gitPath,
						root.StrictlyUnder,
					)
					if err != nil {
						warn(discoveryWarning(
							rootIndex,
							physicalRoot,
							gitPath,
							"validate .git containment",
							err,
							true,
						))
						continue
					}

					topLevel, commonDir, err := gitTopLevelAndCommon(ctx, git, physicalRepository)
					if err != nil {
						if ctxErr := ctx.Err(); ctxErr != nil {
							return result, ctxErr
						}
						warn(discoveryWarning(
							rootIndex,
							physicalRoot,
							physicalRepository,
							"inspect Git repository",
							err,
							false,
						))
						continue
					}
					topMatches, topErr := filesystem.samePhysicalPath(topLevel, physicalRepository)
					commonMatches, commonErr := filesystem.samePhysicalPath(commonDir, physicalGit)
					if topErr != nil || commonErr != nil || !topMatches || !commonMatches {
						associationErr := errors.Join(topErr, commonErr)
						if associationErr == nil {
							associationErr = errors.New(
								"Git top-level or common directory does not match the candidate",
							)
						}
						warn(discoveryWarning(
							rootIndex,
							physicalRoot,
							physicalRepository,
							"validate Git association",
							associationErr,
							true,
						))
						continue
					}

					result.Repositories = append(result.Repositories, Repository{
						Identity:  parts.Identity,
						Host:      parts.Host,
						Owner:     parts.Owner,
						Repo:      parts.Repo,
						Path:      physicalRepository,
						Root:      physicalRoot,
						RootIndex: rootIndex,
					})
				}
			}
		}
	}

	return result, nil
}

// Discover is a concise alias for DiscoverRepositories.
func Discover(
	ctx context.Context,
	roots []string,
	options ...DiscoveryOptions,
) (DiscoveryResult, error) {
	return DiscoverRepositories(ctx, roots, options...)
}

func discoveryDirectory(
	filesystem filesystem,
	physicalize func(string, string, root.ContainmentMode) (string, error),
	rootIndex int,
	rootPath, path, componentKind string,
	validate func() error,
	warn WarningSink,
) bool {
	info, err := filesystem.lstat(path)
	if err != nil {
		warn(discoveryWarning(rootIndex, rootPath, path, "inspect "+componentKind, err, false))
		return false
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		targetInfo, statErr := filesystem.stat(path)
		if statErr != nil {
			warn(discoveryWarning(
				rootIndex,
				rootPath,
				path,
				"follow "+componentKind+" symbolic link",
				statErr,
				true,
			))
			return false
		}
		if !targetInfo.IsDir() {
			return false
		}
	} else if !info.IsDir() {
		return false
	}
	if _, err := physicalize(rootPath, path, root.StrictlyUnder); err != nil {
		warn(discoveryWarning(
			rootIndex,
			rootPath,
			path,
			"validate "+componentKind+" containment",
			err,
			true,
		))
		return false
	}
	if err := validate(); err != nil {
		warn(discoveryWarning(
			rootIndex,
			rootPath,
			path,
			"validate "+componentKind,
			err,
			true,
		))
		return false
	}
	return true
}

func discoveryWarning(
	rootIndex int,
	rootPath, path, operation string,
	err error,
	unsafe bool,
) Warning {
	kind := WarningInspection
	switch {
	case errors.Is(err, fs.ErrPermission):
		kind = WarningPermission
	case unsafe:
		kind = WarningUnsafe
	}
	return Warning{
		Kind:      kind,
		Root:      rootPath,
		RootIndex: rootIndex,
		Path:      path,
		Operation: operation,
		Err:       err,
	}
}

func sortEntries(entries []fs.DirEntry) {
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
}
