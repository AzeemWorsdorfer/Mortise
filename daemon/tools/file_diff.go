// Package tools provides the workspace-scoped file_diff tool and diff engine.
//
// Responsibilities:
//   - Read a workspace file through descriptor-anchored resolution
//   - Compare current contents with the latest in-memory write history
//   - Produce unified diffs and line-change statistics
//
// Is NOT responsible for writing files, approval policy, or persistent history.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

type filePathParams struct {
	Path string `json:"path"`
}

// FileDiff is the built-in file_diff tool bound to a workspace root.
type FileDiff struct {
	workspaceRoot string
	history       *FileHistory
}

// NewFileDiff constructs a file_diff tool. An optional FileWrite history lets
// callers explicitly share history; otherwise the workspace history is used.
func NewFileDiff(workspaceRoot string, sharedHistory ...*FileHistory) *FileDiff {
	history := workspaceHistory(workspaceRoot, defaultUndoStackSize)
	if len(sharedHistory) > 0 && sharedHistory[0] != nil {
		history = sharedHistory[0]
	}
	return &FileDiff{workspaceRoot: workspaceRoot, history: history}
}

// Name implements Tool.
func (f *FileDiff) Name() string { return "file_diff" }

// Description implements Tool.
func (f *FileDiff) Description() string {
	return "Show the unified diff between a file's current contents and its last written version."
}

// ParameterSchema implements Tool.
func (f *FileDiff) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to workspace root"}
  },
  "required": ["path"]
}`)
}

// Risk implements Tool.
func (f *FileDiff) Risk() mortisev1.RiskLevel { return mortisev1.RiskLevel_SAFE }

// Execute returns the current file's diff against the latest saved version, or
// treats the current file as a new file when no history entry exists.
func (f *FileDiff) Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
	var p filePathParams
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" {
		return failedResult(fmt.Errorf("file_diff: params must include a \"path\" string"))
	}
	if err := ctx.Err(); err != nil {
		return failedResult(err)
	}
	current, target, displayPath, _, err := readWorkspaceFile(f.workspaceRoot, p.Path, "file_diff", false)
	if err != nil {
		return failedResult(err)
	}
	previous, previousExists, found := f.history.last(target)
	if !found {
		previous = ""
		previousExists = false
	}
	additions, deletions := diffStats(previous, current)
	return &ToolResult{Success: true, Output: unifiedDiff(displayPath, previous, current, previousExists), Additions: additions, Deletions: deletions}, nil
}

func unifiedDiff(path, oldContent, newContent string, oldExists bool) string {
	oldLines := splitDiffLines(oldContent)
	newLines := splitDiffLines(newContent)
	operations := diffOperations(oldLines, newLines)
	additions, deletions := countDiffStats(operations)
	if additions == 0 && deletions == 0 {
		return ""
	}
	oldHeader := "--- a/" + path
	if !oldExists {
		oldHeader = "--- /dev/null"
	}
	newHeader := "+++ b/" + path
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n%s\n@@ -%d,%d +%d,%d @@\n", oldHeader, newHeader,
		diffStart(len(oldLines)), len(oldLines), diffStart(len(newLines)), len(newLines))
	for _, operation := range operations {
		out.WriteByte(operation.kind)
		out.WriteString(operation.line)
		if !strings.HasSuffix(operation.line, "\n") {
			out.WriteByte('\n')
			out.WriteString("\\ No newline at end of file\n")
		}
	}
	return out.String()
}

func diffStats(oldContent, newContent string) (int, int) {
	return countDiffStats(diffOperations(splitDiffLines(oldContent), splitDiffLines(newContent)))
}

func countDiffStats(operations []diffOperation) (int, int) {
	additions, deletions := 0, 0
	for _, operation := range operations {
		switch operation.kind {
		case '+':
			additions++
		case '-':
			deletions++
		}
	}
	return additions, deletions
}

func diffStart(lineCount int) int {
	if lineCount == 0 {
		return 0
	}
	return 1
}

type diffOperation struct {
	kind byte
	line string
}

func splitDiffLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

const maxDiffMatrixCells = 1_000_000

func diffOperations(oldLines, newLines []string) []diffOperation {
	if len(oldLines) == 0 || len(newLines) == 0 || len(oldLines) > maxDiffMatrixCells/len(newLines) {
		return replacementOperations(oldLines, newLines)
	}
	rows := len(oldLines) + 1
	cols := len(newLines) + 1
	lcs := make([][]int, rows)
	for i := range lcs {
		lcs[i] = make([]int, cols)
	}
	for oldIndex := len(oldLines) - 1; oldIndex >= 0; oldIndex-- {
		for newIndex := len(newLines) - 1; newIndex >= 0; newIndex-- {
			if oldLines[oldIndex] == newLines[newIndex] {
				lcs[oldIndex][newIndex] = 1 + lcs[oldIndex+1][newIndex+1]
			} else if lcs[oldIndex+1][newIndex] >= lcs[oldIndex][newIndex+1] {
				lcs[oldIndex][newIndex] = lcs[oldIndex+1][newIndex]
			} else {
				lcs[oldIndex][newIndex] = lcs[oldIndex][newIndex+1]
			}
		}
	}
	operations := make([]diffOperation, 0, len(oldLines)+len(newLines))
	for oldIndex, newIndex := 0, 0; oldIndex < len(oldLines) || newIndex < len(newLines); {
		switch {
		case oldIndex < len(oldLines) && newIndex < len(newLines) && oldLines[oldIndex] == newLines[newIndex]:
			operations = append(operations, diffOperation{' ', oldLines[oldIndex]})
			oldIndex++
			newIndex++
		case newIndex < len(newLines) && (oldIndex == len(oldLines) || lcs[oldIndex][newIndex+1] >= lcs[oldIndex+1][newIndex]):
			operations = append(operations, diffOperation{'+', newLines[newIndex]})
			newIndex++
		default:
			operations = append(operations, diffOperation{'-', oldLines[oldIndex]})
			oldIndex++
		}
	}
	return operations
}

func replacementOperations(oldLines, newLines []string) []diffOperation {
	operations := make([]diffOperation, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines {
		operations = append(operations, diffOperation{'-', line})
	}
	for _, line := range newLines {
		operations = append(operations, diffOperation{'+', line})
	}
	return operations
}
