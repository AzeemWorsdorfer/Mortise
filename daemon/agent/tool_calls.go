// Package documentation lives in doc.go. This file implements the
// tool-call half of the AgentLoop: driving one proposed tool
// invocation through pending → execute → completed, and recording
// outcomes for provider feedback.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// handleToolCall drives one proposed tool invocation: enter ACTING,
// emit ToolCallPending, execute the tool through the registry, and
// emit ToolCallCompleted with the real outcome. The caller moves to
// OBSERVING after the provider's complete batch of tool calls.
func (l *AgentLoop) handleToolCall(ctx context.Context, turn int32, ev ProviderEvent) (bool, error) {
	callID := l.emitToolPending(ctx, turn, ev)

	result := l.awaitApproval(ctx, callID)
	if result == nil {
		result = l.executeTool(ctx, callID, ev)
	} else {
		l.recordToolOutcome(callID, ev, result)
	}

	l.emitToolCompleted(callID, turn, ev.ToolName, result)
	return false, nil
}

// emitToolPending transitions PLANNING → ACTING and publishes the
// ToolCallPending event. The risk level comes from the registered
// tool; unregistered tools report RISK_UNKNOWN. Returns the
// generated call ID used to pair the pending and completed events.
func (l *AgentLoop) emitToolPending(ctx context.Context, turn int32, ev ProviderEvent) string {
	// Enter ACTING once per provider batch; subsequent tool calls in
	// the same batch remain in ACTING until EventDone is observed.
	if l.Phase() == mortisev1.AgentPhase_PLANNING {
		l.transitionWithReport(mortisev1.AgentPhase_ACTING, "tool call proposed")
	}

	l.mu.Lock()
	callID := fmt.Sprintf("call-%d-%d", turn, l.toolCalls)
	l.toolCalls++
	l.mu.Unlock()

	riskLevel := mortisev1.RiskLevel_RISK_UNKNOWN
	if l.tools != nil {
		if tool, ok := l.tools.Get(ev.ToolName); ok {
			riskLevel = tool.Risk()
		}
	}
	preview, additions, deletions := l.previewTool(ctx, ev)
	requiresApproval := l.requiresApproval(ev.ToolName)
	if requiresApproval {
		l.approvalMu.Lock()
		l.pendingApprovals[callID] = make(chan approvalDecision, 1)
		l.approvalMu.Unlock()
	}

	pendingEvent := &mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_ACTING,
		TurnNumber: turn,
		Payload: &mortisev1.ServerEvent_ToolPending{
			ToolPending: &mortisev1.ToolCallPending{
				CallId:           callID,
				ToolName:         ev.ToolName,
				ParametersJson:   ev.ParametersJSON,
				RiskLevel:        riskLevel,
				RequiresApproval: requiresApproval,
				DiffPreview:      preview,
				Additions:        int32(additions),
				Deletions:        int32(deletions),
			},
		},
	}
	if requiresApproval {
		if !l.publishApproval(pendingEvent) {
			l.resolveApproval(callID, approvalDecision{reason: "approval request could not be delivered"})
		}
	} else {
		l.publish(pendingEvent)
	}
	return callID
}

type toolPreviewer interface {
	Preview(context.Context, json.RawMessage) (*tools.ToolResult, error)
}

func (l *AgentLoop) previewTool(ctx context.Context, ev ProviderEvent) (string, int, int) {
	if l.tools == nil {
		return "", 0, 0
	}
	tool, ok := l.tools.Get(ev.ToolName)
	if !ok {
		return "", 0, 0
	}
	previewer, ok := tool.(toolPreviewer)
	if !ok {
		return "", 0, 0
	}
	result, err := previewer.Preview(ctx, json.RawMessage(ev.ParametersJSON))
	if err != nil || result == nil || !result.Success {
		return "", 0, 0
	}
	return truncateResultSummary(result.Output), result.Additions, result.Deletions
}

func (l *AgentLoop) requiresApproval(toolName string) bool {
	l.approvalMu.Lock()
	defer l.approvalMu.Unlock()
	return toolName == "file_write" && l.approvalRules[toolName] == "confirm"
}

func (l *AgentLoop) publishApproval(event *mortisev1.ServerEvent) bool {
	publisher, ok := l.bus.(interface {
		PublishApproval(*mortisev1.ServerEvent) bool
	})
	if !ok {
		l.publish(event)
		return l.bus != nil
	}
	event.TimestampMs = uint64(time.Now().UnixMilli())
	return publisher.PublishApproval(event)
}

func (l *AgentLoop) awaitApproval(ctx context.Context, callID string) *tools.ToolResult {
	l.approvalMu.Lock()
	channel, pending := l.pendingApprovals[callID]
	l.approvalMu.Unlock()
	if !pending {
		return nil
	}
	var decision approvalDecision
	select {
	case decision = <-channel:
	case <-ctx.Done():
		decision = approvalDecision{reason: ctx.Err().Error()}
	}
	l.approvalMu.Lock()
	delete(l.pendingApprovals, callID)
	l.approvalMu.Unlock()
	if decision.approved {
		return nil
	}
	if decision.reason == "" {
		decision.reason = "tool call rejected"
	}
	return &tools.ToolResult{Success: false, Error: fmt.Errorf("file_write: %s", decision.reason)}
}

// resultSummaryLimit caps how much of a tool's output is carried in
// ToolCallCompleted.result_summary and fed back to the provider.
const resultSummaryLimit = 200

// truncateResultSummary returns the first resultSummaryLimit
// characters of s, never splitting a multi-byte UTF-8 rune (a tool
// may read non-ASCII file contents).
func truncateResultSummary(s string) string {
	runes := []rune(s)
	if len(runes) <= resultSummaryLimit {
		return s
	}
	return string(runes[:resultSummaryLimit])
}

// executeTool runs the proposed call through the registry and
// records its outcome for provider feedback. A nil registry or an
// unknown tool name yields a failed result rather than aborting the
// run — failures are observations for the model, not loop errors.
func (l *AgentLoop) executeTool(ctx context.Context, callID string, ev ProviderEvent) *tools.ToolResult {
	if err := ctx.Err(); err != nil {
		res := &tools.ToolResult{Success: false, Error: err}
		l.recordToolOutcome(callID, ev, res)
		return res
	}

	var (
		res *tools.ToolResult
		err error
	)
	if l.tools == nil {
		err = fmt.Errorf("no tool registry configured")
	} else {
		res, err = l.tools.Execute(ctx, ev.ToolName, json.RawMessage(ev.ParametersJSON))
	}

	if res == nil {
		res = &tools.ToolResult{}
	}
	// Normalize: the loop treats res.Error as the single source of
	// truth for failure, whichever side of the seam set it.
	switch {
	case err != nil && res.Error == nil:
		res.Error = err
	case res.Error != nil && err == nil:
		err = res.Error
	}
	if err == nil && !res.Success {
		// A tool may report failure via Success alone; give it a
		// synthetic error so downstream has one source of truth.
		err = fmt.Errorf("tool %q reported failure", ev.ToolName)
		res.Error = err
	}

	l.recordToolOutcome(callID, ev, res)
	return res
}

// recordToolOutcome stores an executed or rejected tool result for the
// provider's next request.
func (l *AgentLoop) recordToolOutcome(callID string, ev ProviderEvent, res *tools.ToolResult) {
	output := res.Output
	if res.Error != nil {
		output = res.Error.Error()
	}
	outcome := ToolOutcome{
		CallID:   callID,
		ToolName: ev.ToolName,
		Output:   truncateResultSummary(output),
	}
	l.mu.Lock()
	l.toolOutcomes = append(l.toolOutcomes, outcome)
	l.mu.Unlock()
}

// emitToolCompleted publishes the ToolCallCompleted event for the
// executed call. The caller transitions ACTING → OBSERVING after the
// complete provider batch has been processed.
func (l *AgentLoop) emitToolCompleted(callID string, turn int32, toolName string, res *tools.ToolResult) {
	completed := &mortisev1.ToolCallCompleted{
		CallId:       callID,
		ToolName:     toolName,
		Success:      res.Error == nil,
		DurationMs:   res.DurationMs,
		FilesChanged: append([]string(nil), res.FilesChanged...),
		Additions:    int32(res.Additions),
		Deletions:    int32(res.Deletions),
	}
	if completed.Success {
		completed.ResultSummary = truncateResultSummary(res.Output)
	} else {
		completed.ErrorMessage = truncateResultSummary(res.Error.Error())
	}

	l.publish(&mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_ACTING,
		TurnNumber: turn,
		Payload: &mortisev1.ServerEvent_ToolCompleted{
			ToolCompleted: completed,
		},
	})

}
