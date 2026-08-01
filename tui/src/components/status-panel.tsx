/**
 * status-panel.tsx — Minimal system status display.
 *
 * Renders a single Ink panel showing the daemon's SystemStatus fields:
 * session name, model/provider, connection state, uptime, token count,
 * and cost. This is the "hello world" of the TUI — proving the full
 * transport chain from daemon → Connect stream → rendered UI.
 *
 * See: ticket 03 — TS TUI Scaffold
 */

import React from 'react';
import { Box, Text } from 'ink';

import type { SystemStatus } from '../gen/mortise/v1/agent_pb.js';
import type { ConnectionState } from '../transport.js';

export interface StatusPanelProps {
  readonly status: SystemStatus | null;
  readonly connectionState: ConnectionState;
}

/**
 * Formats uptime seconds into a human-readable "Xm Xs" string.
 */
function formatUptime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s}s`;
}

/**
 * Formats a cost value in USD to "$X.XX".
 */
function formatCost(cost: number): string {
  return `$${cost.toFixed(2)}`;
}

/**
 * Returns a human-readable status label from the ConnectionState.
 */
function statusLabel(state: ConnectionState): string {
  switch (state.kind) {
    case 'connected':
      return 'Connected \u2713';
    case 'reconnecting':
      return `Reconnecting\u2026 (attempt ${state.attempt}, next in ${state.nextInMs}ms)`;
    case 'daemon-not-running':
      return `Daemon not running. Start with \`mortised\``;
  }
}

export function StatusPanel({ status, connectionState }: StatusPanelProps): React.ReactElement {
  const sessionName = status?.sessionName || 'untitled';
  const modelId = status?.modelId || '—';
  const providerId = status?.providerId || '—';
  const uptimeSec = status ? Number(status.uptimeSec) : 0;
  const tokens = status?.sessionTokenTotal ?? 0;
  const cost = status?.sessionCostTotal ?? 0;

  return (
    <Box flexDirection="column" borderStyle="single" paddingLeft={1} paddingRight={1}>
      <Box>
        <Text bold> Mortise </Text>
      </Box>
      <Box flexDirection="column" paddingTop={1} paddingBottom={1} paddingLeft={1} paddingRight={1}>
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
  );
}
