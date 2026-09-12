// Package agent tests for the MockProvider: the canned-event provider
// used by tests and the TUI's mock mode.
//
// Responsibilities:
//   - Verify each mock turn emits thinking, text, a tool proposal,
//     and a usage-carrying EventDone
//   - Verify the final turn signals completion by omitting the tool
//     call so the AgentLoop finishes after it
//
// Is NOT responsible for AgentLoop behavior; those concerns belong to
// agent_test.go and tool_execution_test.go.
//
// See: docs/specs/01-core-agent-harness.md §3.1, §3.3.
package agent

import (
	"context"
	"testing"
	"time"
)

func TestMockProvider_SendPrompt_ReturnsConfigurableSequence(t *testing.T) {
	t.Parallel()

	mp := NewMockProvider(2)
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

	// A non-final turn emits: thinking → text → tool_call → done
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
	if events2[2].ToolName != "file_write" || events2[2].ParametersJSON != `{"path":"mortise-demo.txt","content":"Mortise demo write\n"}` {
		t.Fatalf("turn 2 tool = %+v, want file_write with demo content", events2[2])
	}

	// Turn 3 signals completion: no tool call, just thinking, text,
	// and a usage-carrying EventDone.
	ch3, err := mp.SendPrompt(ctx, &ProviderRequest{UserMessage: "task 3"})
	if err != nil {
		t.Fatalf("SendPrompt turn 3: %v", err)
	}
	events3 := drainChannel(ch3)
	if len(events3) != 3 {
		t.Fatalf("turn 3: expected exactly 3 events, got %d: %+v", len(events3), events3)
	}
	assertEventType(t, events3[0].Type, EventThinking, "turn 3 first event")
	assertEventType(t, events3[1].Type, EventText, "turn 3 second event")
	assertEventType(t, events3[2].Type, EventDone, "turn 3 third event")
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

func drainChannel(ch <-chan ProviderEvent) []ProviderEvent {
	var out []ProviderEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}
