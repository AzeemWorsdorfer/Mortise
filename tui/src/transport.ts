/**
 * transport.ts — Connect transport over Unix domain socket.
 *
 * Connects to the Mortise daemon via a Unix domain socket using
 * @connectrpc/connect-node. Opens a bidi stream to AgentService.Connect,
 * receives ServerEvent messages, and handles reconnection with
 * exponential backoff when the daemon disconnects.
 *
 * See: ticket 03 — TS TUI Scaffold
 */

import path from 'node:path';
import os from 'node:os';

import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-node';

import { AgentService } from './gen/mortise/v1/agent_connect.js';
import type { ServerEvent } from './gen/mortise/v1/agent_pb.js';

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export type ConnectionState =
  | { readonly kind: 'connected' }
  | { readonly kind: 'reconnecting'; readonly attempt: number; readonly nextInMs: number }
  | { readonly kind: 'daemon-not-running'; readonly socketPath: string };

export type EventCallback = (event: ServerEvent) => void;
export type StateCallback = (state: ConnectionState) => void;

export interface TransportHandle {
  /** Close the transport and stop reconnection attempts. */
  close(): void;
}

// ---------------------------------------------------------------------------
// Reconnection constants
// ---------------------------------------------------------------------------

/** Initial backoff delay in ms. Doubles each attempt. */
const INITIAL_BACKOFF_MS = 1000;

/** Maximum backoff delay in ms. */
const MAX_BACKOFF_MS = 30_000;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Resolves the daemon socket path. Uses MORTISE_SOCKET env var if set,
 * otherwise defaults to ~/.mortise/mortise.sock.
 */
export function resolveSocketPath(override?: string): string {
  if (override) return override;
  const env = process.env['MORTISE_SOCKET'];
  if (env) return env;
  return path.join(os.homedir(), '.mortise', 'mortise.sock');
}

/**
 * Returns the backoff delay for a given attempt number (1-based).
 */
function backoffMs(attempt: number): number {
  const delay = INITIAL_BACKOFF_MS * 2 ** (attempt - 1);
  return Math.min(delay, MAX_BACKOFF_MS);
}

/**
 * An empty async iterable used as the ClientCommand stream (we don't
 * send commands yet — ticket 16+).
 */
async function* emptyInput(): AsyncIterable<never> {
  // Never yields — the stream stays open for receiving only.
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

/**
 * Open a bidirectional Connect stream to the daemon at `socketPath`.
 * Events are delivered via `onEvent`; connection state changes via
 * `onStateChange`. The returned handle can be used to close the transport.
 *
 * The transport automatically reconnects with exponential backoff if the
 * daemon disconnects. If the daemon is not running initially, it shows
 * "daemon-not-running" but continues retrying — so starting the daemon
 * later restores the connection.
 */
export function connect(
  socketPath: string,
  onEvent: EventCallback,
  onStateChange: StateCallback,
): TransportHandle {
  let closed = false;

  const transport = createConnectTransport({
    httpVersion: '1.1',
    baseUrl: 'http://localhost',
    nodeOptions: { socketPath },
  });

  // ------------------------------------------------------------------
  // Inner: attempt a single connection + stream loop.
  // Returns: 'connected' if we got at least one event and then the
  // stream ended; 'unreachable' if the socket is missing or refused;
  // 'error' for transient failures.
  // ------------------------------------------------------------------
  type AttemptResult = 'connected' | 'unreachable' | 'error';

  async function attemptConnection(): Promise<AttemptResult> {
    if (closed) return 'error';

    try {
      const client = createClient(AgentService, transport);

      // connect() returns an AsyncIterable<ServerEvent>. We pass an
      // empty async iterable for the ClientCommand side since we
      // don't send commands yet.
      const events = client.connect(emptyInput());

      // Read the first event (should be SystemStatus from the daemon).
      const iterator = events[Symbol.asyncIterator]();
      const first = await iterator.next();

      if (closed) {
        // Break the iteration by returning (iterator.return is implicit).
        return 'error';
      }

      if (first.done || !first.value) {
        // Stream ended immediately — daemon disconnected.
        return 'error';
      }

      onStateChange({ kind: 'connected' });
      onEvent(first.value);

      // Continue reading remaining events.
      for await (const event of { [Symbol.asyncIterator]: () => iterator }) {
        if (closed) break;
        onEvent(event);
      }

      // Stream ended (daemon disconnected / stream closed).
      return 'connected';
    } catch (err: unknown) {
      if (closed) return 'error';

      const code = (err as NodeJS.ErrnoException).code;
      if (code === 'ENOENT' || code === 'ENOTFOUND' || code === 'ECONNREFUSED') {
        return 'unreachable';
      }
      return 'error';
    }
  }

  // ------------------------------------------------------------------
  // Reconnection loop. Runs as a background async IIFE.
  // ------------------------------------------------------------------
  (async function reconnectLoop() {
    let attempt = 0;

    while (!closed) {
      attempt++;

      const result = await attemptConnection();

      if (closed) return;

      if (result === 'connected') {
        // Connected and stream ended → reset counter, reconnect.
        attempt = 0;
      } else if (result === 'unreachable' && attempt === 1) {
        // First attempt failed because daemon isn't running.
        onStateChange({ kind: 'daemon-not-running', socketPath });
        // Fall through to retry — don't stop the loop.
      }

      // Compute backoff and wait before next attempt.
      const delay = backoffMs(attempt);
      onStateChange({ kind: 'reconnecting', attempt, nextInMs: delay });

      await sleep(delay);
    }
  })();

  return {
    close() {
      closed = true;
    },
  };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
