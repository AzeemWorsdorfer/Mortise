package agent

import (
	"context"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ---------------------------------------------------------------------------
// Provider interface
// ---------------------------------------------------------------------------

// Provider is the seam between the AgentLoop and any LLM backend
// (Anthropic, OpenAI, Gemini, a mock, etc.). The loop calls
// SendPrompt once per turn, iterates over the response channel,
// translates each ProviderEvent into a ServerEvent, and publishes
// through the EventBus.
//
// Implementations of Provider carry their own credentials and
// transport; the AgentLoop is never aware of API keys or HTTP
// clients.
type Provider interface {
	// SendPrompt sends a turn request to the model and returns a
	// channel of events. The channel is closed when the turn is
	// complete (either EventDone or the provider's internal
	// context was canceled). The caller must drain the channel
	// to avoid goroutine leaks.
	SendPrompt(ctx context.Context, req *ProviderRequest) (<-chan ProviderEvent, error)

	// ProviderID returns a stable identifier for this provider
	// (e.g., "anthropic", "openai", "mock").
	ProviderID() string
}

// ---------------------------------------------------------------------------
// Provider event types
// ---------------------------------------------------------------------------

// ProviderEventType tags the kind of streaming event coming from a
// Provider. The AgentLoop maps each type to a specific ServerEvent
// oneof payload.
type ProviderEventType int

const (
	// EventThinking is a reasoning / chain-of-thought chunk the
	// model produced before the final answer. Mapped to
	// ServerEvent.thinking.
	EventThinking ProviderEventType = iota

	// EventText is a final-answer text chunk. Mapped to
	// ServerEvent.text.
	EventText

	// EventToolCall is a proposed tool invocation. The AgentLoop
	// sets the phase to ACTING and emits ToolCallPending, then
	// (in this ticket) a stubbed ToolCallCompleted. Real tool
	// execution arrives in ticket 07+.
	EventToolCall

	// EventDone signals the end of the turn. The Provider's
	// Usage field carries token/cost stats for the turn.
	EventDone
)

// String returns a human-readable label for the event type.
func (t ProviderEventType) String() string {
	switch t {
	case EventThinking:
		return "thinking"
	case EventText:
		return "text"
	case EventToolCall:
		return "tool_call"
	case EventDone:
		return "done"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// ProviderEvent
// ---------------------------------------------------------------------------

// ProviderEvent is a single streaming event from the model. The
// AgentLoop reads events from the channel returned by SendPrompt
// and translates each one into a ServerEvent.
type ProviderEvent struct {
	Type           ProviderEventType
	Text           string     // set for EventThinking, EventText
	ToolName       string     // set for EventToolCall
	ParametersJSON string     // set for EventToolCall
	Usage          *UsageInfo // set for EventDone
}

// UsageInfo captures the token and cost accounting for one turn.
// It is embedded in ProviderEvent and mapped to UsageDelta in
// ToolCallCompleted and SessionSummary protobuf messages.
type UsageInfo struct {
	InputTokens  int32
	OutputTokens int32
	CacheTokens  int32
	CostUSD      float64
}

// ---------------------------------------------------------------------------
// ProviderRequest
// ---------------------------------------------------------------------------

// ProviderRequest is the request the AgentLoop sends to the
// Provider at the start of a turn. It carries the system prompt,
// the user message, and any tool definitions the model can call.
type ProviderRequest struct {
	SystemPrompt string
	UserMessage  string
	// ToolDefinitions is a JSON string of the available tools
	// (stubbed in this ticket; real tool schemas in ticket 07+).
	ToolDefinitions string
}

// ---------------------------------------------------------------------------
// EventPublisher seam
// ---------------------------------------------------------------------------

// EventPublisher is the seam the AgentLoop uses to publish
// ServerEvents. In production this is the daemon's EventBus.Publish
// method. Tests supply a spy that records events for assertions.
type EventPublisher interface {
	Publish(event *mortisev1.ServerEvent)
}
