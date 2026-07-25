---
status: ready-for-agent
blocked-by: ["07-tool-interface-file-read"]
---

# 09 — Shell Execution

**What to build:** A shell execution tool that runs shell commands as subprocesses, streaming stdout and stderr back to the agent. Config-driven allowlist and denylist control which commands can run. Every command is classified by risk level (safe, modifies files, destructive). A 120-second timeout prevents runaway processes. The TUI renders live shell output as it streams.

## What to build

### Shell tool

- **Name:** `shell_exec`
- **Parameters:** `{ "command": { "type": "string" }, "working_dir": { "type": "string" } }`
- **Implementation:** Runs the command via `/bin/sh -c` as a subprocess. `working_dir` defaults to workspace root.

### Command classification

Before execution, every command is classified:

1. **Denylist check** — match the command string against `tools.shell.blocked_patterns` from config (e.g., `"rm -rf"`, `"git push --force"`, `"sudo"`). Any match → `RiskDestructive`.
2. **Allowlist check** — extract the base command (first token). If it's in `tools.shell.allowed_commands`, classify as `RiskModifiesFiles`. If recognized as read-only (`ls`, `cat`, `head`, `tail`, `wc`, `find` without `-delete`), classify as `RiskSafe`.
3. **Default** — any command not in allowed_commands is `RiskDestructive` unless explicitly allowed.

`RiskDestructive` commands always require approval regardless of auto-approve config (the approval gate is ticket 11 — this ticket classifies correctly and sets the risk level on the `ToolCallPending` event).

### Execution

- `exec.CommandContext(ctx, "/bin/sh", "-c", command)` with working directory set
- Stream stdout and stderr line-by-line or in chunks to the event bus as `ToolOutputChunk` events (add a `ToolOutputChunk` to the proto schema if needed, or use `ToolCallCompleted` streaming)
- Context is cancelled if the command exceeds `timeout_sec` from config (default 120s)
- On timeout: SIGTERM → wait 3s → SIGKILL
- Non-interactive only: no TTY allocation, stdin is `/dev/null`
- Environment: inherits the daemon's environment, plus `WORKSPACE=<workspace_root>` and `GIT_DIR` if in a repo

### ToolResult

- `Success`: `true` if exit code is 0, `false` otherwise
- `Output`: combined stdout + stderr (last 10000 chars if output is large)
- `Error`: set if the command was blocked by policy or timed out

### TUI rendering

Live shell output streams to the Trace Session panel:

```
TRACE SESSION
─────────────────
  [Turn 3] shell_exec
    Input:  npm test
    Output: > mortise@0.1.0 test
            > jest
            PASS src/agent_test.go
            ...
    Exit:   0
    Duration: 4.2s  ✓
```

For long-running commands, output appears incrementally as chunks arrive.

## Acceptance criteria

- [ ] Running an allowed command (`ls`, `npm test`) executes and returns stdout/stderr
- [ ] Running a blocked pattern (`rm -rf /`) is classified `RiskDestructive` and sets `requires_approval=true` on the ToolCallPending event
- [ ] Running an unknown command is classified `RiskDestructive` unless added to allowed_commands
- [ ] Command exceeding timeout is killed (SIGTERM → SIGKILL) and returns `success=false` with timeout error
- [ ] `working_dir` parameter is respected (command runs in the specified directory)
- [ ] TUI renders live shell output as it streams
