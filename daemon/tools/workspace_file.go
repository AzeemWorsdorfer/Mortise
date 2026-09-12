// workspace_file.go — descriptor-anchored workspace file access.
//
// Responsibilities:
//   - Open files and parent directories without symlink traversal
//   - Read, create, and update workspace files with checked cleanup errors
//   - Anchor every access to the file objects verified during path resolution
//
// Is NOT responsible for path and boundary resolution (see
// workspace_path.go), undo history, diff formatting, approval policy,
// or tool registration.
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

var errWorkspaceFileMissing = errors.New("workspace file missing")

// file serialize against their recorded history.
// on all exits, with close errors surfaced alongside the primary error.
// and held open until the write replaces the file.

type anchoredWorkspaceParent struct {
	parentFD    int
	descriptors []int
	rootFD      int
	cleanPath   string
	target      string
}

// openAnchoredWorkspaceParent is the shared resolution prologue for
// file tools: it opens and identity-verifies the workspace root,
// resolves the requested path, captures ancestor identities, and opens
// every parent directory. On success the caller owns anchor.descriptors
// (anchor.rootFD included) and must close them; parentFD is the last
// directory on the path.
func openAnchoredWorkspaceParent(root, requested string, options workspaceOpenOptions) (anchoredWorkspaceParent, error) {
	rootFD, rootIdentity, err := openWorkspaceRoot(root, options.toolName)
	if err != nil {
		return anchoredWorkspaceParent{}, err
	}
	resolvedRoot, err := resolvedWorkspaceRoot(root, options.toolName)
	if err != nil {
		_ = unix.Close(rootFD) //nolint:errcheck // caller already has a failure reason
		return anchoredWorkspaceParent{}, err
	}
	if err := verifyWorkspacePathIdentity(resolvedRoot, rootIdentity); err != nil {
		_ = unix.Close(rootFD) //nolint:errcheck // caller already has a failure reason
		return anchoredWorkspaceParent{}, err
	}
	cleanPath, err := cleanWorkspacePath(resolvedRoot, requested, options.toolName)
	if err != nil {
		_ = unix.Close(rootFD) //nolint:errcheck // caller already has a failure reason
		return anchoredWorkspaceParent{}, err
	}
	cleanPath, target, err := resolveWorkspaceTarget(resolvedRoot, cleanPath, requested, options)
	if err != nil {
		_ = unix.Close(rootFD) //nolint:errcheck // caller already has a failure reason
		return anchoredWorkspaceParent{}, err
	}
	expected, err := workspaceAncestorIdentities(resolvedRoot, cleanPath)
	if err != nil {
		_ = unix.Close(rootFD) //nolint:errcheck // caller already has a failure reason
		return anchoredWorkspaceParent{}, boundaryPathError(options.toolName, requested, err)
	}
	expected[0] = rootIdentity
	parentFD, descriptors, err := openWorkspaceParent(resolvedRoot, cleanPath, requested, workspaceParentOptions{
		createParents: options.createParents,
		allowMissing:  options.allowMissing,
		toolName:      options.toolName,
		expected:      expected,
		rootFD:        rootFD,
	})
	anchor := anchoredWorkspaceParent{rootFD: rootFD, cleanPath: cleanPath, target: target}
	if err != nil {
		return anchor, err
	}
	anchor.parentFD = parentFD
	anchor.descriptors = descriptors
	return anchor, nil
}

// reports so a file_write preview is not mislabeled as file_diff.
func readWorkspaceFile(root, requested, toolName string, allowMissing bool) (string, string, string, bool, error) {
	file, target, cleanPath, existed, err := openWorkspaceFile(root, requested, workspaceOpenOptions{
		allowMissing: allowMissing,
		flags:        unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW,
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
	rootFD        int
}

// openWorkspaceFile resolves and opens a workspace file, returning the
// descriptor-anchored file plus its resolved target and display path.
// With allowMissing, a missing file yields a nil file and existed=false
// instead of an error.
func openWorkspaceFile(root, requested string, options workspaceOpenOptions) (*os.File, string, string, bool, error) {
	anchor, err := openAnchoredWorkspaceParent(root, requested, options)
	if err != nil {
		if errors.Is(err, errWorkspaceFileMissing) && options.allowMissing {
			return nil, anchor.target, anchor.cleanPath, false, nil
		}
		return nil, "", "", false, err
	}
	components := strings.Split(anchor.cleanPath, string(filepath.Separator))
	file, err := openWorkspaceFileDescriptor(anchor.parentFD, components[len(components)-1], anchor.target, options)
	if err != nil {
		closeErr := closeDescriptors(anchor.descriptors)
		if closeErr != nil {
			return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %w", options.toolName, closeErr)
		}
		if options.allowMissing && errors.Is(err, unix.ENOENT) {
			return nil, anchor.target, anchor.cleanPath, false, nil
		}
		return nil, "", "", false, boundaryPathError(options.toolName, requested, err)
	}
	closeErr := closeDescriptors(anchor.descriptors)
	if closeErr != nil {
		fileErr := file.Close()
		if fileErr != nil {
			return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %v; closing file: %w", options.toolName, closeErr, fileErr)
		}
		return nil, "", "", false, fmt.Errorf("%s: closing workspace path: %w", options.toolName, closeErr)
	}
	return file, anchor.target, anchor.cleanPath, true, nil
}

func openWorkspaceFileDescriptor(parentFD int, name, target string, options workspaceOpenOptions) (*os.File, error) {
	fileFD, err := unix.Openat(parentFD, name, options.flags, uint32(options.mode))
	if err != nil {
		return nil, err
	}
	if err := rejectRegularFile(fileFD, options.toolName, name); err != nil {
		if closeErr := unix.Close(fileFD); closeErr != nil {
			return nil, fmt.Errorf("%v; closing file descriptor: %w", err, closeErr)
		}
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

func rejectRegularFile(fileFD int, toolName, requested string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		return fmt.Errorf("%s: inspecting %q: %w", toolName, requested, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			return fmt.Errorf("%s: %q is a directory, not a regular file", toolName, requested)
		}
		return fmt.Errorf("%s: %q is not a regular file", toolName, requested)
	}
	return nil
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

func openWorkspaceRoot(root, toolName string) (int, workspaceIdentity, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, workspaceIdentity{}, fmt.Errorf("%s: opening workspace root: %w", toolName, err)
	}
	identity, err := workspaceIdentityForDescriptor(rootFD)
	if err != nil {
		closeErr := unix.Close(rootFD)
		return 0, workspaceIdentity{}, errors.Join(fmt.Errorf("%s: inspecting workspace root: %w", toolName, err), closeErr)
	}
	return rootFD, identity, nil
}

// openWorkspaceParent walks cleanPath component by component with
// openat, creating missing parents when allowed, and verifying each
// opened directory against the identities captured before resolution.
// Every descriptor it opens is closed on all exits; on success the
// caller owns the returned list.
func openWorkspaceParent(root, cleanPath, requested string, options workspaceParentOptions) (int, []int, error) {
	rootFD := options.rootFD
	if rootFD < 0 {
		var err error
		rootFD, _, err = openWorkspaceRoot(root, options.toolName)
		if err != nil {
			return 0, nil, err
		}
	}
	descriptors := []int{rootFD}
	if err := verifyWorkspaceParentRoot(rootFD, options, descriptors); err != nil {
		return 0, nil, err
	}
	parentFD := rootFD
	components := strings.Split(cleanPath, string(filepath.Separator))
	for index, component := range components[:len(components)-1] {
		nextFD, created, openErr := openWorkspaceParentComponent(parentFD, component, requested, options, descriptors)
		if index+1 >= len(options.expected) && openErr == nil && !created {
			closeErr := errors.Join(unix.Close(nextFD), closeDescriptors(descriptors))
			if closeErr != nil {
				return 0, nil, fmt.Errorf("%s: verifying %q: workspace path changed during resolution; closing: %w", options.toolName, requested, closeErr)
			}
			return 0, nil, fmt.Errorf("%s: verifying %q: workspace path changed during resolution", options.toolName, requested)
		}
		if openErr != nil {
			if errors.Is(openErr, errWorkspaceFileMissing) {
				return 0, nil, openErr
			}
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

// verifyWorkspaceParentRoot re-checks the root descriptor against the
// identity captured before resolution, rejecting a swapped root.
func verifyWorkspaceParentRoot(rootFD int, options workspaceParentOptions, descriptors []int) error {
	if len(options.expected) == 0 {
		return nil
	}
	if err := verifyWorkspaceIdentity(rootFD, options.expected[0]); err != nil {
		closeErr := closeDescriptors(descriptors)
		if closeErr != nil {
			return fmt.Errorf("%s: verifying workspace root: %v; closing: %w", options.toolName, err, closeErr)
		}
		return fmt.Errorf("%s: verifying workspace root: %w", options.toolName, err)
	}
	return nil
}

// openWorkspaceParentComponent opens one parent directory. It creates
// a missing component when createParents is set, reports
// errWorkspaceFileMissing when allowMissing is set without parent
// creation, and closes descriptors on any parent-creation failure.
func openWorkspaceParentComponent(parentFD int, component, requested string, options workspaceParentOptions, descriptors []int) (int, bool, error) {
	nextFD, openErr := openWorkspaceDirectory(parentFD, component)
	if openErr == nil {
		return nextFD, false, nil
	}
	if errors.Is(openErr, unix.ENOENT) && options.allowMissing && !options.createParents {
		if closeErr := closeDescriptors(descriptors); closeErr != nil {
			return 0, false, fmt.Errorf("%s: resolving %q: %v; closing workspace path: %w", options.toolName, requested, openErr, closeErr)
		}
		return 0, false, errWorkspaceFileMissing
	}
	if errors.Is(openErr, unix.ENOENT) && options.createParents {
		mkdirErr := unix.Mkdirat(parentFD, component, 0o755)
		if mkdirErr != nil {
			closeErr := closeDescriptors(descriptors)
			if closeErr != nil {
				return 0, false, fmt.Errorf("%s: creating parent directories for %q: %v; closing workspace path: %w", options.toolName, requested, mkdirErr, closeErr)
			}
			if errors.Is(mkdirErr, unix.EEXIST) {
				return 0, false, fmt.Errorf("%s: verifying %q: workspace path changed during resolution", options.toolName, requested)
			}
			return 0, false, fmt.Errorf("%s: creating parent directories for %q: %w", options.toolName, requested, mkdirErr)
		}
		nextFD, openErr = openWorkspaceDirectory(parentFD, component)
		return nextFD, true, openErr
	}
	return 0, false, openErr
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
