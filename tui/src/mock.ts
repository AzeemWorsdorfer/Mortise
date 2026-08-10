/**
 * mock.ts — Fake data for --mock mode.
 *
 * Responsibilities:
 *   - Provides createMockStatus() for ticking SystemStatus demo data
 *   - Provides createMockPhaseEvents() for simulating a full agent
 *     loop phase cycle with thinking, text, tool-call, and summary events
 *
 * Is NOT responsible for:
 *   - Real daemon transport (see: transport.ts)
 *   - State management or event dispatch (see: main.tsx)
 *   - Rendering (see: components/status-panel.tsx)
 *
 * See: ticket 03 — TS TUI Scaffold, ticket 06 — Agent Loop
 */

import { protoInt64 } from '@bufbuild/protobuf';
import {
  SystemStatus,
  ServerEvent,
  PhaseTransitionEvent,
  ThinkingChunk,
  AgentText,
  ToolCallPending,
  ToolCallCompleted,
  SessionSummary,
  AgentPhase,
  RiskLevel,
} from './gen/mortise/v1/agent_pb.js';

type TimestampFn = () => ReturnType<typeof protoInt64.parse>;

const MOCK_START_TIME = Date.now();

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

/**
 * Creates a mock sequence of ServerEvent objects that simulate a
 * full agent loop cycle: IDLE → PLANNING → ACTING → OBSERVING →
 * DECIDING → COMPLETED, with thinking, text, and tool-call events
 * along the way.
 */
export function createMockPhaseEvents(): ServerEvent[] {
  let ts = protoInt64.parse(Date.now());
  const nextTs = () => {
    ts = protoInt64.parse(Number(ts) + 200);
    return ts;
  };

  const events: ServerEvent[] = [];

  addPhaseTransition(
    events,
    nextTs,
    AgentPhase.PLANNING,
    1,
    AgentPhase.IDLE,
    AgentPhase.PLANNING,
    'user prompt received',
  );
  addThinkingEvent(events, nextTs, AgentPhase.PLANNING, 1, 'Let me analyze the task...');
  addTextEvent(
    events,
    nextTs,
    AgentPhase.PLANNING,
    1,
    "I'll start by reading the project structure.",
  );
  addPhaseTransition(
    events,
    nextTs,
    AgentPhase.ACTING,
    1,
    AgentPhase.PLANNING,
    AgentPhase.ACTING,
    'tool call proposed',
  );
  addToolPending(
    events,
    nextTs,
    AgentPhase.ACTING,
    1,
    'call-1-0',
    'file_read',
    '{"path":"package.json"}',
  );
  addToolCompleted(
    events,
    nextTs,
    AgentPhase.ACTING,
    1,
    'call-1-0',
    'file_read',
    true,
    '[stub] tool "file_read" executed successfully',
    100,
  );
  addPhaseTransition(
    events,
    nextTs,
    AgentPhase.OBSERVING,
    1,
    AgentPhase.ACTING,
    AgentPhase.OBSERVING,
    'tool executed',
  );
  addPhaseTransition(
    events,
    nextTs,
    AgentPhase.DECIDING,
    1,
    AgentPhase.OBSERVING,
    AgentPhase.DECIDING,
    'evaluating results',
  );
  addPhaseTransition(
    events,
    nextTs,
    AgentPhase.COMPLETED,
    1,
    AgentPhase.DECIDING,
    AgentPhase.COMPLETED,
    'all steps complete',
  );
  addSessionSummary(events, nextTs, AgentPhase.COMPLETED, 1, 1, 1, 230, 0.001, 3, 0);

  return events;
}

// ---------------------------------------------------------------------------
// Phase event helpers
// ---------------------------------------------------------------------------

function addPhaseTransition(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  from: AgentPhase,
  to: AgentPhase,
  reason: string,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: {
        case: 'phaseChange',
        value: new PhaseTransitionEvent({ from, to, reason }),
      },
    }),
  );
}

function addThinkingEvent(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  text: string,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: { case: 'thinking', value: new ThinkingChunk({ text }) },
    }),
  );
}

function addTextEvent(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  text: string,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: { case: 'text', value: new AgentText({ text }) },
    }),
  );
}

function addToolPending(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  callId: string,
  toolName: string,
  parametersJson: string,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: {
        case: 'toolPending',
        value: new ToolCallPending({
          callId,
          toolName,
          parametersJson,
          riskLevel: RiskLevel.SAFE,
          requiresApproval: false,
        }),
      },
    }),
  );
}

function addToolCompleted(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  callId: string,
  toolName: string,
  success: boolean,
  resultSummary: string,
  durationMs: number,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: {
        case: 'toolCompleted',
        value: new ToolCallCompleted({
          callId,
          toolName,
          success,
          resultSummary,
          durationMs: protoInt64.parse(durationMs),
        }),
      },
    }),
  );
}

function addSessionSummary(
  events: ServerEvent[],
  nextTs: TimestampFn,
  phase: AgentPhase,
  turn: number,
  totalTurns: number,
  totalToolCalls: number,
  totalTokens: number,
  totalCost: number,
  durationSec: number,
  errorCount: number,
): void {
  events.push(
    new ServerEvent({
      timestampMs: nextTs(),
      phase,
      turnNumber: turn,
      payload: {
        case: 'summary',
        value: new SessionSummary({
          totalTurns,
          totalToolCalls,
          totalTokens,
          totalCost,
          durationSec: protoInt64.parse(durationSec),
          errorCount,
        }),
      },
    }),
  );
}
