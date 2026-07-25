---
status: ready-for-agent
blocked-by: ["06-agent-loop-mock-provider"]
---

# 13 — SQLite Conversation + JSONL Event Log

**What to build:** Persistent storage for session data — the full conversation history in SQLite (for resumption) and an append-only JSONL event log (the source of truth for observability, handoff, and crash recovery). Every turn is written to both stores atomically. The TUI can load and display historical turns from a previous session.

## What to build

### SQLite conversation storage

Extend the `sessions` table and add a `messages` table:

```sql
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    turn_number INTEGER NOT NULL,
    role TEXT NOT NULL,              -- 'system', 'user', 'assistant', 'tool'
    content TEXT,                    -- message text (for text messages)
    tool_calls_json TEXT,            -- JSON array of tool calls (for assistant messages with tools)
    tool_call_id TEXT,               -- which tool call this result responds to (for tool messages)
    timestamp_ms INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);
```

- Write a message on every turn: user prompt, assistant text, tool calls, tool results
- Messages are ordered by `turn_number` then `id`
- Session metadata (`turn_count`, `last_active_at`) is updated in the `sessions` table on each write

### JSONL event log

A single file per session: `~/.mortise/sessions/<session-uuid>.jsonl`

Every `ServerEvent` emitted through the event bus is appended as one JSON line:

```jsonl
{"timestamp_ms":1720000001000,"phase":"PLANNING","turn_number":1,"type":"PhaseTransitionEvent","payload":{"from":"IDLE","to":"PLANNING","reason":"User prompt received"}}
{"timestamp_ms":1720000002000,"phase":"ACTING","turn_number":1,"type":"ToolCallPending","payload":{"call_id":"abc123","tool_name":"file_read","parameters_json":"{\"path\":\"main.go\"}","risk_level":"SAFE","requires_approval":false}}
```

- One JSON object per line (no trailing comma, no outer array)
- File is opened in append mode, flushed after each event (or batched per turn)
- File is rotated: max 100MB per file, then a new `.jsonl.2` file starts (or use date-based rotation)

### Write ordering

For atomicity across both stores:
1. Append to JSONL first (append-only, cannot fail mid-write like SQLite can)
2. Write to SQLite in a transaction
3. If SQLite write fails, the JSONL has the event — the next read can detect and reconcile

### Reading conversation history

On session load (daemon start or TUI connect):
1. Query `messages` from SQLite ordered by `turn_number`, `id`
2. Return as the `Message[]` slice for the session
3. The TUI can request message history via a new `ClientCommand.GetHistory` (or a dedicated RPC)

### TUI historical view

When the TUI connects to an existing session (not a fresh one), the Chat panel loads the full message history from the daemon and renders it. New messages append as they arrive.

## Acceptance criteria

- [ ] After 3 turns, SQLite `messages` table has entries for all user prompts, assistant responses, tool calls, and tool results
- [ ] JSONL file at `~/.mortise/sessions/<uuid>.jsonl` has one line per event, all valid JSON
- [ ] SQLite and JSONL are consistent: replaying JSONL events produces the same conversation as SQLite
- [ ] Session `turn_count` and `last_active_at` are updated after each turn
- [ ] TUI loading an existing session displays the full conversation history from SQLite
- [ ] Write ordering: JSONL written before SQLite; if SQLite fails, JSONL has the data
