/**
 * mock.ts — Fake SystemStatus data for --mock mode.
 *
 * Provides sample data so the TUI can be developed and demoed without a
 * running daemon. The uptime counter ticks independently.
 *
 * See: ticket 03 — TS TUI Scaffold
 */

import { protoInt64 } from '@bufbuild/protobuf';
import { SystemStatus } from './gen/mortise/v1/agent_pb.js';

const MOCK_START_TIME = Date.now();

/**
 * Returns a fresh SystemStatus message populated with fake demo data.
 * The uptime is computed relative to `MOCK_START_TIME`, so it increases
 * on every call.
 */
export function createMockStatus(): SystemStatus {
  const uptimeSec = Math.floor((Date.now() - MOCK_START_TIME) / 1000);

  return new SystemStatus({
    modelId: 'claude-sonnet-4-20250514',
    providerId: 'anthropic',
    connectedClients: 1,
    sessionTokenTotal: 1247,
    sessionCostTotal: 0.03,
    sessionId: 'demo-session-uuid',
    sessionName: 'demo-session',
    workspacePath: '/home/dev/my-project',
    branch: 'main',
    uptimeSec: protoInt64.parse(uptimeSec),
  });
}
