/**
 * main.tsx — Mortise TUI entry point.
 *
 * Parses CLI flags, connects to the daemon (or runs in --mock mode),
 * and renders the StatusPanel component. Incoming ServerEvents are
 * folded into AppState by the shared reducer in app-state.ts, so the
 * panel renders live phase transitions, thinking chunks, agent text,
 * and tool-call status with diff previews.
 *
 * CLI flags:
 *   --mock          Run with fake data (no daemon required)
 *   --socket PATH   Override default socket path
 *
 * See: ticket 03 — TS TUI Scaffold, ticket 06 — Agent Loop
 */

import React, { useEffect, useReducer, useRef } from 'react';
import { render } from 'ink';

import { connect, resolveSocketPath } from './transport.js';
import type { TransportHandle } from './transport.js';
import { createMockStatus, createMockPhaseEvents } from './mock.js';
import { StatusPanel } from './components/status-panel.js';
import { appReducer, dispatchServerEvent, INITIAL_STATE } from './app-state.js';

// ---------------------------------------------------------------------------
// CLI flag parsing
// ---------------------------------------------------------------------------

const ARGV = process.argv.slice(2);

const isMock = ARGV.includes('--mock');
const socketFlagIndex = ARGV.indexOf('--socket');
const socketOverride =
  socketFlagIndex >= 0 && socketFlagIndex + 1 < ARGV.length ? ARGV[socketFlagIndex + 1] : undefined;

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
