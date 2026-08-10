// Package agent owns the Mortise agent loop: the phase-aware state
// machine that drives the core Planning → Acting → Observing →
// Deciding cycle. Every turn negotiates with a Provider (the LLM
// adapter) and emits ServerEvents through the daemon's EventBus so
// connected TUI clients render live phase transitions, thinking
// chunks, agent text, and tool-call status.
//
// This package is the beating heart of Mortise. All later tickets —
// tools, approval gates, context loading, handoff — plug into the
// seams exposed here.
//
//   - provider.go:   the Provider interface, event types, and request
//     struct consumed by AgentLoop
//   - agent.go:      the AgentLoop struct, the phase state machine,
//     and the Run() method
//   - mock_provider.go: a MockProvider that returns configurable
//     canned sequences without a real API call
//
// The package does NOT own:
//   - Tool execution (stubbed here, real in ticket 07+)
//   - Approval gates (ticket 11)
//   - Persistence (session package)
//   - The EventBus (daemon package owns it; agent publishes through
//     the EventPublisher interface)
//
// See: docs/specs/01-core-agent-harness.md §3.1, §3.3
package agent
