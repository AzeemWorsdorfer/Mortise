// Package tools provides descriptor-anchored workspace file access.
//
// Responsibilities:
//   - Resolve internal symlinks and enforce the workspace boundary
//   - Open files and parent directories without symlink traversal
//   - Read, create, and update workspace files with checked cleanup errors
//
// Is NOT responsible for undo history, diff formatting, approval policy, or
// tool registration.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func writeWorkspaceFile(root, requested, content string) (string, string, string, bool, error) {
	_, _, _, existed, err := readWorkspaceFile(root, requested, "file_write", true)
	if err != nil {
		return "", "", "", false, err
	}
	file, target, cleanPath, _, err := openWorkspaceFile(root, requested, workspaceOpenOptions{
		createParents: true,
		flags:         unix.O_RDWR | unix.O_CREAT | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		mode:          0o644,
		toolName:      "file_write",
	})
	if err != nil {
		return "", "", "", false, err
	}
	previous, err := io.ReadAll(file)
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err == nil {
		err = file.Truncate(0)
	}
	if err == nil {
		_, err = file.WriteString(content)
	}
	closeErr := file.Close()
	if err != nil {
		if closeErr != nil {
			return "", "", "", false, fmt.Errorf("file_write: writing %q: %v; closing: %w", requested, err, closeErr)
		}
		return "", "", "", false, fmt.Errorf("file_write: writing %q: %w", requested, err)
	}
	if closeErr != nil {
		return "", "", "", false, fmt.Errorf("file_write: closing %q: %w", requested, closeErr)
	}
	return target, cleanPath, string(previous), existed, nil
}

// readWorkspaceFile opens and reads a workspace file for tools that
// need current contents. The toolName labels any error the tool
// reports so a file_write preview is not mislabeled as file_diff.
func readWorkspaceFile(root, requested, toolName string, allowMissing bool) (string, string, string, bool, error) {
	file, target, cleanPath, existed, err := openWorkspaceFile(root, requested, workspaceOpenOptions{
		allowMissing: allowMissing,
		flags:        unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		toolName:     toolName,
	})
	if err != nil {
		return "", "", "", false, err
	}
	if file == nil {
		return "", target, cleanPath, existed, nil
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		if closeErr != nil {
			return "", "", "", false, fmt.Errorf("%s: reading %q: %v; closing: %w", toolName, requested, readErr, closeErr)
		}
		return "", "", "", false, fmt.Errorf("%s: reading %q: %w", toolName, requested, readErr)
	}
	if closeErr != nil {
		return "", "", "", false, fmt.Errorf("%s: closing %q: %w", toolName, requested, closeErr)
	}
	return string(content), target, cleanPath, existed, nil
}

type workspaceOpenOptions struct {
	createParents bool
	allowMissing  bool
	flags         int
	mode          int
	toolName      string
}

type workspaceParentOptions struct {
	createParents bool
	allowMissing  bool
	toolName      string
	expected      []workspaceIdentity
}

type workspaceIdentity struct {
	device int32
	inode  uint64
}

var errWorkspaceFileMissing = errors.New("workspace file missing")

func openWorkspaceFile(root, requested string, options workspaceOpenOptions) (*os.File, string, string, bool, error) {
	resolvedRoot, err := resolvedWorkspaceRoot(root, options.toolName)
	if err != nil {
		return nil, "", "", false, err
	}
	cleanPath, err := cleanWorkspacePath(resolvedRoot, requested, options.toolName)
	if err != nil {
		return nil, "", "", false, err
	}
	cleanPath, target, err := resolveWorkspaceTarget(resolvedRoot, cleanPath, requested, options)
	if err != nil {
		return nil, "", "", false, err
	}
	components := strings.Split(cleanPath, string(filepath.Separator))
	expected, err := workspaceAncestorIdentities(resolvedRoot, cleanPath)
	if err != nil {
		return nil, "", "", false, boundaryPathError(options.toolName, requested, err)
	}
	parentFD, descriptors, err := openWorkspaceParent(resolvedRoot, cleanPath, requested, workspaceParentOptions{
		createParents: options.createParents,
		allowMissing:  options.allowMissing,
		toolName:      options.toolName,
		expected:      expected,
	})
	if errors.Is(err, errWorkspaceFileMissing) && options.allowMissing {
		return nil, target, cleanPath, false, nil
	}
	if err != nil {
		return nil, "", "", false, err
	}
	file, err := openWorkspaceFileDescriptor(parentFD, components[len(components)-1], target, options)
	if err != nil {
		closeErr := closeDescriptors(descriptors)
		if closeErr != nil {
			return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %w", options.toolName, closeErr)
		}
		if options.allowMissing && errors.Is(err, unix.ENOENT) {
			return nil, target, cleanPath, false, nil
		}
		return nil, "", "", false, boundaryPathError(options.toolName, requested, err)
	}
	closeErr := closeDescriptors(descriptors)
	if closeErr != nil {
		fileErr := file.Close()
		if fileErr != nil {
			return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %v; closing file: %w", options.toolName, closeErr, fileErr)
		}
		return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %w", options.toolName, closeErr)
	}
	return file, target, cleanPath, true, nil
}

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

func openWorkspaceFileDescriptor(parentFD int, name, target string, options workspaceOpenOptions) (*os.File, error) {
	fileFD, err := unix.Openat(parentFD, name, options.flags, uint32(options.mode))
	if err != nil {
		return nil, err
	}
	if err := rejectHardLink(fileFD, options.toolName, name); err != nil {
		if closeErr := unix.Close(fileFD); closeErr != nil {
			return nil, fmt.Errorf("%v; closing file descriptor: %w", err, closeErr)
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fileFD), target)
	if file == nil {
		if closeErr := unix.Close(fileFD); closeErr != nil {
			return nil, fmt.Errorf("closing file descriptor: %w", closeErr)
		}
		return nil, errors.New("opening file returned an invalid file")
	}
	return file, nil
}

func rejectHardLink(fileFD int, toolName, requested string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		return fmt.Errorf("%s: inspecting %q: %w", toolName, requested, err)
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink > 1 {
		return fmt.Errorf("%s: %q is a hard link and is outside the workspace boundary", toolName, requested)
	}
	return nil
}

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
	return workspaceIdentity{device: stat.Dev, inode: stat.Ino}, nil
}

func workspaceIdentityForDescriptor(fd int) (workspaceIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return workspaceIdentity{}, err
	}
	return workspaceIdentity{device: stat.Dev, inode: stat.Ino}, nil
}

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

func openWorkspaceParent(root, cleanPath, requested string, options workspaceParentOptions) (int, []int, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: opening workspace root: %w", options.toolName, err)
	}
	descriptors := []int{rootFD}
	if len(options.expected) > 0 {
		if err := verifyWorkspaceIdentity(rootFD, options.expected[0]); err != nil {
			closeErr := closeDescriptors(descriptors)
			if closeErr != nil {
				return 0, nil, fmt.Errorf("%s: verifying workspace root: %v; closing: %w", options.toolName, err, closeErr)
			}
			return 0, nil, fmt.Errorf("%s: verifying workspace root: %w", options.toolName, err)
		}
	}
	parentFD := rootFD
	components := strings.Split(cleanPath, string(filepath.Separator))
	for index, component := range components[:len(components)-1] {
		nextFD, openErr := openWorkspaceDirectory(parentFD, component)
		if index+1 >= len(options.expected) && openErr == nil {
			closeErr := closeDescriptors(descriptors)
			if closeErr != nil {
				return 0, nil, fmt.Errorf("%s: verifying %q: workspace path changed during resolution; closing: %w", options.toolName, requested, closeErr)
			}
			return 0, nil, fmt.Errorf("%s: verifying %q: workspace path changed during resolution", options.toolName, requested)
		}
		if errors.Is(openErr, unix.ENOENT) && options.allowMissing && !options.createParents {
			if closeErr := closeDescriptors(descriptors); closeErr != nil {
				return 0, nil, fmt.Errorf("%s: resolving %q: %v; closing workspace path: %w", options.toolName, requested, openErr, closeErr)
			}
			return 0, nil, errWorkspaceFileMissing
		}
		if errors.Is(openErr, unix.ENOENT) && options.createParents {
			mkdirErr := unix.Mkdirat(parentFD, component, 0o755)
			if mkdirErr != nil {
				closeErr := closeDescriptors(descriptors)
				if closeErr != nil {
					return 0, nil, fmt.Errorf("%s: creating parent directories for %q: %v; closing workspace path: %w", options.toolName, requested, mkdirErr, closeErr)
				}
				if errors.Is(mkdirErr, unix.EEXIST) {
					return 0, nil, fmt.Errorf("%s: verifying %q: workspace path changed during resolution", options.toolName, requested)
				}
				return 0, nil, fmt.Errorf("%s: creating parent directories for %q: %w", options.toolName, requested, mkdirErr)
			}
			nextFD, openErr = openWorkspaceDirectory(parentFD, component)
		}
		if openErr != nil {
			closeErr := closeDescriptors(descriptors)
			if closeErr != nil {
				return 0, nil, fmt.Errorf("%s: resolving %q: %v; closing workspace path: %w", options.toolName, requested, openErr, closeErr)
			}
			return 0, nil, boundaryPathError(options.toolName, requested, openErr)
		}
		descriptors = append(descriptors, nextFD)
		if len(descriptors)-1 < len(options.expected) {
			if verifyErr := verifyWorkspaceIdentity(nextFD, options.expected[len(descriptors)-1]); verifyErr != nil {
				closeErr := closeDescriptors(descriptors)
				if closeErr != nil {
					return 0, nil, fmt.Errorf("%s: verifying %q: %v; closing: %w", options.toolName, requested, verifyErr, closeErr)
				}
				return 0, nil, fmt.Errorf("%s: verifying %q: %w", options.toolName, requested, verifyErr)
			}
		}
		parentFD = nextFD
	}
	return parentFD, descriptors, nil
}

func openWorkspaceDirectory(parentFD int, component string) (int, error) {
	return unix.Openat(parentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func closeDescriptors(descriptors []int) error {
	var firstErr error
	for _, descriptor := range descriptors {
		if err := unix.Close(descriptor); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func boundaryPathError(toolName, requested string, err error) error {
	if errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %q does not exist in the workspace", toolName, requested)
	}
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%s: %q resolves outside the workspace", toolName, requested)
	}
	return fmt.Errorf("%s: resolving %q: %w", toolName, requested, err)
}

func resolvedWorkspaceRoot(root, toolName string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%s: workspace root %q does not exist", toolName, root)
	}
	return resolved, nil
}

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

func workspaceFileTargetWithin(root, target string) bool {
	return workspaceContains(root, target) && filepath.Clean(root) != filepath.Clean(target)
}
