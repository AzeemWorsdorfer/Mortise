package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ---------------------------------------------------------------------------
// Provider interface
// ---------------------------------------------------------------------------

// stubProvider is a minimal Provider that closes its event channel
// immediately. Used in tests that only need the loop to start and stop.
type stubProvider struct {
	events []ProviderEvent
}

func (s *stubProvider) SendPrompt(ctx context.Context, req *ProviderRequest) (<-chan ProviderEvent, error) {
	ch := make(chan ProviderEvent, len(s.events))
	for _, e := range s.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (s *stubProvider) ProviderID() string { return "stub" }

// ---------------------------------------------------------------------------
// Event bus spy
// ---------------------------------------------------------------------------

// eventSpy is a minimal EventPublisher for tests. It records every
// published ServerEvent in order so tests can assert phase transitions
// and event payloads.
type eventSpy struct {
	mu     sync.Mutex
	events []*mortisev1.ServerEvent
}

func (s *eventSpy) Publish(ev *mortisev1.ServerEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *eventSpy) Events() []*mortisev1.ServerEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*mortisev1.ServerEvent, len(s.events))
	copy(out, s.events)
	return out
}

// phaseTransitions filters the spy's events to only PhaseTransitionEvent payloads.
func (s *eventSpy) phaseTransitions() []*mortisev1.PhaseTransitionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*mortisev1.PhaseTransitionEvent
	for _, ev := range s.events {
		if pc := ev.GetPhaseChange(); pc != nil {
			out = append(out, pc)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Phase transition tests
// ---------------------------------------------------------------------------

func TestAgentLoop_StartsIdle(t *testing.T) {
	t.Parallel()

	loop := NewAgentLoop(Options{
		Provider: &stubProvider{},
	})
	if loop.Phase() != mortisev1.AgentPhase_IDLE {
		t.Fatalf("new AgentLoop: want IDLE, got %v", loop.Phase())
	}
}

func TestAgentLoop_Run_TransitionsThroughPhases(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventThinking, Text: "Let me analyze the task..."},
			{Type: EventText, Text: "I'll start by reading the project structure."},
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"package.json"}`},
			{Type: EventDone, Usage: &UsageInfo{InputTokens: 10, OutputTokens: 5, CostUSD: 0.001}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "read the project structure")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	transitions := spy.phaseTransitions()

	// Expected: IDLE→PLANNING, PLANNING→ACTING, ACTING→OBSERVING,
	// OBSERVING→DECIDING, DECIDING→COMPLETED
	if len(transitions) < 5 {
		t.Fatalf("expected at least 5 phase transitions, got %d", len(transitions))
	}

	// IDLE → PLANNING
	assertTransition(t, transitions[0], mortisev1.AgentPhase_IDLE, mortisev1.AgentPhase_PLANNING, "user prompt received")

	// PLANNING → ACTING
	assertTransition(t, transitions[1], mortisev1.AgentPhase_PLANNING, mortisev1.AgentPhase_ACTING, "tool call proposed")

	// ACTING → OBSERVING
	assertTransition(t, transitions[2], mortisev1.AgentPhase_ACTING, mortisev1.AgentPhase_OBSERVING, "tool executed")

	// OBSERVING → DECIDING
	assertTransition(t, transitions[3], mortisev1.AgentPhase_OBSERVING, mortisev1.AgentPhase_DECIDING, "evaluating results")

	// DECIDING → COMPLETED
	assertTransition(t, transitions[4], mortisev1.AgentPhase_DECIDING, mortisev1.AgentPhase_COMPLETED, "all steps complete")

	if loop.Phase() != mortisev1.AgentPhase_COMPLETED {
		t.Errorf("final phase: want COMPLETED, got %v", loop.Phase())
	}
}

func TestAgentLoop_InvalidTransition_Blocked(t *testing.T) {
	t.Parallel()

	loop := NewAgentLoop(Options{})

	// IDLE → ACTING without PLANNING should be blocked
	err := loop.transitionTo(mortisev1.AgentPhase_ACTING, "skip planning")
	if err == nil {
		t.Fatal("expected error for IDLE → ACTING transition")
	}
	if loop.Phase() != mortisev1.AgentPhase_IDLE {
		t.Errorf("phase should remain IDLE after invalid transition, got %v", loop.Phase())
	}
}

func TestAgentLoop_InvalidTransition_LogsWarning(t *testing.T) {
	t.Parallel()

	loop := NewAgentLoop(Options{})
	err := loop.transitionTo(mortisev1.AgentPhase_COMPLETED, "skip all phases")
	if err == nil {
		t.Fatal("expected error for IDLE → COMPLETED transition")
	}
}

func TestAgentLoop_MultiTurn_CompletesAfterFinalTurn(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventThinking, Text: "Turn 1 thinking..."},
			{Type: EventText, Text: "Turn 1 text."},
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"go.mod"}`},
			{Type: EventDone, Usage: &UsageInfo{InputTokens: 5, OutputTokens: 3}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "check go.mod")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	transitions := spy.phaseTransitions()
	if len(transitions) < 5 {
		t.Fatalf("expected at least 5 phase transitions, got %d", len(transitions))
	}

	// Last transition should be DECIDING → COMPLETED
	last := transitions[len(transitions)-1]
	assertTransition(t, last, mortisev1.AgentPhase_DECIDING, mortisev1.AgentPhase_COMPLETED, "all steps complete")
}

func TestAgentLoop_EmitsThinkingAndTextEvents(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventThinking, Text: "Analyzing..."},
			{Type: EventText, Text: "Here is what I found."},
			{Type: EventDone, Usage: &UsageInfo{}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "analyze")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := spy.Events()
	var thinkingCount, textCount int
	for _, ev := range events {
		if ev.GetThinking() != nil {
			thinkingCount++
		}
		if ev.GetText() != nil {
			textCount++
		}
	}
	if thinkingCount < 1 {
		t.Error("expected at least 1 thinking event")
	}
	if textCount < 1 {
		t.Error("expected at least 1 text event")
	}
}

func TestAgentLoop_EmitsToolCallEvents(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "file_write", ParametersJSON: `{"path":"out.txt","content":"hello"}`},
			{Type: EventDone, Usage: &UsageInfo{}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "write file")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := spy.Events()
	var pendingCount, completedCount int
	for _, ev := range events {
		if ev.GetToolPending() != nil {
			pendingCount++
		}
		if ev.GetToolCompleted() != nil {
			completedCount++
		}
	}
	if pendingCount < 1 {
		t.Error("expected at least 1 ToolCallPending event")
	}
	if completedCount < 1 {
		t.Error("expected at least 1 ToolCallCompleted event")
	}
}

func TestAgentLoop_ContextCancel_ReturnsError(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	// blockingProvider: SendPrompt returns a channel that never closes
	// and never yields events. The context cancellation in Run should
	// cause the loop to bail out.
	blockingProvider := &blockingProvider{}
	loop := NewAgentLoop(Options{
		Provider: blockingProvider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := loop.Run(ctx, "slow task")
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

// blockingProvider is a Provider whose SendPrompt returns a channel
// that never yields events. It is used to test context cancellation.
type blockingProvider struct{}

func (b *blockingProvider) SendPrompt(ctx context.Context, _ *ProviderRequest) (<-chan ProviderEvent, error) {
	ch := make(chan ProviderEvent)
	// Never close the channel and never send — the loop will block
	// until ctx is canceled.
	return ch, nil
}

func (b *blockingProvider) ProviderID() string { return "blocking" }

func TestAgentLoop_EventToolCall_StubExecution(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "shell_exec", ParametersJSON: `{"cmd":"go test ./..."}`},
			{Type: EventDone, Usage: &UsageInfo{}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "run tests")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := spy.Events()
	for _, ev := range events {
		if tc := ev.GetToolCompleted(); tc != nil {
			if !tc.Success {
				t.Error("stub tool execution should report success=true")
			}
			if tc.ResultSummary == "" {
				t.Error("stub tool execution should have a result summary")
			}
		}
	}
}

func TestAgentLoop_SessionSummary_OnComplete(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventText, Text: "done"},
			{Type: EventDone, Usage: &UsageInfo{InputTokens: 10, OutputTokens: 5}},
		},
	}
	loop := NewAgentLoop(Options{
		Provider: provider,
		Bus:      spy,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := loop.Run(ctx, "do it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := spy.Events()
	var summary *mortisev1.SessionSummary
	for _, ev := range events {
		if s := ev.GetSummary(); s != nil {
			summary = s
		}
	}
	if summary == nil {
		t.Fatal("expected a SessionSummary event on completion")
	}
	if summary.TotalTurns < 1 {
		t.Errorf("TotalTurns: want >= 1, got %d", summary.TotalTurns)
	}
}

// ---------------------------------------------------------------------------
// Mock provider tests
// ---------------------------------------------------------------------------

func TestMockProvider_SendPrompt_ReturnsConfigurableSequence(t *testing.T) {
	t.Parallel()

	mp := NewMockProvider(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := mp.SendPrompt(ctx, &ProviderRequest{
		SystemPrompt: "You are a helpful assistant.",
		UserMessage:  "Read the project.",
	})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	var events []ProviderEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Should get: thinking → text → tool_call → done
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d: %+v", len(events), events)
	}
	assertEventType(t, events[0].Type, EventThinking, "first event")
	assertEventType(t, events[1].Type, EventText, "second event")
	assertEventType(t, events[2].Type, EventToolCall, "third event")
	assertEventType(t, events[3].Type, EventDone, "fourth event")
}

func TestMockProvider_MultiTurn_DifferentSequences(t *testing.T) {
	t.Parallel()

	mp := NewMockProvider(3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Turn 1
	ch1, err := mp.SendPrompt(ctx, &ProviderRequest{UserMessage: "task 1"})
	if err != nil {
		t.Fatalf("SendPrompt turn 1: %v", err)
	}
	events1 := drainChannel(ch1)
	if len(events1) < 4 {
		t.Fatalf("turn 1: expected at least 4 events, got %d", len(events1))
	}

	// Turn 2
	ch2, err := mp.SendPrompt(ctx, &ProviderRequest{UserMessage: "task 2"})
	if err != nil {
		t.Fatalf("SendPrompt turn 2: %v", err)
	}
	events2 := drainChannel(ch2)
	if len(events2) < 4 {
		t.Fatalf("turn 2: expected at least 4 events, got %d", len(events2))
	}

	// Turn 3
	ch3, err := mp.SendPrompt(ctx, &ProviderRequest{UserMessage: "task 3"})
	if err != nil {
		t.Fatalf("SendPrompt turn 3: %v", err)
	}
	events3 := drainChannel(ch3)
	if len(events3) < 4 {
		t.Fatalf("turn 3: expected at least 4 events, got %d", len(events3))
	}
}

func TestMockProvider_Usage(t *testing.T) {
	t.Parallel()

	mp := NewMockProvider(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := mp.SendPrompt(ctx, &ProviderRequest{UserMessage: "test"})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	events := drainChannel(ch)
	done := events[len(events)-1]
	if done.Usage == nil {
		t.Fatal("EventDone: expected Usage to be populated")
	}
	if done.Usage.InputTokens <= 0 {
		t.Errorf("InputTokens: want > 0, got %d", done.Usage.InputTokens)
	}
	if done.Usage.OutputTokens <= 0 {
		t.Errorf("OutputTokens: want > 0, got %d", done.Usage.OutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertTransition(t *testing.T, pc *mortisev1.PhaseTransitionEvent, from, to mortisev1.AgentPhase, reason string) {
	t.Helper()
	if pc.From != from {
		t.Errorf("PhaseTransition.From: want %v, got %v", from, pc.From)
	}
	if pc.To != to {
		t.Errorf("PhaseTransition.To: want %v, got %v", to, pc.To)
	}
	if pc.Reason != reason {
		t.Errorf("PhaseTransition.Reason: want %q, got %q", reason, pc.Reason)
	}
}

func assertEventType(t *testing.T, got, want ProviderEventType, label string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: want %v, got %v", label, want, got)
	}
}

func drainChannel(ch <-chan ProviderEvent) []ProviderEvent {
	var out []ProviderEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}
