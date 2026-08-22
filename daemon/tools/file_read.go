package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// Execute implements Tool. It resolves the requested path against
// the workspace root, verifies the result stays inside the
// boundary after symlink resolution, then reads the file.
func (f *FileRead) Execute(_ context.Context, params json.RawMessage) (*ToolResult, error) {
	var p fileReadParams
	if err := json.Unmarshal(params, &p); err != nil || p.Path == "" {
		return nil, fmt.Errorf("file_read: params must include a \"path\" string")
	}

	resolvedRoot, err := filepath.EvalSymlinks(f.workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("file_read: workspace root %q does not exist", f.workspaceRoot)
	}
	rootPrefix := resolvedRoot + string(filepath.Separator)

	// Lexical containment first: reject paths that climb out of
	// the workspace before any file system lookups happen.
	target := filepath.Join(resolvedRoot, p.Path)
	switch {
	case target == resolvedRoot:
		return nil, fmt.Errorf("file_read: %q is the workspace root itself; pass a file path relative to it", p.Path)
	case !strings.HasPrefix(target, rootPrefix):
		return nil, fmt.Errorf("file_read: %q resolves outside the workspace root", p.Path)
	}

	// Symlink resolution second: reject links that escape even if
	// the lexical path stayed inside.
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file_read: %q does not exist in the workspace", p.Path)
		}
		return nil, fmt.Errorf("file_read: resolving %q: %w", p.Path, err)
	}

	if !strings.HasPrefix(resolved, rootPrefix) {
		return nil, fmt.Errorf("file_read: %q resolves outside the workspace root", p.Path)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("file_read: stat %q: %w", p.Path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("file_read: %q is a directory, not a file", p.Path)
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("file_read: reading %q: %w", p.Path, err)
	}

	return &ToolResult{Success: true, Output: string(content)}, nil
}
