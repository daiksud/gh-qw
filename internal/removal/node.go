// Package removal provides shared observations for managed removal operations.
package removal

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/daiksud/gh-qw/internal/fsidentity"
	"github.com/daiksud/gh-qw/internal/local"
	"github.com/daiksud/gh-qw/internal/physicalpath"
)

// ErrSafety reports a runtime safety refusal.
var ErrSafety = errors.New("remove safety refusal")

// Filesystem supplies required read-only object observation seams.
type Filesystem struct {
	Lstat        func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	SameFile     func(fs.FileInfo, fs.FileInfo) bool
}

// Kind identifies the expected filesystem object shape.
type Kind uint8

const (
	Directory Kind = iota + 1
	RegularFile
)

// Node captures the identity of one physical filesystem object.
type Node struct {
	Path string
	info fs.FileInfo
	kind Kind
}

func outputPath(path string) string { return local.NormalizePathForOutput(filepath.Clean(path)) }
func pathStrictlyWithin(base, path string) bool {
	return base != "" && path != "" && physicalpath.StrictlyWithin(base, path)
}

// CaptureNode captures a real object within an established physical boundary.
func CaptureNode(
	filesystem Filesystem,
	boundary, path string,
	kind Kind,
	strictlyWithin bool,
) (Node, error) {
	if path == "" || !filepath.IsAbs(path) {
		return Node{}, fmt.Errorf("path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	if strictlyWithin && !pathStrictlyWithin(boundary, path) {
		return Node{}, fmt.Errorf("path %q is outside boundary %q", path, boundary)
	}

	info, err := fsidentity.Capture(path, filesystem.Lstat, filesystem.SameFile)
	if err != nil {
		return Node{}, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return Node{}, errors.New("path is a symbolic link")
	}
	switch kind {
	case Directory:
		if !info.IsDir() {
			return Node{}, errors.New("path is not a directory")
		}
	case RegularFile:
		if !info.Mode().IsRegular() {
			return Node{}, errors.New("path is not a regular file")
		}
	default:
		return Node{}, errors.New("unknown path kind")
	}

	physical, err := filesystem.EvalSymlinks(path)
	if err != nil {
		return Node{}, err
	}
	if !filepath.IsAbs(physical) {
		return Node{}, fmt.Errorf("resolved path %q is not absolute", physical)
	}
	physical = filepath.Clean(physical)
	if !physicalpath.SamePath(physical, path) {
		return Node{}, fmt.Errorf(
			"path resolves through a symbolic link to %q",
			physical,
		)
	}
	if strictlyWithin && !pathStrictlyWithin(boundary, physical) {
		return Node{}, fmt.Errorf(
			"resolved path %q is outside boundary %q",
			physical,
			boundary,
		)
	}
	return Node{Path: path, info: info, kind: kind}, nil
}

// CaptureOptionalDirectory distinguishes an absent directory from an unsafe observation.
func CaptureOptionalDirectory(
	filesystem Filesystem,
	boundary, path string,
) (Node, bool, error) {
	node, err := CaptureNode(
		filesystem,
		boundary,
		path,
		Directory,
		true,
	)
	if errors.Is(err, fs.ErrNotExist) {
		return Node{}, false, nil
	}
	return node, err == nil, err
}

// CaptureCleanupChain captures existing parent identities up to but excluding the boundary.
func CaptureCleanupChain(
	filesystem Filesystem,
	boundary, start string,
) ([]Node, error) {
	boundary = filepath.Clean(boundary)
	start = filepath.Clean(start)
	if physicalpath.SamePath(start, boundary) {
		return nil, nil
	}
	if !pathStrictlyWithin(boundary, start) {
		return nil, fmt.Errorf("cleanup start %q is outside boundary %q", start, boundary)
	}

	var result []Node
	for current := start; !physicalpath.SamePath(current, boundary); current = filepath.Dir(current) {
		if !pathStrictlyWithin(boundary, current) {
			return nil, fmt.Errorf(
				"cleanup directory %q escaped boundary %q",
				current,
				boundary,
			)
		}
		node, err := CaptureNode(
			filesystem,
			boundary,
			current,
			Directory,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("inspect cleanup directory %q: %w", current, err)
		}
		result = append(result, node)
	}
	return result, nil
}

// CompareNodeSlices compares ordered object observations.
func CompareNodeSlices(
	filesystem Filesystem,
	before, after []Node,
	description string,
) error {
	if len(before) != len(after) {
		return fmt.Errorf("%w: %s path set changed", ErrSafety, description)
	}
	for index := range before {
		if err := CompareNode(
			filesystem,
			before[index],
			after[index],
			description,
		); err != nil {
			return err
		}
	}
	return nil
}

// CompareNode rejects path, shape, or filesystem identity changes.
func CompareNode(
	filesystem Filesystem,
	before, after Node,
	description string,
) error {
	if before.kind != after.kind || !physicalpath.SamePath(before.Path, after.Path) {
		return fmt.Errorf("%w: %s path changed", ErrSafety, description)
	}
	if before.info == nil || after.info == nil ||
		!filesystem.SameFile(before.info, after.info) {
		return fmt.Errorf(
			"%w: %s %q was replaced",
			ErrSafety,
			description,
			outputPath(before.Path),
		)
	}
	return nil
}

// RevalidateNode observes the same path again without moving its trusted boundary.
func RevalidateNode(
	filesystem Filesystem,
	snapshot Node,
	description string,
) error {
	current, err := CaptureNode(
		filesystem,
		"",
		snapshot.Path,
		snapshot.kind,
		false,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: revalidate %s %q: %v",
			ErrSafety,
			description,
			outputPath(snapshot.Path),
			err,
		)
	}
	return CompareNode(filesystem, snapshot, current, description)
}
