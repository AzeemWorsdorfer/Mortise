---
status: ready-for-human
blocked-by: ["01-project-foundation"]
assigned-to: build-agent
started-on: 2026-07-27
branch: feature/02-go-daemon-scaffold
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

- [x] `make build` produces a runnable `./bin/mortised` binary
- [x] `./bin/mortised` starts, creates `~/.mortise/mortise.sock` and `~/.mortise/mortise.db`
- [x] A raw socket connection (e.g., `nc -U ~/.mortise/mortise.sock`) receives a valid protobuf-encoded `ServerEvent` with `SystemStatus` payload (verified end-to-end with a real Connect-RPC client in `daemon/daemon/handler_test.go`)
- [x] `SIGINT` (Ctrl+C) shuts down cleanly: socket removed, DB connection closed, exit code 0
- [x] Config validation: daemon exits with a clear error if `version` is missing or not `1`
- [x] Config merge: project `.mortise.json` values override global `~/.mortise/config.json` values

## Comments

### What was built (build-agent, 2026-07-27)

Implemented across 6 new Go packages under `daemon/`:

| Path | Responsibility |
|---|---|
| `daemon/config/` | JSON load, project/global merge, version validation |
| `daemon/session/` | Pure-Go SQLite (modernc.org/sqlite) + sessions table migration |
| `daemon/daemon/` | h2c listener, Connect-RPC handler, SystemStatus on connect, command log, graceful shutdown |
| `daemon/cmd/mortised/` | CLI entry point (`--config`, `--socket`, `--mortise-dir`) |
| `proto/mortise/v1/agent.proto` | New `service AgentService { rpc Connect(...) }` |
| `daemon/gen/`, `tui/src/gen/` | Regenerated stubs |

### Decisions worth noting

- **SQLite driver**: `modernc.org/sqlite` (pure Go, no CGO) so the binary stays statically linked and under the 30 MB NFR. `mattn/go-sqlite3` would have required CGO and broken that target.
- **Wire format over Unix socket**: full **Connect protocol** (HTTP/2 prior knowledge) via `h2c.NewHandler`. Connect's bidi stream relies on HTTP/2 framing; HTTP/1.1 + Connect bidi is not part of the spec.
- **macOS socket path limit** (104 bytes): `t.TempDir()` names routinely exceed it. Tests use a custom `shortTempDir(t)` helper that returns `os.MkdirTemp("", "mort")` so the daemon's `long test name + `.Test`` paths don't push us over.
- **Stale-socket handling**: `newUnixListener` unlinks a stale `unix` socket at the target path, but refuses to clobber a regular file with the same name — both are tested.
- **`run()` context parameter**: `run()` accepts an optional `context.Context` for tests; production (when called from `main`) uses `signal.NotifyContext` for SIGINT/SIGTERM. Tests pass `context.Background()` + `cancel()`.
- **`--socket` default empty**: the socket path defaults to `<mortise-dir>/mortise.sock` so a project-local state directory gives a project-local socket without users having to set the flag twice.

### Verification (all passing on macOS/arm64)

```
$ cd daemon && go test -race -count=1 ./...
ok    github.com/AzeemWorsdorfer/Mortise/daemon/cmd/mortised   1.812s
ok    github.com/AzeemWorsdorfer/Mortise/daemon/config         1.187s
ok    github.com/AzeemWorsdorfer/Mortise/daemon/daemon         2.001s
ok    github.com/AzeemWorsdorfer/Mortise/daemon/session        1.421s

$ gofmt -s -l . ; go vet ./... ; golangci-lint run
(clean)

$ make build
$ ls -la bin/
-rwxr-xr-x ... 14343266 ... bin/mortised
$ file bin/mortised
bin/mortised: Mach-O 64-bit executable arm64

$ ./bin/mortised --mortise-dir /tmp/mortise-test/.mortise
... config loaded ... socket bound ... mortised listening
$ kill -INT $! ; wait $!
... mortised shut down cleanly
$ echo $?
0
$ ls /tmp/mortise-test/.mortise/
config.json  mortise.db
# socket file was removed as expected
```

### Known issue (pre-commit hook, NOT blocking CI)

`make proto-check` and the `eslint --max-warnings 0` step in lint-staged both react to the new generated stub file `tui/src/gen/mortise/v1/agent_connect.ts`:

1. **`make proto-check`**: fails locally because the regen'd stubs (added in this ticket) are an uncommitted diff. After `git add -A && git commit`, the diff is empty and the gate passes. This is the expected behavior of a regen gate.
2. **lint-staged → eslint**: ESLint v8 prints `File ignored because of a matching ignore pattern` (informational, exit code 0 on its own) for any file passed by path that matches `.eslintignore`/`ignorePatterns`. With `--max-warnings 0`, that notice blocks the commit. The `agent_connect.ts` file in question sits under `tui/src/gen/`, which is correctly ignored — eslint just doesn't know how to tell us "ok this file is skipped, no warning needed" in lint-staged's per-file invocation mode. Suggested fix (one of):
   - Add a small node wrapper `scripts/eslint-ignore-aware.mjs` that strips that one specific notice.
   - Bump ESLint to v9 which has the `--no-warn-ignored` flag.
   - Switch lint-staged to a matcher that uses `git ls-files` directly so eslint runs only on non-ignored files.

The CI workflow runs the same checks but is unaffected by the lint-staged wrapper question (CI doesn't run lint-staged). All other CI-relevant checks (gofmt, go vet, golangci-lint, tsc --noEmit, repo-wide checks, proto regen diff) pass cleanly when invoked directly via `make ci`.

### Files changed

```
.eslintignore                                        |   3 +
.scratch/core-agent-harness/issues/02-go-daemon-scaffold.md
Makefile                                             |  22 +-
daemon/cmd/mortised/{main.go,main_test.go,bind.go,branch.go}    | ~150 LoC + ~70 LoC test
daemon/config/{config.go,config_test.go,doc.go}       | ~280 LoC + ~125 LoC test
daemon/daemon/{daemon.go,handler.go,doc.go,bind.go,socket.go,socket_test.go,handler_test.go} | ~360 LoC + ~340 LoC test
daemon/gen/...                                       | (regen)
daemon/go.{mod,sum}                                  | (deps added)
daemon/main.go                                       | (deleted, replaced by cmd/mortised)
daemon/session/{store.go,doc.go,path.go,store_test.go} | ~125 LoC + ~125 LoC test
package.json                                         |   3 +
proto/mortise/v1/agent.proto                         |  14 +  (service Connect)
tui/src/gen/mortise/v1/agent_connect.ts              | (regen)
```

### Branch state

Work is fully **staged but not committed** (3 commit attempts all blocked by the ESLint-warn-on-ignored issue above; staging is untouched). Awaiting user permission to commit + open PR.
