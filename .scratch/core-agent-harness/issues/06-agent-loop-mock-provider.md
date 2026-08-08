---
status: ready-for-agent
---

# 06 — Agent Loop State Machine + Mock Provider

**What to build:** The full `AgentLoop` state machine that drives Mortise's core behavior: `Idle → Planning → Acting → Observing → Deciding → (loop or complete)`. A mock provider returns canned responses so the full phase cycle can be demonstrated without a real API key or network call. Every phase transition emits a `PhaseTransitionEvent` through the event bus. The TUI renders live phase transitions in the top bar.

This is the beating heart of Mortise — all later tickets (tools, approval, context, etc.) plug into this loop.

## What to build

### AgentLoop struct

```go
type AgentLoop struct {
    phase     AgentPhase
    session   *Session
    provider  Provider
    toolExec  *ToolExecutor     // stub for now, real in ticket 07
    eventBus  *EventBus
    cancelFn  context.CancelFunc
    mu        sync.Mutex
}
```

### Phase state machine

Full implementation of the phase transitions as diagrammed in spec §3.1:

```
IDLE → PLANNING → ACTING → OBSERVING → DECIDING → (PLANNING or COMPLETED)
  ↑        ↑         ↑          ↑           ↑
  └────────┴─────────┴──────────┴───────────┘  (PAUSED interrupt from any phase)
```

- `IDLE` → user sends prompt → `PLANNING`
- `PLANNING` → mock provider returns a plan → `ACTING`
- `ACTING` → mock tool call "executed" → `OBSERVING`
- `OBSERVING` → mock tool result evaluated → `DECIDING`
- `DECIDING` → either back to `PLANNING` (more steps) or `COMPLETED` (done)
- Any phase + `Ctrl+P` → `PAUSED` (actual pause logic in ticket 12; this ticket just accepts the transition)

### PhaseTransitionEvent

Every transition emits a `PhaseTransitionEvent` on the event bus:
```protobuf
message PhaseTransitionEvent {
  AgentPhase from = 1;
  AgentPhase to = 2;
  string reason = 3;
}
```

The `reason` field explains the trigger: "user prompt received", "plan complete", "tool executed", "all steps complete", "user interrupted".

### Mock provider

A `MockProvider` implementing the `Provider` interface (spec §3.3) that returns hardcoded responses without any API call:

- `SendPrompt(ctx, req)` returns a channel of `ProviderEvent`
- Sequence of events:
  1. `EventThinking` — "Let me analyze the task…"
  2. `EventText` — "I'll start by reading the project structure."
  3. `EventToolCall` — proposes `file_read` on `package.json`
  4. `EventDone` — turn complete, `Usage` populated with fake token counts
- Configurable turns: the mock provider accepts a `turnCount` parameter and returns canned sequences for turns 1, 2, 3. Turn 3 signals completion so `DECIDING → COMPLETED`.

### AgentLoop.Run(ctx)

The main loop method:

1. Set phase to `PLANNING`, emit PhaseTransitionEvent
2. Call `provider.SendPrompt()` with system prompt + user message + tool definitions
3. Iterate over the provider event channel:
   - `EventThinking` → emit `ThinkingChunk` events to event bus
   - `EventText` → emit `AgentText` events to event bus
   - `EventToolCall` → set phase to `ACTING`, emit `ToolCallPending` event, wait 1s (simulated tool execution), emit `ToolCallCompleted` with `success=true` and a fake result
   - `EventDone` → set phase to `OBSERVING`, evaluate, then `DECIDING`
4. If more turns remain → loop back to `PLANNING`
5. If done → `COMPLETED`, emit `SessionSummary`

### TUI integration

The TUI (from ticket 03) now subscribes to `PhaseTransitionEvent` and `AgentText` events:

- Top of the panel shows the current phase: `[IDLE]` → `[PLANNING]` → `[ACTING]` → `[OBSERVING]` → `[DECIDING]` → `[COMPLETED]`
- Agent text and thinking chunks scroll in the display area
- Phase transitions are visibly animated or color-coded

Keep the TUI changes minimal — just enough to prove the phase machine works end-to-end. The full TUI dashboard is ticket 16.

## Acceptance criteria

- [ ] Agent loop starts in `IDLE`, transitions through all 5 active phases on a mock turn, and ends in `COMPLETED` after the final turn
- [ ] Every phase transition emits a `PhaseTransitionEvent` with correct `from`, `to`, and descriptive `reason` string
- [ ] Mock provider returns a configurable sequence of thinking → text → tool_call → done events
- [ ] TUI renders live phase transitions: user sees `[PLANNING]` → `[ACTING]` → `[OBSERVING]` → `[DECIDING]` cycle in real time
- [ ] Invalid phase transitions (e.g., `IDLE → ACTING` without `PLANNING`) are blocked and logged
- [ ] Agent loop can be started with a mock prompt, run to completion, and the session status transitions to `Completed`
