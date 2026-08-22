---
status: ready-for-human
blocked-by: []
---

# 07 — Tool Interface, Registry & File Read

**What to build:** The Go `Tool` interface and `ToolRegistry` — the abstraction that all tools (built-in, custom, MCP) conform to. Implement the first real built-in tool: `file_read`. The agent loop's mock provider is replaced: when the agent proposes a file_read tool call, the real tool executes and the agent loop processes the result. The TUI Trace Session panel renders the tool call with name, input path, output snippet, and duration.

## What to build

### Tool interface

```go
type Tool interface {
    Name() string
    Description() string
    ParameterSchema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error)
}
```

- `Name()` — unique identifier, e.g., `"file_read"`, `"file_write"`
- `Description()` — human-readable, used in system prompts and TUI display
- `ParameterSchema()` — JSON Schema for the tool's parameters (used for model tool definitions and validation)
- `Execute()` — runs the tool with parsed parameters, returns a result

### ToolResult

```go
type ToolResult struct {
    Success      bool
    Output       string
    FilesChanged []string
    DurationMs   int64
    Error        error
}
```

### ToolRegistry

```go
type ToolRegistry struct {
    builtIn map[string]Tool
    mu      sync.RWMutex
}
```

- `Register(tool Tool)` — add a built-in tool (panics on duplicate name)
- `Get(name string) (Tool, bool)` — lookup by name
- `List() []ToolDefinition` — returns all registered tools in the format the model expects
- `Execute(ctx, name, paramsJSON) (*ToolResult, error)` — lookup + execute in one call

### file_read tool

- **Name:** `file_read`
- **Parameters:** `{ "path": { "type": "string", "description": "Path relative to workspace root" } }`
- **Risk:** `RiskSafe`
- **Behavior:** Reads the file at `workspace_root + "/" + path`. Returns file contents as UTF-8 string. Returns error if file doesn't exist, is a directory, or is outside workspace boundary.
- **Workspace boundary:** Resolve the absolute path. If it does not start with `workspace_root + "/"`, reject with an error. Symlinks are resolved before boundary check.

### Integration with AgentLoop

Replace the mock tool execution in the agent loop with real tool execution:

- When the provider emits `EventToolCall`, the agent loop calls `toolRegistry.Execute(ctx, toolName, paramsJSON)`
- `ToolCallCompleted` event populates `success`, `result_summary` (first 200 chars of output), `duration_ms`, and `error_message`
- Tool results are fed back to the provider in the next turn as a tool result message

### TUI Trace Session panel

The TUI receives `ToolCallPending` and `ToolCallCompleted` events and renders them in a timeline:

```
TRACE SESSION
─────────────────
  [Turn 1] file_read
    Input:  package.json
    Result: {
              "name": "mortise",
              ...
            }
    Duration: 2ms  ✓
```

## Acceptance criteria

- [x] `ToolRegistry.Register` panics on duplicate tool name
- [x] `file_read` returns file contents when given a valid path within the workspace
- [x] `file_read` returns an error for paths outside the workspace root (including symlink escapes)
- [x] `file_read` returns an error for non-existent files
- [x] Agent loop with mock provider calls file_read → real execution happens → `ToolCallCompleted` with `success=true` and file contents in `result_summary`
- [x] TUI renders at least one file_read call in the trace panel with name, input, output, duration, and success status
- [x] `ToolResult.DurationMs` is accurate (measured from Execute start to return)
