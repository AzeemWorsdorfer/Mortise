# Spec #1: Core Agent Harness

**Status:** Draft v1  
**Depends on:** Nothing (foundational spec)  
**Depended on by:** Spec #2 (Observability), Spec #3 (Provider Agnosticism), Spec #4 (Session Handoff), Spec #5 (Collaboration)  
**Phase:** 1  
**Last updated:** 2026-07-24  

---

## 1. Overview & Scope

The Core Agent Harness is the foundation of Mortise. It defines the agentic loop, tool execution, session lifecycle, file and shell operations, project context loading, and the protocol that connects the Go daemon to the TypeScript TUI client. All other features (observability, provider agnosticism, handoff, collaboration) build on abstractions defined here.

**In scope (v1):**
- Phase-aware agent loop: `Idle → Planning → Acting → Observing → Deciding`
- Built-in tools: file read/write/diff, shell execution, git operations
- Approval gate with three levels: per-tool-type policy, per-invocation override, user interrupt
- Session lifecycle with persistence (SQLite + JSONL event log)
- Project context loading (file tree + smart selection)
- Protobuf protocol between daemon and TUI client
- TUI dashboard layout (Mock 1)
- P1: custom tools, slash commands (`/`), personas, MCP server support, skills (`/skill-name`), `@` references

**Out of scope (v1):**
- Multi-agent sub-task delegation (P2)
- Containerized sandboxing

**User stories served:**
- Solo developer: see every tool call, switch providers, hand off sessions
- Team lead / reviewer: attach to sessions (via Spec #5)
- Platform engineer: structured logs (via Spec #2)

---

## 2. P0/P1/P2 Requirements

### P0 (must-have)

| ID | Requirement | Details |
|----|-------------|---------|
| CH-01 | Phase-aware agent loop | `Idle → Planning → Acting → Observing → Deciding → (loop)`. See §3 for state machine. |
| CH-02 | File operations | `file_read` (auto-approved), `file_write` (with diff preview), `file_diff`. Workspace-root sandbox boundary. Undo stack (last 10 writes). |
| CH-03 | Shell execution | Direct subprocess with configurable allowlist/denylist. Non-interactive only. Streamed stdout/stderr. 120s default timeout. |
| CH-04 | Approval gate | Three levels: per-tool-type policy in config, per-invocation override via risk detection, user interrupt (`Ctrl+P`). Destructive commands always require confirmation. |
| CH-05 | Git awareness | As built-in tools: `git_diff`, `git_status`, `git_commit_draft`. Agent calls them like any tool. |
| CH-06 | Project context loading | Tier 1: file tree (SQLite, respecting `.gitignore`). Tier 3: git-aware smart selection (changed files, mentioned files, touched files). Sent as initial context + per-turn additions. |
| CH-07 | Interruptible execution | `Ctrl+P` pauses the agent. User can resume, cancel, or inspect. Phase transitions to `PAUSED`. |
| CH-08 | Session persistence | SQLite stores session metadata + full conversation history. JSONL stores append-only event log. Resumable across daemon restarts. |
| CH-09 | TUI dashboard | Top bar + Left sidebar (Trace Session + System) + Center (Chat) + Right sidebar (Todos, Files Changed, Recent Activity, Context) + Bottom bar. See §3 for layout. |
| CH-10 | Protobuf protocol | Phase-as-envelope `ServerEvent` with `oneof payload`. See §5 for schema. |

### P1 (should-have)

| ID | Requirement | Details |
|----|-------------|---------|
| CH-11 | Custom tools | Config-driven tool definitions with `${variable}` template resolution. Reuses shell execution pipeline. |
| CH-12 | Slash commands | `/` prefix. `/run tests`, `/commit`, `/handoff`, `/switch openai`, `/skill-name`. Client-side interception with `ClientCommand` proto. |
| CH-13 | Personas | Named system prompt presets in config. Selectable via config key or `/persona name` command. |
| CH-14 | MCP server support | Process-managed MCP servers. Connects on session start, negotiates `tools/list`, registers remote tools with `mcp:` prefix. Status in SYSTEM panel. |
| CH-15 | Skills | `/skill-name` injects a named instruction set into the next turn's system prompt. Defined in config under `skills`. |
| CH-16 | `@` context references | `@file.ts` adds file to context. `@agent-name` reserved for P2 multi-agent delegation. |

### P2 (future consideration)

- Multi-agent sub-task delegation (`@agent-name` spawns scoped sub-agents)

---

## 3. Domain Model / Data Model

### 3.1 Agent Loop State Machine

```
                    ┌─────────────────────────────────────┐
                    │                                     │
                    ▼                                     │
    ┌──────┐   user prompt   ┌──────────┐   plan ready   ┌────────┐
    │ IDLE │────────────────►│ PLANNING │───────────────►│ ACTING │
    └──────┘                 └──────────┘                └────────┘
       ▲                          ▲                          │
       │                          │                          │ tool results
       │                          │                          ▼
       │                    ┌──────────┐   evaluation    ┌───────────┐
       │                    │ DECIDING │◄────────────────│ OBSERVING │
       │                    └──────────┘                 └───────────┘
       │                          │
       │                          │ task complete
       │                          ▼
       │                     ┌───────────┐
       │                     │ COMPLETED │
       │                     └───────────┘
       │
       │  Ctrl+P                  Ctrl+P                Ctrl+P
       │  ┌──────────┐    ┌──────────┐    ┌──────────┐
       └──┤  PAUSED  │◄───┤  PAUSED  │◄───┤  PAUSED  │
          └──────────┘    └──────────┘    └──────────┘
               │                │                │
               │ resume         │ resume         │ resume
               ▼                ▼                ▼
          (return to        (return to       (return to
           prior phase)     prior phase)     prior phase)
```

States: `IDLE`, `PLANNING`, `ACTING`, `OBSERVING`, `DECIDING`, `PAUSED`, `COMPLETED`, `ERRORED`, `CRASHED`.

### 3.2 Session Lifecycle

```
CREATED → RUNNING ⇄ PAUSED
              │
              ├──→ COMPLETED (terminal)
              ├──→ ERRORED → RUNNING (resumable after fix)
              └──→ CRASHED → RUNNING (auto-resume on daemon restart)
```

### 3.3 Go Types

```go
// Agent Loop
type AgentLoop struct {
    phase       AgentPhase
    session     *Session
    provider    Provider
    toolExec    *ToolExecutor
    eventBus    chan ServerEvent
    cancelFn    context.CancelFunc
}

type AgentPhase int
const (
    PhaseIdle AgentPhase = iota
    PhasePlanning
    PhaseActing
    PhaseObserving
    PhaseDeciding
    PhasePaused
    PhaseCompleted
    PhaseErrored
)

// Tool Execution
type ToolRegistry struct {
    builtIn  map[string]Tool
    custom   map[string]Tool
    mcpTools map[string]Tool  // prefixed: "mcp:servername/toolname"
}

type Tool interface {
    Name() string
    Description() string
    ParameterSchema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error)
}

type RiskLevel int
const (
    RiskSafe RiskLevel = iota
    RiskModifiesFiles
    RiskDestructive
)

type ToolResult struct {
    Success      bool
    Output       string
    FilesChanged []string
    DurationMs   int64
    Error        error
}

// Session
type Session struct {
    ID            string
    Name          string
    Status        SessionStatus
    WorkspacePath string
    Branch        string
    Messages      []Message          // full conversation history
    TurnCount     int
    CreatedAt     time.Time
    LastActiveAt  time.Time
    Config        *Config
}

type SessionStatus int
const (
    SessionCreated SessionStatus = iota
    SessionRunning
    SessionPaused
    SessionCompleted
    SessionErrored
    SessionCrashed
)

// Provider Interface (implemented by Spec #3)
type Provider interface {
    ID() string
    ModelID() string
    SendPrompt(ctx context.Context, req PromptRequest) (<-chan ProviderEvent, error)
    EstimateCost(inputTokens, outputTokens int) float64
    ValidateCredentials(ctx context.Context) error
}

type PromptRequest struct {
    Messages    []Message
    Tools       []ToolDefinition
    MaxTokens   int
    Temperature float64
}

type ProviderEvent struct {
    Type     ProviderEventType
    Thinking string
    Text     string
    ToolCall *ToolCall
    Usage    *Usage
    Done     bool
    Error    error
}

type ProviderEventType int
const (
    EventThinking ProviderEventType = iota
    EventText
    EventToolCall
    EventDone
)

type Usage struct {
    InputTokens  int
    OutputTokens int
    CacheTokens  int
}
```

### 3.4 TUI Component Tree (TypeScript / Ink + React)

```
<App>
  <TopBar wordmark path branch status clock />
  <MainLayout>
    <LeftSidebar>
      <TraceSessionPanel events />     {/* TRACE SESSION timeline */}
      <SystemPanel model tokens tools memory uptime />
    </LeftSidebar>
    <CenterPanel>
      <ChatPanel messages>
        <ExecutionPlan steps />        {/* numbered plan during PLANNING */}
        <StepTracker currentStep />    {/* live progress during ACTING */}
      </ChatPanel>
      <InputBar />                     {/* prompt + slash commands */}
    </CenterPanel>
    <RightSidebar>
      <TodosPanel steps />             {/* execution plan with statuses */}
      <FilesChangedPanel files />
      <RecentActivityPanel events />
      <ContextPanel files symbols deps codemap />
    </RightSidebar>
  </MainLayout>
  <BottomBar connected workspace model context tools />
</App>
```

---

## 4. Interface/API Design

### 4.1 Provider Interface

Defined in §3.3. Implemented by Spec #3 (Provider Agnosticism). The agent loop calls `SendPrompt()` which returns a `<-chan ProviderEvent`. Events stream as: `EventThinking` (reasoning) → `EventToolCall` (tool proposal) → `EventDone` (turn complete).

### 4.2 Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    ParameterSchema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (*ToolResult, error)
}
```

Built-in tools register on daemon start. Custom tools are parsed from config. MCP tools are discovered at runtime. All three share the same `Tool` interface.

### 4.3 Config Schema

```jsonc
// .mortise.json (project) overrides ~/.mortise/config.json (global)
{
  "version": 1,
  "persona": "default",
  "tools": {
    "approval": {
      "file_read": "auto",
      "file_write": "confirm",
      "shell_safe": "confirm",
      "shell_destructive": "confirm"
    },
    "shell": {
      "allowed_commands": ["ls", "cat", "git", "npm", "go", "bun"],
      "blocked_patterns": ["rm -rf", "git push --force", "sudo"],
      "sandbox": true,
      "timeout_sec": 120
    },
    "max_parallel_tools": 1,
    "undo_stack_size": 10,
    "custom": [
      {
        "name": "run_tests",
        "description": "Run the project's test suite",
        "parameters": { "filter": { "type": "string" } },
        "command": "npm test -- --filter=${filter}",
        "working_dir": "${workspace}",
        "approval": "auto",
        "timeout_sec": 300
      }
    ]
  },
  "providers": {
    "default": "anthropic",
    "fallback": null,
    "models": {
      "anthropic": { "model": "claude-sonnet-4-20250514" },
      "openai": { "model": "gpt-4o" }
    }
  },
  "personas": {
    "default": "You are Mortise, a coding agent...",
    "concise": "You are Mortise. Be brief. Prefer code over explanation.",
    "architect": "You are Mortise, an expert software architect. Before writing code, always describe the design and trade-offs."
  },
  "skills": {
    "tdd": {
      "description": "Test-driven development workflow",
      "prompt": "You are in TDD mode. Follow red-green-refactor..."
    },
    "code-review": {
      "description": "Review code changes thoroughly",
      "prompt": "Review all changes. Check for: correctness, security..."
    }
  },
  "mcp_servers": [
    {
      "name": "filesystem",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed"],
      "transport": "stdio"
    }
  ],
  "context": {
    "max_turns_before_handoff_hint": 50,
    "include_git_diff": true,
    "max_files_in_context": 200,
    "respect_gitignore": true
  },
  "logging": {
    "level": "info",
    "session_log_dir": "~/.mortise/sessions/"
  }
}
```

### 4.4 Protobuf Schema

```protobuf
syntax = "proto3";
package mortise.v1;

message ServerEvent {
  uint64 timestamp_ms = 1;
  AgentPhase phase = 2;
  int32 turn_number = 3;
  oneof payload {
    PhaseTransitionEvent phase_change = 10;
    ThinkingChunk thinking = 11;
    ToolCallPending tool_pending = 12;
    ToolCallCompleted tool_completed = 13;
    AgentText text = 14;
    SystemStatus status = 15;
    SessionSummary summary = 16;
  }
}

enum AgentPhase {
  PHASE_UNKNOWN = 0;
  IDLE = 1;  PLANNING = 2;  ACTING = 3;
  OBSERVING = 4;  DECIDING = 5;  PAUSED = 6;
  COMPLETED = 7;  ERRORED = 8;
}

message PhaseTransitionEvent {
  AgentPhase from = 1;  AgentPhase to = 2;  string reason = 3;
}

message ThinkingChunk  { string text = 1; }
message AgentText       { string text = 1; }

message ToolCallPending {
  string call_id = 1;  string tool_name = 2;
  string parameters_json = 3;  RiskLevel risk_level = 4;
  bool requires_approval = 5;
}

enum RiskLevel {
  RISK_UNKNOWN = 0;  SAFE = 1;  MODIFIES_FILES = 2;  DESTRUCTIVE = 3;
}

message ToolCallCompleted {
  string call_id = 1;  string tool_name = 2;  bool success = 3;
  string result_summary = 4;  int64 duration_ms = 5;
  UsageDelta usage = 6;  string error_message = 7;
}

message UsageDelta {
  int32 input_tokens = 1;  int32 output_tokens = 2;
  int32 cache_tokens = 3;  double cost_usd = 4;
}

message SystemStatus {
  string model_id = 1;  string provider_id = 2;
  int32 connected_clients = 3;  int32 session_token_total = 4;
  double session_cost_total = 5;  string session_id = 6;
  string session_name = 7;  string workspace_path = 8;
  string branch = 9;  int64 uptime_sec = 10;
}

message SessionSummary {
  int32 total_turns = 1;  int32 total_tool_calls = 2;
  int32 total_tokens = 3;  double total_cost = 4;
  int64 duration_sec = 5;  int32 error_count = 6;
}

message ClientCommand {
  oneof command {
    PauseSession pause = 1;  ResumeSession resume = 2;
    CancelTask cancel = 3;  SwitchProvider switch_provider = 4;
    TriggerHandoff handoff = 5;  ApproveToolCall approve = 6;
    RejectToolCall reject = 7;
  }
}
```

---

## 5. Component Interactions

```
User types prompt in TUI
  → ClientCommand over Unix socket to daemon
    → Agent Loop: IDLE → PLANNING
      → PhaseTransitionEvent broadcast to all TUIs
      → Provider.SendPrompt(system_prompt + context + user_message + tools)
        → Stream returns:
          → EventThinking → ThinkingChunk → TUI renders reasoning
          → EventToolCall → ToolCallPending → TUI shows approval gate
            → User approves (or auto-approve)
              → Tool.Execute()
                → Shell: spawn process, stream stdout/stderr
                → File: compute diff, write to disk, push to undo stack
              → ToolCallCompleted → TUI updates trace + files changed
          → EventDone → Agent Loop: ACTING → OBSERVING
            → Agent evaluates tool results
            → DECIDING: continue with next step, or signal completion
              → (loop back to PLANNING for next turn, or → COMPLETED)
```

---

## 6. Error States & Edge Cases

| Scenario | Behavior |
|----------|----------|
| Provider API returns error mid-stream | Agent transitions to `ERRORED`. Session preserved. User can inspect, fix (e.g., API key), and resume. |
| Tool execution panics or times out | Tool marked as failed. `ToolCallCompleted` with `success=false` sent to agent as feedback. Agent can retry or adjust approach. Stack trace logged to JSONL, not shown to user unless expanded. |
| Daemon process crashes mid-session | On restart, daemon reads last known state from SQLite, sets session to `RUNNING`, and notifies the TUI to reconnect. JSONL contains all events up to crash point. |
| User presses `Ctrl+P` during tool execution | Daemon finishes current tool if safe (< 5s remaining), then transitions to `PAUSED`. If tool is destructive or long-running, daemon sends SIGTERM, waits 3s, then SIGKILL. |
| `file_write` targets path outside workspace | Treated as `RiskDestructive`. Requires explicit confirmation regardless of auto-approve mode. TUI highlights the path in Rust Ochre. |
| Two custom tools have the same name | Daemon refuses to start. Config validation error. |
| MCP server crashes mid-session | All tools from that server become unavailable. In-progress calls return error. Agent is notified. SYSTEM panel updates MCP status to "disconnected." User can restart the server via `/mcp restart <name>`. |
| Session SQLite file is corrupted | Daemon attempts recovery from JSONL event log (replay events to rebuild state). If unrecoverable, session is marked `ERRORED` with a clear message. |
| `@file.ts` references a non-existent file | Agent receives the reference. If it tries to read the file, `file_read` returns "file not found" as a tool result — same as if the agent chose the wrong path. |

---

## 7. Acceptance Criteria

- [ ] **CH-01:** Given a user prompt, agent transitions through `Idle → Planning → Acting → Observing → Deciding` phases with visible phase label in the TUI top bar for each transition.
- [ ] **CH-02:** Given a `file_write` proposal in confirm mode, the TUI renders a unified diff with syntax highlighting and waits for Y/n/edit before writing to disk.
- [ ] **CH-02:** Given 11 sequential `file_write` operations, the undo stack retains only the last 10. The 11th write evicts the oldest entry.
- [ ] **CH-03:** Given a shell command matching a blocked pattern (e.g., `rm -rf /`), it is treated as `DESTRUCTIVE` and blocked pending explicit approval, regardless of auto-approve mode.
- [ ] **CH-04:** A tool call with `risk_level = DESTRUCTIVE` cannot be auto-approved — the config's `shell_destructive: "auto"` is rejected at daemon startup.
- [ ] **CH-05:** Given a session with uncommitted changes, the agent can call `git_diff` and receive a structured diff summary as a tool result.
- [ ] **CH-07:** Given a running agent, pressing `Ctrl+P` transitions the phase to `PAUSED` within 5 seconds, with the option to resume or cancel.
- [ ] **CH-08:** Given a `CRASHED` session (daemon killed mid-session), on daemon restart the session auto-resumes with the full conversation history intact and the agent can continue.
- [ ] **CH-09:** The TUI renders the 5-panel layout (top bar, left sidebar, center chat, right sidebar, bottom bar) and updates all panels within 500ms of a phase transition.
- [ ] **CH-10:** A new TUI connecting to a running session receives a `SystemStatus` event immediately with the current phase, session identity, and token/cost counters.

---

## 8. Dependencies

**Blocks:** Nothing (this is the foundation).

**Required by:**
- **Spec #2 (Observability):** needs `ToolCallPending`, `ToolCallCompleted`, `UsageDelta` events. Hooks into the `ServerEvent` stream. Uses `SessionSummary` for session-level stats.
- **Spec #3 (Provider Agnosticism):** implements the `Provider` interface defined in §3.3/§4.1.
- **Spec #4 (Session Handoff):** consumes `Session.Messages` from SQLite and the JSONL event log. Uses `SessionSummary`.
- **Spec #5 (Collaboration):** extends `ServerEvent` with presence/attach/detach. Uses `ClientCommand` for co-driver actions. Wraps the daemon's event broadcast for multi-client fan-out.

---

## 9. NFRs

| NFR | Target | Applies to |
|-----|--------|-----------|
| Tool call panel update latency | <500ms | TUI trace session + system panels |
| Phase transition broadcast | <100ms | Phase label in top bar |
| Session state persistence | Write every turn, fsync within 1s | SQLite |
| Daemon startup time (cold) | <2s on typical project | Go binary |
| Daemon restart recovery | <5s to auto-resume last session | Crash recovery |
| Config validation | Fail-fast at daemon startup | `.mortise.json` |
| API credentials | Never in logs, JSONL, or exported artifacts | OS keyring |
| Binary size (Go daemon) | <30MB (statically linked) | Portability NFR |

---

## 10. Open Questions

- [ ] MCP transport: stdio only for v1, or also HTTP/SSE? (Pare down scope)
- [ ] Custom tool `working_dir`: `${workspace}` only, or also `${file}` / `${selection}` / `${git_root}` variables?
- [ ] @file reference: should `@` auto-detection work inline (e.g., "look at @src/main.ts") or only as explicit `@file` command?
- [ ] Should the session SQLite file be per-project (`.mortise/sessions.db`) or per-user (`~/.mortise/sessions.db`)?
