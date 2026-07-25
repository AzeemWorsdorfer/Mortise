---
status: ready-for-agent
blocked-by: ["02-go-daemon-scaffold"]
---

# 04 — Session Model & Lifecycle

**What to build:** The Go `Session` struct and its lifecycle state machine. A session is created when the daemon starts (or when a user creates one), transitions through `Created → Running → Paused → Completed`, and persists its metadata to SQLite. The SystemStatus event now reflects real session data instead of placeholder values.

## What to build

### Session struct

Implement the `Session` type in Go as specified in the spec §3.3:

```go
type Session struct {
    ID            string
    Name          string
    Status        SessionStatus
    WorkspacePath string
    Branch        string
    Messages      []Message
    TurnCount     int
    CreatedAt     time.Time
    LastActiveAt  time.Time
    Config        *Config
}
```

- `ID` is a UUID generated on creation
- `Name` defaults to the workspace directory name, renameable
- `WorkspacePath` is the absolute path of the workspace directory
- `Branch` is detected from git (if in a repo) or empty

### SessionStatus enum

```go
type SessionStatus int
const (
    SessionCreated SessionStatus = iota
    SessionRunning
    SessionPaused
    SessionCompleted
    SessionErrored
    SessionCrashed
)
```

Transitions:
- `Created → Running` (immediately on daemon start)
- `Running → Paused` (user interrupts) → `Running` (user resumes)
- `Running → Completed` (agent finishes)
- `Running → Errored` (unrecoverable error) → `Running` (after fix + resume)
- `Running → Crashed` (daemon killed) → `Running` (auto-resume on restart)

Only valid transitions are allowed; invalid transitions log a warning and are no-ops.

### SQLite persistence

- Table `sessions`: `id TEXT PRIMARY KEY`, `name TEXT`, `status INTEGER`, `workspace_path TEXT`, `branch TEXT`, `turn_count INTEGER`, `created_at TEXT`, `last_active_at TEXT`, `config_json TEXT`
- On daemon start: create the session row (insert or update if session exists)
- On status change: update the row
- On daemon shutdown: update `last_active_at`

### SystemStatus update

The `SystemStatus` event sent on client connect now reads from the live Session object:
- `session_id`: Session.ID
- `session_name`: Session.Name
- `workspace_path`: Session.WorkspacePath
- `branch`: Session.Branch
- All other fields remain as-is for now (tokens/cost/turns handled in later tickets)

### Multiple sessions (future consideration)

For v1, the daemon manages one active session. Starting the daemon with a different `--workspace` flag creates a different session. The schema and code should be written to support querying by session ID so multi-session support (later spec) requires only a query change, not a schema migration.

## Acceptance criteria

- [ ] Daemon starts → session row appears in SQLite with status `Running`
- [ ] TUI connecting shows live session ID and workspace path in SystemStatus (not placeholder values)
- [ ] Session status transitions through `Created → Running → Paused → Running → Completed` and each transition is reflected in SQLite
- [ ] Invalid transition (e.g., `Completed → Running`) is logged and ignored
- [ ] Daemon restart with same workspace path loads existing session from SQLite
