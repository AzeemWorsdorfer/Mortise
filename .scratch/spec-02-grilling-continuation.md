---
status: ready-for-agent
spec: 02-full-observability
---

# Spec #2: Full Observability — Grilling Continuation

## Context

We completed 13 grilling rounds for Spec #1 (Core Agent Harness). All decisions locked. Spec #1 synthesized into `docs/specs/01-core-agent-harness.md`. Resume grilling from this point in a clean session — load this file, load the `grill-with-docs` skill, and continue.

## What Spec #2 must cover

From the PRD §6.2:

**P0:**
- Real-time tool-call panel (tool name, inputs, output, duration, status)
- Live token counter (input/output/cached per turn + cumulative)
- Live cost counter (running $ estimate per session per provider)
- Structured exportable session log (JSON/JSONL) — *partially covered by Spec #1's JSONL event log*
- Session-level summary view (tokens, cost, tool-call count, duration, error rate)

**P1:**
- Historical dashboard across sessions
- Configurable alerts/thresholds
- OpenTelemetry span export

**P2:**
- Team-wide cost/usage reporting

## Dependencies

- **Depends on Spec #1** (needs `ServerEvent` stream, `ToolCallPending`, `ToolCallCompleted`, `UsageDelta`, `SessionSummary`)
- **Needed by Spec #4** (Handoff) and Spec #5 (Collaboration)

## Key decisions to grill

1. How does Observability hook into the `ServerEvent` stream? (Middleware? Separate subscriber? Direct SQLite queries?)
2. Where does cost calculation live? (Provider adapter returns cost? Or Observability calculates from token counts?)
3. What's the JSONL schema extension for Observability-specific events?
4. How does the TUI render live counters? (Which panel? What Protobuf messages drive them?)
5. Historical dashboard: in-memory aggregation, SQLite queries, or separate time-series store?
6. Alert/threshold model: config-driven, event-triggered, displayed in TUI or pushed to OS notification?
7. How does session log export work? (CLI command? TUI button? Format options: JSONL, CSV, OpenTelemetry?)

## Reference files

- PRD: `mortise-prd.md` §6.2, §6.4
- Architecture: `mortise-architecture.html`
- Brand system: `design/mortise-brand-system.html`
- UI Mock: `design/UI_Dark_Mock.png`
- Spec #1: `docs/specs/01-core-agent-harness.md`
- CONTEXT.md: (to be populated by domain-modeling as terms resolve)

## Platform / agent state

- Go daemon + TypeScript TUI (Ink + React, Bun/Node) + Protobuf/Connect-RPC protocol
- Brand: dark timber theme, Brass Joint accent, Sap Green success, Rust Ochre error
- Fonts: JetBrains Mono (display), IBM Plex Sans (body), IBM Plex Mono (code)
- Transport: Unix domain socket (local), WebSocket/gRPC (remote, Spec #5)

## Current domain glossary

Refer to `docs/specs/01-core-agent-harness.md` for the full domain model including: `AgentPhase`, `ServerEvent`, `AgentLoop`, `Provider`, `ToolRegistry`, `Session`, `RiskLevel`, `ToolCallPending`, `ToolCallCompleted`, `UsageDelta`, `ClientCommand`.

---

*Start a new Mortise session, load `grill-with-docs`, read `docs/specs/01-core-agent-harness.md` and this file, then continue grilling from decision #1.*
