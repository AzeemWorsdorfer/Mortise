// Package daemon — doc.go describes the package's role in the system.
//
// The daemon package owns the Mortise daemon's runtime: the Unix socket
// listener, the Connect-RPC handler, the broadcast bus for ServerEvents,
// and the graceful-shutdown state machine.
//
// Responsibilities:
//   - Bind a Unix domain socket and accept TUI client connections.
//   - Dispatch incoming ClientCommands (ticket 02 only logs them).
//   - Broadcast a SystemStatus event on every successful connect.
//   - Track connected-client count for SystemStatus reporting.
//
// Is NOT responsible for:
//   - The agent loop itself — that lives in the agent package (ticket 03+).
//   - Tool execution — that lives in the tools package (ticket 06+).
//   - Persisting sessions — that lives in the session package.
//   - Provider selection — that lives in the providers package.
//
// See: docs/specs/01-core-agent-harness.md §4.4, §5
package daemon
