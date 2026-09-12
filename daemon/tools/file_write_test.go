// Package tools tests workspace-scoped file writing and diff behavior.
//
// Responsibilities:
//   - Verify workspace-safe writes, previews, diffs, and undo history
//   - Exercise boundary and concurrent filesystem race behavior
//
// Is NOT responsible for approval policy or daemon wire transport; those
// concerns are covered by the agent and daemon package tests.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestFileWriteCreatesFileAndReturnsUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	tool := NewFileWrite(root)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"nested/file.txt","content":"hello\n"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Fatal("res.Success = false, want true")
	}
	got, err := os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("file contents = %q, want %q", got, "hello\n")
	}
	if !strings.Contains(res.Output, "@@") || !strings.Contains(res.Output, "+hello") {
		t.Errorf("result output = %q, want unified diff containing new line", res.Output)
	}
	if len(res.FilesChanged) != 1 || res.FilesChanged[0] != "nested/file.txt" {
		t.Errorf("FilesChanged = %v, want [nested/file.txt]", res.FilesChanged)
	}
	if res.Additions != 1 || res.Deletions != 0 {
		t.Errorf("diff stats = +%d -%d, want +1 -0", res.Additions, res.Deletions)
	}
}

func TestFileWriteOverwritesExistingFileAndTracksHistory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := NewFileWrite(root)
	differ := NewFileDiff(root, writer.History())

	res, err := writer.Execute(context.Background(), json.RawMessage(`{"path":"file.txt","content":"after\n"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute = (%+v, %v), want success", res, err)
	}
	if !strings.Contains(res.Output, "-before") || !strings.Contains(res.Output, "+after") {
		t.Errorf("write diff = %q, want before and after lines", res.Output)
	}

	diff, err := differ.Execute(context.Background(), json.RawMessage(`{"path":"file.txt"}`))
	if err != nil || !diff.Success {
		t.Fatalf("file_diff = (%+v, %v), want success", diff, err)
	}
	if !strings.Contains(diff.Output, "-before") || !strings.Contains(diff.Output, "+after") {
		t.Errorf("file_diff output = %q, want previous and current contents", diff.Output)
	}
}

func TestFileDiffLabelsExistingEmptyFileAsExisting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writer := NewFileWrite(root)
	if _, err := writer.Execute(context.Background(), mustJSONWrite("empty.txt", "after\n")); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	res, err := NewFileDiff(root, writer.History()).Execute(context.Background(), json.RawMessage(`{"path":"empty.txt"}`))
	if err != nil || !res.Success {
		t.Fatalf("file_diff = (%+v, %v), want success", res, err)
	}
	if strings.Contains(res.Output, "--- /dev/null") || !strings.Contains(res.Output, "--- a/empty.txt") {
		t.Errorf("result output = %q, want existing-file header", res.Output)
	}
}

func TestFileWriteRejectsWorkspaceEscapesIncludingSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tool := NewFileWrite(root)

	for _, path := range []string{"../secret.txt", "escape/new.txt", "escape"} {
		t.Run(path, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), mustJSONWrite(path, "nope"))
			if err == nil {
				t.Fatal("Execute returned nil error, want boundary rejection")
			}
			if res == nil || res.Success {
				t.Fatalf("result = %+v, want success=false", res)
			}
		})
	}
}

func TestFileWritePreservesExistingMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileWrite(root).Execute(context.Background(), mustJSONWrite("private.txt", "updated")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestFileToolsRejectHardLinksToOutsideFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.txt")
	if err := os.Link(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	reader, err := NewFileRead(root).Execute(context.Background(), json.RawMessage(`{"path":"linked.txt"}`))
	if err == nil || (reader != nil && reader.Success) {
		t.Fatalf("file_read = (%+v, %v), want hard-link rejection", reader, err)
	}
	writer, err := NewFileWrite(root).Execute(context.Background(), mustJSONWrite("linked.txt", "changed"))
	if err == nil || writer == nil || writer.Success {
		t.Fatalf("file_write = (%+v, %v), want hard-link rejection", writer, err)
	}
	contents, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "secret" {
		t.Fatalf("outside file contents = %q, want unchanged", contents)
	}
}

func TestFileDiffRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	res, err := NewFileDiff(root).Execute(context.Background(), json.RawMessage(`{"path":"link.txt"}`))
	if err == nil {
		t.Fatal("Execute returned nil error, want boundary rejection")
	}
	if res == nil || res.Success {
		t.Fatalf("result = %+v, want success=false", res)
	}
}

func TestFileToolsAllowInternalSymlinks(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}

	writer := NewFileWrite(root)
	params := mustJSONWrite("alias/file.txt", "inside\n")
	preview, err := writer.Preview(context.Background(), params)
	if err != nil || !preview.Success {
		t.Fatalf("Preview = (%+v, %v), want success", preview, err)
	}
	if _, err := os.Stat(filepath.Join(actual, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("preview created internal target; Stat err = %v", err)
	}
	if _, err := writer.Execute(context.Background(), params); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(actual, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile internal target: %v", err)
	}
	if string(contents) != "inside\n" {
		t.Fatalf("internal target contents = %q, want %q", contents, "inside\n")
	}

	if _, err := writer.Execute(context.Background(), mustJSONWrite("alias/file.txt", "changed\n")); err != nil {
		t.Fatalf("second Execute through internal link: %v", err)
	}
	differ := NewFileDiff(root, writer.History())
	diff, err := differ.Execute(context.Background(), json.RawMessage(`{"path":"alias/file.txt"}`))
	if err != nil || !diff.Success {
		t.Fatalf("file_diff = (%+v, %v), want success", diff, err)
	}
	if !strings.Contains(diff.Output, "-inside") || !strings.Contains(diff.Output, "+changed") {
		t.Fatalf("file_diff output = %q, want history diff through internal link", diff.Output)
	}
}

func TestFileWriteUndoStackDefaultsAndCapsAtTenEntries(t *testing.T) {
	for _, stackSize := range []int{0, maxUndoStackSize + 5} {
		t.Run(strconv.Itoa(stackSize), func(t *testing.T) {
			root := t.TempDir()
			writer := NewFileWrite(root, stackSize)
			for index := 0; index < maxUndoStackSize+2; index++ {
				if _, err := writer.Execute(context.Background(), mustJSONWrite("file.txt", strconv.Itoa(index))); err != nil {
					t.Fatalf("write %d: %v", index, err)
				}
			}
			resolvedRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatalf("EvalSymlinks: %v", err)
			}
			entries := writer.History().entries[filepath.Join(resolvedRoot, "file.txt")]
			if len(entries) != maxUndoStackSize {
				t.Fatalf("undo entries = %d, want hard cap %d", len(entries), maxUndoStackSize)
			}
		})
	}
}

func TestFileWriteLargeReplacementKeepsDiffBounded(t *testing.T) {
	root := t.TempDir()
	writer := NewFileWrite(root)
	oldContent := strings.Repeat("old\n", 1500)
	newContent := strings.Repeat("new\n", 1500)
	if _, err := writer.Execute(context.Background(), mustJSONWrite("large.txt", oldContent)); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	same, err := writer.Execute(context.Background(), mustJSONWrite("large.txt", oldContent))
	if err != nil || !same.Success || same.Additions != 0 || same.Deletions != 0 || same.Output != "" {
		t.Fatalf("identical replacement = (%+v, %v), want empty diff", same, err)
	}
	res, err := writer.Execute(context.Background(), mustJSONWrite("large.txt", newContent))
	if err != nil || !res.Success {
		t.Fatalf("replacement = (%+v, %v), want success", res, err)
	}
	if res.Additions != 1500 || res.Deletions != 1500 {
		t.Fatalf("diff stats = +%d -%d, want +1500 -1500", res.Additions, res.Deletions)
	}
}

func TestFileWriteUnifiedDiffMarksMissingFinalNewlines(t *testing.T) {
	tests := []struct {
		name        string
		oldContent  string
		newContent  string
		wantMarkers int
	}{
		{name: "neither has final newline", oldContent: "before", newContent: "after", wantMarkers: 2},
		{name: "only old has final newline", oldContent: "before\n", newContent: "after", wantMarkers: 1},
		{name: "only new has final newline", oldContent: "before", newContent: "after\n", wantMarkers: 1},
		{name: "both have final newline", oldContent: "before\n", newContent: "after\n", wantMarkers: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte(tt.oldContent), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := NewFileWrite(root).Execute(context.Background(), mustJSONWrite("file.txt", tt.newContent))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := strings.Count(res.Output, "\\ No newline at end of file"); got != tt.wantMarkers {
				t.Errorf("marker count = %d, want %d; diff = %q", got, tt.wantMarkers, res.Output)
			}
		})
	}
}

func TestFileWriteRejectsConcurrentAncestorSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	subdirectory := filepath.Join(root, "sub")
	insideBackup := filepath.Join(root, "sub-backup")
	if err := os.Mkdir(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writer := NewFileWrite(root)

	stop := make(chan struct{})
	var swaps sync.WaitGroup
	swaps.Add(1)
	go func() {
		defer swaps.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := swapAncestor(subdirectory, insideBackup, outside); err != nil {
				continue
			}
		}
	}()

	for index := 0; index < 200; index++ {
		if _, err := writer.Execute(context.Background(), mustJSONWrite("sub/file.txt", strconv.Itoa(index))); err != nil {
			continue
		}
	}
	close(stop)
	swaps.Wait()
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); err == nil {
		t.Fatal("concurrent ancestor swap caused a write outside the workspace")
	}
	restoreAncestor(t, subdirectory, insideBackup)
}

func TestFileDiffRejectsConcurrentAncestorSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	subdirectory := filepath.Join(root, "sub")
	insideBackup := filepath.Join(root, "sub-backup")
	if err := os.Mkdir(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdirectory, "file.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "file.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	differ := NewFileDiff(root)

	stop := make(chan struct{})
	var swaps sync.WaitGroup
	swaps.Add(1)
	go func() {
		defer swaps.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := swapAncestor(subdirectory, insideBackup, outside); err != nil {
				continue
			}
		}
	}()

	for index := 0; index < 200; index++ {
		if _, err := differ.Execute(context.Background(), json.RawMessage(`{"path":"sub/file.txt"}`)); err != nil {
			continue
		}
	}
	close(stop)
	swaps.Wait()
	contents, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatalf("ReadFile outside target: %v", err)
	}
	if string(contents) != "outside" {
		t.Fatalf("outside target changed to %q", contents)
	}
	restoreAncestor(t, subdirectory, insideBackup)
}

func TestFileWriteUndoStackHonorsConfiguredSize(t *testing.T) {
	root := t.TempDir()
	writer := NewFileWrite(root, 2)
	for index, content := range []string{"one", "two", "three"} {
		params := mustJSONWrite("file.txt", content)
		if _, err := writer.Execute(context.Background(), params); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
	}

	// The third write retains only the previous two contents: "one" was
	// evicted, so a new diff begins at "two" rather than "one".
	differ := NewFileDiff(root, writer.History())
	res, err := differ.Execute(context.Background(), json.RawMessage(`{"path":"file.txt"}`))
	if err != nil {
		t.Fatalf("file_diff: %v", err)
	}
	if strings.Contains(res.Output, "one") || !strings.Contains(res.Output, "two") || !strings.Contains(res.Output, "three") {
		t.Errorf("file_diff output = %q, want only retained previous version and current contents", res.Output)
	}
}

func TestFileDiffWithoutUndoEntryReportsNewFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("full contents\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := NewFileDiff(root).Execute(context.Background(), json.RawMessage(`{"path":"new.txt"}`))
	if err != nil || !res.Success {
		t.Fatalf("Execute = (%+v, %v), want success", res, err)
	}
	if !strings.Contains(res.Output, "--- /dev/null") || !strings.Contains(res.Output, "+full contents") {
		t.Errorf("result output = %q, want new-file diff with full contents", res.Output)
	}
}

func swapAncestor(subdirectory, backup, outside string) error {
	if err := os.Rename(subdirectory, backup); err != nil {
		return err
	}
	if err := os.Symlink(outside, subdirectory); err != nil {
		if restoreErr := os.Rename(backup, subdirectory); restoreErr != nil {
			return fmt.Errorf("create symlink: %v; restore directory: %w", err, restoreErr)
		}
		return err
	}
	if err := os.Remove(subdirectory); err != nil {
		return err
	}
	return os.Rename(backup, subdirectory)
}

func restoreAncestor(t *testing.T, subdirectory, backup string) {
	t.Helper()
	info, err := os.Lstat(subdirectory)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(subdirectory); err != nil {
			t.Fatalf("remove temporary symlink: %v", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("inspect swapped ancestor: %v", err)
	}
	if _, err := os.Stat(backup); err == nil {
		if info, statErr := os.Lstat(subdirectory); statErr == nil && info.IsDir() {
			if removeErr := os.RemoveAll(subdirectory); removeErr != nil {
				t.Fatalf("remove temporary ancestor: %v", removeErr)
			}
		} else if statErr != nil && !os.IsNotExist(statErr) {
			t.Fatalf("inspect temporary ancestor: %v", statErr)
		}
		if err := os.Rename(backup, subdirectory); err != nil {
			t.Fatalf("restore swapped ancestor: %v", err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect ancestor backup: %v", err)
	}
}

func mustJSONWrite(path, content string) json.RawMessage {
	return json.RawMessage(`{"path":` + strconv.Quote(path) + `,"content":` + strconv.Quote(content) + `}`)
}
