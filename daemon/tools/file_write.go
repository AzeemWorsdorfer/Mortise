// Package tools provides the workspace-scoped file_write tool.
//
// Responsibilities:
//   - Validate file_write parameters and execute workspace writes
//   - Produce write previews and record previous contents in shared history
//
// Is NOT responsible for path traversal, diff algorithms, approval policy, or
// persistent undo storage; those concerns belong to workspace, diff, agent,
// and session layers.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// FileWrite is the built-in file_write tool bound to a workspace root.
type FileWrite struct {
	workspaceRoot string
	history       *FileHistory
}

// NewFileWrite constructs a file_write tool. An optional stack size overrides
// the default of ten previous contents per file and is capped at ten.
func NewFileWrite(workspaceRoot string, stackSize ...int) *FileWrite {
	return &FileWrite{
		workspaceRoot: workspaceRoot,
		history:       workspaceHistory(workspaceRoot, configuredStackSize(stackSize)),
	}
}

// History returns the in-memory history used by this file-write tool.
func (f *FileWrite) History() *FileHistory { return f.history }

// Preview computes a file-write diff without mutating the workspace.
func (f *FileWrite) Preview(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
	var p fileWriteParams
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" || p.Content == nil {
		return failedResult(fmt.Errorf("file_write: params must include a \"path\" string and \"content\" string"))
	}
	if err := ctx.Err(); err != nil {
		return failedResult(err)
	}
	current, _, displayPath, exists, err := readWorkspaceFile(f.workspaceRoot, p.Path, "file_write", true)
	if err != nil {
		return failedResult(err)
	}
	additions, deletions := diffStats(current, *p.Content)
	return &ToolResult{
		Success:      true,
		Output:       unifiedDiff(displayPath, current, *p.Content, exists),
		FilesChanged: []string{displayPath},
		Additions:    additions,
		Deletions:    deletions,
	}, nil
}

// Name implements Tool.
func (f *FileWrite) Name() string { return "file_write" }

// Description implements Tool.
func (f *FileWrite) Description() string {
	return "Write content to a file relative to the workspace root and return a unified diff."
}

// ParameterSchema implements Tool.
func (f *FileWrite) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to workspace root"},
    "content": {"type": "string", "description": "Complete file contents"}
  },
  "required": ["path", "content"]
}`)
}

// Risk implements Tool.
func (f *FileWrite) Risk() mortisev1.RiskLevel { return mortisev1.RiskLevel_MODIFIES_FILES }

type fileWriteParams struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}

// Execute writes a workspace file, records its previous contents, and returns
// the resulting unified diff and workspace-relative changed path.
func (f *FileWrite) Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error) {
	var p fileWriteParams
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" || p.Content == nil {
		return failedResult(fmt.Errorf("file_write: params must include a \"path\" string and \"content\" string"))
	}
	if err := ctx.Err(); err != nil {
		return failedResult(err)
	}

	target, displayPath, previous, exists, err := writeWorkspaceFile(f.workspaceRoot, p.Path, *p.Content)
	if err != nil {
		return failedResult(err)
	}
	f.history.push(historyTargetPath(f.workspaceRoot, target), previous, exists)
	diff := unifiedDiff(displayPath, previous, *p.Content, exists)
	additions, deletions := diffStats(previous, *p.Content)
	return &ToolResult{
		Success:      true,
		Output:       diff,
		FilesChanged: []string{displayPath},
		Additions:    additions,
		Deletions:    deletions,
	}, nil
}

func configuredStackSize(stackSize []int) int {
	if len(stackSize) == 0 || stackSize[0] <= 0 {
		return defaultUndoStackSize
	}
	return min(stackSize[0], maxUndoStackSize)
}
