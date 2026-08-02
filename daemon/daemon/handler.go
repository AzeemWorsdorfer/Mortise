package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ConnectHandler implements the mortise.v1.AgentService Connect bidi
// stream method.
//
// In ticket 05 the handler's job is:
//   - Subscribe the new client to the EventBus and start a
//     goroutine that drains the subscription channel to the
//     stream (this is how every future event reaches the TUI).
//   - Publish a SystemStatus event through the bus so the new
//     client (and any others already connected) sees live
//     session state.
//   - Receive ClientCommands in a loop and log each one.
//   - Unsubscribe and clean up on disconnect.
//
// The agent loop (turn planning, tool execution, etc.) is added in
// ticket 06+ — at that point, this handler will also forward
// commands into the loop and rely on other producers publishing
// events through the bus.
type ConnectHandler struct {
	daemon *Daemon
	logger *slog.Logger

	now func() time.Time
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
	defer bus.Unsubscribe(clientID)

	// Start the drain goroutine BEFORE publishing the
	// SystemStatus so the event is guaranteed to land on the
	// stream. The 64-deep subscription buffer provides a safety
	// net if the goroutine is briefly starved by the scheduler.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for ev := range subCh {
			if err := stream.Send(ev); err != nil {
				// Best-effort: the stream is broken, the
				// receive loop below will see EOF on its
				// next Receive and exit, taking the defer
				// unsubscribe with it.
				return
			}
		}
	}()

	sessionID := h.sessionIDForLog()
	h.logger.Info("client connected", "client_id", clientID, "session_id", sessionID)

	// Publish the initial SystemStatus through the bus. The
	// publish is non-blocking; on a saturated publish channel the
	// event is dropped (and counted) but the next event will still
	// flow. ConnectedClients is read from the bus at publish time
	// so it reflects the count after this client subscribed.
	bus.Publish(h.buildSystemStatus())

	// Block reading commands until the client disconnects or the
	// stream context is canceled. We log every command; acting on
	// them is ticket 06+ work.
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
		h.logCommand(msg, sessionID)
	}

	// Trigger Unsubscribe via the defer; wait for the drain
	// goroutine to exit so it cannot race with the unsubscribe
	// on the bus's subscriber map. The drain goroutine will
	// exit naturally when Unsubscribe closes subCh, but waiting
	// here gives us a deterministic teardown for tests.
	<-drainDone
	return nil
}

// errNoEventBus is the error returned when the handler is invoked
// without a wired EventBus (i.e., Serve was never called).
var errNoEventBus = errors.New("daemon: EventBus not initialized")

// sessionIDForLog returns the daemon's current session ID for log
// lines, or "<none>" when the daemon has not been wired to a session
// (which should only happen in misconfigured tests).
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
	return &mortisev1.ServerEvent{
		TimestampMs: uint64(h.now().UnixMilli()),
		Phase:       mortisev1.AgentPhase_IDLE,
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

// logCommand emits a structured log line for an incoming ClientCommand.
func (h *ConnectHandler) logCommand(cmd *mortisev1.ClientCommand, sessionID string) {
	which := "unknown"
	switch cmd.GetCommand().(type) {
	case *mortisev1.ClientCommand_Pause:
		which = "pause"
	case *mortisev1.ClientCommand_Resume:
		which = "resume"
	case *mortisev1.ClientCommand_Cancel:
		which = "cancel"
	case *mortisev1.ClientCommand_SwitchProvider:
		which = "switch_provider"
	case *mortisev1.ClientCommand_Handoff:
		which = "handoff"
	case *mortisev1.ClientCommand_Approve:
		which = "approve"
	case *mortisev1.ClientCommand_Reject:
		which = "reject"
	}
	h.logger.Info("received command",
		"session_id", sessionID,
		"command", which,
		"payload", cmd.String(),
	)
}

func safeString(d *Daemon, getter func(*Daemon) string) string {
	if d == nil {
		return ""
	}
	return getter(d)
}


