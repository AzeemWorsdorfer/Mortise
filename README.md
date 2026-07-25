# Mortise

> Dark-timber agentic coding tool. Phase-aware agent loop, structured tool calls, terminal-native UX.

## Status

Work in progress. Tracked as tickets under `.scratch/`.

## Components

- **Daemon** — Go process, the source of truth. Runs the agent loop, executes tools, persists sessions.
- **TUI Client** — TypeScript (Ink + React) terminal UI. Thin client that connects to the daemon.
- **Protocol** — Protobuf over Connect-RPC (Unix domain socket locally, WebSocket/gRPC remote).

## Documentation

- `CONTEXT.md` — domain glossary
- `docs/specs/01-core-agent-harness.md` — the foundational spec
- `mortise-prd.md` — product requirements
- `mortise-architecture.html` — architecture overview
- `AGENTS.md` — agent instructions for this repo

## Development

Build / generate / test commands will be wired up by ticket 1 (`feature/01-project-foundation`).
