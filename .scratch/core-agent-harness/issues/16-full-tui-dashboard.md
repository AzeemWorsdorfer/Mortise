---
status: ready-for-agent
blocked-by: ["11-approval-gate", "12-interrupt-system", "14-crash-recovery", "15-project-context-loading"]
---

# 16 — Full TUI Dashboard

**What to build:** The complete terminal UI dashboard — the 5-panel layout as designed in the spec, rendered with Ink + React and the Mortise brand system (dark timber background, Brass Joint accent, Sap Green success, Rust Ochre error). Every panel from tickets 03–15 is integrated into a cohesive single-screen dashboard that updates within 500ms of any daemon event. This is the visual capstone of Phase 0 — the TUI that a user actually interacts with.

## What to build

### Full component tree

```
<App>
  <TopBar wordmark path branch status clock />
  <MainLayout>
    <LeftSidebar>
      <TraceSessionPanel events />
      <SystemPanel model tokens tools memory uptime />
    </LeftSidebar>
    <CenterPanel>
      <ChatPanel messages>
        <ExecutionPlan steps />
        <StepTracker currentStep />
      </ChatPanel>
      <InputBar />
    </CenterPanel>
    <RightSidebar>
      <TodosPanel steps />
      <FilesChangedPanel files />
      <RecentActivityPanel events />
      <ContextPanel files symbols deps />
    </RightSidebar>
  </MainLayout>
  <BottomBar connected workspace model context tools />
</App>
```

### Panel specifications

**TopBar** (fixed, 1 row):
- Left: "Mortise" wordmark in Brass Joint (#D4A03C)
- Center: current workspace path (truncated if too long) + git branch in parentheses
- Right: phase indicator (e.g., `[PLANNING]`) + elapsed clock (HH:MM:SS since session start)

**LeftSidebar** (25% width):
- *Trace Session* — scrollable timeline of all `ToolCallPending`/`ToolCallCompleted` events. Each entry shows: tool name, input summary, result summary, duration, success/failure icon. Color-coded by risk level. Auto-scrolls to latest.
- *System* — static info panel: model ID, provider, connected clients count, session token total, session cost total, daemon uptime, memory usage (if available). Updates on every `SystemStatus` event.

**CenterPanel** (50% width):
- *Chat* — scrollable message log. User messages left-aligned, agent messages right-aligned. Tool calls inline. Thinking/reasoning in dimmed text. Execution plan rendered as numbered list during `PLANNING` phase. Step tracker shows "Step 2/5" during `ACTING`.
- *InputBar* — single-line text input at the bottom of the center panel. Supports slash commands (`/` prefix), `@` references (placeholder for P1), and multi-line paste. Enter sends the prompt; Shift+Enter inserts a newline. Shows character count.

**RightSidebar** (25% width):
- *Todos* — the current execution plan as a checklist. Steps marked ✓ (completed), → (in progress), or ○ (pending). Updates on plan changes.
- *Files Changed* — list of files modified in the current session. Each entry shows: path, change type (added/modified/deleted), diff stat (+N -N). Updates on file_write tool completions.
- *Recent Activity* — last 10 events (phase transitions, tool calls) in compact form. A quick-glance timeline.
- *Context* — from ticket 15. Scrollable list of loaded context files with git status markers.

**BottomBar** (fixed, 1 row):
- Left: connection status (● green / ○ red) + "Connected to mortised" or "Reconnecting…"
- Center: workspace path (short form)
- Right: model name, context files count, registered tools count

### Brand system

Apply the Mortise brand from the design system:

- **Background:** Dark timber (#1A1817)
- **Accent/Primary:** Brass Joint (#D4A03C) — wordmark, active phase, selected items
- **Success:** Sap Green (#6B8E4E) — completed steps, successful tool calls, connected status
- **Error:** Rust Ochre (#C4553F) — errors, destructive risk, disconnected status
- **Text:** Off-white (#E8E4DD) primary, warm gray (#8A857D) secondary
- **Borders:** Dark warm (#2A2724)
- **Fonts:** JetBrains Mono for display (wordmark, phase label), IBM Plex Mono for code (diffs, paths, shell output), IBM Plex Sans for body text (chat messages, labels)
- (Note: terminal font rendering is limited to the terminal's configured font. Use ANSI escape codes for bold, dim, and color. Actual font selection depends on the terminal emulator.)

### Performance

- Phase transition to TUI update: <100ms
- Tool call panel update: <500ms
- Large chat history (100+ messages): render without frame drops (use React.memo, virtualization if needed)
- Scroll performance: smooth scrolling in Chat and Trace Session panels

### Responsive / resizing

- Panels resize proportionally when the terminal window is resized
- Minimum terminal size: 120×40. Below that, panels collapse gracefully (sidebar panels hide, center takes full width).
- Ink's `useStdout` dimensions hook drives the layout calculations.

### Loading states

- On TUI start before daemon connection: show a splash/connecting screen with the wordmark
- On daemon disconnect: overlay "Connection lost — reconnecting…" with a spinner
- On session load (recovering from crash): show "Loading session…" with progress indicator

## Acceptance criteria

- [ ] All 5 main areas (top bar, left sidebar, center, right sidebar, bottom bar) render on startup
- [ ] Phase transitions update the top bar phase indicator and the Trace Session panel within 100ms
- [ ] Tool calls (file_read, file_write, shell_exec, git_diff, git_status) appear in Trace Session with correct formatting, duration, and status
- [ ] User prompts typed in the InputBar appear in the Chat panel; agent responses stream in
- [ ] Approval dialog (from ticket 11) renders inline in the Chat panel when a tool requires confirmation
- [ ] Pause overlay (from ticket 12) appears on Ctrl+P with Resume/Cancel options
- [ ] Files Changed panel updates on file_write completions
- [ ] Context panel shows changed/mentioned/recent files with git status markers
- [ ] Connection status in BottomBar reflects daemon state (connected, reconnecting, disconnected)
- [ ] Terminal resize reflows all panels proportionally; below 120×40, sidebars collapse
- [ ] Brand colors are applied: Brass Joint for accent, Sap Green for success, Rust Ochre for errors
