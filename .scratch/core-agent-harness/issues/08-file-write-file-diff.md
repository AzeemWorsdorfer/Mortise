---
status: ready-for-agent
blocked-by: ["07-tool-interface-file-read"]
---

# 08 — File Write + File Diff

**What to build:** The `file_write` and `file_diff` built-in tools. `file_write` writes content to disk with a unified diff preview before committing and maintains an undo stack of the last 10 writes. `file_diff` computes the diff between a file's current state and a given content or the last write. Both are classified `RiskModifiesFiles` and enforce workspace boundary.

## What to build

### file_write tool

- **Name:** `file_write`
- **Parameters:** `{ "path": { "type": "string" }, "content": { "type": "string" } }`
- **Risk:** `RiskModifiesFiles`

**Behavior:**
1. Resolve absolute path from workspace root. Reject if outside workspace boundary (same logic as file_read).
2. If the file exists, read current content for diff computation. If parent directories don't exist, create them (with `0755`).
3. Compute a unified diff between current content (or empty) and new content.
4. Append current content to the undo stack for this file (max 10 entries per file).
5. Write new content to disk.
6. Emit `ToolCallCompleted` with `result_summary` containing the diff and `files_changed: [path]`.

### Undo stack

- Per-file undo stack, capped at 10 entries
- Each entry stores the previous content (full file contents, not a diff patch)
- Stack is in-memory only (not persisted to SQLite — persistence of undo is out of scope for v1)
- `undo_stack_size: 10` in config is read and applied
- 11th write to the same file evicts the oldest entry

### file_diff tool

- **Name:** `file_diff`
- **Parameters:** `{ "path": { "type": "string" } }`
- **Risk:** `RiskSafe`
- **Behavior:** Computes a unified diff between the current file on disk and either:
  - The content at the top of the undo stack (last written version), OR
  - If no undo entry exists, shows the file contents as "new file"
- Returns the diff as a string in the result

### Workspace boundary enforcement

Both tools must resolve symlinks before the boundary check. Any path that resolves outside `workspace_root + "/"` is rejected with an error and the tool result has `success=false`.

### TUI rendering

The file_write diff is displayed in the Trace Session panel and a Files Changed panel (minimal for now, full panel in ticket 16):

```
TRACE SESSION
─────────────────
  [Turn 2] file_write
    Input:  src/main.go
    Diff:   +12 -3
    Result: File written (12 additions, 3 deletions)
    Duration: 3ms  ✓

  FILES CHANGED
  ─────────────────
    M src/main.go        (+12 -3)
```

## Acceptance criteria

- [ ] `file_write` creates new files (including parent directories) and overwrites existing files
- [ ] `file_write` returns a unified diff in the result summary
- [ ] Writing to a path outside the workspace is rejected with `success=false`
- [ ] Undo stack retains last 10 writes per file; 11th evicts the oldest
- [ ] `file_diff` returns the diff between current file and last write
- [ ] `file_diff` on a file with no undo entry returns "new file" with full contents
- [ ] Agent loop calls file_write → TUI renders diff preview in trace panel
