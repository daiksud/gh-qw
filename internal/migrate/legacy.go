package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/daiksud/gh-qw/internal/gitcmd"
)

const legacyRootEnvironment = "GHQ_ROOT"

// ConfigGit is the Git output seam used to read legacy ghq configuration.
type ConfigGit interface {
	Output(context.Context, ...string) ([]byte, error)
}

// LegacyOptions supplies legacy-root discovery dependencies.
type LegacyOptions struct {
	LookupEnv       func(string) (string, bool)
	HomeDir         func() (string, error)
	Filesystem      Filesystem
	IsConfigMissing func(error) bool
}

// LegacyWarning describes one unusable legacy root value.
type LegacyWarning struct {
	Value string
	Err   error
}

// Error implements error.
func (w LegacyWarning) Error() string {
	return fmt.Sprintf("legacy root %q: %v", w.Value, w.Err)
}

// LegacyResult contains ordered, physical legacy roots.
type LegacyResult struct {
	Roots    []string
	Warnings []LegacyWarning
}

// DiscoverLegacyRoots implements ghq-compatible source-root discovery without
// persisting any legacy setting.
func DiscoverLegacyRoots(
	ctx context.Context,
	git ConfigGit,
	options LegacyOptions,
) (LegacyResult, error) {
	if git == nil {
		return LegacyResult{}, errors.New("discover legacy roots: nil Git runner")
	}
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	homeDir := options.HomeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	isMissing := options.IsConfigMissing
	if isMissing == nil {
		isMissing = func(err error) bool {
			exitCode, ok := gitcmd.CommandExitCode(err)
			return ok && exitCode == 1
		}
	}

	var rawRoots []string
	envRoot, _ := lookupEnv(legacyRootEnvironment)
	if envRoot != "" {
		rawRoots = filepath.SplitList(envRoot)
	} else {
		genericRoots, err := legacyGenericRoots(ctx, git, isMissing)
		if err != nil {
			return LegacyResult{}, err
		}
		rawRoots = genericRoots
		if len(rawRoots) == 0 {
			home, err := homeDir()
			if err != nil {
				return LegacyResult{}, fmt.Errorf("discover legacy roots: home directory unavailable: %w", err)
			}
			if home == "" || !filepath.IsAbs(home) {
				return LegacyResult{}, fmt.Errorf("discover legacy roots: home directory %q is not absolute", home)
			}
			rawRoots = []string{filepath.Join(home, "ghq")}
		}

		urlRoots, err := legacyURLRoots(ctx, git, isMissing)
		if err != nil {
			return LegacyResult{}, err
		}
		rawRoots = append(rawRoots, urlRoots...)
	}

	filesystem := newFilesystem(options.Filesystem)
	result := LegacyResult{}
	for _, rawRoot := range rawRoots {
		physicalRoot, err := legacyPhysicalRoot(rawRoot, homeDir, filesystem)
		if err != nil {
			result.Warnings = append(result.Warnings, LegacyWarning{Value: rawRoot, Err: err})
			continue
		}

		duplicate := false
		for _, root := range result.Roots {
			equal, compareErr := filesystem.samePhysicalPath(root, physicalRoot)
			if compareErr != nil {
				return LegacyResult{}, fmt.Errorf("deduplicate legacy roots: %w", compareErr)
			}
			if equal {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Roots = append(result.Roots, physicalRoot)
		}
	}

	if len(result.Roots) == 0 {
		if len(result.Warnings) != 0 {
			return LegacyResult{}, fmt.Errorf(
				"discover legacy roots: no usable root remains: %w",
				result.Warnings[0].Err,
			)
		}
		return LegacyResult{}, errors.New("discover legacy roots: no root was configured")
	}
	return result, nil
}

func legacyGenericRoots(
	ctx context.Context,
	git ConfigGit,
	isMissing func(error) bool,
) ([]string, error) {
	output, err := git.Output(ctx, "config", "--path", "--get-all", "ghq.root")
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read legacy ghq.root values: %w", err)
	}

	values := legacyLines(output)
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
	return values, nil
}

func legacyURLRoots(
	ctx context.Context,
	git ConfigGit,
	isMissing func(error) bool,
) ([]string, error) {
	output, err := git.Output(ctx, "config", "--path", "--get-regexp", `^ghq\..+\.root$`)
	if err != nil {
		if isMissing(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read URL-specific legacy roots: %w", err)
	}
	return legacyRegexpValues(output), nil
}

func legacyLines(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	if strings.IndexByte(string(output), 0) >= 0 {
		text := strings.TrimSuffix(string(output), "\x00")
		return strings.Split(text, "\x00")
	}
	text := strings.TrimSuffix(string(output), "\n")
	text = strings.TrimSuffix(text, "\r")
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	return lines
}

func legacyRegexpValues(output []byte) []string {
	if len(output) == 0 {
		return nil
	}

	var records []string
	if strings.IndexByte(string(output), 0) >= 0 {
		records = strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	} else {
		records = legacyLines(output)
	}

	values := make([]string, 0, len(records))
	for _, record := range records {
		if record == "" {
			continue
		}
		if newline := strings.IndexByte(record, '\n'); newline >= 0 {
			values = append(values, record[newline+1:])
			continue
		}
		if separator := strings.IndexAny(record, " \t"); separator >= 0 {
			values = append(values, strings.TrimLeft(record[separator+1:], " \t"))
		}
	}
	return values
}

func legacyPhysicalRoot(
	rawRoot string,
	homeDir func() (string, error),
	filesystem filesystem,
) (string, error) {
	if rawRoot == "" {
		return "", errors.New("path must not be empty")
	}
	if strings.TrimSpace(rawRoot) == "" {
		return "", errors.New("path must not contain only whitespace")
	}
	if strings.IndexByte(rawRoot, 0) >= 0 {
		return "", errors.New("path must not contain NUL")
	}

	expanded, err := legacyExpandHome(rawRoot, homeDir)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", errors.New("path must be absolute after Git --path and tilde expansion")
	}

	physical, err := filesystem.physicalizeAbsolute(filepath.Clean(expanded))
	if err != nil {
		return "", err
	}
	info, err := filesystem.stat(physical)
	switch {
	case err == nil && !info.IsDir():
		return "", errors.New("path exists and is not a directory")
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("inspect root: %w", err)
	}
	if err == nil {
		if _, readErr := filesystem.readDir(physical); readErr != nil {
			return "", fmt.Errorf("read root: %w", readErr)
		}
	}
	return physical, nil
}

func legacyExpandHome(rawPath string, homeDir func() (string, error)) (string, error) {
	homePrefix := strings.HasPrefix(rawPath, "~/") ||
		(filepath.Separator == '\\' && strings.HasPrefix(rawPath, `~\`))
	if rawPath != "~" && !homePrefix {
		if strings.HasPrefix(rawPath, "~") {
			return "", errors.New("only ~ and a native home-directory separator are supported")
		}
		return rawPath, nil
	}

	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("home directory unavailable: %w", err)
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory %q is not absolute", home)
	}
	if rawPath == "~" {
		return filepath.Clean(home), nil
	}
	return filepath.Join(home, filepath.FromSlash(strings.TrimLeft(rawPath[2:], `/\`))), nil
}
