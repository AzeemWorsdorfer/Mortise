package daemon

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// ConnectHandler implements the mortise.v1.AgentService Connect bidi
// stream method.
//
// In ticket 02, the handler's job is narrow:
//   - Send a single SystemStatus event as soon as the stream opens.
//   - Receive ClientCommands in a loop and log each one.
//   - Hold the stream open until the client disconnects.
//
// The agent loop (turn planning, tool execution, etc.) is added in
// ticket 03+ — at that point, this handler will also forward commands
// into the loop and stream the loop's ServerEvents back to the client.
type ConnectHandler struct {
	daemon *Daemon
	logger *slog.Logger

	newID func() string
	now   func() time.Time

	connAdd  func()
	connDrop func()
}

// Connect is the connect-go bidi-stream entry point.
func (h *ConnectHandler) Connect(
	ctx context.Context,
	stream *connect.BidiStream[mortisev1.ClientCommand, mortisev1.ServerEvent],
) error {
	if h.connAdd != nil {
		h.connAdd()
	}
	defer func() {
		if h.connDrop != nil {
			h.connDrop()
		}
	}()

	sessionID := h.newID()
	h.logger.Info("client connected", "session_id", sessionID)

	if err := h.sendSystemStatus(stream, sessionID); err != nil {
		h.logger.Warn("send initial SystemStatus", "err", err, "session_id", sessionID)
		return err
	}

	// Block reading commands until the client disconnects or the
	// stream context is canceled. We log every command; acting on
	// them is ticket 03+ work.
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		msg, err := stream.Receive()
		if err != nil {
			// io.EOF / "stream closed" is a normal disconnect.
			h.logger.Info("client disconnected", "session_id", sessionID, "err", err)
			return nil
		}
		h.logCommand(msg, sessionID)
	}
}

// sendSystemStatus emits the initial SystemStatus event for a newly
// connected client.
func (h *ConnectHandler) sendSystemStatus(
	stream *connect.BidiStream[mortisev1.ClientCommand, mortisev1.ServerEvent],
	sessionID string,
) error {
	uptime := int64(0)
	if h.daemon != nil {
		uptime = int64(h.daemon.Uptime().Seconds())
	}
	ev := &mortisev1.ServerEvent{
		TimestampMs: uint64(h.now().UnixMilli()),
		Phase:       mortisev1.AgentPhase_IDLE,
		TurnNumber:  0,
		Payload: &mortisev1.ServerEvent_Status{
			Status: &mortisev1.SystemStatus{
				ModelId:           safeString(h.daemon, func(d *Daemon) string { return d.ModelID }),
				ProviderId:        safeString(h.daemon, func(d *Daemon) string { return d.ProviderID }),
				ConnectedClients:  int32(safeInt(h.daemon, func(d *Daemon) int { return d.ConnectedClients() })),
				SessionTokenTotal: 0,
				SessionCostTotal:  0,
				SessionId:         sessionID,
				SessionName:       "untitled",
				WorkspacePath:     safeString(h.daemon, func(d *Daemon) string { return d.Workspace }),
				Branch:            safeString(h.daemon, func(d *Daemon) string { return d.Branch }),
				UptimeSec:         uptime,
			},
		},
	}
	return stream.Send(ev)
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

func safeInt(d *Daemon, getter func(*Daemon) int) int {
	if d == nil {
		return 0
	}
	return getter(d)
}
