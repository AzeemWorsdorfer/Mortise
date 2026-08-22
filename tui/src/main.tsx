/**
 * main.tsx — Mortise TUI entry point.
 *
 * Parses CLI flags, connects to the daemon (or runs in --mock mode),
 * and renders the StatusPanel component. In ticket 06, the app handles
 * all ServerEvent payload types (phaseChange, thinking, toolPending,
 * toolCompleted, text, status, summary) and accumulates them in state
 * so the panel renders live phase transitions, thinking chunks, agent
 * text, and tool-call status.
 *
 * CLI flags:
 *   --mock          Run with fake data (no daemon required)
 *   --socket PATH   Override default socket path
 *
 * See: ticket 03 — TS TUI Scaffold, ticket 06 — Agent Loop
 */

import React, { useEffect, useReducer, useRef } from 'react';
import { render } from 'ink';

import type {
  SystemStatus,
  PhaseTransitionEvent,
  AgentPhase,
  ServerEvent,
  SessionSummary,
  ToolCallCompleted,
  ToolCallPending,
} from './gen/mortise/v1/agent_pb.js';

import type { ConnectionState, TransportHandle } from './transport.js';
import { connect, resolveSocketPath } from './transport.js';
import { createMockStatus, createMockPhaseEvents } from './mock.js';
import { StatusPanel, type AppState } from './components/status-panel.js';

// ---------------------------------------------------------------------------
// CLI flag parsing
// ---------------------------------------------------------------------------

const ARGV = process.argv.slice(2);

const isMock = ARGV.includes('--mock');
const socketFlagIndex = ARGV.indexOf('--socket');
const socketOverride =
  socketFlagIndex >= 0 && socketFlagIndex + 1 < ARGV.length ? ARGV[socketFlagIndex + 1] : undefined;

// ---------------------------------------------------------------------------
// App state
// ---------------------------------------------------------------------------

type AppAction =
  | { type: 'status'; status: SystemStatus }
  | { type: 'phase_change'; event: PhaseTransitionEvent; phase: AgentPhase }
  | { type: 'thinking'; text: string }
  | { type: 'text'; text: string }
  | {
      type: 'tool_pending';
      callId: string;
      toolName: string;
      parametersJson: string;
      turnNumber?: number;
    }
  | {
      type: 'tool_completed';
      callId: string;
      success: boolean;
      resultSummary: string;
      durationMs?: number;
    }
  | { type: 'summary'; text: string }
  | { type: 'connection'; state: ConnectionState };

function appReducer(state: AppState, action: AppAction): AppState {
  switch (action.type) {
    case 'status':
      return { ...state, status: action.status };
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
      return {
        ...state,
        toolCalls: [
          ...state.toolCalls,
          {
            callId: action.callId,
            turnNumber: action.turnNumber,
            toolName: action.toolName,
            parametersJson: action.parametersJson,
            status: 'pending' as const,
          },
        ].slice(-20),
      };
    case 'tool_completed':
      return {
        ...state,
        toolCalls: state.toolCalls.map((tc) =>
          tc.callId === action.callId
            ? {
                ...tc,
                status: action.success ? ('completed' as const) : ('failed' as const),
                resultSummary: action.resultSummary,
                durationMs: action.durationMs,
              }
            : tc,
        ),
      };
    case 'summary':
      return { ...state, summary: action.text };
    case 'connection':
      return { ...state, connectionState: action.state };
    default:
      return state;
  }
}

const INITIAL_STATE: AppState = {
  status: null,
  phase: 0, // PHASE_UNKNOWN
  phaseHistory: [],
  thinkingLines: [],
  textLines: [],
  toolCalls: [],
  summary: null,
  connectionState: { kind: 'reconnecting', attempt: 0, nextInMs: 0 },
};

function phaseFromEvent(ev: ServerEvent): AgentPhase {
  return ev.phase ?? 0;
}

// dispatchServerEvent converts a ServerEvent into one or more AppAction
// dispatches. Used by both RealApp (live transport) and MockApp (simulated).
function dispatchServerEvent(ev: ServerEvent, dispatch: (action: AppAction) => void): void {
  switch (ev.payload.case) {
    case 'status': {
      dispatch({ type: 'status', status: ev.payload.value as SystemStatus });
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
    resultSummary: tc.resultSummary,
    durationMs: Number(tc.durationMs),
  });
}

// formatSummary renders the SessionSummary payload as a single text block.
function formatSummary(sm: SessionSummary): string {
  return `Turns: ${sm.totalTurns}  Tools: ${sm.totalToolCalls}  Tokens: ${sm.totalTokens}  Cost: $${sm.totalCost.toFixed(4)}`;
}

// ---------------------------------------------------------------------------
// App components
// ---------------------------------------------------------------------------

function RealApp(): React.ReactElement {
  const [state, dispatch] = useReducer(appReducer, INITIAL_STATE);
  const handleRef = useRef<TransportHandle | null>(null);

  useEffect(() => {
    const socketPath = resolveSocketPath(socketOverride);

    const handle = connect(
      socketPath,
      (event) => dispatchServerEvent(event, dispatch),
      (connState) => {
        dispatch({ type: 'connection', state: connState });
      },
    );

    handleRef.current = handle;

    return () => {
      handle.close();
    };
  }, []);

  return <StatusPanel state={state} />;
}

function MockApp(): React.ReactElement {
  const [state, dispatch] = useReducer(appReducer, {
    ...INITIAL_STATE,
    connectionState: { kind: 'connected' },
    status: createMockStatus(),
  });

  useEffect(() => {
    const interval = setInterval(() => {
      dispatch({ type: 'status', status: createMockStatus() });
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  // Simulate phase transitions in mock mode.
  useEffect(() => {
    let idx = 0;
    const mockEvents = createMockPhaseEvents();
    const interval = setInterval(() => {
      if (idx >= mockEvents.length) {
        clearInterval(interval);
        return;
      }
      dispatchServerEvent(mockEvents[idx]!, dispatch);
      idx++;
    }, 500);
    return () => clearInterval(interval);
  }, []);

  return <StatusPanel state={state} />;
}

// ---------------------------------------------------------------------------
// Render
// ---------------------------------------------------------------------------

render(isMock ? <MockApp /> : <RealApp />);
