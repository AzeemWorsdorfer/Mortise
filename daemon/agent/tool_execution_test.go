package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// reqRecorder wraps a stubProvider and captures every
// ProviderRequest it receives, so tests can assert that tool
// results are fed back to the provider.
type reqRecorder struct {
	inner *stubProvider

	mu   sync.Mutex
	reqs []*ProviderRequest
}

func (r *reqRecorder) SendPrompt(ctx context.Context, req *ProviderRequest) (<-chan ProviderEvent, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.inner.SendPrompt(ctx, req)
}

func (r *reqRecorder) ProviderID() string { return r.inner.ProviderID() }

func (r *reqRecorder) Requests() []*ProviderRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*ProviderRequest{}, r.reqs...)
}

// toolCompletedEvents filters spy events to ToolCallCompleted payloads.
func toolCompletedEvents(spy *eventSpy) []*mortisev1.ToolCallCompleted {
	var out []*mortisev1.ToolCallCompleted
	for _, ev := range spy.Events() {
		if tc := ev.GetToolCompleted(); tc != nil {
			out = append(out, tc)
		}
	}
	return out
}

// newTestRegistry returns a registry with a real file_read bound to
// a workspace containing package.json.
func newTestRegistry(t *testing.T) (*tools.ToolRegistry, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"mortise"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.Register(tools.NewFileRead(root))
	return reg, root
}

func TestAgentLoop_ExecutesRealToolCall(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg, _ := newTestRegistry(t)
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"package.json"}`},
			{Type: EventDone, Usage: &UsageInfo{InputTokens: 10, OutputTokens: 5}},
		},
	}
	loop := NewAgentLoop(Options{Provider: provider, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "read package.json"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	completed := toolCompletedEvents(spy)
	if len(completed) != 1 {
		t.Fatalf("got %d ToolCallCompleted events, want 1", len(completed))
	}
	tc := completed[0]

	if !tc.Success {
		t.Errorf("Success = false, want true (error_message=%q)", tc.ErrorMessage)
	}
	if !strings.Contains(tc.ResultSummary, `"name":"mortise"`) {
		t.Errorf("ResultSummary = %q, want file contents", tc.ResultSummary)
	}
	if tc.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want >= 0", tc.DurationMs)
	}
}

func TestAgentLoop_PendingEventCarriesRiskLevel(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg, _ := newTestRegistry(t)
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"package.json"}`},
			{Type: EventDone},
		},
	}
	loop := NewAgentLoop(Options{Provider: provider, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "read package.json"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, ev := range spy.Events() {
		if tp := ev.GetToolPending(); tp != nil {
			if tp.RiskLevel != mortisev1.RiskLevel_SAFE {
				t.Errorf("RiskLevel = %v, want SAFE", tp.RiskLevel)
			}
			return
		}
	}
	t.Fatal("no ToolCallPending event published")
}

func TestAgentLoop_FailedToolCallFeedsErrorBack(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg, _ := newTestRegistry(t)
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"missing.txt"}`},
			{Type: EventDone},
		},
	}
	loop := NewAgentLoop(Options{Provider: provider, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A failed tool call must not abort the run.
	if err := loop.Run(ctx, "read missing file"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	completed := toolCompletedEvents(spy)
	if len(completed) != 1 {
		t.Fatalf("got %d ToolCallCompleted events, want 1", len(completed))
	}
	tc := completed[0]
	if tc.Success {
		t.Error("Success = true for non-existent file, want false")
	}
	if tc.ErrorMessage == "" {
		t.Error("ErrorMessage empty, want an error description")
	}
}

func TestAgentLoop_UnknownToolReportsFailure(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg, _ := newTestRegistry(t)
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "shell_exec", ParametersJSON: `{"command":"ls"}`},
			{Type: EventDone},
		},
	}
	loop := NewAgentLoop(Options{Provider: provider, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "run ls"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	completed := toolCompletedEvents(spy)
	if len(completed) != 1 {
		t.Fatalf("got %d ToolCallCompleted events, want 1", len(completed))
	}
	if completed[0].Success {
		t.Error("Success = true for unregistered tool, want false")
	}
	if !strings.Contains(completed[0].ErrorMessage, "shell_exec") {
		t.Errorf("ErrorMessage = %q, want mention of unknown tool", completed[0].ErrorMessage)
	}
}

func TestAgentLoop_ToolResultsFedBackToProvider(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg, _ := newTestRegistry(t)
	recorder := &reqRecorder{inner: &stubProvider{
		events: []ProviderEvent{
			{Type: EventDone},
		},
	}}
	loop := NewAgentLoop(Options{Provider: recorder, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Execute one tool through the loop's real tool-call path.
	done, err := loop.handleToolCall(ctx, 1, ProviderEvent{
		Type:           EventToolCall,
		ToolName:       "file_read",
		ParametersJSON: `{"path":"package.json"}`,
	})
	if err != nil || done {
		t.Fatalf("handleToolCall: done=%v err=%v", done, err)
	}

	// The next turn's request must carry the executed outcome.
	if _, err := loop.sendTurn(ctx, 2, "continue"); err != nil {
		t.Fatalf("sendTurn: %v", err)
	}

	reqs := recorder.Requests()
	if len(reqs) != 1 {
		t.Fatalf("provider received %d requests, want 1", len(reqs))
	}
	if len(reqs[0].ToolResults) != 1 {
		t.Fatalf("request carried %d tool results, want 1", len(reqs[0].ToolResults))
	}
	res := reqs[0].ToolResults[0]
	if res.ToolName != "file_read" || !strings.Contains(res.Output, `"name":"mortise"`) {
		t.Errorf("tool result = %+v, want file_read output with contents", res)
	}
}
