# CONTEXT.md

Domain glossary and terminology for the Mortise project.

This file is populated by the `domain-modeling` skill as terms and decisions are resolved. The canonical copy lives in the Obsidian vault at `/Users/azeemworsdorfer/Obsidian/Mortise/CONTEXT.md`.

---

## Core concepts

- **Daemon** — The Go process that runs the agent loop, tool execution, session state, and event broadcast. The single source of truth.
- **TUI Client** — The TypeScript terminal UI (Ink + React) that renders panels and handles user input. A thin client that connects to the daemon.
- **Session** — A single agent-coding session. Has a UUID, a name (renameable), a workspace, and persists across daemon restarts via SQLite.
- **Turn** — One complete agent loop cycle: Planning → Acting → Observing → Deciding → (loop or complete).

## Agent loop

- **Agent Loop** — The phase-aware state machine driving all agent behavior: `Idle → Planning → Acting → Observing → Deciding`.
- **Planning** — The agent analyzes the task and produces an execution plan (numbered steps).
- **Acting** — The agent executes a tool call (file write, shell command, etc.).
- **Observing** — The agent receives and evaluates tool results.
- **Deciding** — The agent evaluates whether the step succeeded and decides what to do next (plan more, act on next step, or signal completion).
- **Paused** — The user has interrupted the agent. The tool queue is held. No new model calls.

## Tool execution

- **Tool** — A callable action the agent can invoke: file_read, file_write, file_diff, shell_exec, git_diff, git_commit_draft, or custom/MCP tools.
- **Tool Registry** — The collection of all available tools (built-in, custom from config, MCP-discovered).
- **Approval Gate** — The mechanism that blocks, allows, or auto-approves tool calls based on per-tool-type policy and risk level.
- **Risk Level** — Safe (read-only), ModifiesFiles (writes within workspace), Destructive (shell outside workspace, rm -rf, git push --force). Destructive always requires confirmation.

## Protocol

- **ServerEvent** — The Protobuf envelope for all daemon→client messages. Wraps a payload in a phase context.
- **AgentPhase** — The Protobuf enum on every ServerEvent showing the current phase (`IDLE`, `PLANNING`, `ACTING`, `OBSERVING`, `DECIDING`, `PAUSED`, `COMPLETED`, `ERRORED`).
- **ClientCommand** — Client→daemon Protobuf messages (pause, resume, cancel, switch provider, approve/reject tool, trigger handoff).
- **Connect-RPC** — The RPC framework for Protobuf over Unix domain sockets (local) and WebSocket/gRPC (remote).

## Persistence

- **JSONL Event Log** — Append-only structured log per session. Records every tool call and model turn. Source of truth for observability and handoff.
- **SQLite** — Stores session metadata, conversation history (for resumption), tool undo stack, and project file tree cache.
- **OS Keyring** — Provider API credentials are stored in the OS keychain, never in logs or exports.

## Configuration

- **`.mortise.json`** — Project-level config file (overrides `~/.mortise/config.json`). JSON format, versioned (`"version": 1`). Controls tools, approval policies, providers, personas, MCP servers, and context limits.
- **Persona** — A named system prompt preset (e.g., "default", "concise", "architect"). Selectable via config or `/persona` command.
- **Skill** — A named instruction set injected into the next turn via `/skill-name`. Like a dynamic persona activated mid-session.
- **MCP Server** — A Model Context Protocol server that exposes additional tools. Process-managed by the daemon. Tools registered with `mcp:` prefix.

## Commands

- **Slash Command** — A `/`-prefixed input intercepted by the TUI client. `/run tests`, `/commit`, `/handoff`, `/switch openai`, `/skill-name`.
- **`@` Reference** — `@file.ts` adds a file to context. `@agent-name` reserved for P2 multi-agent delegation.

## Collaboration (Spec #5, phase 4)

- **Watch Mode** — Read-only attach to a shared session.
- **Co-driver** — A connected user with write access to the shared session.
- **Invite Token** — Expiring, scoped token for session access control.
- **Relay Server** — The optional remote server that forwards WebSocket/gRPC traffic between a remote TUI and the daemon.

## Handoff (Spec #4, phase 3)

- **Handoff Artifact** — A structured summary of session state (task, decisions, open threads, file context) that bootstraps a fresh-context session.
- **Context Window** — The model's token budget. Handoff is triggered when the window nears capacity.
