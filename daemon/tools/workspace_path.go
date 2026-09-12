// workspace_path.go — workspace path resolution and identity anchoring.
//
// Responsibilities:
//   - Resolve requested paths against the workspace root
//   - Reject paths that escape the boundary, lexically or via symlinks
//   - Capture and verify (device, inode) identities so a resolution
//     pass is anchored to the same filesystem objects throughout
//
// Is NOT responsible for opening file descriptors, reading or writing
// file contents, undo history, or diff formatting; those live in
// workspace_file.go and the tool implementations.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type workspaceIdentity struct {
	// device and inode identify a directory across a resolution pass.
	// device is uint64 because Stat_t.Dev is unsigned on Linux and
	// signed on darwin; uint64(stat.Dev) is lossless on both.
	device uint64
	inode  uint64
}

// resolveWorkspaceTarget maps a cleaned relative path onto its lexical
// target under root, resolving the longest existing prefix of symlinks.
// It returns the resolved path relative to the resolved root and the
// absolute resolved target, rejecting anything that escapes the
// workspace boundary.
func resolveWorkspaceTarget(root, cleanPath, requested string, options workspaceOpenOptions) (string, string, error) {
	lexicalTarget := filepath.Join(root, cleanPath)
	resolvedTarget, err := resolveExistingPrefix(lexicalTarget, options.createParents || options.allowMissing)
	if err != nil {
		return "", "", boundaryPathError(options.toolName, requested, err)
	}
	if !workspaceFileTargetWithin(root, resolvedTarget) {
		return "", "", fmt.Errorf("%s: %q resolves outside the workspace root", options.toolName, requested)
	}
	resolvedPath, err := filepath.Rel(root, resolvedTarget)
	if err != nil {
		return "", "", fmt.Errorf("%s: resolving %q relative path: %w", options.toolName, requested, err)
	}
	return resolvedPath, resolvedTarget, nil
}

// resolveExistingPrefix resolves symlinks along the longest existing
// prefix of path, rejoining any missing trailing components so that
// not-yet-created paths resolve against their existing ancestors.
func resolveExistingPrefix(path string, allowMissing bool) (string, error) {
	candidate := path
	missing := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			return filepath.Join(resolved, filepath.Join(missing...)), nil
		}
		if !allowMissing || !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", err
		}
		missing = append([]string{filepath.Base(candidate)}, missing...)
		candidate = parent
	}
}

// boundaryPathError labels a path-resolution failure with the calling
// tool and the requested path, translating sentinel errno values into
// boundary-specific messages.
func boundaryPathError(toolName, requested string, err error) error {
	if errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %q does not exist in the workspace", toolName, requested)
	}
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%s: %q resolves outside the workspace", toolName, requested)
	}
	return fmt.Errorf("%s: resolving %q: %w", toolName, requested, err)
}

// resolvedWorkspaceRoot resolves the workspace root through symlinks.
func resolvedWorkspaceRoot(root, toolName string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%s: workspace root %q does not exist", toolName, root)
	}
	return resolved, nil
}

// cleanWorkspacePath rejects absolute requests and any request whose
// lexical resolution leaves the workspace root.
func cleanWorkspacePath(root, requested, toolName string) (string, error) {
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("%s: %q resolves outside the workspace root", toolName, requested)
	}
	cleanPath := filepath.Clean(requested)
	target := filepath.Join(root, cleanPath)
	if !workspaceFileTargetWithin(root, target) {
		if target == root {
			return "", fmt.Errorf("%s: %q is the workspace root itself; pass a file path relative to it", toolName, requested)
		}
		return "", fmt.Errorf("%s: %q resolves outside the workspace root", toolName, requested)
	}
	return cleanPath, nil
}

func workspaceContains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// workspaceFileTargetWithin reports whether target stays inside root
// without being the root itself.
func workspaceFileTargetWithin(root, target string) bool {
	return workspaceContains(root, target) && filepath.Clean(root) != filepath.Clean(target)
}

// workspaceAncestorIdentities captures the (device, inode) identity of
// the root and every existing parent directory of cleanPath, stopping
// at the first missing ancestor so created directories are verified
// against nothing.
func workspaceAncestorIdentities(root, cleanPath string) ([]workspaceIdentity, error) {
	components := strings.Split(cleanPath, string(filepath.Separator))
	identities := make([]workspaceIdentity, 0, len(components))
	current := root
	for index := -1; index < len(components)-1; index++ {
		if index >= 0 {
			current = filepath.Join(current, components[index])
		}
		identity, err := workspaceIdentityForPath(current)
		if errors.Is(err, os.ErrNotExist) {
			return identities, nil
		}
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func workspaceIdentityForPath(path string) (workspaceIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return workspaceIdentity{}, err
	}
	return workspaceIdentity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}

// verifyWorkspacePathIdentity re-stats the resolved workspace root and
// rejects a swap that happened between opening it and resolving paths.
func verifyWorkspacePathIdentity(path string, expected workspaceIdentity) error {
	actual, err := workspaceIdentityForPath(path)
	if err != nil {
		return fmt.Errorf("workspace root changed during resolution: %w", err)
	}
	if actual != expected {
		return errors.New("workspace root changed during resolution")
	}
	return nil
}

func workspaceIdentityForDescriptor(fd int) (workspaceIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return workspaceIdentity{}, err
	}
	return workspaceIdentity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}

// verifyWorkspaceIdentity re-stats an opened directory descriptor and
// rejects a component that was swapped during resolution.
func verifyWorkspaceIdentity(fd int, expected workspaceIdentity) error {
	actual, err := workspaceIdentityForDescriptor(fd)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("workspace path changed during resolution")
	}
	return nil
}
