// Package daemon — handler_test.go exercises the Connect handler end to
// end over a real Unix socket and a real Connect-RPC client. These are
// the acceptance tests for tickets 02-04: the wire format is real, the
// session identity is real, and SQLite is in the loop.
package daemon

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
	"github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1/mortisev1connect"
	"github.com/AzeemWorsdorfer/Mortise/daemon/session"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const testWorkspace = "/tmp/mortise-handler-test-workspace"

func TestHandler_Connect_SendsSystemStatus(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")
	d := newTestDaemon(t, sockPath, "test-model", "test-provider", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()

	waitForSocket(t, sockPath)

	client := mortisev1connect.NewAgentServiceClient(
		newUnixHTTPClient(sockPath),
		"http://unix",
	)
	stream := client.Connect(ctx)
	defer func() { _ = stream.CloseRequest() }()

	// For a bidi stream, the first Send (even with a nil body) opens
	// the request. Without it, Receive blocks forever waiting for the
	// server response.
	if err := stream.Send(nil); err != nil {
		t.Fatalf("open stream: %v", err)
	}

	streamResp, err := stream.Receive()
	if err != nil {
		t.Fatalf("first Receive: %v", err)
	}
	status := streamResp.GetStatus()
	if status == nil {
		t.Fatalf("first event: want SystemStatus, got %+v", streamResp)
	}
	if status.ModelId != "test-model" {
		t.Errorf("ModelId: want %q, got %q", "test-model", status.ModelId)
	}
	if status.ProviderId != "test-provider" {
		t.Errorf("ProviderId: want %q, got %q", "test-provider", status.ProviderId)
	}
	wantID := session.NewSessionID(testWorkspace)
	if status.SessionId != wantID {
		t.Errorf("SessionId: want deterministic %q, got %q", wantID, status.SessionId)
	}
	if status.SessionName != session.DefaultName(testWorkspace) {
		t.Errorf("SessionName: want %q, got %q",
			session.DefaultName(testWorkspace), status.SessionName)
	}
	if status.WorkspacePath != testWorkspace {
		t.Errorf("WorkspacePath: want %q, got %q", testWorkspace, status.WorkspacePath)
	}
	if status.ConnectedClients != 1 {
		t.Errorf("ConnectedClients: want 1, got %d", status.ConnectedClients)
	}
}

func TestHandler_Connect_LogsIncomingCommand(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")

	var count atomic.Int32
	h := slog.NewJSONHandler(&countingWriter{count: &count}, &slog.HandlerOptions{Level: slog.LevelInfo})
	d := newTestDaemon(t, sockPath, "m", "p", slog.New(h))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()

	waitForSocket(t, sockPath)

	client := mortisev1connect.NewAgentServiceClient(
		newUnixHTTPClient(sockPath),
		"http://unix",
	)
	stream := client.Connect(ctx)
	defer func() { _ = stream.CloseRequest() }()

	if err := stream.Send(nil); err != nil {
		t.Fatalf("open stream: %v", err)
	}

	// Drain the initial SystemStatus so the next Receive blocks on a new event.
	if _, err := stream.Receive(); err != nil {
		t.Fatalf("first Receive: %v", err)
	}

	if err := stream.Send(&mortisev1.ClientCommand{
		Command: &mortisev1.ClientCommand_Pause{
			Pause: &mortisev1.PauseSession{Reason: "user pressed Ctrl+P"},
		},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The handler should log a "received command" line. We poll briefly
	// for the log line because the send is async w.r.t. handler processing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() > 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count.Load() < 2 {
		t.Errorf("expected at least 2 log lines (status + command), got %d", count.Load())
	}
}

func TestHandler_Connect_MultiClientFanOut(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")
	d := newTestDaemon(t, sockPath, "m", "p", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()

	waitForSocket(t, sockPath)

	client := mortisev1connect.NewAgentServiceClient(
		newUnixHTTPClient(sockPath),
		"http://unix",
	)

	openStream := func(t *testing.T) *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent] {
		t.Helper()
		stream := client.Connect(ctx)
		if err := stream.Send(nil); err != nil {
			t.Fatalf("open stream: %v", err)
		}
		// Drain the SystemStatus that the bus delivers on connect;
		// we want the next Receive to block on a fresh publish.
		if _, err := stream.Receive(); err != nil {
			t.Fatalf("first Receive: %v", err)
		}
		return stream
	}

	// drainStatus is a tiny helper that pulls the next ServerEvent
	// off stream and asserts it is a SystemStatus (the kind of
	// event the bus delivers on every new subscription).
	drainStatus := func(t *testing.T, label string, stream *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent]) {
		t.Helper()
		resCh := make(chan struct {
			r *mortisev1.ServerEvent
			e error
		}, 1)
		go func() {
			r, e := stream.Receive()
			resCh <- struct {
				r *mortisev1.ServerEvent
				e error
			}{r, e}
		}()
		select {
		case got := <-resCh:
			if got.e != nil {
				t.Fatalf("%s: drain: %v", label, got.e)
			}
			if got.r.GetStatus() == nil {
				t.Fatalf("%s: drain: want SystemStatus, got %+v", label, got.r)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: drain: no event within 2s", label)
		}
	}

	// Connect A. Then connect B. B's SystemStatus fans out to both
	// A and B, so each stream has the new event in its subscription
	// buffer. We need to drain BOTH before publishing the test
	// event, or the test will read a stale SystemStatus instead of
	// the PhaseTransitionEvent.
	a := openStream(t)
	defer func() { _ = a.CloseRequest() }()
	b := openStream(t)
	defer func() { _ = b.CloseRequest() }()

	// A still has B's SystemStatus buffered; B has nothing.
	drainStatus(t, "a (b's status)", a)

	// Both streams are now subscribed. Publish a synthetic
	// PhaseTransitionEvent through the bus and confirm both
	// receive it.
	ev := &mortisev1.ServerEvent{
		Phase:      mortisev1.AgentPhase_PLANNING,
		TurnNumber: 7,
		Payload: &mortisev1.ServerEvent_PhaseChange{
			PhaseChange: &mortisev1.PhaseTransitionEvent{
				From:   mortisev1.AgentPhase_IDLE,
				To:     mortisev1.AgentPhase_PLANNING,
				Reason: "test fan-out",
			},
		},
	}
	d.EventBus.Publish(ev)

	got := func(label string, stream *connect.BidiStreamForClient[mortisev1.ClientCommand, mortisev1.ServerEvent]) *mortisev1.PhaseTransitionEvent {
		t.Helper()
		type result struct {
			resp *mortisev1.ServerEvent
			err  error
		}
		resCh := make(chan result, 1)
		go func() {
			r, e := stream.Receive()
			resCh <- result{resp: r, err: e}
		}()
		select {
		case r := <-resCh:
			if r.err != nil {
				t.Fatalf("%s: Receive after publish: %v", label, r.err)
			}
			pc := r.resp.GetPhaseChange()
			if pc == nil {
				t.Fatalf("%s: want PhaseTransitionEvent, got %+v", label, r.resp)
			}
			return pc
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: did not receive published PhaseTransitionEvent within 2s", label)
			return nil
		}
	}
	if pc := got("a", a); pc.GetReason() != "test fan-out" {
		t.Errorf("a: reason: want %q, got %q", "test fan-out", pc.GetReason())
	}
	if pc := got("b", b); pc.GetReason() != "test fan-out" {
		t.Errorf("b: reason: want %q, got %q", "test fan-out", pc.GetReason())
	}
}

func TestServe_GracefulShutdown_RemovesSocket(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")
	d := newTestDaemon(t, sockPath, "m", "p", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()

	waitForSocket(t, sockPath)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("socket should be removed after shutdown; stat err = %v", err)
	}
}

// newTestDaemon constructs a Daemon with a real net.Listener, a real
// session.Store, and a real Session wired in. The session is created
// via the store so the handler exercises the same code path as
// production.
func newTestDaemon(t *testing.T, sockPath, model, provider string, logger *slog.Logger) *Daemon {
	t.Helper()

	listener, err := newUnixListener(sockPath)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}

	dir := t.TempDir()
	store, err := session.Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	sess := &session.Session{
		ID:            session.NewSessionID(testWorkspace),
		Name:          session.DefaultName(testWorkspace),
		Status:        session.StatusRunning,
		WorkspacePath: testWorkspace,
		Branch:        "main",
		TurnCount:     0,
		CreatedAt:     now,
		LastActiveAt:  now,
	}
	if err := store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("store.CreateSession: %v", err)
	}

	d := &Daemon{
		Listener:   listener,
		SocketPath: sockPath,
		ModelID:    model,
		ProviderID: provider,
		Workspace:  testWorkspace,
		Branch:     "main",
		Logger:     logger,
		Store:      store,
	}
	d.SetSession(sess)
	return d
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %q did not appear", path)
}

// newUnixHTTPClient returns an http.Client that dials via a Unix
// domain socket AND speaks HTTP/2 prior knowledge (h2c). The Connect
// protocol's bidi-stream encoding rides on HTTP/2 framing; without h2c
// the client and server would disagree about request framing and the
// stream would hang.
func newUnixHTTPClient(sockPath string) connect.HTTPClient {
	// http2.Transport dials via DialTLSContext even for h2c. We supply
	// a stub TLS config and dial a unix socket ourselves, returning
	// the raw conn. http2 falls back to plaintext when AllowHTTP is
	// true and the conn is *not* a *tls.Conn.
	dial := func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sockPath)
	}
	transport := &http2.Transport{
		DialTLSContext:  dial,
		AllowHTTP:       true,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only stub
	}
	return &http.Client{Transport: transport}
}

var _ = h2c.NewHandler // keep import stable if a future test uses it directly.

type countingWriter struct {
	count *atomic.Int32
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.count.Add(1)
	return len(p), nil
}

// Compile-time assertion: the handler we ship must satisfy the
// generated interface. If we rename a method or drop a parameter,
// this stops compiling before the runtime test runs.
var _ mortisev1connect.AgentServiceHandler = (*ConnectHandler)(nil)
