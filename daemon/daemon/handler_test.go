// Package daemon — handler_test.go exercises the Connect handler end to
// end over a real Unix socket and a real Connect-RPC client. These are
// the acceptance tests for ticket 02: the wire format is real, not
// mocked.
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
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

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
	if status.SessionId == "" {
		t.Error("SessionId: want non-empty temp UUID")
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

// newTestDaemon constructs a Daemon with a real net.Listener so the
// handler exercises the same code path as production.
func newTestDaemon(t *testing.T, sockPath, model, provider string, logger *slog.Logger) *Daemon {
	t.Helper()

	listener, err := newUnixListener(sockPath)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}

	return &Daemon{
		Listener:     listener,
		SocketPath:   sockPath,
		ModelID:      model,
		ProviderID:   provider,
		Workspace:    "/tmp/workspace",
		Branch:       "main",
		Logger:       logger,
		NewSessionID: randomSessionID,
	}
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
	buf   []byte
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.count.Add(1)
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Compile-time assertion: the handler we ship must satisfy the
// generated interface. If we rename a method or drop a parameter,
// this stops compiling before the runtime test runs.
var _ mortisev1connect.AgentServiceHandler = (*ConnectHandler)(nil)
