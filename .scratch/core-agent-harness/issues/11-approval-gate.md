---
status: ready-for-agent
blocked-by: ["08-file-write-file-diff", "09-shell-execution"]
---

# 11 — Approval Gate

**What to build:** The approval gate — the mechanism that blocks, allows, or auto-approves tool calls based on their risk level and the user's configured policy. Every `ToolCallPending` event carries a `risk_level` and a `requires_approval` flag. Destructive commands can never be auto-approved. The TUI renders an approval dialog for any tool call that needs confirmation before execution proceeds.

## What to build

### Risk classification

Every tool registers a base `RiskLevel`. The shell tool additionally checks the command against the allowlist/denylist (from ticket 09) and may escalate the risk level. The final risk level is attached to `ToolCallPending.risk_level`.

Risk levels:
- `RiskSafe` (0) — read-only operations: `file_read`, `git_diff`, `git_status`
- `RiskModifiesFiles` (1) — writes within workspace: `file_write`, `git_commit_draft`, shell commands in allowlist
- `RiskDestructive` (2) — shell outside workspace, blocked patterns, `rm -rf`, `sudo`, `git push --force`

### Approval policy engine

Read `tools.approval` from config. The policy is a map from tool category to mode:

```json
{
  "file_read": "auto",
  "file_write": "confirm",
  "shell_safe": "confirm",
  "shell_destructive": "confirm"
}
```

Modes:
- `"auto"` — tool executes immediately. No TUI prompt. Agent proceeds without waiting.
- `"confirm"` — tool is paused. TUI shows approval dialog. Agent waits for user input.

**Hard rule:** `RiskDestructive` tools can NEVER be auto-approved. The daemon validates this at startup: if config says `shell_destructive: "auto"`, the daemon refuses to start with a clear error message. This is a safety invariant.

### Approval flow in AgentLoop

When a tool call is proposed:

1. `ToolCallPending` is emitted with `risk_level` and `requires_approval` set
2. If `requires_approval == false`: execute immediately, emit `ToolCallCompleted`
3. If `requires_approval == true`: the agent loop pauses and waits for a `ClientCommand` from the TUI:
   - `ClientCommand.ApproveToolCall` → execute the tool, emit `ToolCallCompleted`
   - `ClientCommand.RejectToolCall` → emit `ToolCallCompleted` with `success=false` and `error_message: "Rejected by user"`
   - Timeout (60s default): auto-reject with `error_message: "Approval timed out"`

### TUI approval dialog

When the TUI receives a `ToolCallPending` with `requires_approval=true`, it renders an approval prompt:

```
┌─ Approval Required ─────────────────────────────────────────────┐
│                                                                  │
│  Tool:     file_write                                            │
│  File:     src/main.go                                           │
│  Risk:     Modifies Files                                        │
│                                                                  │
│  ── Diff Preview ────────────────────────────────────────────── │
│  -  old line                                                     │
│  +  new line                                                     │
│  ─────────────────────────────────────────────────────────────── │
│                                                                  │
│  [Y] Approve    [n] Reject    [e] Edit                           │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

For `RiskDestructive` tools, the dialog uses the Rust Ochre brand color and shows an additional warning:

```
│  ⚠ DESTRUCTIVE COMMAND — This cannot be auto-approved.          │
│  Command:  rm -rf /path/to/stuff                                 │
│                                                                  │
│  [y] I understand the risk — execute    [N] Reject               │
```

- `Y` or `y` → sends `ClientCommand.ApproveToolCall`
- `n` or `N` → sends `ClientCommand.RejectToolCall`
- `e` (for file_write) → opens the diff in an editor (defer to future ticket; for now, reject + prompt user to edit manually)
- Destructive: only `y` (explicit lowercase to confirm understanding) or `N` (reject)

### Config validation at startup

The daemon must refuse to start if:
- `tools.approval` contains a key that matches no known risk level
- `shell_destructive` is set to `"auto"` (hard safety invariant)
- Any approval mode is not one of `"auto"` or `"confirm"`

## Acceptance criteria

- [ ] `file_read` (RiskSafe) with `auto` policy executes immediately without TUI prompt
- [ ] `file_write` (RiskModifiesFiles) with `confirm` policy pauses agent loop and waits for user approval
- [ ] TUI shows approval dialog for a confirm-mode file_write with diff preview
- [ ] User approves → tool executes → ToolCallCompleted with success
- [ ] User rejects → ToolCallCompleted with success=false and rejection message
- [ ] `RiskDestructive` shell command shows "This cannot be auto-approved" warning with Rust Ochre coloring
- [ ] Daemon refuses to start if config sets `shell_destructive: "auto"` with clear error message
- [ ] Approval timeout (60s) auto-rejects the tool call
