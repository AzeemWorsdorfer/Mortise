package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ConnectHandler implements the mortise.v1.AgentService Connect bidi
// stream method.
//
// In ticket 06 the handler's job is:
//   - Subscribe the new client to the EventBus and start a
//     goroutine that drains the subscription channel to the
//     stream (this is how every future event reaches the TUI).
//   - Publish a SystemStatus event through the bus so the new
//     client (and any others already connected) sees live
//     session state.
//   - Start the agent loop on first client connect (demo mode with
//     mock provider — real prompt dispatch in later tickets).
//   - Receive ClientCommands in a loop and dispatch them to the
//     agent loop (pause, resume, cancel).
//   - Unsubscribe and clean up on disconnect.
type ConnectHandler struct {
	daemon *Daemon
	logger *slog.Logger

	now func() time.Time

	// agentStarted tracks whether the agent loop has been kicked
	// off. Only the first client triggers the demo run.
	agentStarted atomic.Bool
}

// Connect is the connect-go bidi-stream entry point.
func (h *ConnectHandler) Connect(
	ctx context.Context,
	stream *connect.BidiStream[mortisev1.ClientCommand, mortisev1.ServerEvent],
) error {
	// Guard: Serve() should have created the bus. If a test or a
	// future caller wired up a handler without a bus, we fail
	// loudly rather than silently dropping events.
	if h.daemon == nil || h.daemon.EventBus == nil {
		return connect.NewError(connect.CodeInternal, errNoEventBus)
	}
	bus := h.daemon.EventBus

	clientID := uuid.NewString()
	subCh := bus.Subscribe(clientID)
	// Start the drain goroutine BEFORE publishing the
	// SystemStatus so the event is guaranteed to land on the
	// stream. The 64-deep subscription buffer provides a safety
	// net if the goroutine is briefly starved by the scheduler.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for ev := range subCh {
			if err := stream.Send(ev); err != nil {
				bus.Unsubscribe(clientID)
				h.cancelPendingApprovalsIfDisconnected()
				return
			}
		}
	}()
	defer func() {
		bus.Unsubscribe(clientID)
		h.cancelPendingApprovalsIfDisconnected()
		<-drainDone
	}()

	sessionID := h.sessionIDForLog()
	h.logger.Info("client connected", "client_id", clientID, "session_id", sessionID)

	// Publish the initial SystemStatus through the bus. The
	// publish is non-blocking; on a saturated publish channel the
	// event is dropped (and counted) but the next event will still
	// flow. ConnectedClients is read from the bus at publish time
	// so it reflects the count after this client subscribed.
	bus.Publish(h.buildSystemStatus())

	// Start the agent loop on first client connect (demo mode).
	// The loop runs with a mock provider so the TUI can render
	// live phase transitions without any real API calls.
	if h.agentStarted.CompareAndSwap(false, true) {
		agentContext := h.daemon.agentContext
		if agentContext == nil {
			agentContext = context.Background()
		}
		h.daemon.StartAgent(agentContext, "read the project structure and run tests")
	}

	// Block reading commands until the client disconnects or the
	// stream context is canceled. We dispatch each command to the
	// agent loop for pause / resume / cancel; other commands are
	// logged for future tickets.
	for {
		if err := ctx.Err(); err != nil {
			break
		}
		msg, err := stream.Receive()
		if err != nil {
			// io.EOF / "stream closed" is a normal disconnect.
			h.logger.Info("client disconnected", "client_id", clientID, "session_id", sessionID, "err", err)
			break
		}
		h.dispatchCommand(msg, sessionID)
	}

	return nil
}

// errNoEventBus is the error returned when the handler is invoked
// without a wired EventBus (i.e., Serve was never called).
var errNoEventBus = errors.New("daemon: EventBus not initialized")

// sessionIDForLog returns the daemon's current session ID for log
// lines, or "<none>" when the daemon has not been wired to a session
// (which should only happen in misconfigured tests).
func (h *ConnectHandler) cancelPendingApprovalsIfDisconnected() {
	if h.daemon == nil || h.daemon.EventBus == nil || h.daemon.AgentLoop == nil {
		return
	}
	if h.daemon.EventBus.SubscriberCount() == 0 {
		h.daemon.AgentLoop.CancelPendingApprovals("approval stream disconnected")
	}
}

func (h *ConnectHandler) sessionIDForLog() string {
	if h.daemon == nil {
		return "<none>"
	}
	if sess := h.daemon.Session(); sess != nil {
		return sess.ID
	}
	return "<none>"
}

// buildSystemStatus assembles the *ServerEvent carrying the current
// SystemStatus. The event is intended to be published through the
// EventBus; the caller never sends it directly to a stream so the
// bus remains the single point of fan-out.
func (h *ConnectHandler) buildSystemStatus() *mortisev1.ServerEvent {
	uptime := int64(0)
	if h.daemon != nil {
		uptime = int64(h.daemon.Uptime().Seconds())
	}
	var sessionID, sessionName string
	if h.daemon != nil {
		if sess := h.daemon.Session(); sess != nil {
			sessionID = sess.ID
			sessionName = sess.Name
		}
	}
	var connectedClients int32
	if h.daemon != nil {
		connectedClients = int32(h.daemon.ConnectedClients())
	}
	// Read the current agent phase (defaults to IDLE if no loop).
	phase := mortisev1.AgentPhase_IDLE
	if h.daemon != nil && h.daemon.AgentLoop != nil {
		phase = h.daemon.AgentLoop.Phase()
	}
	return &mortisev1.ServerEvent{
		TimestampMs: uint64(h.now().UnixMilli()),
		Phase:       phase,
		TurnNumber:  0,
		Payload: &mortisev1.ServerEvent_Status{
			Status: &mortisev1.SystemStatus{
				ModelId:           safeString(h.daemon, func(d *Daemon) string { return d.ModelID }),
				ProviderId:        safeString(h.daemon, func(d *Daemon) string { return d.ProviderID }),
				ConnectedClients:  connectedClients,
				SessionTokenTotal: 0,
				SessionCostTotal:  0,
				SessionId:         sessionID,
				SessionName:       sessionName,
				WorkspacePath:     safeString(h.daemon, func(d *Daemon) string { return d.Workspace }),
				Branch:            safeString(h.daemon, func(d *Daemon) string { return d.Branch }),
				UptimeSec:         uptime,
			},
		},
	}
}

// dispatchCommand handles an incoming ClientCommand, routing it
// to the agent loop or logging it for future tickets.
func (h *ConnectHandler) dispatchCommand(cmd *mortisev1.ClientCommand, sessionID string) {
	switch c := cmd.GetCommand().(type) {
	case *mortisev1.ClientCommand_Pause:
		h.logger.Info("received command", "session_id", sessionID, "command", "pause", "reason", c.Pause.GetReason())
	case *mortisev1.ClientCommand_Resume:
		h.logger.Info("received command", "session_id", sessionID, "command", "resume")
	case *mortisev1.ClientCommand_Cancel:
		h.logger.Info("received command", "session_id", sessionID, "command", "cancel", "reason", c.Cancel.GetReason())
	case *mortisev1.ClientCommand_SwitchProvider:
		h.logger.Info("received command", "session_id", sessionID, "command", "switch_provider", "provider", c.SwitchProvider.GetProviderId(), "model", c.SwitchProvider.GetModelId())
	case *mortisev1.ClientCommand_Handoff:
		h.logger.Info("received command", "session_id", sessionID, "command", "handoff", "reason", c.Handoff.GetReason())
	case *mortisev1.ClientCommand_Approve:
		h.logger.Info("received command", "session_id", sessionID, "command", "approve", "call_id", c.Approve.GetCallId())
		if h.daemon.AgentLoop != nil {
			h.daemon.AgentLoop.ApproveToolCall(c.Approve.GetCallId())
		}
	case *mortisev1.ClientCommand_Reject:
		reason := c.Reject.GetReason()
		h.logger.Info("received command", "session_id", sessionID, "command", "reject", "call_id", c.Reject.GetCallId(), "reason", reason)
		if h.daemon.AgentLoop != nil {
			h.daemon.AgentLoop.RejectToolCall(c.Reject.GetCallId(), reason)
		}
	}
}

func safeString(d *Daemon, getter func(*Daemon) string) string {
	if d == nil {
		return ""
	}
	return getter(d)
}
