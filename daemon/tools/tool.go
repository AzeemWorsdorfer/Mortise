// Package tools implements Mortise's Tool execution domain: the
// Tool interface that all tools (built-in, custom, MCP) conform to,
// the ToolRegistry that owns them, and the built-in tools.
//
// See: tickets 07-08 — Tool Interface, Registry & File Operations.
package tools

import (
	"context"
	"encoding/json"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// Tool is the abstraction every callable action the agent can
// invoke conforms to: built-in tools, config-defined custom tools,
// and MCP-discovered tools.
type Tool interface {
	// Name is the unique identifier the model uses to call the
	// tool, e.g. "file_read", "file_write".
	Name() string

	// Description is a human-readable summary used in system
	// prompts and TUI display.
	Description() string

	// ParameterSchema is the JSON Schema for the tool's
	// parameters, sent to the model as its tool definition and
	// used for validation.
	ParameterSchema() json.RawMessage

	// Risk reports the tool's risk level for the Approval Gate.
	Risk() mortisev1.RiskLevel

	// Execute runs the tool with the raw JSON parameters.
	Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error)
}

// ToolResult is the outcome of one tool execution. DurationMs is
// measured by the ToolRegistry around Execute; individual tools do
// not set it themselves. Additions and Deletions describe a
// file diff when the tool produces one.
type ToolResult struct {
	Success      bool
	Output       string
	FilesChanged []string
	Additions    int
	Deletions    int
	DurationMs   int64
	Error        error
}

func failedResult(err error) (*ToolResult, error) {
	return &ToolResult{Success: false, Error: err}, err
}
