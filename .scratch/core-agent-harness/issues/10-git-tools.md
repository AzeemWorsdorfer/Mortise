---
status: ready-for-agent
blocked-by: ["07-tool-interface-file-read"]
---

# 10 — Git Tools

**What to build:** Three git-aware built-in tools that the agent can call like any other tool: `git_diff` for reviewing changes, `git_status` for checking repo state, and `git_commit_draft` for staging and drafting commits. These are the foundation of Mortise's git awareness — the agent can inspect and propose changes without the daemon having special git privileges beyond what the agent requests.

## What to build

### git_diff tool

- **Name:** `git_diff`
- **Parameters:** `{ "staged": { "type": "boolean" } }` — if true, show staged diff; default false shows unstaged
- **Risk:** `RiskSafe`
- **Behavior:** Runs `git diff` (or `git diff --staged`) in the workspace root. Returns the unified diff output as the result. If no git repo exists, returns an informative error.

### git_status tool

- **Name:** `git_status`
- **Parameters:** none
- **Risk:** `RiskSafe`
- **Behavior:** Runs `git status --porcelain` and returns the porcelain output. Parse it into a structured summary: staged changes (M, A, D, R), unstaged changes (M, D), untracked files (??). The structured summary is included alongside the raw porcelain output for easier consumption by the model.

### git_commit_draft tool

- **Name:** `git_commit_draft`
- **Parameters:** `{ "message": { "type": "string" }, "files": { "type": "array", "items": { "type": "string" } } }` — commit message and optional file list to stage
- **Risk:** `RiskModifiesFiles` — does NOT push, only stages and commits locally
- **Behavior:**
  1. If `files` is provided, runs `git add <files...>`
  2. Runs `git commit -m "<message>"`
  3. Returns the commit hash and summary
  - Does NOT push. Does NOT amend. Does NOT force. Only a simple local commit.
  - If any of these operations fail, returns `success=false` with the git error message

### Implementation

All three tools use the shell execution pipeline (ticket 09) internally but are registered as separate, named tools. This gives the agent explicit, typed tool definitions rather than a generic "run any git command" surface.

The `git_diff` and `git_status` tools are `RiskSafe` — they read repo state without modifying anything.

`git_commit_draft` is `RiskModifiesFiles` — it modifies the git index and creates a commit object, but never pushes to a remote.

### TUI rendering

Git tool calls appear in the Trace Session panel with color-coded output:

```
TRACE SESSION
─────────────────
  [Turn 4] git_diff
    Result: src/agent.go  | 3 ++-
            src/tools.go  | 12 ++++++++++--
            2 files changed, 13 insertions(+), 3 deletions(-)
    Duration: 45ms  ✓

  [Turn 5] git_status
    Result: M  src/agent.go
            M  src/tools.go
            ?? src/newfile.go
            2 modified, 1 untracked
    Duration: 32ms  ✓
```

## Acceptance criteria

- [ ] `git_diff` returns the unified diff for the workspace repo
- [ ] `git_diff` returns an informative error when the workspace is not a git repo
- [ ] `git_status` returns structured porcelain output (staged/unstaged/untracked breakdown)
- [ ] `git_commit_draft` stages specified files, commits with the given message, and returns the commit hash
- [ ] `git_commit_draft` does NOT push or modify remote state
- [ ] All three tools appear in the tool registry and can be called by the agent loop
- [ ] TUI renders git_diff and git_status results in the trace panel
