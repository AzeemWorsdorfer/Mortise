---
status: ready-for-agent
blocked-by: ["06-agent-loop-mock-provider"]
---

# 12 — Interrupt System (Ctrl+P)

**What to build:** The interrupt system that lets the user pause the agent at any time by pressing `Ctrl+P`. The daemon gracefully handles the pause — finishing safe operations, terminating long-running or destructive ones — and transitions to `PAUSED`. From `PAUSED`, the user can resume (continue where the agent left off) or cancel (end the task). The TUI binds `Ctrl+P` and renders the pause/resume/cancel UI.

## What to build

### Daemon: pause handling

When `ClientCommand.Pause` is received:

1. The agent loop transitions from current phase to `PAUSED`
2. A `PhaseTransitionEvent` is emitted with `reason: "User interrupted"`
3. If a tool is currently executing:
   - **Safe tool (RiskSafe) or <5s remaining:** let it finish, then transition to `PAUSED`
   - **Long-running (>5s remaining) or RiskDestructive:** send SIGTERM to the subprocess, wait 3s, then SIGKILL. Emit `ToolCallCompleted` with `success=false` and `error_message: "Interrupted by user"`
4. If the agent is waiting for model response: cancel the provider context. The turn is abandoned. Agent loop records the partial state for possible resumption.
5. The daemon enters `PAUSED` and waits for further commands.

### Daemon: resume

When `ClientCommand.Resume` is received while `PAUSED`:

- Transition back to the phase the agent was in before the pause
- If a tool was interrupted, the agent receives the `ToolCallCompleted` with the interruption error and can decide how to proceed (retry, adjust, abandon)
- If a model call was interrupted, the agent loop starts a new turn from the last known state

### Daemon: cancel

When `ClientCommand.Cancel` is received while `PAUSED`:

- The agent loop transitions to `COMPLETED` (or `ERRORED` if the task was mid-execution)
- A `SessionSummary` is emitted
- The session is marked as completed in SQLite

### TUI: keybinding

- `Ctrl+P` is intercepted by the TUI input handling (Ink's `useInput` or raw stdin handler)
- Sends `ClientCommand.Pause` to the daemon
- The top bar updates to show `[PAUSED]` in the phase indicator

### TUI: pause screen

When `PAUSED` phase is active, the TUI renders an overlay or replaces the input bar:

```
┌─ Paused ────────────────────────────────────────────────────────┐
│                                                                  │
│  Agent is paused. Current task: "Refactor the auth module"       │
│  Turn 3 of 8                                                     │
│                                                                  │
│  [r] Resume    [c] Cancel    [i] Inspect                         │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

- `r` → sends `ClientCommand.Resume`
- `c` → sends `ClientCommand.Cancel` (with confirmation prompt: "Cancel current task? [y/N]")
- `i` → stays paused, lets user scroll through the trace session panel (no daemon command needed)

### Edge case: double pause

If the user presses `Ctrl+P` while already `PAUSED`, it's a no-op. No additional events. Log at debug level.

### Edge case: pause during planning

If the agent is in `PLANNING` (waiting for model response) when paused, the model context is cancelled. On resume, the agent replans or continues from the last received model output.

## Acceptance criteria

- [ ] `Ctrl+P` during any phase transitions to `PAUSED` and emits `PhaseTransitionEvent` with correct `from`/`to`/`reason`
- [ ] Safe tool (<5s remaining) finishes before pause takes effect
- [ ] Destructive or long-running tool is terminated (SIGTERM → SIGKILL after 3s)
- [ ] Resume returns to the phase before pause; agent can continue from interruption
- [ ] Cancel ends the session and emits `SessionSummary`
- [ ] TUI renders `[PAUSED]` in the phase indicator and shows the pause overlay with Resume/Cancel options
- [ ] `Ctrl+P` while already paused is a no-op
