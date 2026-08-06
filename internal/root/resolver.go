package root

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daiksud/gh-qw/internal/config"
	"github.com/daiksud/gh-qw/internal/xdg"
)

const (
	repositoryRootEnvironment = "GHQW_ROOT"
	worktreeRootEnvironment   = "GHQW_WORKTREE_ROOT"
	herdrEnvironment          = "GHQW_HERDR"
	defaultRepositoryRoot     = "~/ghqw"
)

// ErrInvalidRoot identifies root configuration that cannot be used safely.
// Callers should treat this as a configuration or usage error.
var ErrInvalidRoot = errors.New("invalid root configuration")

// Kind identifies the resolved setting involved in a validation failure.
type Kind string

const (
	// Repository identifies a repository root.
	Repository Kind = "repository root"
	// Worktree identifies the linked-worktree root.
	Worktree Kind = "worktree root"
	// Herdr identifies the Herdr integration default (see GHQW_HERDR and
	// the configuration file's herdr key).
	Herdr Kind = "herdr setting"
)

// InvalidError describes a root value that fails path or overlap validation.
type InvalidError struct {
	kind   Kind
	path   string
	reason string
}

// Error implements error.
func (e *InvalidError) Error() string {
	if e == nil {
		return ErrInvalidRoot.Error()
	}
	if e.path == "" {
		return fmt.Sprintf("%s: %s: %s", ErrInvalidRoot, e.kind, e.reason)
	}
	return fmt.Sprintf("%s: %s %q: %s", ErrInvalidRoot, e.kind, e.path, e.reason)
}

// Is makes root validation failures discoverable as both root and
// configuration errors.
func (e *InvalidError) Is(target error) bool {
	return target == ErrInvalidRoot || target == config.ErrInvalid
}

// Kind returns the kind of root involved in the failure.
func (e *InvalidError) Kind() Kind {
	if e == nil {
		return ""
	}
	return e.kind
}

// Path returns the rejected path, when one is available.
func (e *InvalidError) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

// Reason returns the validation failure without the path.
func (e *InvalidError) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

// ConfigLoader is the configuration dependency used by Resolver.
type ConfigLoader interface {
	Load() (config.Config, error)
}

// Options supplies resolver dependencies. Nil functions use operating-system
// defaults.
type Options struct {
	ConfigLoader ConfigLoader
	LookupEnv    func(string) (string, bool)
	HomeDir      func() (string, error)
	Lstat        func(string) (fs.FileInfo, error)
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	SameFile     func(fs.FileInfo, fs.FileInfo) bool
}

// Result is the normalized, physical root configuration, plus the resolved
// Herdr integration default.
type Result struct {
	RepositoryRoots []string
	WorktreeRoot    string
	// Herdr is the resolved default for --herdr/--no-herdr on worktree
	// add, worktree remove, and rm, used only when neither flag is given:
	// GHQW_HERDR when it is set to a recognized value, otherwise the
	// configuration file's herdr key, otherwise false. See internal/herdr
	// for the integration itself.
	Herdr bool
}

// Primary returns the first repository root, or an empty string when the
// result has no repository roots.
func (r Result) Primary() string {
	if len(r.RepositoryRoots) == 0 {
		return ""
	}
	return r.RepositoryRoots[0]
}

// Resolver resolves and caches repository and worktree roots.
type Resolver struct {
	once sync.Once

	configLoader ConfigLoader
	lookupEnv    func(string) (string, bool)
	homeDir      func() (string, error)
	lstat        func(string) (fs.FileInfo, error)
	stat         func(string) (fs.FileInfo, error)
	evalSymlinks func(string) (string, error)
	sameFile     func(fs.FileInfo, fs.FileInfo) bool

	result Result
	err    error
}

// NewResolver returns a resolver backed by the standard configuration loader,
// environment, home-directory lookup, and filesystem.
func NewResolver() *Resolver {
	return NewResolverWithOptions(Options{})
}

// NewResolverWithOptions returns a resolver with injectable dependencies.
func NewResolverWithOptions(options Options) *Resolver {
	homeDir := options.HomeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	homeDir = memoizeHomeDir(homeDir)

	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	configLoader := options.ConfigLoader
	if configLoader == nil {
		configLoader = config.NewLoaderWithOptions(config.LoaderOptions{
			LookupEnv: lookupEnv,
			HomeDir:   homeDir,
		})
	}

	lstat := options.Lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	stat := options.Stat
	if stat == nil {
		stat = os.Stat
	}
	evalSymlinks := options.EvalSymlinks
	if evalSymlinks == nil {
		evalSymlinks = filepath.EvalSymlinks
	}
	sameFile := options.SameFile
	if sameFile == nil {
		sameFile = os.SameFile
	}

	return &Resolver{
		configLoader: configLoader,
		lookupEnv:    lookupEnv,
		homeDir:      homeDir,
		lstat:        lstat,
		stat:         stat,
		evalSymlinks: evalSymlinks,
		sameFile:     sameFile,
	}
}

// Resolve resolves roots at most once and returns a defensive copy.
func (r *Resolver) Resolve() (Result, error) {
	if r == nil {
		return Result{}, errors.New("resolve roots: nil resolver")
	}

	r.once.Do(func() {
		r.result, r.err = r.resolve()
	})

	return cloneResult(r.result), r.err
}

func (r *Resolver) resolve() (Result, error) {
	loader := r.configLoader
	if loader == nil {
		loader = config.NewLoader()
	}

	fileConfig, err := loader.Load()
	if err != nil {
		return Result{}, fmt.Errorf("resolve roots: load configuration: %w", err)
	}

	rawRepositoryRoots, err := r.selectRepositoryRoots(fileConfig)
	if err != nil {
		return Result{}, err
	}
	rawWorktreeRoot, err := r.selectWorktreeRoot(fileConfig)
	if err != nil {
		return Result{}, err
	}
	herdr, err := r.selectHerdr(fileConfig)
	if err != nil {
		return Result{}, err
	}

	repositoryRoots := make([]string, 0, len(rawRepositoryRoots))
	for _, rawRoot := range rawRepositoryRoots {
		physicalRoot, err := r.physicalizeConfiguredPath(Repository, rawRoot)
		if err != nil {
			return Result{}, err
		}

		duplicate, err := r.containsEquivalent(repositoryRoots, physicalRoot)
		if err != nil {
			return Result{}, fmt.Errorf("deduplicate repository roots: %w", err)
		}
		if !duplicate {
			repositoryRoots = append(repositoryRoots, physicalRoot)
		}
	}

	worktreeRoot, err := r.physicalizeConfiguredPath(Worktree, rawWorktreeRoot)
	if err != nil {
		return Result{}, err
	}

	for _, repositoryRoot := range repositoryRoots {
		relation, err := r.relationship(repositoryRoot, worktreeRoot)
		if err != nil {
			return Result{}, fmt.Errorf("validate root separation: %w", err)
		}
		if relation != relationDisjoint {
			return Result{}, newInvalidError(
				Worktree,
				worktreeRoot,
				fmt.Sprintf("must be disjoint from repository root %q", repositoryRoot),
			)
		}
	}

	return Result{
		RepositoryRoots: repositoryRoots,
		WorktreeRoot:    worktreeRoot,
		Herdr:           herdr,
	}, nil
}

func (r *Resolver) selectRepositoryRoots(fileConfig config.Config) ([]string, error) {
	lookupEnv := r.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	if value, ok := lookupEnv(repositoryRootEnvironment); ok && value != "" {
		roots := filepath.SplitList(value)
		for index, root := range roots {
			if root == "" {
				return nil, newInvalidError(
					Repository,
					"",
					fmt.Sprintf("%s contains an empty path-list entry at index %d", repositoryRootEnvironment, index),
				)
			}
		}
		if len(roots) == 0 {
			return nil, newInvalidError(
				Repository,
				"",
				fmt.Sprintf("%s does not contain a path", repositoryRootEnvironment),
			)
		}
		return roots, nil
	}

	if len(fileConfig.Roots) != 0 {
		return append([]string(nil), fileConfig.Roots...), nil
	}
	return []string{defaultRepositoryRoot}, nil
}

func (r *Resolver) selectWorktreeRoot(fileConfig config.Config) (string, error) {
	lookupEnv := r.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	if value, ok := lookupEnv(worktreeRootEnvironment); ok && value != "" {
		return value, nil
	}
	if fileConfig.WorktreeRoot != "" {
		return fileConfig.WorktreeRoot, nil
	}

	homeDir := r.homeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	base, err := xdg.BaseDir(lookupEnv, homeDir, xdg.DataHome, ".local", "share")
	if err != nil {
		return "", fmt.Errorf("resolve default worktree root: %w", err)
	}
	return filepath.Join(base, "ghqw", "worktrees"), nil
}

// selectHerdr resolves the Herdr integration default: GHQW_HERDR when set
// to a recognized boolean token, otherwise the configuration file's herdr
// key, otherwise false. Recognized tokens are matched case-insensitively:
// "1", "true", "yes", and "on" are true; "0", "false", "no", and "off" are
// false. Any other non-empty GHQW_HERDR value is a configuration error,
// discoverable as both ErrInvalidRoot and config.ErrInvalid so it maps to
// the same usage-error exit status as a malformed GHQW_ROOT or
// GHQW_WORKTREE_ROOT (see internal/cmd.ExitCode).
func (r *Resolver) selectHerdr(fileConfig config.Config) (bool, error) {
	lookupEnv := r.lookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	if value, ok := lookupEnv(herdrEnvironment); ok && value != "" {
		switch strings.ToLower(value) {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off":
			return false, nil
		default:
			return false, newInvalidError(
				Herdr,
				"",
				fmt.Sprintf(
					"%s=%q is not a recognized boolean (use 1/true/yes/on or 0/false/no/off)",
					herdrEnvironment,
					value,
				),
			)
		}
	}
	return fileConfig.Herdr, nil
}

func (r *Resolver) physicalizeConfiguredPath(kind Kind, rawPath string) (string, error) {
	path, err := r.prepareConfiguredPath(kind, rawPath)
	if err != nil {
		return "", err
	}

	physicalPath, err := r.physicalize(path, true)
	if err == nil {
		return physicalPath, nil
	}

	var shapeError *pathShapeError
	if errors.As(err, &shapeError) {
		return "", newInvalidError(kind, rawPath, shapeError.reason)
	}
	return "", fmt.Errorf("physicalize %s %q: %w", kind, rawPath, err)
}

func (r *Resolver) prepareConfiguredPath(kind Kind, rawPath string) (string, error) {
	if rawPath == "" {
		return "", newInvalidError(kind, rawPath, "path must not be empty")
	}
	if strings.TrimSpace(rawPath) == "" {
		return "", newInvalidError(kind, rawPath, "path must not contain only whitespace")
	}
	if strings.IndexByte(rawPath, 0) >= 0 {
		return "", newInvalidError(kind, rawPath, "path must not contain NUL")
	}

	expandedPath, err := r.expandHome(rawPath)
	if err != nil {
		var shapeError *pathShapeError
		if errors.As(err, &shapeError) {
			return "", newInvalidError(kind, rawPath, shapeError.reason)
		}
		return "", fmt.Errorf("expand %s %q: %w", kind, rawPath, err)
	}
	if !filepath.IsAbs(expandedPath) {
		return "", newInvalidError(kind, rawPath, "path must be absolute after tilde expansion")
	}

	return filepath.Clean(expandedPath), nil
}

func (r *Resolver) expandHome(rawPath string) (string, error) {
	var suffix string
	switch {
	case rawPath == "~":
		suffix = ""
	case strings.HasPrefix(rawPath, "~/"):
		suffix = strings.TrimLeft(rawPath[2:], "/")
	case filepath.Separator == '\\' && strings.HasPrefix(rawPath, `~\`):
		suffix = strings.TrimLeft(rawPath[2:], `\/`)
	case strings.HasPrefix(rawPath, "~"):
		return "", &pathShapeError{reason: "only ~, ~/, and the native home separator are supported; ~user is invalid"}
	default:
		return rawPath, nil
	}

	homeDir := r.homeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("home directory unavailable: %w", err)
	}
	if home == "" {
		return "", errors.New("home directory is empty")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory %q is not absolute", home)
	}
	if suffix == "" {
		return filepath.Clean(home), nil
	}
	return filepath.Join(home, filepath.FromSlash(suffix)), nil
}

func (r *Resolver) containsEquivalent(paths []string, candidate string) (bool, error) {
	for _, path := range paths {
		equal, err := r.pathsEqual(path, candidate)
		if err != nil {
			return false, err
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func cloneResult(result Result) Result {
	result.RepositoryRoots = append([]string(nil), result.RepositoryRoots...)
	return result
}

func newInvalidError(kind Kind, path, reason string) *InvalidError {
	return &InvalidError{
		kind:   kind,
		path:   path,
		reason: reason,
	}
}

func memoizeHomeDir(homeDir func() (string, error)) func() (string, error) {
	var once sync.Once
	var home string
	var err error

	return func() (string, error) {
		once.Do(func() {
			home, err = homeDir()
		})
		return home, err
	}
}
