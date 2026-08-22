package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/AzeemWorsdorfer/Mortise/daemon/agent"
	"github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1/mortisev1connect"
	"github.com/AzeemWorsdorfer/Mortise/daemon/session"
	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// Daemon is the Mortise daemon's runtime. It owns the listening
// socket, the Connect-RPC handler, the EventBus that fans
// ServerEvents out to every TUI client, the system status
// snapshot, and the current Session.
//
// A Daemon value is constructed by main (or a test) and run with
// Serve(ctx). Serve blocks until ctx is canceled or the listener
// fails, then cleans up the socket file and returns.
//
// The Daemon does not own session creation: it only holds a
// reference to a Session that main has already loaded or created
// from the SQLite store. Likewise the Daemon constructs the
// EventBus during Serve and tears it down in shutdown, so callers
// do not need to wire it up.
type Daemon struct {
	// Listener is the bound socket the daemon will serve on. Required.
	Listener net.Listener

	// SocketPath is the filesystem path of the Listener. Used to
	// remove the socket file on shutdown. Required.
	SocketPath string

	// ModelID / ProviderID come from the merged config; they're
	// broadcast in every SystemStatus event.
	ModelID    string
	ProviderID string

	// Workspace is the path the daemon considers the user's working
	// directory. Reported in SystemStatus.workspace_path.
	Workspace string

	// Branch is the current git branch, if any. Reported in
	// SystemStatus.branch. Empty string when not in a git repo.
	Branch string

	// Logger receives structured log lines. Required (use a no-op
	// logger in tests that don't care).
	Logger *slog.Logger

	// Store is the SQLite-backed session persistence layer. Required
	// when the daemon is wired up by main; tests that do not need
	// session persistence may leave it nil.
	Store *session.Store

	// EventBus is the in-process fan-out hub for ServerEvents.
	// It is created by Serve (callers should not set it directly)
	// and torn down by shutdown. While Serve is running, handlers
	// call EventBus.Publish to broadcast; clients subscribe via
	// EventBus.Subscribe on connect.
	EventBus *EventBus

	// AgentLoop is the phase-aware state machine that drives the
	// agent's Planning → Acting → Observing → Deciding cycle.
	// Created by Serve from the daemon's EventBus. The handler
	// starts the loop when the user submits a prompt (or, in
	// demo mode, on first client connect with a mock provider).
	AgentLoop *agent.AgentLoop

	// session is the in-memory reference to the current Session. It
	// is set by main (or a test) after loading or creating the
	// session row. The Daemon does not mutate it directly; the
	// handler reads it through Session() when emitting
	// SystemStatus events.
	session *session.Session

	startedAt  time.Time
	uptimeOnce sync.Once
}

// SetSession records the daemon's current session. The reference is
// held as-is; the Daemon does not take ownership of any persistence
// concerns. Callers (typically main) are expected to have already
// loaded or created the session via the Store.
func (d *Daemon) SetSession(s *session.Session) { d.session = s }

// Session returns the daemon's current session, or nil if no session
// has been set. The returned pointer is shared with the Daemon;
// callers must not mutate it.
func (d *Daemon) Session() *session.Session { return d.session }

// Serve starts the HTTP server on the Daemon's Listener and blocks
// until ctx is canceled. On exit it closes the listener (which
// removes the socket file on Unix) and returns any HTTP-server error.
func (d *Daemon) Serve(ctx context.Context) error {
	if d.Listener == nil {
		return errors.New("daemon: Listener is required")
	}
	if d.Logger == nil {
		return errors.New("daemon: Logger is required")
	}
	d.uptimeOnce.Do(func() { d.startedAt = time.Now() })

	// Spin up the EventBus and its fan-out goroutine. The bus is
	// owned by the daemon and lives for the duration of Serve;
	// shutdown() calls Close on it to release subscriber channels.
	d.EventBus = NewEventBus()
	d.EventBus.SetLogger(d.Logger.With("component", "eventbus"))
	go d.EventBus.Run(ctx)

	// Wire up the AgentLoop with the EventBus as its publisher.
	// The mock provider is used for demo purposes; real providers
	// will be wired in later tickets.
	workspace := d.Workspace
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			d.Logger.Warn("no workspace configured and Getwd failed; file tools will reject all reads", "err", err)
		}
		workspace = cwd
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewFileRead(workspace))

	d.AgentLoop = agent.NewAgentLoop(agent.Options{
		Provider: agent.NewMockProvider(3),
		Bus:      d.EventBus,
		Tools:    registry,
		Logger:   d.Logger.With("component", "agent_loop"),
	})

	mux := http.NewServeMux()
	path, handler := mortisev1connect.NewAgentServiceHandler(&ConnectHandler{
		daemon: d,
		logger: d.Logger.With("component", "agent_service"),
		now:    time.Now,
	})
	mux.Handle(path, handler)

	// h2c so connect-go can speak the Connect protocol (HTTP/2 prior
	// knowledge) over a raw socket without TLS.
	httpServer := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(d.Listener) }()

	d.Logger.Info("mortised listening",
		"socket", d.SocketPath,
		"model", d.ModelID,
		"provider", d.ProviderID,
		"session_id", sessionIDForLog(d.Session()),
	)

	select {
	case <-ctx.Done():
		return d.shutdown(ctx, httpServer, errCh)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		if rmErr := os.Remove(d.SocketPath); rmErr != nil {
			d.Logger.Warn("socket remove on serve error", "err", rmErr)
		}
		return fmt.Errorf("daemon: serve: %w", err)
	}
}

// shutdown performs a graceful HTTP server shutdown followed by
// listener close and socket-file cleanup. It blocks until Serve
// returns on errCh.
func (d *Daemon) shutdown(ctx context.Context, srv *http.Server, errCh <-chan error) error {
	d.Logger.Info("mortised shutting down", "reason", ctx.Err())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		d.Logger.Warn("http.Server.Shutdown error", "err", err)
	}
	// Force-close the listener so the socket file is unlinked
	// even if Shutdown did not stop in-flight connections.
	if err := d.Listener.Close(); err != nil {
		d.Logger.Warn("listener close during shutdown", "err", err)
	}
	if err := os.Remove(d.SocketPath); err != nil {
		d.Logger.Warn("socket remove during shutdown", "err", err)
	}
	// Tear down the EventBus now that no more client goroutines
	// are pushing to it. Run() has already returned because ctx
	// was canceled; Close releases every subscriber channel so
	// in-flight drain goroutines exit cleanly.
	if d.EventBus != nil {
		d.EventBus.Close()
	}
	// Wait for Serve to return.
	<-errCh
	return nil
}

// ConnectedClients returns the number of clients currently holding an
// open Connect stream. The count is the EventBus's subscriber count;
// the bus is the single source of truth for "how many clients are
// listening right now".
func (d *Daemon) ConnectedClients() int {
	if d.EventBus == nil {
		return 0
	}
	return d.EventBus.SubscriberCount()
}

// Uptime returns the time elapsed since the daemon began serving.
// Returns 0 before Serve has been called.
func (d *Daemon) Uptime() time.Duration {
	if d.startedAt.IsZero() {
		return 0
	}
	return time.Since(d.startedAt)
}

// StartAgent kicks off the agent loop with the given user prompt.
// It runs in a background goroutine and does not block. If the
// loop is already running, StartAgent is a no-op.
func (d *Daemon) StartAgent(ctx context.Context, prompt string) {
	if d.AgentLoop == nil {
		d.Logger.Warn("StartAgent called but AgentLoop is nil")
		return
	}
	go func() {
		d.Logger.Info("agent loop started", "prompt", prompt)
		if err := d.AgentLoop.Run(ctx, prompt); err != nil {
			if !errors.Is(err, context.Canceled) {
				d.Logger.Warn("agent loop exited with error", "err", err)
			}
		}
		d.Logger.Info("agent loop completed")
	}()
}

// sessionIDForLog extracts the session ID for log lines, or "<none>"
// when the daemon has not yet been wired to a session.
func sessionIDForLog(s *session.Session) string {
	if s == nil {
		return "<none>"
	}
	return s.ID
}
