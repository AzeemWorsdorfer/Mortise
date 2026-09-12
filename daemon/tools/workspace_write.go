// workspace_write.go — workspace file write machinery.
//
// Responsibilities:
//   - Resolve and anchor file_write targets with parent directories held open
//   - Replace files atomically through same-directory temporary files
//   - Read current contents and preserve file modes for the replacement
//
// Is NOT responsible for path and boundary resolution (see
// workspace_path.go), generic file opening (see workspace_file.go),
// history recording, or diff formatting.
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

type workspaceWriteTarget struct {
	parentFD    int
	descriptors []int
	name        string
	target      string
	cleanPath   string
}

// writeWorkspaceFile resolves the requested path, then writes content
// under the per-path history lock so concurrent writes to the same

func writeWorkspaceFile(root, requested, content string, history *FileHistory) (string, string, string, bool, error) {
	workspace, err := openWorkspaceWriteTarget(root, requested)
	if err != nil {
		return "", "", "", false, err
	}
	lockPath := historyTargetPath(root, workspace.target)
	var (
		target      string
		displayPath string
		previous    string
		existed     bool
	)
	err = history.withFileLock(lockPath, func() error {
		var err error
		target, displayPath, previous, existed, err = writeOpenedWorkspaceFile(workspace, content)
		if err != nil {
			return err
		}
		history.push(historyTargetPath(root, target), previous, existed)
		return nil
	})
	if err != nil {
		return "", "", "", false, err
	}
	return target, displayPath, previous, existed, nil
}

// writeOpenedWorkspaceFile replaces the file through a same-directory
// temporary file and atomic rename, reading the previous contents for
// history first. Every descriptor opened for the write path is closed

func writeOpenedWorkspaceFile(workspace workspaceWriteTarget, content string) (string, string, string, bool, error) {
	previous, existed, err := readWorkspaceTarget(workspace.parentFD, workspace.name, workspace.target)
	if err != nil {
		pathErr := closeDescriptors(workspace.descriptors)
		return "", "", "", false, combineWorkspaceErrors("file_write: reading existing file", err, nil, nil, pathErr)
	}
	mode, err := workspaceFileMode(workspace.parentFD, workspace.name)
	if err != nil {
		pathErr := closeDescriptors(workspace.descriptors)
		return "", "", "", false, combineWorkspaceErrors("file_write: inspecting existing file", err, nil, nil, pathErr)
	}
	tempFile, tempName, err := createWorkspaceTemp(workspace.parentFD, "file_write", mode)
	if err != nil {
		closeErr := closeDescriptors(workspace.descriptors)
		if closeErr != nil {
			return "", "", "", false, fmt.Errorf("file_write: creating temporary file: %v; closing workspace path: %w", err, closeErr)
		}
		return "", "", "", false, err
	}
	if _, err = tempFile.WriteString(content); err != nil {
		closeErr := tempFile.Close()
		removeErr := unix.Unlinkat(workspace.parentFD, tempName, 0)
		pathErr := closeDescriptors(workspace.descriptors)
		return "", "", "", false, combineWorkspaceErrors("file_write: writing temporary file", err, closeErr, removeErr, pathErr)
	}
	if err = tempFile.Close(); err != nil {
		removeErr := unix.Unlinkat(workspace.parentFD, tempName, 0)
		pathErr := closeDescriptors(workspace.descriptors)
		return "", "", "", false, combineWorkspaceErrors("file_write: closing temporary file", err, nil, removeErr, pathErr)
	}
	if err = unix.Renameat(workspace.parentFD, tempName, workspace.parentFD, workspace.name); err != nil {
		removeErr := unix.Unlinkat(workspace.parentFD, tempName, 0)
		pathErr := closeDescriptors(workspace.descriptors)
		return "", "", "", false, combineWorkspaceErrors("file_write: replacing file", err, nil, removeErr, pathErr)
	}
	if err = closeDescriptors(workspace.descriptors); err != nil {
		return "", "", "", false, fmt.Errorf("file_write: closing workspace path: %w", err)
	}
	return workspace.target, workspace.cleanPath, previous, existed, nil
}

// openWorkspaceWriteTarget anchors the file_write target: every parent
// directory is opened (and created when missing), verified by identity,

func openWorkspaceWriteTarget(root, requested string) (workspaceWriteTarget, error) {
	anchor, err := openAnchoredWorkspaceParent(root, requested, workspaceOpenOptions{
		createParents: true,
		allowMissing:  true,
		toolName:      "file_write",
	})
	if err != nil {
		return workspaceWriteTarget{}, err
	}
	components := strings.Split(anchor.cleanPath, string(filepath.Separator))
	return workspaceWriteTarget{
		parentFD:    anchor.parentFD,
		descriptors: anchor.descriptors,
		name:        components[len(components)-1],
		target:      anchor.target,
		cleanPath:   anchor.cleanPath,
	}, nil
}

func readWorkspaceTarget(parentFD int, name, target string) (string, bool, error) {
	file, err := openWorkspaceFileDescriptor(parentFD, name, target, workspaceOpenOptions{
		flags:    unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		toolName: "file_write",
	})
	if errors.Is(err, unix.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return "", true, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return "", true, closeErr
	}
	return string(content), true, nil
}

func workspaceFileMode(parentFD int, name string) (uint32, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return 0o644, nil
	} else if err != nil {
		return 0, err
	}
	return uint32(stat.Mode & 0o7777), nil
}

func createWorkspaceTemp(parentFD int, toolName string, mode uint32) (*os.File, string, error) {
	for index := 0; index < 100; index++ {
		name := fmt.Sprintf(".mortise-%d-%d", os.Getpid(), index)
		fd, err := unix.Openat(parentFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("%s: creating temporary file: %w", toolName, err)
		}
		if err := unix.Fchmod(fd, mode); err != nil {
			closeErr := unix.Close(fd)
			return nil, "", errors.Join(fmt.Errorf("%s: setting temporary file mode: %w", toolName, err), closeErr)
		}
		file := os.NewFile(uintptr(fd), name)
		if file == nil {
			if closeErr := unix.Close(fd); closeErr != nil {
				return nil, "", fmt.Errorf("%s: closing temporary file: %w", toolName, closeErr)
			}
			return nil, "", fmt.Errorf("%s: creating temporary file returned an invalid file", toolName)
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("%s: creating temporary file: name space exhausted", toolName)
}

func combineWorkspaceErrors(operation string, operationErr, closeErr, removeErr, pathErr error) error {
	errs := []error{fmt.Errorf("%s: %w", operation, operationErr)}
	for _, err := range []error{closeErr, removeErr, pathErr} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// readWorkspaceFile opens and reads a workspace file for tools that
// need current contents. The toolName labels any error the tool
