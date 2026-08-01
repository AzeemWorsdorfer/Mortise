/**
 * main.tsx — Mortise TUI entry point.
 *
 * Parses CLI flags, connects to the daemon (or runs in --mock mode),
 * and renders the StatusPanel component. This is the "hello world" of
 * the TUI — proving the full transport chain works end-to-end.
 *
 * CLI flags:
 *   --mock          Run with fake data (no daemon required)
 *   --socket PATH   Override default socket path
 *
 * See: ticket 03 — TS TUI Scaffold
 */

import React, { useEffect, useState, useRef } from 'react';
import { render } from 'ink';

import type { SystemStatus } from './gen/mortise/v1/agent_pb.js';

import type { ConnectionState, TransportHandle } from './transport.js';
import { connect, resolveSocketPath } from './transport.js';
import { createMockStatus } from './mock.js';
import { StatusPanel } from './components/status-panel.js';

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
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>({
    kind: 'reconnecting',
    attempt: 0,
    nextInMs: 0,
  });
  const handleRef = useRef<TransportHandle | null>(null);

  useEffect(() => {
    const socketPath = resolveSocketPath(socketOverride);

    const handle = connect(
      socketPath,
      (event) => {
        // Only care about SystemStatus events for now.
        if (event.payload.case === 'status') {
          setStatus(event.payload.value);
        }
      },
      (state) => {
        setConnectionState(state);
      },
    );

    handleRef.current = handle;

    return () => {
      handle.close();
    };
  }, []);

  return <StatusPanel status={status} connectionState={connectionState} />;
}

function MockApp(): React.ReactElement {
  const [status, setStatus] = useState<SystemStatus>(createMockStatus);

  useEffect(() => {
    const interval = setInterval(() => {
      setStatus(createMockStatus());
    }, 1000);

    return () => clearInterval(interval);
  }, []);

  return <StatusPanel status={status} connectionState={{ kind: 'connected' }} />;
}

// ---------------------------------------------------------------------------
// Render
// ---------------------------------------------------------------------------

render(isMock ? <MockApp /> : <RealApp />);
