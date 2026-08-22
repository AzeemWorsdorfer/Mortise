/**
 * trace-session.tsx — Renders the Trace Session panel: a timeline
 * of tool calls with name, parsed input, output snippet, duration,
 * and success/failure status.
 *
 * Responsibilities:
 *   - Rendering ToolCallEntry timeline items
 *   - Parsing tool parameters into a readable Input line
 *
 * Is NOT responsible for:
 *   - State management or event dispatch (see: main.tsx)
 *   - Transport (see: transport.ts)
 *
 * See: ticket 07 — Tool Interface, Registry & File Read.
 */

import React from 'react';
import { Box, Text } from 'ink';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ToolCallEntry {
  callId: string;
  turnNumber?: number;
  toolName: string;
  parametersJson: string;
  status: 'pending' | 'completed' | 'failed';
  resultSummary?: string;
  durationMs?: number;
}

export interface TraceSessionProps {
  entries: ToolCallEntry[];
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseToolInput extracts a readable primary input (e.g. "path")
// from the raw JSON parameters. Falls back to the raw string when
// parsing fails.
function parseToolInput(parametersJson: string): string {
  try {
    const params: unknown = JSON.parse(parametersJson);
    if (
      params !== null &&
      typeof params === 'object' &&
      typeof (params as Record<string, unknown>).path === 'string'
    ) {
      return (params as Record<string, unknown>).path as string;
    }
  } catch {
    // fall through to raw display
  }
  return parametersJson;
}

function formatDuration(ms: number | undefined): string {
  if (ms === undefined) {
    return '';
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(1)}s`;
  }
  return `${ms}ms`;
}

const STATUS_ICON = {
  pending: '\u23F3',
  completed: '\u2705',
  failed: '\u274C',
} as const;

const STATUS_COLOR = {
  pending: 'yellow',
  completed: 'green',
  failed: 'red',
} as const;

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

// TraceSession renders one bordered timeline entry per tool call,
// oldest first, matching the ticket's trace format.
export function TraceSession({ entries }: TraceSessionProps): React.ReactElement {
  return (
    <Box borderStyle="single" paddingLeft={1} paddingRight={1} marginTop={1}>
      <Box flexDirection="column">
        <Text bold>TRACE SESSION</Text>
        <Text dimColor>{'\u2500'.repeat(16)}</Text>
        {entries.map((tc) => (
          <Box key={tc.callId} flexDirection="column" marginTop={1}>
            <Text>
              {' '}
              [Turn {tc.turnNumber ?? '?'}]{' '}
              <Text bold color={STATUS_COLOR[tc.status]}>
                {tc.toolName}
              </Text>
            </Text>
            <Text>
              {'  Input:  '}
              <Text>{parseToolInput(tc.parametersJson)}</Text>
            </Text>
            {tc.resultSummary && <Text dimColor>{'  Result: ' + tc.resultSummary}</Text>}
            <Text>
              {'  Duration: '}
              {formatDuration(tc.durationMs) || '...'}{' '}
              <Text color={STATUS_COLOR[tc.status]}>{STATUS_ICON[tc.status]}</Text>
            </Text>
          </Box>
        ))}
      </Box>
    </Box>
  );
}
