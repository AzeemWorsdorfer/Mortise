package agent

import (
	"context"
	"fmt"
)

// MockProvider implements the Provider interface with configurable
// canned responses. No API key or network call is required; the
// mock returns a hardcoded sequence of thinking → text → tool_call
// → done events per turn. The turnCount parameter controls how many
// turns of canned sequences are available; turn N signals completion
// so the AgentLoop transitions from DECIDING to COMPLETED.
//
// The mock is used by tests and by the TUI in --mock mode. It never
// blocks; every call to SendPrompt returns a pre-filled channel.
type MockProvider struct {
	turnCount   int
	currentTurn int
}

// NewMockProvider returns a ready-to-use mock with turnCount
// distinct canned sequences. Each call to SendPrompt advances the
// internal turn counter and returns the corresponding sequence.
// The final turn (turnCount) signals completion.
func NewMockProvider(turnCount int) *MockProvider {
	if turnCount < 1 {
		turnCount = 1
	}
	return &MockProvider{turnCount: turnCount}
}

// ProviderID returns "mock" so the daemon can distinguish it from
// real providers in logs and SystemStatus.
func (m *MockProvider) ProviderID() string { return "mock" }

// SendPrompt returns a channel of ProviderEvent for the current
// turn. The sequence is:
//
//  1. EventThinking  — "Let me analyze the task…"
//  2. EventText      — "I'll start by reading the project structure."
//  3. EventToolCall  — proposes file_read on package.json
//  4. EventDone      — with fake token counts
//
// On the final turn, EventDone carries a completion signal (the
// text "All steps complete."). The AgentLoop uses this to
// transition from DECIDING to COMPLETED.
func (m *MockProvider) SendPrompt(ctx context.Context, req *ProviderRequest) (<-chan ProviderEvent, error) {
	_ = req // canned responses ignore the request
	m.currentTurn++
	turn := m.currentTurn

	ch := make(chan ProviderEvent, 4)

	go func() {
		defer close(ch)

		// Select a tool name, path, thinking, and text per turn.
		var (
			toolName string
			filePath string
			thinking string
			text     string
		)

		switch turn {
		case 1:
			thinking = "Let me analyze the task..."
			text = "I'll start by reading the project structure."
			toolName = "file_read"
			filePath = "package.json"
		case 2:
			thinking = "Looking at the dependencies..."
			text = "I see the project uses Go modules. Let me check the main entry point."
			toolName = "file_read"
			filePath = "main.go"
		case 3:
			thinking = "Almost done, let me verify the build..."
			text = "The project structure looks good. All steps complete."
			toolName = "shell_exec"
			filePath = "go build ./..."
		default:
			thinking = fmt.Sprintf("Continuing with turn %d...", turn)
			text = "Working on the next step."
			toolName = "file_read"
			filePath = fmt.Sprintf("file_%d.go", turn)
		}

		// EventThinking
		select {
		case <-ctx.Done():
			return
		case ch <- ProviderEvent{Type: EventThinking, Text: thinking}:
		}

		// EventText
		select {
		case <-ctx.Done():
			return
		case ch <- ProviderEvent{Type: EventText, Text: text}:
		}

		// EventToolCall
		params := fmt.Sprintf(`{"path":"%s"}`, filePath)
		select {
		case <-ctx.Done():
			return
		case ch <- ProviderEvent{Type: EventToolCall, ToolName: toolName, ParametersJSON: params}:
		}

		// EventDone
		usage := &UsageInfo{
			InputTokens:  150,
			OutputTokens: 80,
			CacheTokens:  0,
			CostUSD:      0.001,
		}
		select {
		case <-ctx.Done():
			return
		case ch <- ProviderEvent{Type: EventDone, Usage: usage}:
		}
	}()

	return ch, nil
}
