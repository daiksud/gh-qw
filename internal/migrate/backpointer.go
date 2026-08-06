package migrate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

type backPointerUpdate struct {
	entry       string
	expected    []byte
	replacement []byte
	mode        fs.FileMode
}

// BackPointerPlan is the preflighted linked-worktree repair plan.
type BackPointerPlan struct {
	source  string
	dest    string
	paths   []string
	updates []backPointerUpdate
}

// RepairPaths returns every linked worktree path that must be supplied to
// git worktree repair.
func (plan BackPointerPlan) RepairPaths() []string {
	return append([]string(nil), plan.paths...)
}

// PlanBackPointers reads every linked-worktree back-pointer and plans the
// rewrites needed for worktrees that move with the main repository.
func PlanBackPointers(
	source, destination string,
	options Filesystem,
) (BackPointerPlan, error) {
	filesystem := newFilesystem(options)
	plan := BackPointerPlan{
		source: filepath.Clean(source),
		dest:   filepath.Clean(destination),
	}
	if !filepath.IsAbs(plan.source) || !filepath.IsAbs(plan.dest) {
		return BackPointerPlan{}, errors.New("linked-worktree repair paths must be absolute")
	}

	worktreesDir := filepath.Join(plan.source, ".git", "worktrees")
	info, err := filesystem.lstat(worktreesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return plan, nil
		}
		return BackPointerPlan{}, fmt.Errorf("inspect linked-worktree metadata %q: %w", worktreesDir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return BackPointerPlan{}, fmt.Errorf("linked-worktree metadata %q is not a physical directory", worktreesDir)
	}

	entries, err := filesystem.readDir(worktreesDir)
	if err != nil {
		return BackPointerPlan{}, fmt.Errorf("read linked-worktree metadata %q: %w", worktreesDir, err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	seenPaths := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(worktreesDir, entry.Name())
		entryInfo, err := filesystem.lstat(entryPath)
		if err != nil {
			return BackPointerPlan{}, fmt.Errorf("inspect worktree entry %q: %w", entryPath, err)
		}
		if entryInfo.Mode()&fs.ModeSymlink != 0 {
			return BackPointerPlan{}, fmt.Errorf("worktree entry %q is a symbolic link", entryPath)
		}
		if !entryInfo.IsDir() {
			continue
		}

		gitdirFile := filepath.Join(entryPath, "gitdir")
		gitdirInfo, err := filesystem.lstat(gitdirFile)
		if err != nil {
			return BackPointerPlan{}, fmt.Errorf("inspect worktree back-pointer %q: %w", gitdirFile, err)
		}
		if gitdirInfo.Mode()&fs.ModeSymlink != 0 || !gitdirInfo.Mode().IsRegular() {
			return BackPointerPlan{}, fmt.Errorf("worktree back-pointer %q is not a regular file", gitdirFile)
		}
		content, err := filesystem.readFile(gitdirFile)
		if err != nil {
			return BackPointerPlan{}, fmt.Errorf("read worktree back-pointer %q: %w", gitdirFile, err)
		}
		rawPath, err := parseBackPointer(content)
		if err != nil {
			return BackPointerPlan{}, fmt.Errorf("parse worktree back-pointer %q: %w", gitdirFile, err)
		}

		gitdirPath := filepath.FromSlash(rawPath)
		if !filepath.IsAbs(gitdirPath) {
			gitdirPath = filepath.Join(entryPath, gitdirPath)
		}
		gitdirPath = filepath.Clean(gitdirPath)
		if !filepath.IsAbs(gitdirPath) || filepath.Base(gitdirPath) != ".git" {
			return BackPointerPlan{}, fmt.Errorf(
				"worktree back-pointer %q does not identify an absolute .git file",
				gitdirFile,
			)
		}

		lexicallyInternal := pathStrictlyWithin(plan.source, gitdirPath)
		physicalGitdir, err := filesystem.physicalizeAbsolute(gitdirPath)
		if err != nil {
			return BackPointerPlan{}, fmt.Errorf("physicalize worktree back-pointer %q: %w", gitdirFile, err)
		}
		physicallyInternal := pathStrictlyWithin(plan.source, physicalGitdir)
		if lexicallyInternal != physicallyInternal {
			return BackPointerPlan{}, fmt.Errorf(
				"worktree back-pointer %q crosses the source boundary through a symbolic link",
				gitdirFile,
			)
		}

		repairGitdir := gitdirPath
		if lexicallyInternal {
			relative, err := filepath.Rel(plan.source, gitdirPath)
			if err != nil {
				return BackPointerPlan{}, fmt.Errorf("make internal worktree path relative: %w", err)
			}
			if relative == "." || filepath.IsAbs(relative) || hasParentPrefix(relative) {
				return BackPointerPlan{}, errors.New("internal worktree path is not safely below the source")
			}
			repairGitdir = filepath.Join(plan.dest, relative)
			replacement := []byte(normalizePath(repairGitdir) + "\n")
			plan.updates = append(plan.updates, backPointerUpdate{
				entry:       entry.Name(),
				expected:    bytes.Clone(content),
				replacement: replacement,
				mode:        gitdirInfo.Mode(),
			})
		}

		repairPath := filepath.Dir(repairGitdir)
		key := normalizePath(repairPath)
		if _, exists := seenPaths[key]; exists {
			return BackPointerPlan{}, fmt.Errorf("multiple worktree entries identify %q", repairPath)
		}
		seenPaths[key] = struct{}{}
		plan.paths = append(plan.paths, repairPath)
	}
	return plan, nil
}

// Revalidate repeats back-pointer planning and rejects any metadata change
// made after the plan was displayed.
func (plan BackPointerPlan) Revalidate(options Filesystem) error {
	current, err := PlanBackPointers(plan.source, plan.dest, options)
	if err != nil {
		return err
	}
	if len(current.paths) != len(plan.paths) || len(current.updates) != len(plan.updates) {
		return errors.New("linked-worktree metadata changed after planning")
	}
	for index := range plan.paths {
		if current.paths[index] != plan.paths[index] {
			return errors.New("linked-worktree paths changed after planning")
		}
	}
	for index := range plan.updates {
		left := current.updates[index]
		right := plan.updates[index]
		if left.entry != right.entry ||
			!bytes.Equal(left.expected, right.expected) ||
			!bytes.Equal(left.replacement, right.replacement) {
			return errors.New("linked-worktree back-pointers changed after planning")
		}
	}
	return nil
}

// ApplyBackPointers rewrites planned internal-worktree pointers after the main
// repository has moved. Every file is rechecked before it is written.
func (plan BackPointerPlan) ApplyBackPointers(options Filesystem) error {
	filesystem := newFilesystem(options)
	worktreesDir := filepath.Join(plan.dest, ".git", "worktrees")
	if len(plan.updates) != 0 {
		info, err := filesystem.lstat(worktreesDir)
		if err != nil {
			return fmt.Errorf("inspect moved linked-worktree metadata %q: %w", worktreesDir, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("moved linked-worktree metadata %q is not a physical directory", worktreesDir)
		}
	}
	for _, update := range plan.updates {
		path := filepath.Join(worktreesDir, update.entry, "gitdir")
		physicalPath, err := filesystem.physicalizeAbsolute(path)
		if err != nil {
			return fmt.Errorf("physicalize moved worktree back-pointer %q: %w", path, err)
		}
		if physicalPath != path || !pathStrictlyWithin(worktreesDir, physicalPath) {
			return fmt.Errorf("moved worktree back-pointer %q escapes its metadata directory", path)
		}
		info, err := filesystem.lstat(path)
		if err != nil {
			return fmt.Errorf("inspect moved worktree back-pointer %q: %w", path, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("moved worktree back-pointer %q is not a regular file", path)
		}
		content, err := filesystem.readFile(path)
		if err != nil {
			return fmt.Errorf("read moved worktree back-pointer %q: %w", path, err)
		}
		if !bytes.Equal(content, update.expected) {
			return fmt.Errorf("moved worktree back-pointer %q changed after planning", path)
		}
		if err := filesystem.writeFile(path, update.replacement, preservedMode(update.mode)); err != nil {
			return fmt.Errorf("rewrite moved worktree back-pointer %q: %w", path, err)
		}
	}
	return nil
}

func parseBackPointer(content []byte) (string, error) {
	content = bytes.TrimSuffix(content, []byte{'\n'})
	content = bytes.TrimSuffix(content, []byte{'\r'})
	if len(content) == 0 {
		return "", errors.New("path is empty")
	}
	if bytes.IndexByte(content, 0) >= 0 ||
		bytes.IndexByte(content, '\n') >= 0 ||
		bytes.IndexByte(content, '\r') >= 0 {
		return "", errors.New("path is not a single line")
	}
	path := string(content)
	if strings.TrimSpace(path) != path {
		return "", errors.New("path has leading or trailing whitespace")
	}
	return path, nil
}
