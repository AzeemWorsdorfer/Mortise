package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ---------------------------------------------------------------------------
// AgentLoop
// ---------------------------------------------------------------------------

// AgentLoop is the phase-aware state machine that drives Mortise's
// core behavior: Idle → Planning → Acting → Observing → Deciding →
// (loop or complete). It owns the current phase, negotiates turns
// with a Provider, and publishes every phase transition and
// streaming event through the EventBus so connected TUI clients
// render live progress.
//
// The zero value is not usable; construct with NewAgentLoop.
type AgentLoop struct {
	phase       mortisev1.AgentPhase
	provider    Provider
	bus         EventPublisher
	logger      *slog.Logger
	turnNumber  int32
	totalTurns  int32
	toolCalls   int32
	totalTokens int32
	totalCost   float64
	mu          sync.Mutex
	cancel      context.CancelFunc
}

// Options configures a new AgentLoop. All fields are optional; a
// zero-value loop starts in IDLE with no provider or bus (useful
// only for testing the phase machine).
type Options struct {
	Provider Provider
	Bus      EventPublisher
	Logger   *slog.Logger
}

// NewAgentLoop constructs a fresh AgentLoop in the IDLE phase.
func NewAgentLoop(opts Options) *AgentLoop {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentLoop{
		phase:    mortisev1.AgentPhase_IDLE,
		provider: opts.Provider,
		bus:      opts.Bus,
		logger:   logger,
	}
}

// Phase returns the current agent phase. Safe to call from any
// goroutine.
func (l *AgentLoop) Phase() mortisev1.AgentPhase {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.phase
}

// ---------------------------------------------------------------------------
// Run — the main loop
// ---------------------------------------------------------------------------

// Run starts the agent loop with the given user prompt. It
// transitions through the full phase cycle, negotiating turns with
// the Provider until the model signals completion or the context is
// canceled.
//
// Run blocks until the loop completes or the context is canceled.
// It returns nil on successful completion, or an error if the
// context was canceled or the provider failed.
func (l *AgentLoop) Run(ctx context.Context, userPrompt string) error {
	if l.provider == nil {
		return errors.New("agent: Run called without a Provider")
	}
	if l.bus == nil {
		return errors.New("agent: Run called without an EventPublisher")
	}

	ctx, cancel := l.installCancel(ctx)
	defer cancel()

	// Transition IDLE → PLANNING.
	if err := l.transitionTo(mortisev1.AgentPhase_PLANNING, "user prompt received"); err != nil {
		return fmt.Errorf("agent: start: %w", err)
	}

	// Main turn loop.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		turn := l.nextTurn()
		eventCh, err := l.sendTurn(ctx, turn, userPrompt)
		if err != nil {
			return err
		}
		turnDone, err := l.processTurn(ctx, eventCh, turn)
		if err != nil {
			return err
		}
		if turnDone {
			break
		}
	}

	l.complete()
	return nil
}

// installCancel wraps ctx with a cancel function stored on the loop
// so external callers can interrupt a running loop.
func (l *AgentLoop) installCancel(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	l.cancel = cancel
	l.mu.Unlock()
	return ctx, cancel
}

// nextTurn increments and returns the loop's turn counter.
func (l *AgentLoop) nextTurn() int32 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.turnNumber++
	return l.turnNumber
}

// sendTurn sends the user prompt to the provider for one turn. On
// provider failure the loop transitions to ERRORED and the error is
// returned wrapped.
func (l *AgentLoop) sendTurn(ctx context.Context, turn int32, userPrompt string) (<-chan ProviderEvent, error) {
	req := &ProviderRequest{
		SystemPrompt: "You are a coding assistant. Plan, act, and observe.",
		UserMessage:  userPrompt,
	}
	eventCh, err := l.provider.SendPrompt(ctx, req)
	if err != nil {
		l.logger.Warn("provider send failed", "err", err, "turn", turn)
		_ = l.transitionTo(mortisev1.AgentPhase_ERRORED, "provider error")
		return nil, fmt.Errorf("agent: provider send: %w", err)
	}
	return eventCh, nil
}

// complete finalizes the loop: DECIDING → COMPLETED and a
// SessionSummary event. A failed transition is logged but the
// summary is still emitted.
func (l *AgentLoop) complete() {
	if err := l.transitionTo(mortisev1.AgentPhase_COMPLETED, "all steps complete"); err != nil {
		l.logger.Warn("transition to COMPLETED failed", "err", err, "phase", l.Phase())
	}
	l.emitSummary()
}

// emitSummary publishes the SessionSummary event with the loop's
// accumulated turn, tool, token, and cost totals.
func (l *AgentLoop) emitSummary() {
	l.mu.Lock()
	summary := &mortisev1.SessionSummary{
		TotalTurns:     l.totalTurns,
		TotalToolCalls: l.toolCalls,
		TotalTokens:    l.totalTokens,
		TotalCost:      l.totalCost,
	}
	l.mu.Unlock()

	l.publish(&mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_COMPLETED,
		TurnNumber: l.turnNumber,
		Payload: &mortisev1.ServerEvent_Summary{
			Summary: summary,
		},
	})
}

// ---------------------------------------------------------------------------
// Turn processing
// ---------------------------------------------------------------------------

// processTurn reads the provider's event channel for one turn,
// translating each ProviderEvent into ServerEvents and publishing
// them through the bus. It returns (done, error) — done is true
// when the model signals completion.
func (l *AgentLoop) processTurn(ctx context.Context, eventCh <-chan ProviderEvent, turn int32) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case ev, ok := <-eventCh:
			if !ok {
				// Channel closed without EventDone — the provider
				// is done but didn't signal explicitly. Treat as
				// completion.
				_ = l.transitionTo(mortisev1.AgentPhase_OBSERVING, "evaluating results")
				_ = l.transitionTo(mortisev1.AgentPhase_DECIDING, "evaluating results")
				return true, nil
			}
			done, err := l.handleProviderEvent(ctx, ev, turn)
			if err != nil || done {
				return done, err
			}
		}
	}
}

// handleProviderEvent routes one ProviderEvent to the publisher
// helper for its type. Returns done=true when the event ends the
// turn (EventDone), or an error when the turn must abort.
func (l *AgentLoop) handleProviderEvent(ctx context.Context, ev ProviderEvent, turn int32) (bool, error) {
	switch ev.Type {
	case EventThinking:
		l.publishThinking(turn, ev.Text)
	case EventText:
		l.publishText(turn, ev.Text)
	case EventToolCall:
		return l.handleToolCall(ctx, turn, ev)
	case EventDone:
		l.handleDone(turn, ev)
		return true, nil
	}
	return false, nil
}

// publishThinking translates an EventThinking chunk into a
// ServerEvent.thinking payload and publishes it.
func (l *AgentLoop) publishThinking(turn int32, text string) {
	l.publish(&mortisev1.ServerEvent{
		Phase:      l.Phase(),
		TurnNumber: turn,
		Payload: &mortisev1.ServerEvent_Thinking{
			Thinking: &mortisev1.ThinkingChunk{Text: text},
		},
	})
}

// publishText translates an EventText chunk into a
// ServerEvent.text payload and publishes it.
func (l *AgentLoop) publishText(turn int32, text string) {
	l.publish(&mortisev1.ServerEvent{
		Phase:      l.Phase(),
		TurnNumber: turn,
		Payload: &mortisev1.ServerEvent_Text{
			Text: &mortisev1.AgentText{Text: text},
		},
	})
}

// handleToolCall drives one proposed tool invocation: transition to
// ACTING, emit ToolCallPending, run the stubbed execution, emit
// ToolCallCompleted, and move to OBSERVING.
func (l *AgentLoop) handleToolCall(ctx context.Context, turn int32, ev ProviderEvent) (bool, error) {
	callID := l.emitToolPending(turn, ev)
	if err := l.stubExecuteTool(ctx); err != nil {
		return false, err
	}
	l.emitToolCompleted(callID, ev.ToolName)
	return false, nil
}

// emitToolPending transitions PLANNING → ACTING and publishes the
// ToolCallPending event. Returns the generated call ID used to pair
// the pending and completed events.
func (l *AgentLoop) emitToolPending(turn int32, ev ProviderEvent) string {
	// Transition PLANNING → ACTING.
	_ = l.transitionTo(mortisev1.AgentPhase_ACTING, "tool call proposed")

	callID := fmt.Sprintf("call-%d-%d", turn, l.toolCalls)
	l.mu.Lock()
	l.toolCalls++
	l.mu.Unlock()

	l.publish(&mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_ACTING,
		TurnNumber: turn,
		Payload: &mortisev1.ServerEvent_ToolPending{
			ToolPending: &mortisev1.ToolCallPending{
				CallId:           callID,
				ToolName:         ev.ToolName,
				ParametersJson:   ev.ParametersJSON,
				RiskLevel:        mortisev1.RiskLevel_SAFE,
				RequiresApproval: false,
			},
		},
	})
	return callID
}

// stubExecuteTool simulates tool execution by sleeping 100ms. Real
// tool execution arrives in ticket 07+.
func (l *AgentLoop) stubExecuteTool(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}

// emitToolCompleted publishes the ToolCallCompleted event for a
// successfully stubbed execution and transitions ACTING → OBSERVING.
func (l *AgentLoop) emitToolCompleted(callID, toolName string) {
	l.publish(&mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_ACTING,
		TurnNumber: l.turnNumber,
		Payload: &mortisev1.ServerEvent_ToolCompleted{
			ToolCompleted: &mortisev1.ToolCallCompleted{
				CallId:        callID,
				ToolName:      toolName,
				Success:       true,
				ResultSummary: fmt.Sprintf("[stub] tool %q executed successfully", toolName),
				DurationMs:    100,
			},
		},
	})

	// Transition ACTING → OBSERVING.
	_ = l.transitionTo(mortisev1.AgentPhase_OBSERVING, "tool executed")
}

// handleDone accumulates turn usage and advances the phase machine
// from wherever the turn ended to DECIDING.
func (l *AgentLoop) handleDone(turn int32, ev ProviderEvent) {
	l.accumulateUsage(turn, ev.Usage)
	l.advanceAfterDone()
}

// accumulateUsage folds the turn's token/cost accounting into the
// loop totals.
func (l *AgentLoop) accumulateUsage(turn int32, usage *UsageInfo) {
	if usage == nil {
		return
	}
	l.mu.Lock()
	l.totalTokens += usage.InputTokens + usage.OutputTokens
	l.totalCost += usage.CostUSD
	l.totalTurns = turn
	l.mu.Unlock()
}

// advanceAfterDone moves the loop from the phase the turn ended in
// to DECIDING. The phase when EventDone arrives depends on whether
// a tool call was executed this turn:
//   - PLANNING → DECIDING (no tool call, straight to decision)
//   - ACTING   → OBSERVING → DECIDING (tool call executed)
//   - OBSERVING → DECIDING (already observing)
func (l *AgentLoop) advanceAfterDone() {
	l.mu.Lock()
	currentPhase := l.phase
	l.mu.Unlock()

	switch currentPhase {
	case mortisev1.AgentPhase_PLANNING:
		_ = l.transitionTo(mortisev1.AgentPhase_DECIDING, "evaluating results")
	case mortisev1.AgentPhase_ACTING:
		_ = l.transitionTo(mortisev1.AgentPhase_OBSERVING, "tool executed")
		_ = l.transitionTo(mortisev1.AgentPhase_DECIDING, "evaluating results")
	case mortisev1.AgentPhase_OBSERVING:
		_ = l.transitionTo(mortisev1.AgentPhase_DECIDING, "evaluating results")
	default:
		_ = l.transitionTo(mortisev1.AgentPhase_DECIDING, "evaluating results")
	}
}

// ---------------------------------------------------------------------------
// Phase transitions
// ---------------------------------------------------------------------------

// ErrInvalidPhaseTransition is returned by transitionTo when the
// requested phase change is not allowed by the state machine. Callers
// compare with errors.Is.
var ErrInvalidPhaseTransition = errors.New("agent: invalid phase transition")

// transitionTo changes the loop's phase to newPhase if the move is
// permitted by the state machine. On a valid transition, a
// PhaseTransitionEvent is published through the bus. On an invalid
// transition, the phase is left unchanged and
// ErrInvalidPhaseTransition is returned (wrapped with the
// from/to/reason for diagnostic logging).
func (l *AgentLoop) transitionTo(newPhase mortisev1.AgentPhase, reason string) error {
	l.mu.Lock()
	from := l.phase
	if !validPhaseTransition(from, newPhase) {
		l.mu.Unlock()
		err := fmt.Errorf("%w: %s → %s (reason: %s)",
			ErrInvalidPhaseTransition, from, newPhase, reason)
		l.logger.Warn("blocked phase transition", "err", err)
		return err
	}
	l.phase = newPhase
	l.mu.Unlock()

	// Publish the transition event.
	l.publish(&mortisev1.ServerEvent{
		Phase:      newPhase,
		TurnNumber: l.turnNumber,
		Payload: &mortisev1.ServerEvent_PhaseChange{
			PhaseChange: &mortisev1.PhaseTransitionEvent{
				From:   from,
				To:     newPhase,
				Reason: reason,
			},
		},
	})
	return nil
}

// validPhaseTransition reports whether moving from → to is allowed.
//
// State machine diagram:
//
//	IDLE → PLANNING → ACTING → OBSERVING → DECIDING → (PLANNING or COMPLETED)
//	  ↑        ↑         ↑          ↑           ↑
//	  └────────┴─────────┴──────────┴───────────┘  (PAUSED interrupt from any phase)
//	Any phase → ERRORED (on provider failure)
func validPhaseTransition(from, to mortisev1.AgentPhase) bool {
	if from == to {
		return false
	}
	switch from {
	case mortisev1.AgentPhase_IDLE:
		return to == mortisev1.AgentPhase_PLANNING || to == mortisev1.AgentPhase_PAUSED || to == mortisev1.AgentPhase_ERRORED
	case mortisev1.AgentPhase_PLANNING:
		return to == mortisev1.AgentPhase_ACTING || to == mortisev1.AgentPhase_DECIDING || to == mortisev1.AgentPhase_PAUSED || to == mortisev1.AgentPhase_ERRORED
	case mortisev1.AgentPhase_ACTING:
		return to == mortisev1.AgentPhase_OBSERVING || to == mortisev1.AgentPhase_PAUSED || to == mortisev1.AgentPhase_ERRORED
	case mortisev1.AgentPhase_OBSERVING:
		return to == mortisev1.AgentPhase_DECIDING || to == mortisev1.AgentPhase_PAUSED || to == mortisev1.AgentPhase_ERRORED
	case mortisev1.AgentPhase_DECIDING:
		return to == mortisev1.AgentPhase_PLANNING || to == mortisev1.AgentPhase_COMPLETED || to == mortisev1.AgentPhase_PAUSED || to == mortisev1.AgentPhase_ERRORED
	case mortisev1.AgentPhase_PAUSED:
		return to == mortisev1.AgentPhase_IDLE || to == mortisev1.AgentPhase_PLANNING || to == mortisev1.AgentPhase_ACTING || to == mortisev1.AgentPhase_OBSERVING || to == mortisev1.AgentPhase_DECIDING
	case mortisev1.AgentPhase_ERRORED:
		return to == mortisev1.AgentPhase_IDLE
	case mortisev1.AgentPhase_COMPLETED:
		return false // terminal
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// publish sends an event through the bus. The event's timestamp is
// set to the current time. If the bus is nil (e.g., in a test that
// doesn't care about events), publish is a no-op.
func (l *AgentLoop) publish(ev *mortisev1.ServerEvent) {
	if l.bus == nil {
		return
	}
	ev.TimestampMs = uint64(time.Now().UnixMilli())
	l.bus.Publish(ev)
}
