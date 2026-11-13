package repospec

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/daiksud/gh-qw/internal/gitcmd"
	"github.com/daiksud/gh-qw/internal/physicalpath"
)

const defaultHost = "github.com"

// Options supplies environment-dependent repository resolution settings.
type Options struct {
	// DefaultHost selects the host for owner/repository shorthand; empty uses github.com.
	DefaultHost string
	SSH         bool
	Root        string
	WorkingDir  string
	// RejectFileScheme rejects a file:// input (local or non-local
	// authority) as a usage error. clone sets this because it delegates to
	// `gh repo clone`, which never accepts a file:// URL; other callers such
	// as list and add --move leave it unset because they support file:// input.
	RejectFileScheme bool
}

// Spec contains a resolved canonical identity, independent clone transport,
// and the literal branch requested for an initial clone.
type Spec struct {
	Identity
	CloneURL string
	Branch   string
}

type localRepositoryPath struct {
	repositoryPath
	host     string
	pathPart string
}

// Resolve fills environment-dependent identity and transport details.
// Local forms require a configured root.
// A branch suffix requires Git, but no repository lookup or network request.
func (p Input) Resolve(ctx context.Context, options Options) (Spec, error) {
	spec, err := p.resolve(ctx, options)
	if err != nil {
		var commandErr *gitcmd.CommandError
		if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Spec{}, fmt.Errorf("resolve repository %q: %w", p.raw, err)
		}
		return Spec{}, inputError(p.raw, err)
	}
	return spec, nil
}

func (p Input) resolve(ctx context.Context, options Options) (Spec, error) {
	if err := ctx.Err(); err != nil {
		return Spec{}, err
	}
	if p.raw == "" {
		return Spec{}, errors.New("repository is empty")
	}
	if options.RejectFileScheme && p.scheme == "file" {
		return Spec{}, errors.New("file:// input is not supported")
	}
	auth, repository := p.authority, p.repository
	var localPath localRepositoryPath
	if p.localPath != "" {
		var err error
		localPath, err = resolveLocalPath(ctx, p.localPath, p.scheme == "file", options)
		if err != nil {
			return Spec{}, err
		}
		auth = authority{hostname: localPath.host, urlHost: localPath.host}
		repository = localPath.repositoryPath
	} else if repository.branch != "" {
		if err := validateBranch(ctx, repository.branch); err != nil {
			return Spec{}, err
		}
	}

	if auth.hostname == "" {
		host := options.DefaultHost
		if host == "" {
			host = defaultHost
		}
		var err error
		auth, err = parseAuthority(host)
		if err != nil {
			return Spec{}, err
		}
	}

	var cloneURL string
	if p.localPath != "" && p.scheme == "file" {
		clonePath, err := cleanFileURLPath(localPath.pathPart)
		if err != nil {
			return Spec{}, err
		}
		cloneURL = (&url.URL{Scheme: "file", Host: p.authority.hostname, Path: clonePath}).String()
	} else {
		scheme, username := p.scheme, p.username
		if scheme == "" {
			scheme = "https"
		}
		if options.SSH && (scheme == "http" || scheme == "https") {
			scheme = "ssh"
			if username == "" {
				username = "git"
			}
		}
		cloneURL = buildRepositoryURL(scheme, auth.urlHost, username, repository.owner, repository.rawRepo)
	}
	return Spec{
		Identity: Identity{Host: auth.hostname, Owner: repository.owner, Repo: repository.repo},
		CloneURL: cloneURL,
		Branch:   repository.branch,
	}, nil
}

func validateBranch(ctx context.Context, branch string) error {
	if err := (*gitcmd.Runner)(nil).CheckRefFormat(ctx, branch); err != nil {
		if errors.Is(err, gitcmd.ErrInvalidRef) {
			return fmt.Errorf("invalid branch suffix: %w", err)
		}
		return fmt.Errorf("validate branch suffix %q: %w", branch, err)
	}
	return nil
}

func resolveLocalPath(ctx context.Context, rawPath string, absolute bool, options Options) (localRepositoryPath, error) {
	if options.Root == "" {
		return localRepositoryPath{}, errors.New("local repository forms require a configured root")
	}

	workingDir := options.WorkingDir
	if !absolute && workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return localRepositoryPath{}, fmt.Errorf("get working directory: %w", err)
		}
	}

	var candidates []localRepositoryPath
	var branchErr error
	for offset := 0; ; {
		relative := strings.IndexByte(rawPath[offset:], '@')
		if relative < 0 {
			break
		}
		index := offset + relative
		pathPart := rawPath[:index]
		match, err := matchLocalRepository(pathPart, workingDir, options.Root, absolute)
		if err == nil {
			branch := rawPath[index+1:]
			if err := validateBranch(ctx, branch); err != nil {
				if !errors.Is(err, gitcmd.ErrInvalidRef) {
					return localRepositoryPath{}, err
				}
				if branchErr == nil {
					branchErr = err
				}
			} else {
				match.branch = branch
				match.pathPart = pathPart
				candidates = append(candidates, match)
			}
		}
		offset = index + 1
	}

	plain, plainErr := matchLocalRepository(rawPath, workingDir, options.Root, absolute)
	if plainErr == nil {
		plain.pathPart = rawPath
		candidates = append(candidates, plain)
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		first := candidates[0]
		for _, candidate := range candidates[1:] {
			if candidate.host != first.host ||
				candidate.owner != first.owner ||
				candidate.repo != first.repo ||
				candidate.branch != first.branch ||
				candidate.pathPart != first.pathPart {
				return localRepositoryPath{}, errors.New("local repository and branch suffix are ambiguous")
			}
		}
		return first, nil
	}
	if branchErr != nil {
		return localRepositoryPath{}, branchErr
	}
	if plainErr != nil {
		return localRepositoryPath{}, plainErr
	}
	return localRepositoryPath{}, errors.New("local path does not identify a repository")
}

func matchLocalRepository(rawPath, workingDir string, configuredRoot string, absolute bool) (localRepositoryPath, error) {
	if rawPath == "" {
		return localRepositoryPath{}, errors.New("local repository path is empty")
	}
	if err := validateLocalPathSyntax(rawPath, absolute); err != nil {
		return localRepositoryPath{}, err
	}

	var candidate string
	if absolute {
		var err error
		candidate, err = fileURLToPath(rawPath)
		if err != nil {
			return localRepositoryPath{}, err
		}
		if !filepath.IsAbs(candidate) {
			return localRepositoryPath{}, errors.New("file URL path must be absolute")
		}
	} else {
		if !isRelativeForm(rawPath) {
			return localRepositoryPath{}, errors.New("local repository path must use ./ or ../")
		}
		candidate = filepath.FromSlash(rawPath)
		if filepath.IsAbs(candidate) {
			return localRepositoryPath{}, errors.New("relative repository path must not be absolute")
		}
		candidate = filepath.Join(workingDir, candidate)
	}

	candidate, err := canonicalFilesystemPath(candidate)
	if err != nil {
		return localRepositoryPath{}, fmt.Errorf("resolve local repository path: %w", err)
	}

	root, err := canonicalFilesystemPath(configuredRoot)
	if err != nil {
		return localRepositoryPath{}, fmt.Errorf("resolve repository root %q: %w", configuredRoot, err)
	}
	invalid := errors.New("local path is not a <host>/<owner>/<repo> directory under the configured root")
	relative, err := filepath.Rel(root, candidate)
	if err != nil || !physicalpath.StrictlyWithin(root, candidate) {
		return localRepositoryPath{}, invalid
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	if len(components) != 3 {
		return localRepositoryPath{}, invalid
	}
	auth, err := parseAuthority(components[0])
	if err != nil || auth.port != "" {
		return localRepositoryPath{}, invalid
	}
	if err := validatePathComponent("owner", components[1]); err != nil {
		return localRepositoryPath{}, invalid
	}
	repo, err := normalizeRepository(components[2])
	if err != nil {
		return localRepositoryPath{}, invalid
	}
	return localRepositoryPath{
		repositoryPath: repositoryPath{owner: components[1], rawRepo: components[2], repo: repo},
		host:           auth.hostname,
	}, nil
}

func validateLocalPathSyntax(path string, absolute bool) error {
	components := strings.Split(path, "/")
	if absolute {
		if len(components) < 2 || components[0] != "" {
			return errors.New("file URL path must be absolute")
		}
		components = components[1:]
		for _, component := range components {
			if component == "" {
				return errors.New("file URL path contains an empty component")
			}
			if component == "." || component == ".." {
				return fmt.Errorf("file URL path contains invalid component %q", component)
			}
		}
		return nil
	}

	seenRepositoryComponent := false
	for _, component := range components {
		if component == "" {
			return errors.New("relative repository path contains an empty component")
		}
		if component == "." || component == ".." {
			if seenRepositoryComponent {
				return fmt.Errorf("relative repository path contains invalid component %q", component)
			}
			continue
		}
		seenRepositoryComponent = true
	}
	return nil
}

func canonicalFilesystemPath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return (physicalpath.Filesystem{}).ResolveAbsolute(absolute)
}

func fileURLToPath(urlPath string) (string, error) {
	if urlPath == "" {
		return "", errors.New("file URL path is empty")
	}
	path := filepath.FromSlash(urlPath)
	if runtime.GOOS == "windows" &&
		len(path) >= 3 &&
		path[0] == filepath.Separator &&
		((path[1] >= 'a' && path[1] <= 'z') || (path[1] >= 'A' && path[1] <= 'Z')) &&
		path[2] == ':' {
		path = path[1:]
	}
	return path, nil
}

func cleanFileURLPath(urlPath string) (string, error) {
	path, err := fileURLToPath(urlPath)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", errors.New("file URL path must be absolute")
	}
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path, nil
}

func buildRepositoryURL(scheme, host, username, owner, repo string) string {
	u := &url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   "/" + owner + "/" + repo,
	}
	if username != "" {
		u.User = url.User(username)
	}
	return u.String()
}
