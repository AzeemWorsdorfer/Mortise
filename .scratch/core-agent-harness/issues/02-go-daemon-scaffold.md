---
status: ready-for-agent
blocked-by: ["01-project-foundation"]
---

# 02 — Go Daemon Scaffold

**What to build:** A Go binary (`cmd/mortised`) that starts the daemon: reads and merges config, opens a Unix domain socket for client connections, initializes a SQLite database, and sends a `SystemStatus` protobuf message to any connecting client. This is the "hello world" of the daemon — no agent loop, no tools, just a running process that proves the transport works end-to-end with a real client.

## What to build

### Daemon entrypoint

`cmd/mortised/main.go`:
- Reads CLI flags: `--config` (optional path to `.mortise.json`), `--socket` (optional Unix socket path, defaults to `~/.mortise/mortise.sock`)
- Merges project config (`.mortise.json` in current or specified workspace) with global config (`~/.mortise/config.json`). Project overrides global. Merged config is the authoritative config for the session.
- Validates merged config: `version` must be `1`, required fields present
- Initializes SQLite database at `~/.mortise/mortise.db` (create directory if needed). Runs schema migration (create `sessions` table with basic columns: `id`, `status`, `workspace_path`, `created_at`). Just the table exists; session data isn't written yet (that's ticket 04).
- Creates and listens on the Unix socket

### Config schema

The daemon must parse the config schema from spec §4.3. At minimum, it reads and validates:
- `version` (must be 1)
- `providers.default` 
- `tools.approval` map
- `tools.shell` (allowed_commands, blocked_patterns, timeout_sec)

The full schema is defined; the daemon reads all fields. Fields not yet used by a ticket are silently accepted (forward-compatible).

### Unix socket transport

- Listen on a Unix domain socket at the configured path
- Use Connect-RPC for Go (connect-go) to handle protobuf serialization over the raw socket
- On client connect: immediately send a `ServerEvent` containing a `SystemStatus` payload populated from the current daemon state (session ID is a placeholder UUID, model from config, uptime starts at 0)
- Accept incoming `ClientCommand` messages (received commands are logged but not yet acted upon)
- Graceful shutdown on SIGINT/SIGTERM: close socket, close DB

### SystemStatus event

The initial SystemStatus sent on connect includes:
- `session_id`: a temp UUID (real session comes in ticket 04)
- `session_name`: "untitled"
- `workspace_path`: from config or current directory
- `model_id` / `provider_id`: from merged config
- `connected_clients`: 1
- `session_token_total`: 0
- `session_cost_total`: 0
- `uptime_sec`: 0

### Build

- `make build` produces `./bin/mortised` (statically linked if possible)
- Binary runs on macOS (darwin/arm64). Linux cross-compilation is a nice-to-have but not required for v1.

## Acceptance criteria

- [ ] `make build` produces a runnable `./bin/mortised` binary
- [ ] `./bin/mortised` starts, creates `~/.mortise/mortise.sock` and `~/.mortise/mortise.db`
- [ ] A raw socket connection (e.g., `nc -U ~/.mortise/mortise.sock`) receives a valid protobuf-encoded `ServerEvent` with `SystemStatus` payload
- [ ] `SIGINT` (Ctrl+C) shuts down cleanly: socket removed, DB connection closed, exit code 0
- [ ] Config validation: daemon exits with a clear error if `version` is missing or not `1`
- [ ] Config merge: project `.mortise.json` values override global `~/.mortise/config.json` values
