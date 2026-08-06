package ghcmd

import (
	"context"
	"fmt"
)

// SubmoduleMode controls whether gh recursively initializes submodules
// during `gh repo clone`.
type SubmoduleMode uint8

const (
	SubmodulesDefault SubmoduleMode = iota
	SubmodulesRecursive
	SubmodulesDisabled
)

// PartialFilter is a supported Git partial clone object filter.
type PartialFilter string

const (
	PartialFilterNone     PartialFilter = ""
	PartialFilterBlobless PartialFilter = "blob:none"
	PartialFilterTreeless PartialFilter = "tree:0"
)

// CloneOptions controls `gh repo clone`.
type CloneOptions struct {
	URL         string
	Destination string
	Shallow     bool
	Branch      string
	Submodules  SubmoduleMode
	Filter      PartialFilter
}

// RepoClone clones a repository with `gh repo clone`, using gh's own
// authentication and GitHub host resolution. It never adds an `upstream`
// remote for a fork, keeping a new clone an ordinary Git repository.
func (r *Runner) RepoClone(ctx context.Context, options CloneOptions) error {
	args, err := repoCloneArgs(options)
	if err != nil {
		return err
	}
	return r.runDir(ctx, "", args...)
}

func repoCloneArgs(options CloneOptions) ([]string, error) {
	if options.URL == "" {
		return nil, fmt.Errorf("gh repo clone: URL is required")
	}
	if err := validateSubmoduleMode(options.Submodules); err != nil {
		return nil, fmt.Errorf("gh repo clone: %w", err)
	}
	switch options.Filter {
	case PartialFilterNone, PartialFilterBlobless, PartialFilterTreeless:
	default:
		return nil, fmt.Errorf("gh repo clone: unsupported partial filter")
	}

	args := []string{"repo", "clone", options.URL}
	if options.Destination != "" {
		args = append(args, options.Destination)
	}
	args = append(args, "--no-upstream")

	var gitFlags []string
	if options.Shallow {
		gitFlags = append(gitFlags, "--depth=1")
	}
	if options.Branch != "" {
		gitFlags = append(gitFlags, "--branch", options.Branch, "--single-branch")
	}
	switch options.Submodules {
	case SubmodulesRecursive:
		gitFlags = append(gitFlags, "--recursive")
	case SubmodulesDisabled:
		gitFlags = append(gitFlags, "--no-recursive")
	}
	if options.Filter != PartialFilterNone {
		gitFlags = append(gitFlags, "--filter="+string(options.Filter))
	}
	if len(gitFlags) > 0 {
		args = append(args, "--")
		args = append(args, gitFlags...)
	}
	return args, nil
}

func validateSubmoduleMode(mode SubmoduleMode) error {
	switch mode {
	case SubmodulesDefault, SubmodulesRecursive, SubmodulesDisabled:
		return nil
	default:
		return fmt.Errorf("unsupported submodule mode")
	}
}

// SyncOptions controls `gh repo sync`.
type SyncOptions struct {
	// Source is the "<owner>/<repo>" to synchronize from. It is required so
	// synchronization always targets the repository's own remote rather than
	// a fork parent, which is `gh repo sync`'s default source.
	Source string
	// Branch selects the branch to synchronize. An empty Branch synchronizes
	// gh's resolved default branch.
	Branch string
	// Token, when non-empty, scopes the gh subprocess to authenticate as
	// that account instead of gh's own active account (see internal/ghauth),
	// by setting GH_TOKEN in its environment. It never becomes a command
	// argument.
	Token string
}

// RepoSync fast-forwards the repository checked out in dir from Source using
// `gh repo sync`, updating both the local branch and its remote-tracking
// ref.
func (r *Runner) RepoSync(ctx context.Context, dir string, options SyncOptions) error {
	args, err := repoSyncArgs(options)
	if err != nil {
		return err
	}
	runner := r
	if options.Token != "" {
		scoped := runner.WithToken(options.Token)
		runner = &scoped
	}
	return runner.runDir(ctx, dir, args...)
}

func repoSyncArgs(options SyncOptions) ([]string, error) {
	if options.Source == "" {
		return nil, fmt.Errorf("gh repo sync: source is required")
	}

	args := []string{"repo", "sync", "--source", options.Source}
	if options.Branch != "" {
		args = append(args, "--branch", options.Branch)
	}
	return args, nil
}
