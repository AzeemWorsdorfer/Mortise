# Mortise

> Dark-timber agentic coding tool. Phase-aware agent loop, structured tool calls, terminal-native UX.

## Status

Work in progress. Implementation tracked as tickets under `.scratch/`.

## Components

- **Daemon** — Go process, the source of truth. Runs the agent loop, executes tools, persists sessions.
- **TUI Client** — TypeScript (Ink + React) terminal UI. Thin client that connects to the daemon.
- **Protocol** — Protobuf over Connect-RPC (Unix domain socket locally, WebSocket/gRPC remote).

## Repository layout

```
Mortise/
├── proto/                 # Source of truth for the wire format
│   └── mortise/v1/
├── daemon/                # Go daemon (binary: mortised)
│   ├── main.go
│   └── gen/               # Generated proto stubs (committed)
├── tui/                   # TypeScript client (Ink + React)
│   ├── src/
│   │   └── gen/           # Generated proto stubs (committed)
│   └── tsconfig.json
├── docs/                  # Specs, ADRs, agent docs
├── .github/workflows/     # CI
├── .husky/                # Pre-commit hooks
├── scripts/               # Repo-wide check scripts
├── Makefile               # Top-level build / lint / test
├── package.json           # Node tooling (proto plugins, lint, etc.)
├── buf.yaml               # Buf module config
└── buf.gen.yaml           # Buf code-gen targets (Go + TS)
```

## Development

### Prerequisites

- Go 1.22+
- Node 20+ (or Bun)
- `protoc` (`brew install protobuf`)
- `buf`, `protoc-gen-go`, `protoc-gen-connect-go`, `golangci-lint` (installed via `make bootstrap`)

### First-time setup

```bash
make bootstrap       # installs Go-side tools into $GOBIN
npm install          # installs Node-side tools + dependencies
```

### Day-to-day

```bash
make proto           # regenerate Go + TypeScript stubs from proto/
make ci              # full CI pipeline: proto-check + format-check + lint + test
make lint            # all linters (TS + Go)
make format          # auto-format all sources
make test            # all tests
```

### Pre-commit

The pre-commit hook runs automatically on `git commit`. It is wired through
Husky and runs:

1. **lint-staged** — per-file prettier, eslint, gofmt, buf format
2. **Repo-wide checks** — secret scan, conflict-marker scan, file-size cap (1 MB), proto-staleness check

To run the hook manually:

```bash
bash .husky/pre-commit
```

To bypass (use sparingly):

```bash
git commit --no-verify
```

See `.husky/pre-commit` and `scripts/precommit-checks.mjs` for the full source.

## Branching

- `main` — production. Merges only from `develop` via PR; protected on GitHub.
- `develop` — integration. Feature branches merge here via PR; protected on GitHub.
- `feature/NN-short-slug` — single ticket's work. Lives at `feature/01-project-foundation` etc.

Protection rules are applied via `gh` CLI to both `main` and `develop`:
PR required (1 approval), status checks (`go-lint`, `ts-lint`, `proto-check`),
linear history, no force-push / deletion, no admin bypass.

## Documentation

- `CONTEXT.md` — domain glossary
- `docs/specs/01-core-agent-harness.md` — the foundational spec
- `AGENTS.md` — agent instructions for this repo

## Tickets

Implementation work is tracked as numbered tickets under
`.scratch/core-agent-harness/issues/`. Each ticket has YAML frontmatter
(`status`, `blocked-by`, `branch`) and a clear acceptance checklist.
