# CODING_STANDARDS.md

Coding standards for the Mortise project. Applies to all contributors (human and agent).

---

## 1. General Principles

These rules apply to every language in the project.

### Human readability first

Code is written for the next developer, not the compiler. Prefer clarity over cleverness.
If a reviewer has to ask "what does this do?", rewrite it.

### Clean code

- No clever one-liner chains that sacrifice readability.
- No premature optimization — profile before optimizing.
- Delete dead code; don't comment it out. Git remembers.

### Separation of concerns

One file = one responsibility. Modules align with the domain concepts defined in
[`CONTEXT.md`](./CONTEXT.md): Agent Loop, Tool Execution, Session Persistence, etc.

If a file describes itself as doing "X and also Y", split it.

### No super functions

Hard limits:

| Language                   | Max function / component | Max file  |
| -------------------------- | ------------------------ | --------- |
| Go                         | 50 lines                 | 500 lines |
| TypeScript (non-component) | 50 lines                 | —         |
| TypeScript (component)     | 200 lines                | 300 lines |

Exceptions (no limit applies):

- Generated code (`daemon/gen/`, `tui/src/gen/`, `**/*.gen.ts`)
- Large constant / enum lookup tables
- Test data fixtures
- Table-driven tests in Go (the test function itself may exceed 50 lines)

### No massive files

If a file approaches its limit, split it within the same package / directory. Don't
create `utils` or `helpers` dumping grounds — split along domain boundaries.

### Naming conventions

- Be obvious and consistent.
- No single-letter variables outside loop indices (`i`, `j`) and error variables (`err`).
- No abbreviations unless universally understood: `ctx`, `req`, `resp`, `err`, `msg`.
- Use the domain terms from `CONTEXT.md`. Don't invent synonyms.

### Documentation

- **File-level doc comment** — required on every non-generated source file.
  Follow the template in each language section below.
- **Function-level doc comment** — required on every exported function / component /
  method. Describe _what_ it does, not _how_.
- **Inline comments** — only when the "why" isn't obvious from the code.
  Never comment the "what" (the code already says that).

### File-level doc comment template

Every source file starts with a comment answering three questions:

1. **What does this file do?** (one line)
2. **What is it responsible for?** (bullet list)
3. **What is it NOT responsible for?** (bullet list — what it delegates or excludes)

Include a reference to the relevant spec chapter, ADR, or related module.

---

## 2. Go Standards

Applies to all code under `daemon/`.

Built on [Effective Go](https://go.dev/doc/effective_go) and enforced by
`gofmt -s`, `go vet`, and `golangci-lint`.

### File-level doc comment

```go
// Package foo manages <one-line summary>.
//
// Responsibilities:
//   - <bullet list of what this package owns>
//
// Is NOT responsible for:
//   - <bullet list of what this package explicitly excludes or delegates>
//
// See: docs/specs/01-core-agent-harness.md §<chapter>
package foo
```

Place this in every package's primary file. If a package has many files, put it in
`doc.go` (Go convention) or the most foundational file in the package.

### Naming

- **Exported**: PascalCase (`AgentLoop`, `ToolRegistry`)
- **Unexported**: camelCase (`runPhase`, `pendingCalls`)
- **Acronyms**: all-caps (`HTTPClient`, `SQLStore`, `JSONLog`, `MCPRegistry`)
- **Packages**: lowercase, single word, no underscores (`agent`, `tools`, `session`)
- **No** `utils`, `helpers`, `common`, or `misc` packages. Every package has a
  clear domain responsibility.

### Package organization

One domain concept per package. Expected structure as the project grows:

```
daemon/
├── agent/         # Agent loop state machine
├── tools/         # Tool registry, built-in tool implementations
├── session/       # Session lifecycle, SQLite persistence
├── providers/     # LLM provider adapters
├── proto/         # Connect-RPC server implementation
├── config/        # .mortise.json loading and validation
├── mcp/           # MCP server process management
└── eventlog/      # JSONL event log writer/reader
```

Do not import across sibling packages in a circular way. If two packages need to
share types, extract a shared `types` or `models` package — but only if the types
are truly cross-cutting.

### Function size

- Maximum: **50 lines**.
- If a function grows beyond this, extract helper functions (unexported, same file
  or same package).
- Maximum **5 parameters**. If you need more, define a config struct:

  ```go
  type RunConfig struct {
      Model    string
      Provider string
      MaxTurns int
      DryRun   bool
      // ...
  }

  func (a *Agent) Run(ctx context.Context, cfg RunConfig) error { ... }
  ```

### Error handling

- Always check errors. Never discard with `_`.
- Wrap errors with context using `fmt.Errorf`:

  ```go
  result, err := tool.Execute(ctx, params)
  if err != nil {
      return fmt.Errorf("executing tool %s: %w", tool.Name(), err)
  }
  ```

- Define custom error types only when callers need to distinguish them
  programmatically (e.g., `ErrRetryable`, `ErrToolNotFound`).
- Use `errors.Is` and `errors.As` for error inspection, not type assertions.

### Interfaces

- Define interfaces where they are **consumed** (caller side), not where they are
  implemented. This keeps interfaces small and focused.
- Keep interfaces small: prefer **≤5 methods**. If an interface grows larger,
  consider splitting it.
- Single-method interfaces use the `-er` suffix: `Logger`, `Executor`, `Reader`.

  ```go
  // Good: small, consumer-defined interface
  type ToolRunner interface {
      Run(ctx context.Context, params json.RawMessage) (ToolResult, error)
  }
  ```

### Context

Every function that performs I/O or crosses a module boundary accepts
`context.Context` as its **first parameter**:

```go
func (s *Store) SaveSession(ctx context.Context, session Session) error { ... }
```

Never store a context in a struct.

### Concurrency

- Prefer channels over mutexes for communication between goroutines.
- Use `sync.WaitGroup` for goroutine lifecycle management.
- All shared state must be documented with its concurrency guarantees
  ("not goroutine-safe", "safe under a mutex", "lock-free", etc.).

### Tests

- Table-driven tests preferred.
- One `*_test.go` file per source file.
- Use the standard `testing` package. No external assertion libraries unless
  explicitly adopted by the team.
- Test function names: `Test<FunctionName>_<Scenario>`.

  ```go
  func TestExecuteTool_Success(t *testing.T) { ... }
  func TestExecuteTool_ToolNotFound(t *testing.T) { ... }
  ```

### Tooling (enforced)

| Tool                | What it checks              | When            |
| ------------------- | --------------------------- | --------------- |
| `gofmt -s`          | Formatting + simplification | pre-commit      |
| `go vet`            | Static analysis             | pre-commit      |
| `golangci-lint run` | Full lint suite             | pre-commit + CI |

All enforced by `.husky/pre-commit` and `.github/workflows/ci.yml`.

---

## 3. TypeScript Standards

Applies to all code under `tui/`.

Built on the project's strict `tsconfig.json`, ESLint, and Prettier configs.

### File-level doc comment

```typescript
/**
 * <One-line summary of what this module does>.
 *
 * Responsibilities:
 *   - <bullet list>
 *
 * Is NOT responsible for:
 *   - <bullet list>
 *
 * See: docs/specs/01-core-agent-harness.md §<chapter>
 */
```

### Naming

| What                    | Convention       | Example                                    |
| ----------------------- | ---------------- | ------------------------------------------ |
| Component files         | `kebab-case.tsx` | `trace-panel.tsx`, `session-header.tsx`    |
| Utility/hook/type files | `camelCase.ts`   | `useSession.ts`, `formatDuration.ts`       |
| Components              | PascalCase       | `TracePanel`, `SessionHeader`              |
| Functions / hooks       | camelCase        | `useSession`, `formatDuration`             |
| Constants               | UPPER_SNAKE_CASE | `MAX_RETRY_COUNT`, `DEFAULT_POLL_INTERVAL` |
| Types / interfaces      | PascalCase       | `SessionData`, `PanelProps`                |

### Component structure

- **One exported component per file.**
- Small private sub-components allowed in the same file if **<30 lines each**.
- If a component exceeds **200 lines**: extract logic into custom hooks,
  split into sub-components, or both.
- Component props always use a typed interface:

  ```typescript
  interface TracePanelProps {
    sessionId: string;
    onClose: () => void;
  }

  export function TracePanel({ sessionId, onClose }: TracePanelProps): React.ReactElement {
    // ...
  }
  ```

### File size

- Maximum: **300 lines**.
- If a component file approaches this limit, extract sub-components into their
  own files within the same directory.

### Function size (non-component)

- Maximum: **50 lines**.
- Extract helper functions or custom hooks when a function grows beyond this.

### Type safety

- **No `any`.** If truly unavoidable, document with:

  ```typescript
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const data: any = await parseUntypedInput(raw);
  // Reason: third-party library returns unstructured JSON without types
  ```

- Use `import type` for type-only imports (enforced as ESLint warning).
- Prefer `interface` for object shapes. Use `type` for unions, intersections,
  and type aliases.
- Derive types from protobuf-generated stubs when representing wire data.

### Hooks

- Custom hooks live in `tui/src/hooks/`.
- One hook per file.
- All hook names start with `use`: `useSession`, `useAgentPhase`, `useToolStream`.
- Hooks must not contain JSX. If a hook needs to return markup, it should be a
  component instead.

### Imports

Order imports in this sequence, with a blank line between groups:

1. Node built-ins (`node:path`, `node:os`)
2. External packages (`react`, `ink`)
3. Generated Protobuf stubs (`./gen/mortise/v1/...`)
4. Local modules (`./hooks/...`, `./components/...`, `./utils/...`)

```typescript
import { readFile } from 'node:fs/promises';

import React, { useState } from 'react';
import { Text, Box } from 'ink';

import { AgentPhase } from './gen/mortise/v1/agent_pb.js';

import { useSession } from './hooks/useSession.js';
```

### No classes

This project uses React function components and hooks exclusively. No class
components, no class-based state management.

### React/Ink specifics

- Use `useState` and `useReducer` for local state; avoid `useRef` for mutable
  state that should trigger re-renders (Ink rendering is synchronous).
- Side effects in `useEffect` with proper cleanup.
- Terminal output width: use `process.stdout.columns` or Ink's `useStdout()`
  hook for responsive layouts. Don't hardcode widths.
- All text that faces the user must be clear and descriptive. No cryptic error
  codes without human-readable messages.

### Tests

- Test files: `*.test.ts(x)` co-located with the source file, or in a
  `__tests__/` subdirectory.
- Use the project's chosen test framework consistently.

### Tooling (enforced)

| Tool                      | What it checks | When            |
| ------------------------- | -------------- | --------------- |
| `prettier --check`        | Formatting     | pre-commit + CI |
| `eslint --max-warnings 1` | Linting        | pre-commit + CI |
| `tsc --noEmit`            | Type safety    | pre-commit + CI |

All enforced by `.husky/pre-commit` and `.github/workflows/ci.yml`.

---

## 4. Protobuf Standards

Applies to all code under `proto/`.

### File header

Every `.proto` file starts with a doc comment:

```protobuf
// Mortise — <one-line purpose>.
//
// Canonical source for <what this proto file defines>.
// Generated stubs are committed to:
//   - daemon/gen/mortise/v1/   (Go, via connect-go)
//   - tui/src/gen/mortise/v1/  (TypeScript, via connect-es)
//
// Regenerate with: `make proto`
// See: docs/specs/01-core-agent-harness.md §<chapter>
```

### Naming

| What        | Convention                        | Example                          |
| ----------- | --------------------------------- | -------------------------------- |
| Messages    | PascalCase                        | `ServerEvent`, `ToolCallPending` |
| Enums       | PascalCase                        | `AgentPhase`, `RiskLevel`        |
| Fields      | snake_case                        | `timestamp_ms`, `tool_name`      |
| Enum values | UPPER_SNAKE_CASE with type prefix | `PHASE_IDLE`, `RISK_SAFE`        |
| Services    | PascalCase + `Service` suffix     | `AgentService`                   |
| RPC methods | PascalCase                        | `StreamEvents`, `SendCommand`    |

### Organization

Within a `.proto` file:

1. Enums first
2. Messages grouped by relationship (request/response pairs together)
3. Services last

### Regeneration

- Always run `make proto` after editing `.proto` files.
- Commit generated stubs in `daemon/gen/` and `tui/src/gen/`.
- **Never hand-edit generated files.** If you need different output, change the
  `.proto` source or the `buf.gen.yaml` configuration.

### Tooling (enforced)

| Tool               | What it checks                 | When                         |
| ------------------ | ------------------------------ | ---------------------------- |
| `buf lint`         | Schema validation              | manual / CI                  |
| `buf format -w`    | Formatting                     | pre-commit (via lint-staged) |
| `make proto-check` | Generated stubs are up-to-date | pre-commit + CI              |

---

## 5. Enforcement Summary

| Rule            | Go enforcement                 | TypeScript enforcement         | When            |
| --------------- | ------------------------------ | ------------------------------ | --------------- |
| Formatting      | `gofmt -s`                     | `prettier`                     | pre-commit      |
| Linting         | `golangci-lint`                | `eslint`                       | pre-commit + CI |
| Type safety     | `go vet`                       | `tsc --noEmit`                 | pre-commit + CI |
| File size       | PR review                      | PR review                      | PR review       |
| Function size   | PR review                      | PR review                      | PR review       |
| Doc comments    | PR review                      | PR review                      | PR review       |
| Proto staleness | `make proto-check`             | `make proto-check`             | pre-commit + CI |
| Secret leaks    | `scripts/precommit-checks.mjs` | `scripts/precommit-checks.mjs` | pre-commit      |

Automated checks run on every commit (`.husky/pre-commit`) and every push to
`main` or `develop` (`.github/workflows/ci.yml`).

Size limits and doc comment quality are enforced by code review — they are not
automated, but they are blocking. A PR that introduces a 600-line file or a
docless exported function should not be merged.

---

## References

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [React TypeScript Cheatsheet](https://react-typescript-cheatsheet.netlify.app/)
- [Buf Style Guide](https://buf.build/docs/best-practices/style-guide/)
- [`CONTEXT.md`](./CONTEXT.md) — domain glossary
- [`docs/specs/01-core-agent-harness.md`](./docs/specs/01-core-agent-harness.md) — foundational spec
