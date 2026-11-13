package root

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnsafeTarget identifies a derived target that is not safely contained by
// its specified root.
var ErrUnsafeTarget = errors.New("unsafe target path")

// ContainmentMode controls whether a target may equal its root.
type ContainmentMode uint8

const (
	// StrictlyUnder requires the target to be a strict descendant of the root.
	StrictlyUnder ContainmentMode = iota
	// AllowEqual permits the target to equal the root as well as be below it.
	AllowEqual
)

// SafetyError describes a target rejected by a runtime containment check.
type SafetyError struct {
	root   string
	target string
	reason string
}

// Error implements error.
func (e *SafetyError) Error() string {
	if e == nil {
		return ErrUnsafeTarget.Error()
	}
	return fmt.Sprintf(
		"%s: target %q with root %q: %s",
		ErrUnsafeTarget,
		e.target,
		e.root,
		e.reason,
	)
}

// Is makes every SafetyError discoverable with errors.Is.
func (e *SafetyError) Is(target error) bool {
	return target == ErrUnsafeTarget
}

// Root returns the boundary used for the safety check.
func (e *SafetyError) Root() string {
	if e == nil {
		return ""
	}
	return e.root
}

// Target returns the rejected target.
func (e *SafetyError) Target() string {
	if e == nil {
		return ""
	}
	return e.target
}

// Reason returns the safety failure without paths.
func (e *SafetyError) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

// PhysicalizeTarget resolves the target's existing symlinks and verifies that
// the result is contained by root. Root must be a physical absolute path
// returned by Resolver; it is intentionally not re-resolved so replacing the
// root with a symlink after startup cannot move the trusted boundary.
func PhysicalizeTarget(rootPath, targetPath string, mode ContainmentMode) (string, error) {
	return (&Resolver{}).PhysicalizeTarget(rootPath, targetPath, mode)
}

// PhysicalizeTarget resolves the target's existing symlinks with this
// resolver's filesystem seams and verifies containment by root.
func (r *Resolver) PhysicalizeTarget(rootPath, targetPath string, mode ContainmentMode) (string, error) {
	if r == nil {
		return "", errors.New("physicalize target: nil resolver")
	}

	rootPath, err := prepareSafetyPath(rootPath)
	if err != nil {
		return "", newSafetyError(rootPath, targetPath, "invalid root boundary: "+err.Error())
	}
	targetPath, err = prepareSafetyPath(targetPath)
	if err != nil {
		return "", newSafetyError(rootPath, targetPath, err.Error())
	}
	if mode != StrictlyUnder && mode != AllowEqual {
		return "", newSafetyError(rootPath, targetPath, "unknown containment mode")
	}

	physicalTarget, err := r.physicalize(targetPath, false)
	if err != nil {
		var shapeError *pathShapeError
		if errors.As(err, &shapeError) {
			return "", newSafetyError(rootPath, targetPath, shapeError.reason)
		}
		return "", fmt.Errorf("physicalize target %q: %w", targetPath, err)
	}

	relation := trustedBoundaryRelationship(rootPath, physicalTarget)
	switch relation {
	case relationContains:
		return physicalTarget, nil
	case relationEqual:
		if mode == AllowEqual {
			return physicalTarget, nil
		}
		return "", newSafetyError(rootPath, physicalTarget, "target must be strictly under root")
	default:
		return "", newSafetyError(rootPath, physicalTarget, "physical target is outside root")
	}
}

func prepareSafetyPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path must not contain only whitespace")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("path must not contain NUL")
	}
	if strings.HasPrefix(path, "~") {
		return "", errors.New("path must already be expanded")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(path), nil
}

type pathShapeError struct {
	reason string
}

func (e *pathShapeError) Error() string {
	if e == nil {
		return "invalid path shape"
	}
	return e.reason
}

func (r *Resolver) physicalize(path string, requireFinalDirectory bool) (string, error) {
	lstat := r.lstat
	if lstat == nil {
		lstat = os.Lstat
	}
	stat := r.stat
	if stat == nil {
		stat = os.Stat
	}
	evalSymlinks := r.evalSymlinks
	if evalSymlinks == nil {
		evalSymlinks = filepath.EvalSymlinks
	}

	start, components, err := splitAbsolutePath(path)
	if err != nil {
		return "", err
	}

	startInfo, err := stat(start)
	if err != nil {
		return "", fmt.Errorf("stat filesystem root %q: %w", start, err)
	}
	if !startInfo.IsDir() {
		return "", &pathShapeError{reason: fmt.Sprintf("filesystem root %q is not a directory", start)}
	}

	current := start
	missingIndex := len(components)
	for index, component := range components {
		candidate := filepath.Join(current, component)
		lstatInfo, err := lstat(candidate)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				missingIndex = index
				break
			}
			return "", fmt.Errorf("lstat path component %q: %w", candidate, err)
		}

		info, err := stat(candidate)
		if err != nil {
			if lstatInfo.Mode()&fs.ModeSymlink != 0 && errors.Is(err, fs.ErrNotExist) {
				return "", &pathShapeError{reason: fmt.Sprintf("path component %q is a dangling symbolic link", candidate)}
			}
			return "", fmt.Errorf("stat path component %q: %w", candidate, err)
		}

		isFinal := index == len(components)-1
		if (!isFinal || requireFinalDirectory) && !info.IsDir() {
			if isFinal {
				return "", &pathShapeError{reason: fmt.Sprintf("path %q exists and is not a directory", candidate)}
			}
			return "", &pathShapeError{reason: fmt.Sprintf("path component %q is not a directory", candidate)}
		}
		current = candidate
	}

	existingAncestor, err := evalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve symbolic links in %q: %w", current, err)
	}
	if !filepath.IsAbs(existingAncestor) {
		return "", fmt.Errorf("resolved path %q is not absolute", existingAncestor)
	}
	existingAncestor = filepath.Clean(existingAncestor)

	physicalPath := existingAncestor
	if missingIndex < len(components) {
		parts := append([]string{existingAncestor}, components[missingIndex:]...)
		physicalPath = filepath.Join(parts...)
	}
	if !filepath.IsAbs(physicalPath) {
		return "", fmt.Errorf("physical path %q is not absolute", physicalPath)
	}
	return filepath.Clean(physicalPath), nil
}

func splitAbsolutePath(path string) (string, []string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("path %q is not absolute", path)
	}

	volume := filepath.VolumeName(path)
	start := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimLeft(rest, string(filepath.Separator))
	if rest == "" {
		return start, nil, nil
	}

	components := strings.Split(rest, string(filepath.Separator))
	return start, components, nil
}

type pathRelation uint8

const (
	relationDisjoint pathRelation = iota
	relationEqual
	relationContains
	relationInside
)

func trustedBoundaryRelationship(first, second string) pathRelation {
	// Runtime checks must not probe the trusted root: it may have been replaced
	// since resolution. Windows path comparison is always case-insensitive;
	// other platforms conservatively require the physical target's spelling
	// to match the cached boundary.
	return lexicalRelationship(first, second, runtime.GOOS == "windows")
}

func lexicalRelationship(first, second string, caseInsensitive bool) pathRelation {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if first == second {
		return relationEqual
	}

	firstVolume, firstComponents := pathParts(first)
	secondVolume, secondComponents := pathParts(second)
	equalComponent := func(left, right string) bool {
		if caseInsensitive {
			return strings.EqualFold(left, right)
		}
		return left == right
	}

	if !equalComponent(firstVolume, secondVolume) {
		return relationDisjoint
	}

	commonLength := min(len(firstComponents), len(secondComponents))
	for index := 0; index < commonLength; index++ {
		if !equalComponent(firstComponents[index], secondComponents[index]) {
			return relationDisjoint
		}
	}

	switch {
	case len(firstComponents) == len(secondComponents):
		return relationEqual
	case len(firstComponents) < len(secondComponents):
		return relationContains
	default:
		return relationInside
	}
}

func pathParts(path string) (string, []string) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimLeft(rest, string(filepath.Separator))
	if rest == "" {
		return volume, nil
	}
	return volume, strings.Split(rest, string(filepath.Separator))
}

func newSafetyError(rootPath, targetPath, reason string) *SafetyError {
	return &SafetyError{
		root:   rootPath,
		target: targetPath,
		reason: reason,
	}
}
