---
status: completed
blocked-by: ["02-go-daemon-scaffold"]
---

# 04 — Session Model & Lifecycle

**What to build:** The Go `Session` domain type, its lifecycle state machine, and full SQLite persistence. On daemon startup, a session is created (or loaded if restarting), persisted to the `sessions` table, and the `SystemStatus` event reads live session data instead of placeholder values. The TUI sees real session identity.

## What to build

### 1. Session domain type (`daemon/session/session.go` — new file)

Implement the `Session` struct and `SessionStatus` enum per spec §3.3:

```go
type Session struct {
    ID            string
    Name          string
    Status        SessionStatus
    WorkspacePath string
    Branch        string
    TurnCount     int
    CreatedAt     time.Time
    LastActiveAt  time.Time
    ConfigJSON    string
}

type SessionStatus int
const (
    StatusCreated SessionStatus = iota
    StatusRunning
    StatusPaused
    StatusCompleted
    StatusErrored
    StatusCrashed
)
```

- `ID` is a deterministic UUIDv5 derived from the workspace path (using `uuid.NameSpaceDNS` namespace). Same workspace → same session ID every time, which enables restart recovery.
- `Name` defaults to the workspace directory basename, renameable.
- `ConfigJSON` stores the serialized merged config as opaque JSON. The session package doesn't parse it.

**SessionStatus transitions:**

```
Created → Running
Running → Paused  → Running
Running → Completed
Running → Errored → Running
Running → Crashed → Running
```

Add a `Transition(newStatus SessionStatus) error` method. Invalid transitions return a descriptive error (logged as a warning, never panic). Only valid transitions are allowed.

### 2. SQLite schema migration (`daemon/session/store.go`)

The existing `sessions` table (ticket 02) has 4 columns: `id`, `status`, `workspace_path`, `created_at`. Add 5 more columns via a column-by-column migration that checks `PRAGMA table_info` before each `ALTER TABLE ... ADD COLUMN`. This is idempotent and safe for databases created by ticket 02.

New columns:

| Column | Type | Notes |
|--------|------|-------|
| `name` | `TEXT NOT NULL DEFAULT ''` | |
| `branch` | `TEXT NOT NULL DEFAULT ''` | |
| `turn_count` | `INTEGER NOT NULL DEFAULT 0` | |
| `last_active_at` | `INTEGER NOT NULL DEFAULT 0` | Unix timestamp |
| `config_json` | `TEXT NOT NULL DEFAULT ''` | |

### 3. CRUD methods on Store (`daemon/session/store.go`)

| Method | Purpose |
|--------|---------|
| `CreateSession(ctx, *Session) error` | `INSERT INTO sessions` |
| `GetSession(ctx, id string) (*Session, error)` | `SELECT` + row scan. Returns wrapped `sql.ErrNoRows` when not found. |
| `UpdateSession(ctx, *Session) error` | `UPDATE` all mutable fields + refreshes `LastActiveAt` |
| `UpdateSessionStatus(ctx, id string, status SessionStatus) error` | Atomic status-only update |

`CreateSession` generates the session UUID (UUIDv5 from workspace) before inserting. The caller provides the other fields.

### 4. Wire Store into Daemon (`daemon/daemon/daemon.go`)

Add a `Store *session.Store` field to `Daemon`. Add a `Session() *session.Session` method that returns the current session. The daemon holds the reference but does not own session creation — that stays in `main.go`.

**Remove** the `NewSessionID` field and `randomSessionID` function — they're replaced by the real session.

### 5. Session creation in `run()` (`daemon/cmd/mortised/main.go`)

In `run()`, after `openStore()`:

1. Compute deterministic session ID from workspace path (UUIDv5).
2. Try `store.GetSession(id)`.
3. If not found: create a new `Session` with status `StatusCreated`, then immediately transition to `StatusRunning` and persist via `store.CreateSession`.
4. If found: transition to `StatusRunning` (idempotent if already running — crash recovery) and persist via `store.UpdateSession`.
5. Pass `*session.Store` to `buildDaemon` so the daemon can serve it to the handler.

### 6. Handler reads from session (`daemon/daemon/handler.go`)

In `sendSystemStatus`:
- Read `session` from `d.Session()`.
- `SessionId` → `session.ID`
- `SessionName` → `session.Name`
- Remove the `newID` field from `ConnectHandler` (no longer needed).
- All other fields remain as-is.

### 7. Cleanup

- Remove `NewSessionID` field from `Daemon`.
- Remove `randomSessionID` function.
- Remove `newID` field from `ConnectHandler`.

## Acceptance criteria

- [ ] Daemon starts → session row inserted in SQLite with status `Running`, name derived from workspace dir, and deterministic UUIDv5 ID
- [ ] TUI connecting shows live session ID and workspace path in SystemStatus (not `"untitled"` or throwaway UUID)
- [ ] Session status transitions through `Created → Running → Paused → Running → Completed` and each transition is reflected in SQLite
- [ ] Invalid transition (e.g., `Completed → Running`) returns an error and is logged as a warning
- [ ] Daemon restart with same workspace path loads existing session from SQLite (same ID, same name, same workspace)
- [ ] All 9 columns exist in the `sessions` table after migration (existing DBs from ticket 02 are upgraded)
- [ ] `GetSession` on a non-existent ID returns a distinguishable "not found" error

## Files

| File | Action |
|------|--------|
| `daemon/session/session.go` | **New** — Session type, SessionStatus, Transition |
| `daemon/session/session_test.go` | **New** — transition tests |
| `daemon/session/store.go` | Add migration + CRUD methods |
| `daemon/session/store_test.go` | Add CRUD tests + schema column assertion |
| `daemon/session/doc.go` | Update package doc |
| `daemon/daemon/daemon.go` | Add Store field, Session(), remove NewSessionID |
| `daemon/daemon/handler.go` | Read from session, remove newID |
| `daemon/daemon/handler_test.go` | Use real session in tests |
| `daemon/cmd/mortised/main.go` | Create/load session, wire store into daemon |
| `daemon/cmd/mortised/main_test.go` | Add session lifecycle tests |

## Implementation order

1. **`session.go` + `session_test.go`** — domain types and transition logic first (no DB dependency)
2. **Schema migration in `store.go`** — add columns, update `Migrate`
3. **CRUD in `store.go`** — `CreateSession`, `GetSession`, `UpdateSession`, `UpdateSessionStatus`
4. **CRUD tests in `store_test.go`** — verify persistence
5. **Wire into `daemon.go`** — add Store field, Session(), remove dead code
6. **Wire into `handler.go`** — read from session, remove newID, update `sendSystemStatus`
7. **Update handler tests** — use real session
8. **Wire into `main.go`** — create/load session in `run()`, pass store to `buildDaemon`
9. **Update main tests** — verify session lifecycle