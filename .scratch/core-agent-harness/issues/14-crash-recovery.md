---
status: ready-for-agent
blocked-by: ["13-sqlite-jsonl-persistence"]
---

# 14 — Crash Recovery

**What to build:** When the daemon process is killed unexpectedly (SIGKILL, power loss, OOM) and restarted, it detects the crashed session, restores its state from SQLite and JSONL, and resumes where it left off. The TUI reconnects and picks up the session with full conversation history and the correct phase. Crash recovery makes Mortise resilient — users never lose a session to a daemon crash.

## What to build

### Detecting a crashed session

On daemon startup:
1. Query SQLite for sessions with `status = SessionRunning` (should only be one active session in v1)
2. If found, the session was running when the daemon last shut down
3. Check `last_active_at`: if more than 30s ago and no clean shutdown was recorded, treat as `CRASHED`
4. Alternatively: on clean shutdown, set session status to `SessionPaused` in SQLite. If daemon starts and finds status `SessionRunning`, it was a crash.

### Restoring state

When a crashed session is detected:

1. Set session status to `SessionCrashed` (transient), then immediately to `SessionRunning`
2. Load `messages` from SQLite ordered by turn — this is the full conversation history
3. Determine last phase from the most recent `PhaseTransitionEvent` in JSONL:
   - Scan the JSONL file backwards for the last `PhaseTransitionEvent`
   - The `to` field is the phase the agent was in when it crashed
   - If the last event was a `ToolCallPending` without a matching `ToolCallCompleted`, the agent was mid-tool-execution
4. Reconstruct the `AgentLoop`:
   - Set `phase` to the recovered phase (or `IDLE` if no events found)
   - Set `session.Messages` to the SQLite conversation history
   - Set `session.TurnCount` from the session metadata
5. Emit `SystemStatus` with the restored session data
6. Emit a `PhaseTransitionEvent` from `CRASHED` to the recovered phase with `reason: "Session recovered after crash"`

### TUI reconnection

When the TUI reconnects after a daemon restart:
1. Receives `SystemStatus` with session data (not placeholder — this has real history, turn count, etc.)
2. Requests conversation history from daemon
3. Renders the full chat history in the Chat panel
4. Renders the current phase in the top bar
5. User can resume by sending a new prompt — agent continues from where it left off

### Consistency verification

On recovery, verify:
- JSONL event count matches SQLite message count (within tolerance — JSONL has more granular events)
- Last event in JSONL is not orphaned (no dangling `ToolCallPending` without completion)
- If inconsistency is found, log a warning and use SQLite as the authoritative source

If JSONL is corrupt or missing but SQLite is intact: recover from SQLite only, log a warning.
If both are corrupt: mark session as `SessionErrored`, inform the user.

### Edge case: crash during file write

If the daemon crashed mid-`file_write`:
- The file on disk may be partially written. This is acceptable — the agent will see the file state on recovery and can decide what to do.
- The undo stack (in-memory only) is lost. This is acceptable for v1 — undo is a convenience, not a safety guarantee.

## Acceptance criteria

- [ ] Kill daemon mid-session (SIGKILL) → restart daemon → session auto-resumes with full conversation history intact
- [ ] TUI reconnects and displays the restored conversation in the Chat panel
- [ ] Phase is correctly restored: if killed during `ACTING`, recovery sets phase to `ACTING`
- [ ] If killed mid-tool-execution, the recovery detects the orphaned `ToolCallPending` and sets phase to `ACTING` + notifies the agent
- [ ] If both SQLite and JSONL are corrupt, session is marked `ERRORED` with a clear message (not a silent failure)
- [ ] Recovery is fast: <5s from daemon start to TUI-ready state for a session with <100 turns
