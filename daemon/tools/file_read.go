package tools

import (
	"context"
	"encoding/json"
	"fmt"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// FileRead is the built-in file_read tool: reads a file relative to
// the workspace root and returns its contents as a UTF-8 string.
// Reads outside the workspace boundary (including through symlinks)
// are rejected. Risk level: Safe (read-only).
type FileRead struct {
	workspaceRoot string
}

// NewFileRead constructs a file_read tool bound to workspaceRoot.
func NewFileRead(workspaceRoot string) *FileRead {
	return &FileRead{workspaceRoot: workspaceRoot}
}

// Name implements Tool.
func (f *FileRead) Name() string { return "file_read" }

// Description implements Tool.
func (f *FileRead) Description() string {
	return "Read a file at the given path relative to the workspace root and return its contents."
}

// ParameterSchema implements Tool.
func (f *FileRead) ParameterSchema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path relative to workspace root"
    }
  },
  "required": ["path"]
}`)
}

// Risk implements Tool.
func (f *FileRead) Risk() mortisev1.RiskLevel { return mortisev1.RiskLevel_SAFE }

// fileReadParams is the typed form of the tool's JSON parameters.
type fileReadParams struct {
	Path string `json:"path"`
}

// Execute implements Tool. It opens the requested file through the
// descriptor-anchored workspace helper, which keeps validation and the
// eventual read bound to the same filesystem objects.
func (f *FileRead) Execute(_ context.Context, params json.RawMessage) (*ToolResult, error) {
	var p fileReadParams
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" {
		return nil, fmt.Errorf("file_read: params must include a \"path\" string")
	}

	content, _, _, err := readWorkspaceFile(f.workspaceRoot, p.Path, "file_read", false)
	if err != nil {
		return nil, err
	}
	return &ToolResult{Success: true, Output: content}, nil
}
