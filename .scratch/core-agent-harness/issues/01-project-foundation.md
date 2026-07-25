---
status: ready-for-agent
blocked-by: []
---

# 01 — Project Foundation

**What to build:** Set up the entire project skeleton — Protobuf schema with code generation for both Go and TypeScript, a pre-commit gate that blocks bad code from being committed, the git branching structure (main/develop/feature), and a CI workflow placeholder ready to wire up when the remote repo is connected.

This ticket delivers a clean, lint-enforced, reproducible foundation that all 15 subsequent tickets build on.

## A. Protobuf schema & code-gen

Define the full Protobuf schema from the spec. Every message listed below must be defined in `.proto` files under a `proto/` directory, with `make proto` regenerating both Go stubs (connect-go) and TypeScript stubs (connect-es). Generated files are committed to the repo.

### Messages to define

- `ServerEvent` — the envelope with `oneof payload`: `PhaseTransitionEvent`, `ThinkingChunk`, `ToolCallPending`, `ToolCallCompleted`, `AgentText`, `SystemStatus`, `SessionSummary`
- `AgentPhase` enum — `IDLE`, `PLANNING`, `ACTING`, `OBSERVING`, `DECIDING`, `PAUSED`, `COMPLETED`, `ERRORED`
- `PhaseTransitionEvent` — `from`, `to`, `reason`
- `ThinkingChunk` — `text`
- `AgentText` — `text`
- `ToolCallPending` — `call_id`, `tool_name`, `parameters_json`, `risk_level`, `requires_approval`
- `RiskLevel` enum — `SAFE`, `MODIFIES_FILES`, `DESTRUCTIVE`
- `ToolCallCompleted` — `call_id`, `tool_name`, `success`, `result_summary`, `duration_ms`, `usage` (UsageDelta), `error_message`
- `UsageDelta` — `input_tokens`, `output_tokens`, `cache_tokens`, `cost_usd`
- `SystemStatus` — `model_id`, `provider_id`, `connected_clients`, `session_token_total`, `session_cost_total`, `session_id`, `session_name`, `workspace_path`, `branch`, `uptime_sec`
- `SessionSummary` — `total_turns`, `total_tool_calls`, `total_tokens`, `total_cost`, `duration_sec`, `error_count`
- `ClientCommand` — `oneof command`: `PauseSession`, `ResumeSession`, `CancelTask`, `SwitchProvider`, `TriggerHandoff`, `ApproveToolCall`, `RejectToolCall`

### Code generation

- Go: `buf generate` or `protoc` producing connect-go service stubs
- TypeScript: `buf generate` or `protoc` producing connect-es stubs
- `make proto` target that regenerates both in one command
- A pre-commit hook verifies generated files are up-to-date (no stale stubs)

## B. Pre-commit gate

Install Husky with lint-staged. The pre-commit hook runs on staged files only and rejects the commit if any check fails.

### Go checks (on staged `*.go` files)
- `gofmt -s -l` — unformatted code is blocked
- `go vet ./...` — vet errors are blocked
- `golangci-lint run` — lint violations are blocked

### TypeScript checks (on staged `*.ts`, `*.tsx` files)
- `prettier --check` — unformatted code is blocked
- `eslint` — lint violations are blocked
- `tsc --noEmit` — type errors are blocked

### Proto checks
- Run `make proto` and verify `git diff --exit-code proto/` — stale generated code is blocked

### General checks (on all staged files)
- Secret detection: block files containing patterns like `sk-`, `api_key`, `-----BEGIN`, `ghp_`, `gho_` unless in `.env.example` or `.gitignore`-coverable locations
- Merge conflict markers: block any file containing `<<<<<<<`, `=======`, `>>>>>>>`
- File size: block any file larger than 1MB

## C. Git branching structure

- `main` — production branch. Only accepts merges from `develop` via PR. No direct pushes.
- `develop` — integration branch. Feature branches merge here after passing pre-commit gates.
- Feature branches: `feature/NN-short-slug` (e.g., `feature/01-project-foundation`, `feature/02-go-daemon-scaffold`)

The initial commit on `main` is a README stub. `develop` branches off `main`. All work for this ticket happens on `feature/01-project-foundation`.

## D. `.gitignore`

Must cover: Go binaries (`mortised`, `*.exe`), `node_modules/`, IDE files (`.vscode/`, `.idea/`), OS files (`.DS_Store`), build artifacts (`dist/`, `bin/`), session data (`.mortise/sessions/`), and any generated proto output that shouldn't be checked in.

## E. CI placeholder

Create a GitHub Actions workflow at `.github/workflows/ci.yml` with the following jobs (commented out or ready to activate once remote is connected):

- `go-lint` — `go vet`, `golangci-lint`, `go test ./...`
- `ts-lint` — `prettier --check`, `eslint`, `tsc --noEmit`
- `proto-check` — `make proto` + diff check

**Branch protection note (to configure on GitHub once remote is connected):**
- Both `main` and `develop` require CI to pass before any PR can merge
- No bypass allowed for any user
- No direct pushes to either branch
- Require at least one approving review (optional, per team policy)

The GitHub Actions file serves as a spec: when the remote is connected and these branch rules are applied, the full CI pipeline activates.

## Acceptance criteria

- [ ] `make proto` compiles without errors and produces clean Go + TypeScript stubs
- [ ] Pre-commit hook blocks: unformatted Go, unformatted TS, type errors, stale proto stubs, secrets in staged files, merge conflict markers, files >1MB
- [ ] `main` and `develop` branches exist with correct lineage
- [ ] `.gitignore` covers all generated and build artifacts
- [ ] `.github/workflows/ci.yml` exists with the three job definitions
