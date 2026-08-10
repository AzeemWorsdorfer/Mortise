// Package session — owns the lifecycle and persistence of a Mortise
// Session, from domain type to SQLite row.
//
// The package is split across three files:
//
//   - session.go: the Session struct, SessionStatus enum, and the
//     lifecycle state machine. Pure Go, no database dependency. This
//     is the canonical definition of "what a session is".
//
//   - store.go: the SQLite-backed Store, including schema migration
//     and CRUD methods. Owns the database/sql connection pool.
//
//   - path.go: filesystem helpers (parent-dir creation) shared by
//     Open and any future code that needs to write to the same
//     directory.
//
// Responsibilities:
//   - Define and enforce the documented session lifecycle.
//   - Generate deterministic session IDs (UUIDv5) from workspace
//     paths so a workspace can always recover its prior session.
//   - Persist sessions to SQLite and migrate the schema in place
//     across daemon versions.
//
// Is NOT responsible for:
//   - The agent loop or turn planning (that lives in the agent
//     package; later ticket).
//   - Storing conversation history or the JSONL event log (eventlog
//     package; later ticket).
//   - Provider selection or credentials (providers package).
//
// See: docs/specs/01-core-agent-harness.md §3.2, §3.3
package session
