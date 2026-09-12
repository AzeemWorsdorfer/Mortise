// Package daemon tests the Connect wire path for file-write approval.
//
// Responsibilities:
//   - Verify approval and rejection commands travel over Connect
//   - Verify previews precede mutation and rejected writes remain absent
//
// Is NOT responsible for the daemon's general stream fan-out or tool
// implementation details; those concerns belong to handler_test.go and tools.
//
// See: docs/specs/01-core-agent-harness.md §3.3, §4.4, ticket 08.
package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AzeemWorsdorfer/Mortise/daemon/agent"
	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
	"github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1/mortisev1connect"
)

func TestHandler_Connect_ApprovalCommandsControlFileWrite(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(map[bool]string{true: "approve", false: "reject"}[approved], func(t *testing.T) {
			runWireApprovalScenario(t, approved)
		})
	}
}

func runWireApprovalScenario(t *testing.T, approved bool) {
	t.Helper()
	root := t.TempDir()
	d := newTestDaemon(t, filepath.Join(shortTempDir(t), "x.sock"), "m", "p", slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.Workspace = root
	d.ToolApproval = map[string]string{"file_write": "confirm"}
	d.AgentProvider = approvalProvider{parameters: `{"path":"nested/wire.txt","content":"wire\n"}`}
	stop, done := startApprovalServer(t, d)
	defer stopApprovalServer(t, stop, done)
	waitForToolRegistry(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := mortisev1connect.NewAgentServiceClient(newUnixHTTPClient(d.SocketPath), "http://unix").Connect(ctx)
	defer closeApprovalStream(t, stream)
	if err := stream.Send(nil); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	pending := receivePendingTool(t, stream)
	if !pending.RequiresApproval || pending.DiffPreview == "" {
		t.Fatalf("pending approval event = %+v, want approval and preview", pending)
	}
	if _, err := os.Stat(filepath.Join(root, "nested")); !os.IsNotExist(err) {
		t.Fatalf("preview created parent directory; Stat err = %v", err)
	}
	if err := stream.Send(approvalCommand(pending.CallId, approved)); err != nil {
		t.Fatalf("send approval command: %v", err)
	}
	completed := receiveCompletedTool(t, stream)
	if completed.Success != approved {
		t.Fatalf("completed.Success = %v, want %v", completed.Success, approved)
	}
	_, statErr := os.Stat(filepath.Join(root, "nested", "wire.txt"))
	if approved && statErr != nil {
		t.Fatalf("approved write missing: %v", statErr)
	}
	if !approved && !os.IsNotExist(statErr) {
		t.Fatalf("rejected write exists; Stat err = %v", statErr)
	}
}

func startApprovalServer(t *testing.T, d *Daemon) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()
	waitForSocket(t, d.SocketPath)
	return cancel, done
}

func stopApprovalServer(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func waitForToolRegistry(t *testing.T, d *Daemon) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for d.ToolRegistry() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if d.ToolRegistry() == nil {
		t.Fatal("Serve did not initialize ToolRegistry")
	}
}

func closeApprovalStream(t *testing.T, stream *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent]) {
	t.Helper()
	if err := stream.CloseRequest(); err != nil {
		t.Logf("CloseRequest: %v", err)
	}
}

func approvalCommand(callID string, approved bool) *mortisev1.ClientCommand {
	if approved {
		return &mortisev1.ClientCommand{Command: &mortisev1.ClientCommand_Approve{
			Approve: &mortisev1.ApproveToolCall{CallId: callID},
		}}
	}
	return &mortisev1.ClientCommand{Command: &mortisev1.ClientCommand_Reject{
		Reject: &mortisev1.RejectToolCall{CallId: callID, Reason: "not authorized"},
	}}
}

// approvalProvider emits one file_write proposal and a completion marker.
type approvalProvider struct {
	parameters string
}

func (p approvalProvider) SendPrompt(context.Context, *agent.ProviderRequest) (<-chan agent.ProviderEvent, error) {
	events := make(chan agent.ProviderEvent, 2)
	events <- agent.ProviderEvent{Type: agent.EventToolCall, ToolName: "file_write", ParametersJSON: p.parameters}
	events <- agent.ProviderEvent{Type: agent.EventDone}
	close(events)
	return events, nil
}

func (p approvalProvider) ProviderID() string { return "approval-test" }

func receivePendingTool(t *testing.T, stream *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent]) *mortisev1.ToolCallPending {
	t.Helper()
	for {
		event, err := stream.Receive()
		if err != nil {
			t.Fatalf("Receive pending: %v", err)
		}
		if pending := event.GetToolPending(); pending != nil {
			return pending
		}
	}
}

func receiveCompletedTool(t *testing.T, stream *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent]) *mortisev1.ToolCallCompleted {
	t.Helper()
	for {
		event, err := stream.Receive()
		if err != nil {
			t.Fatalf("Receive completed: %v", err)
		}
		if completed := event.GetToolCompleted(); completed != nil {
			return completed
		}
	}
}
