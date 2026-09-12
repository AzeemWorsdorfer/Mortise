/**
 * app-state.ts — Mortise TUI app state domain.
 *
 * Owns the AppAction union, the appReducer that folds ServerEvents
 * into AppState, and the dispatch helpers that convert one incoming
 * ServerEvent into reducer actions. Shared by the real transport app
 * and the mock app so both render through the same state machine.
 *
 * Is NOT responsible for transport (transport.ts), presentation
 * (components/), or process entry (main.tsx).
 *
 * See: ticket 03 — TS TUI Scaffold, ticket 06 — Agent Loop,
 * ticket 08 — File Write & File Diff.
 */

import type {
  SystemStatus,
  PhaseTransitionEvent,
  AgentPhase,
  ServerEvent,
  SessionSummary,
  ToolCallCompleted,
  ToolCallPending,
} from './gen/mortise/v1/agent_pb.js';

import type { ConnectionState } from './transport.js';
import type { AppState } from './components/status-panel.js';

export type AppAction =
  | { type: 'status'; status: SystemStatus; phase?: AgentPhase }
  | { type: 'phase_change'; event: PhaseTransitionEvent; phase: AgentPhase }
  | { type: 'thinking'; text: string }
  | { type: 'text'; text: string }
  | {
      type: 'tool_pending';
      callId: string;
      toolName: string;
      parametersJson: string;
      diffPreview?: string;
      additions?: number;
      deletions?: number;
      turnNumber?: number;
    }
  | {
      type: 'tool_completed';
      callId: string;
      success: boolean;
      resultSummary: string;
      filesChanged: string[];
      additions: number;
      deletions: number;
      durationMs?: number;
    }
  | { type: 'summary'; text: string }
  | { type: 'connection'; state: ConnectionState };

export const INITIAL_STATE: AppState = {
  status: null,
  phase: 0, // PHASE_UNKNOWN
  phaseHistory: [],
  thinkingLines: [],
  textLines: [],
  toolCalls: [],
  filesChanged: [],
  summary: null,
  connectionState: { kind: 'reconnecting', attempt: 0, nextInMs: 0 },
};

export function appReducer(state: AppState, action: AppAction): AppState {
  switch (action.type) {
    case 'status':
      return {
        ...state,
        status: action.status,
        phase: action.phase ?? state.phase,
      };
    case 'phase_change':
      return {
        ...state,
        phase: action.phase,
        phaseHistory: [...state.phaseHistory, action.event].slice(-20),
      };
    case 'thinking':
      return {
        ...state,
        thinkingLines: [...state.thinkingLines, action.text].slice(-50),
      };
    case 'text':
      return {
        ...state,
        textLines: [...state.textLines, action.text].slice(-50),
      };
    case 'tool_pending':
      return recordToolPending(state, action);
    case 'tool_completed':
      return updateToolCompleted(state, action);
    case 'summary':
      return { ...state, summary: action.text };
    case 'connection':
      return { ...state, connectionState: action.state };
    default:
      return state;
  }
}

// recordToolPending appends one pending tool call, keeping the trace
// bounded to the latest 20 entries.
function recordToolPending(
  state: AppState,
  action: Extract<AppAction, { type: 'tool_pending' }>,
): AppState {
  return {
    ...state,
    toolCalls: [
      ...state.toolCalls,
      {
        callId: action.callId,
        turnNumber: action.turnNumber,
        toolName: action.toolName,
        parametersJson: action.parametersJson,
        diffPreview: action.diffPreview,
        additions: action.additions,
        deletions: action.deletions,
        status: 'pending' as const,
      },
    ].slice(-20),
  };
}

function updateToolCompleted(
  state: AppState,
  action: Extract<AppAction, { type: 'tool_completed' }>,
): AppState {
  return {
    ...state,
    filesChanged: [
      ...state.filesChanged.filter((change) => !action.filesChanged.includes(change.path)),
      ...action.filesChanged.map((path) => ({
        path,
        diff: action.resultSummary,
        additions: action.additions,
        deletions: action.deletions,
      })),
    ],
    toolCalls: state.toolCalls.map((tc) =>
      tc.callId === action.callId
        ? {
            ...tc,
            status: action.success ? ('completed' as const) : ('failed' as const),
            resultSummary: action.resultSummary,
            durationMs: action.durationMs,
            filesChanged: action.filesChanged,
            additions: action.additions,
            deletions: action.deletions,
          }
        : tc,
    ),
  };
}

function phaseFromEvent(ev: ServerEvent): AgentPhase {
  return ev.phase ?? 0;
}

// dispatchServerEvent converts a ServerEvent into one or more AppAction
// dispatches. Used by both RealApp (live transport) and MockApp (simulated).
export function dispatchServerEvent(ev: ServerEvent, dispatch: (action: AppAction) => void): void {
  switch (ev.payload.case) {
    case 'status': {
      dispatch({
        type: 'status',
        status: ev.payload.value as SystemStatus,
        phase: phaseFromEvent(ev),
      });
      break;
    }
    case 'phaseChange': {
      dispatch({ type: 'phase_change', event: ev.payload.value, phase: phaseFromEvent(ev) });
      break;
    }
    case 'thinking': {
      dispatch({ type: 'thinking', text: ev.payload.value.text });
      break;
    }
    case 'text': {
      dispatch({ type: 'text', text: ev.payload.value.text });
      break;
    }
    case 'toolPending': {
      dispatchToolPending(ev.payload.value, ev.turnNumber, dispatch);
      break;
    }
    case 'toolCompleted': {
      dispatchToolCompleted(ev.payload.value, dispatch);
      break;
    }
    case 'summary': {
      dispatch({ type: 'summary', text: formatSummary(ev.payload.value) });
      break;
    }
  }
}

// dispatchToolPending forwards a toolPending payload to the reducer.
function dispatchToolPending(
  tp: ToolCallPending,
  turnNumber: number,
  dispatch: (action: AppAction) => void,
): void {
  dispatch({
    type: 'tool_pending',
    callId: tp.callId,
    toolName: tp.toolName,
    parametersJson: tp.parametersJson,
    diffPreview: tp.diffPreview,
    additions: tp.additions,
    deletions: tp.deletions,
    turnNumber,
  });
}

// dispatchToolCompleted forwards a toolCompleted payload to the reducer.
// protobuf-es v1 surfaces int64 fields as bigint on Node.
function dispatchToolCompleted(tc: ToolCallCompleted, dispatch: (action: AppAction) => void): void {
  dispatch({
    type: 'tool_completed',
    callId: tc.callId,
    success: tc.success,
    resultSummary: tc.success ? tc.resultSummary : tc.errorMessage,
    filesChanged: tc.filesChanged,
    additions: tc.additions,
    deletions: tc.deletions,
    durationMs: Number(tc.durationMs),
  });
}

// formatSummary renders the SessionSummary payload as a single text block.
function formatSummary(sm: SessionSummary): string {
  return `Turns: ${sm.totalTurns}  Tools: ${sm.totalToolCalls}  Tokens: ${sm.totalTokens}  Cost: $${sm.totalCost.toFixed(4)}`;
}
