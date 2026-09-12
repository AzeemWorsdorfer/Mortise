package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// flakyTool reports Success=false without an Error, exercising the
// normalization that keeps res.Error the single source of truth.
type flakyTool struct{}

func (flakyTool) Name() string                     { return "flaky" }
func (flakyTool) Description() string              { return "always reports failure" }
func (flakyTool) ParameterSchema() json.RawMessage { return json.RawMessage(`{}`) }
func (flakyTool) Risk() mortisev1.RiskLevel        { return mortisev1.RiskLevel_SAFE }
func (flakyTool) Execute(_ context.Context, _ json.RawMessage) (*tools.ToolResult, error) {
	return &tools.ToolResult{Success: false, Output: "it broke"}, nil
}

func TestAgentLoop_ToolReportingFailureWithoutError(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg := tools.NewRegistry()
	reg.Register(flakyTool{})
	provider := &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "flaky", ParametersJSON: `{}`},
			{Type: EventDone},
		},
	}
	loop := NewAgentLoop(Options{Provider: provider, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "use the flaky tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	completed := toolCompletedEvents(spy)
	if len(completed) != 1 {
		t.Fatalf("got %d ToolCallCompleted events, want 1", len(completed))
	}
	tc := completed[0]
	if tc.Success {
		t.Error("Success = true for tool reporting Success=false, want false")
	}
	if tc.ErrorMessage == "" {
		t.Error("ErrorMessage empty, want synthetic error message")
	}
	if !strings.Contains(tc.ErrorMessage, "flaky") {
		t.Errorf("ErrorMessage = %q, want mention of the tool name", tc.ErrorMessage)
	}
}

func TestAgentLoop_DuplicateApprovalResolutionDoesNotBlock(t *testing.T) {
	loop := NewAgentLoop(Options{})
	loop.approvalMu.Lock()
	loop.pendingApprovals["call-1"] = make(chan approvalDecision, 1)
	loop.approvalMu.Unlock()

	if !loop.ApproveToolCall("call-1") {
		t.Fatal("first approval returned false")
	}
	result := make(chan bool, 1)
	go func() { result <- loop.ApproveToolCall("call-1") }()
	select {
	case got := <-result:
		if got {
			t.Fatal("duplicate approval returned true")
		}
	case <-time.After(time.Second):
		t.Fatal("duplicate approval blocked")
	}
}

func TestTruncateResultSummary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int // rune count of result; -1 means identical output
	}{
		{
			name:  "short string passes through unchanged",
			input: "hello",
			want:  -1,
		},
		{
			name:  "exactly at limit passes through unchanged",
			input: strings.Repeat("a", resultSummaryLimit),
			want:  -1,
		},
		{
			name:  "over limit truncated to limit runes",
			input: strings.Repeat("b", resultSummaryLimit+50),
			want:  resultSummaryLimit,
		},
		{
			name:  "multi-byte runes are not split",
			input: strings.Repeat("é", resultSummaryLimit+10), // 2 bytes per rune
			want:  resultSummaryLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateResultSummary(tt.input)

			if !utf8.ValidString(got) {
				t.Fatal("truncated summary is not valid UTF-8")
			}
			if tt.want == -1 {
				if got != tt.input {
					t.Errorf("truncate(%d chars) = %d chars, want unchanged", utf8.RuneCountInString(tt.input), utf8.RuneCountInString(got))
				}
				return
			}
			if gotRunes := utf8.RuneCountInString(got); gotRunes != tt.want {
				t.Errorf("truncate(%d chars) = %d chars, want %d", utf8.RuneCountInString(tt.input), gotRunes, tt.want)
			}
		})
	}
}
