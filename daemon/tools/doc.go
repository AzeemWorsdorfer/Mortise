// Package tools implements Mortise's Tool execution domain: the
// Tool interface that all tools (built-in, custom, MCP) conform to,
// the ToolRegistry that owns them, and the built-in file tools
// (file_read, file_write, file_diff) with descriptor-anchored
// workspace sandboxing, write previews, and a per-file undo history.
//
// Responsibilities:
//   - Define the Tool abstraction every callable action conforms to:
//     name, description, parameter JSON Schema, risk level, and
//     Execute.
//   - Own the ToolRegistry: registration, lookup by name, listing
//     tool definitions for provider requests, and execution with
//     wall-clock duration measurement.
//   - Implement the built-in file tools (file_read, file_write,
//     file_diff) with workspace-root sandboxing, write previews,
//     and a per-file undo history.
//   - Anchor every file access to the file objects verified during
//     path resolution (see workspace_path.go and workspace_file.go).
//
// Is NOT responsible for:
//   - The agent loop or its phase state machine (agent package);
//     tools are invoked by the loop, never the other way around.
//   - Approval policy decisions — the registry reports each tool's
//     Risk, but the confirm/auto policy and approval flow live in
//     the agent and daemon packages (ticket 11).
//   - Provider or MCP negotiation — remote tools register through
//     the same Tool interface (ticket 13+); this package never talks
//     to a provider directly.
//   - Path traversal beyond the workspace boundary is rejected, not
//     implemented — file tools enforce the sandbox, they do not
//     define a general filesystem API.
//
// See: docs/specs/01-core-agent-harness.md §3.3, tickets 07-08 —
// Tool Interface, Registry & File Operations.
package tools
