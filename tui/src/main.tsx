// Mortise TUI client — scaffold.
//
// As of ticket 01 (project foundation), this is a minimal React/Ink entry point
// that imports the generated protobuf stubs to verify the proto build pipeline.
// The actual TUI panels (trace session, system, chat, files, context) are
// implemented across tickets 03 and 16.

import React from 'react';
import { Text, render } from 'ink';

import { AgentPhase, RiskLevel } from './gen/mortise/v1/agent_pb.js';

export function App(): React.ReactElement {
  return (
    <Text>
      Mortise TUI — scaffold (ticket 01). Agent phase:{' '}
      {AgentPhase[AgentPhase.PHASE_UNKNOWN] ?? 'UNKNOWN'}, Risk:{' '}
      {RiskLevel[RiskLevel.RISK_UNKNOWN] ?? 'UNKNOWN'}.
    </Text>
  );
}

render(<App />);
