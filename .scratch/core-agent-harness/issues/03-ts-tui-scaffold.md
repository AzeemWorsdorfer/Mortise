---
status: completed
blocked-by: ["01-project-foundation"]
---

# 03 — TS TUI Scaffold

**What to build:** A TypeScript terminal UI application (Ink + React, run via Bun or Node) that connects to the Go daemon over the Unix socket, deserializes `ServerEvent` protobuf messages, and renders a minimal status display. This is the "hello world" of the TUI — a single panel showing the daemon's `SystemStatus` fields, proving the full transport chain works.

## What to build

### Project setup

- Package at `tui/` using Bun (or Node with tsx) as the runtime
- Framework: Ink (React for terminals) — already on the tech stack
- Connect-RPC client (connect-es) at `@connectrpc/connect` with the generated transport stubs from ticket 01
- Connect to the daemon's Unix socket path (default `~/.mortise/mortise.sock`, configurable via CLI flag or env var)

### Transport layer

- Unix domain socket connection using connect-es's `createConnectTransport` with a custom Unix socket transport (or a `node:net` socket adapter)
- Subscribe to the event stream: connect, receive `ServerEvent` messages continuously
- Handle disconnect: detect socket close, attempt reconnection with exponential backoff (1s, 2s, 4s, max 30s), display "Reconnecting…" status
- Handle daemon not running: display clear "Daemon not running. Start with `mortised`" message

### Minimal UI

A single Ink component that renders:

```
┌─ Mortise ───────────────────────────────────────┐
│                                                  │
│  Session: untitled                               │
│  Model:   claude-sonnet-4-20250514 (anthropic)   │
│  Status:  Connected ✓                            │
│  Uptime:  0m 12s                                 │
│  Tokens:  0 total                                │
│  Cost:    $0.00                                  │
│                                                  │
└──────────────────────────────────────────────────┘
```

Fields update in real-time as `SystemStatus` events arrive from the daemon.

This is deliberately minimal — the full dashboard layout is ticket 16. This ticket proves the TUI can receive and render protobuf events.

### Development experience

- `bun run dev` (or `npm run dev`) starts the TUI with hot reload
- `bun run build` produces a production bundle
- TypeScript strict mode enabled (`strict: true` in tsconfig)
- ESLint + Prettier configured (inherited from ticket 01's tooling)

### Mock mode for development

When no daemon is available, the TUI should support a `--mock` flag that renders the same panel with fake data. This lets TUI development proceed independently of the daemon.

## Acceptance criteria

- [ ] `bun run ./tui` starts the TUI and connects to a running daemon on the Unix socket
- [ ] TUI renders a panel showing session name, model, connected status, uptime, token count, cost from the daemon's `SystemStatus` event
- [ ] Disconnecting the daemon triggers "Reconnecting…" status; restarting the daemon restores "Connected ✓"
- [ ] `--mock` flag renders the same panel with fake data (daemon not required)
- [ ] `bun run build` completes without errors
