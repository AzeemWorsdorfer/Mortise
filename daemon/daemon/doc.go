// Package daemon — doc.go describes the package's role in the system.
//
// The daemon package owns the Mortise daemon's runtime: the Unix socket
// listener, the Connect-RPC handler, the EventBus that fans
// ServerEvents out to every connected TUI client, and the
// graceful-shutdown state machine.
//
// Responsibilities:
//   - Bind a Unix domain socket and accept TUI client connections.
//   - Subscribe every accepted client to the EventBus and pump
//     events from the bus into the client's stream.
//   - Start the agent loop on the first client connect (demo mode
//     with a mock provider — ticket 06) and stream its events to
//     every connected client.
//   - Dispatch incoming ClientCommands (pause / resume / cancel /
//     switch_provider / handoff / approve / reject) — currently only
//     logged; routing them into the agent loop is a later ticket.
//   - Publish a SystemStatus event through the bus on every
//     successful connect so all observers see fresh state.
//   - Track connected-client count for SystemStatus reporting via
//     the bus's subscriber count.
//
// Is NOT responsible for:
//   - The agent loop implementation — that lives in the agent
//     package; the daemon only constructs the loop and owns its
//     lifecycle (Serve / StartAgent), not the state machine itself.
//   - Tool execution — that lives in the tools package (later ticket).
//   - Persisting sessions — that lives in the session package.
//   - Provider selection — that lives in the providers package.
//
// See: docs/specs/01-core-agent-harness.md §4.4, §5
package daemon
