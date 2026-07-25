---
status: ready-for-agent
blocked-by: ["06-agent-loop-mock-provider"]
---

# 15 — Project Context Loading

**What to build:** The project context system that gives the agent awareness of the codebase. A SQLite-backed file tree index (respecting `.gitignore`) provides fast file lookups. Git-aware smart selection automatically loads the most relevant files into the agent's context — changed files, files mentioned in the prompt, and recently touched files. The TUI Context panel renders which files are loaded and lets the user see the agent's view of the project.

## What to build

### File tree index

On session start (and on-demand refresh), the daemon scans the workspace:

1. Walk the directory tree recursively, respecting `.gitignore` rules
2. For each file, store in SQLite:
   ```sql
   CREATE TABLE file_tree (
       path TEXT PRIMARY KEY,         -- relative to workspace root
       size_bytes INTEGER,
       modified_at TEXT,              -- ISO 8601
       is_binary INTEGER DEFAULT 0,   -- detected from content sniffing
       language TEXT,                  -- detected from extension
       git_status TEXT                 -- 'modified', 'added', 'deleted', 'untracked', 'clean'
   );
   ```
3. Skip: `node_modules`, `.git`, `.mortise`, binary files >1MB, symlinks outside workspace
4. The index is refreshed on-demand (when the agent requests it) or when the TUI opens the Context panel

### Smart context selection

When building the initial system prompt for a new turn, the daemon includes a context payload:

1. **Changed files** — files with `git_status IN ('modified', 'added', 'untracked')`. This is the primary signal — the agent sees what's currently being worked on.
2. **Mentioned files** — if the user's prompt mentions filenames or paths, those are loaded into context regardless of git status.
3. **Recently touched files** — files from the last 3 turns that were read or written by the agent. This maintains continuity across turns.
4. **Project structure summary** — a tree view of the top 3 levels of the workspace (file count per directory, not full paths). This gives the agent spatial awareness without blowing the token budget.

Config controls:
```json
"context": {
    "max_files_in_context": 200,
    "include_git_diff": true,
    "respect_gitignore": true
}
```

- `max_files_in_context`: hard cap. If more files match the selection criteria, prioritize changed > mentioned > touched.
- `include_git_diff`: if true, include the actual diff content for changed files (not just filenames).
- `respect_gitignore`: if true, excluded files never appear in the index.

### Context payload format

The context is injected into the system prompt as structured text:

```
<project_context>
  <workspace>/Users/azeem/Projects/myapp</workspace>
  <git_branch>feature/add-auth</git_branch>

  <files_changed>
    src/auth/login.ts (+23 -5)
    src/auth/middleware.ts (+45 -0)
    tests/auth.test.ts (+12 -0)
  </files_changed>

  <files_mentioned>
    src/config.ts (mentioned in prompt)
  </files_mentioned>

  <project_structure>
    src/ (15 files)
      auth/ (3 files)
      api/ (5 files)
    tests/ (8 files)
    package.json
    tsconfig.json
  </project_structure>
</project_context>
```

### Per-turn context additions

The agent can request additional files via the existing `file_read` tool — no special "add to context" command needed. When the agent reads a file, it's automatically added to the `recently_touched` set for subsequent turns.

### TUI Context panel

The right sidebar's Context panel renders:

```
CONTEXT
─────────────────
  Files loaded: 8/200

  Changed (3):
  M src/auth/login.ts
  A src/auth/middleware.ts
  M tests/auth.test.ts

  Mentioned (1):
  M src/config.ts

  Recent (4):
  R package.json
  R tsconfig.json
  R src/index.ts
  W src/auth/login.ts
```

- `M` = modified, `A` = added, `D` = deleted, `R` = read, `W` = written
- Colors: Sap Green for added, Rust Ochre for deleted, Brass Joint for modified
- Scrollable if files exceed panel height

## Acceptance criteria

- [ ] File tree index is built on session start, respecting `.gitignore`
- [ ] `node_modules`, `.git`, `.mortise`, and binary files >1MB are excluded from the index
- [ ] Git-aware selection loads changed files into the initial context
- [ ] Files mentioned in the user prompt are included in context
- [ ] `max_files_in_context` caps the number of files; changed files take priority
- [ ] TUI Context panel renders changed, mentioned, and recent files with correct git status markers
- [ ] The agent's system prompt includes the `<project_context>` block with file information
- [ ] Context panel updates when files change (via file_write tool calls or git status changes)
