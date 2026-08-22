/**
 * status-panel.tsx — Renders the Mortise agent dashboard.
 *
 * Responsibilities:
 *   - Displays system status (session, model, uptime, tokens, cost)
 *   - Shows live agent phase badge with color coding
 *   - Renders phase transition history
 *   - Renders agent output (thinking chunks, text responses)
 *   - Displays tool call status (pending/completed/failed)
 *   - Shows session summary on completion
 *
 * Is NOT responsible for:
 *   - Transport or daemon connection (see: transport.ts)
 *   - State management or event dispatch (see: main.tsx)
 *   - Mock data generation (see: mock.ts)
 *
 * See: ticket 03 — TS TUI Scaffold, ticket 06 — Agent Loop
 */

import React from 'react';
import { Box, Text } from 'ink';

import type { SystemStatus, AgentPhase, PhaseTransitionEvent } from '../gen/mortise/v1/agent_pb.js';

import type { ConnectionState } from '../transport.js';
import { TraceSession, type ToolCallEntry } from './trace-session.js';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type { ToolCallEntry };

export interface AppState {
  status: SystemStatus | null;
  phase: AgentPhase;
  phaseHistory: PhaseTransitionEvent[];
  thinkingLines: string[];
  textLines: string[];
  toolCalls: ToolCallEntry[];
  summary: string | null;
  connectionState: ConnectionState;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const PHASE_LABELS: Record<number, string> = {
  0: '???',
  1: 'IDLE',
  2: 'PLANNING',
  3: 'ACTING',
  4: 'OBSERVING',
  5: 'DECIDING',
  6: 'PAUSED',
  7: 'COMPLETED',
  8: 'ERRORED',
};

const PHASE_COLORS: Record<number, string> = {
  0: 'gray',
  1: 'gray',
  2: 'blue',
  3: 'yellow',
  4: 'cyan',
  5: 'magenta',
  6: 'yellow',
  7: 'green',
  8: 'red',
};

function formatUptime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s}s`;
}

function formatCost(cost: number): string {
  return `$${cost.toFixed(2)}`;
}

// formatBackoff renders a backoff delay in whole seconds when it is
// an exact multiple of 1000ms (2s, 4s, 8s, …), milliseconds otherwise.
function formatBackoff(ms: number): string {
  if (ms >= 1000 && ms % 1000 === 0) {
    return `${ms / 1000}s`;
  }
  return `${ms}ms`;
}

function statusLabel(state: ConnectionState): string {
  switch (state.kind) {
    case 'connected':
      return 'Connected \u2713';
    case 'reconnecting':
      return `Reconnecting\u2026 (attempt ${state.attempt}, next in ${formatBackoff(state.nextInMs)})`;
    case 'daemon-not-running':
      return `Daemon not running. Start with \`mortised\``;
  }
}

function phaseLabel(phase: AgentPhase): string {
  return PHASE_LABELS[phase] ?? '???';
}

function phaseColor(phase: AgentPhase): string {
  return PHASE_COLORS[phase] ?? 'gray';
}

function formatPhaseHistory(history: PhaseTransitionEvent[]): string {
  return history.map((ev) => `${phaseLabel(ev.from)} \u2192 ${phaseLabel(ev.to)}`).join('  ');
}

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

export function StatusPanel({ state }: { state: AppState }): React.ReactElement {
  const { status, phase, connectionState } = state;
  const sessionName = status?.sessionName || 'untitled';
  const modelId = status?.modelId || '\u2014';
  const providerId = status?.providerId || '\u2014';
  const uptimeSec = status ? Number(status.uptimeSec) : 0;
  const tokens = status?.sessionTokenTotal ?? 0;
  const cost = status?.sessionCostTotal ?? 0;

  return (
    <Box flexDirection="column">
      {/* ---- Top bar: session info + phase ---- */}
      <Box borderStyle="single" paddingLeft={1} paddingRight={1}>
        <Box flexDirection="column">
          <Box>
            <Text bold>{' Mortise '}</Text>
            <Text color={phaseColor(phase)}>[{phaseLabel(phase)}]</Text>
          </Box>
          <Text>
            {'Session: '}
            <Text dimColor>{sessionName}</Text>
          </Text>
          <Text>
            {'Model:   '}
            <Text dimColor>
              {modelId} ({providerId})
            </Text>
          </Text>
          <Text>
            {'Status:  '}
            <Text
              color={
                connectionState.kind === 'connected'
                  ? 'green'
                  : connectionState.kind === 'reconnecting'
                    ? 'yellow'
                    : 'red'
              }
            >
              {statusLabel(connectionState)}
            </Text>
          </Text>
          <Text>
            {'Uptime:  '}
            <Text dimColor>{formatUptime(uptimeSec)}</Text>
          </Text>
          <Text>
            {'Tokens:  '}
            <Text dimColor>{tokens} total</Text>
          </Text>
          <Text>
            {'Cost:    '}
            <Text dimColor>{formatCost(cost)}</Text>
          </Text>
        </Box>
      </Box>

      {/* ---- Phase history ---- */}
      {state.phaseHistory.length > 0 && (
        <Box borderStyle="single" paddingLeft={1} paddingRight={1} marginTop={1}>
          <Box flexDirection="column">
            <Text bold>Phase History</Text>
            <Text dimColor>{formatPhaseHistory(state.phaseHistory)}</Text>
          </Box>
        </Box>
      )}

      {/* ---- Thinking / Agent text ---- */}
      {(state.thinkingLines.length > 0 || state.textLines.length > 0) && (
        <Box borderStyle="single" paddingLeft={1} paddingRight={1} marginTop={1}>
          <Box flexDirection="column">
            <Text bold>Agent Output</Text>
            {state.thinkingLines.map((line, i) => (
              <Text key={`think-${i}`} dimColor>
                {'  \uD83D\uDCA1 '}
                {line}
              </Text>
            ))}
            {state.textLines.map((line, i) => (
              <Text key={`text-${i}`}>
                {'  '}
                {line}
              </Text>
            ))}
          </Box>
        </Box>
      )}

      {/* ---- Trace Session (tool call timeline) ---- */}
      {state.toolCalls.length > 0 && <TraceSession entries={state.toolCalls} />}

      {/* ---- Session summary ---- */}
      {state.summary && (
        <Box borderStyle="single" paddingLeft={1} paddingRight={1} marginTop={1}>
          <Box flexDirection="column">
            <Text bold color="green">
              Session Complete
            </Text>
            <Text>{state.summary}</Text>
          </Box>
        </Box>
      )}
    </Box>
  );
}
