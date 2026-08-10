// Package main — main_test.go exercises the mortised entrypoint.
// It covers:
//   - config validation (version, malformed JSON, project-overrides-global)
//   - start/shutdown lifecycle, including DB file presence
//   - ticket 04: session row creation, restart-time re-load, and
//     the public daemon surface that exposes them.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AzeemWorsdorfer/Mortise/daemon/config"
	"github.com/AzeemWorsdorfer/Mortise/daemon/session"
)

func TestRun_InvalidVersionFailsFast(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := run(context.Background(), runOptions{
		socketPath: filepath.Join(dir, "x.sock"),
		mortiseDir: dir,
	}, logger)
	if err == nil {
		t.Fatal("run: want error for unsupported config version, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Errorf("run: want error to mention 'invalid config', got %v", err)
	}
}

func TestRun_MalformedJSONFailsFast(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{ not json`), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := run(context.Background(), runOptions{
		socketPath: filepath.Join(dir, "x.sock"),
		mortiseDir: dir,
	}, logger)
	if err == nil {
		t.Fatal("run: want error for malformed config, got nil")
	}
}

func TestRun_ProjectOverridesGlobal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Project config sets persona=concise; global sets persona=architect.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"version":1,"persona":"architect","providers":{"default":"anthropic","models":{"anthropic":{"model":"claude-sonnet-4-20250514"}}}}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(dir, "project.mortise.json")
	if err := os.WriteFile(projectPath,
		[]byte(`{"version":1,"persona":"concise","providers":{"default":"openai","models":{"openai":{"model":"gpt-4o"}}}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(projectPath, filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Persona != "concise" {
		t.Errorf("Persona: project should win, want %q, got %q", "concise", cfg.Persona)
	}
	if cfg.Providers.Default != "openai" {
		t.Errorf("Providers.Default: project should win, want %q, got %q", "openai", cfg.Providers.Default)
	}
}

func TestRun_StartsAndShutsDown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"version":1,"providers":{"default":"openai","models":{"openai":{"model":"gpt-4o"}}}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, runOptions{
			socketPath: filepath.Join(dir, "x.sock"),
			mortiseDir: dir,
		}, logger)
	}()

	// Wait briefly for the socket to appear, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "x.sock")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.sock")); err != nil {
		t.Fatalf("daemon never bound socket: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after ctx cancel")
	}

	if _, err := os.Stat(filepath.Join(dir, "mortise.db")); err != nil {
		t.Errorf("mortise.db should exist after run: %v", err)
	}
}

// TestRun_CreatesSessionOnFirstStart asserts the ticket 04 acceptance
// criterion: starting the daemon creates a session row in SQLite with
// status Running, name derived from the workspace dir, and a
// deterministic UUIDv5 ID.
func TestRun_CreatesSessionOnFirstStart(t *testing.T) {
	t.Parallel()
	dir := shortTempDirUnderTmp(t)

	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"version":1,"providers":{"default":"openai","models":{"openai":{"model":"gpt-4o"}}}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, runOptions{
			socketPath: filepath.Join(dir, "x.sock"),
			mortiseDir: dir,
		}, logger)
	}()

	// Wait for the socket to confirm run() has progressed past
	// session creation.
	waitForSocket(t, filepath.Join(dir, "x.sock"), 5*time.Second)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	// Inspect the session row that run() left behind.
	store, err := session.Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workspace := workspacePath()
	wantID := session.NewSessionID(workspace)
	got, err := store.GetSession(context.Background(), wantID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != session.StatusRunning {
		t.Errorf("Status: want %v, got %v", session.StatusRunning, got.Status)
	}
	if got.Name != session.DefaultName(workspace) {
		t.Errorf("Name: want %q, got %q", session.DefaultName(workspace), got.Name)
	}
	if got.WorkspacePath != workspace {
		t.Errorf("WorkspacePath: want %q, got %q", workspace, got.WorkspacePath)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt: want non-zero, got zero")
	}
	if got.LastActiveAt.IsZero() {
		t.Error("LastActiveAt: want non-zero, got zero")
	}
}

// TestRun_RestartReusesExistingSession asserts crash-recovery: running
// the daemon twice in a row against the same workspace path must
// resolve to the same Session row, not create a duplicate.
func TestRun_RestartReusesExistingSession(t *testing.T) {
	t.Parallel()
	dir := shortTempDirUnderTmp(t)

	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"version":1,"providers":{"default":"openai","models":{"openai":{"model":"gpt-4o"}}}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// First start.
	{
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, runOptions{
				socketPath: filepath.Join(dir, "x.sock"),
				mortiseDir: dir,
			}, logger)
		}()
		waitForSocket(t, filepath.Join(dir, "x.sock"), 5*time.Second)
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("first run: %v", err)
		}
	}

	// Mutate the persisted row to a non-running state so we can
	// verify the second start transitions it back to Running.
	store, err := session.Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	workspace := workspacePath()
	id := session.NewSessionID(workspace)
	loaded, err := store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession (first): %v", err)
	}
	originalCreatedAt := loaded.CreatedAt
	originalName := loaded.Name
	if err := loaded.Transition(session.StatusCrashed); err != nil {
		t.Fatalf("Transition -> Crashed: %v", err)
	}
	if err := store.UpdateSession(context.Background(), loaded); err != nil {
		t.Fatalf("UpdateSession (crash): %v", err)
	}

	// Second start.
	{
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, runOptions{
				socketPath: filepath.Join(dir, "x.sock"),
				mortiseDir: dir,
			}, logger)
		}()
		waitForSocket(t, filepath.Join(dir, "x.sock"), 5*time.Second)
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("second run: %v", err)
		}
	}

	// Reload from SQLite. Same ID, same name, same workspace, and
	// the row is back in Running — not a fresh row.
	reloaded, err := store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession (second): %v", err)
	}
	if reloaded.Status != session.StatusRunning {
		t.Errorf("Status after restart: want %v, got %v", session.StatusRunning, reloaded.Status)
	}
	if reloaded.Name != originalName {
		t.Errorf("Name after restart: want %q, got %q", originalName, reloaded.Name)
	}
	if !reloaded.CreatedAt.Equal(originalCreatedAt) {
		t.Errorf("CreatedAt mutated across restart: want %v, got %v",
			originalCreatedAt, reloaded.CreatedAt)
	}
	_ = store.Close()
}

// TestLoadOrCreateSession_TransitionsThroughFullLifecycle exercises
// the documented state machine against a real SQLite store: created
// → running → paused → running → completed. Each transition is
// reflected in the database.
func TestLoadOrCreateSession_TransitionsThroughFullLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := session.Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("session.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	workspace := "/tmp/lifecycle-workspace"
	id := session.NewSessionID(workspace)

	// First load: creates the row in Running.
	sess, err := loadOrCreateSession(context.Background(), store, workspace, nil, logger)
	if err != nil {
		t.Fatalf("first loadOrCreateSession: %v", err)
	}
	if sess.ID != id {
		t.Errorf("ID: want %q, got %q", id, sess.ID)
	}
	if sess.Status != session.StatusRunning {
		t.Errorf("initial Status: want %v, got %v", session.StatusRunning, sess.Status)
	}

	// Pause.
	if err := sess.Transition(session.StatusPaused); err != nil {
		t.Fatalf("Transition Running -> Paused: %v", err)
	}
	if err := store.UpdateSession(context.Background(), sess); err != nil {
		t.Fatalf("UpdateSession (paused): %v", err)
	}
	persisted, err := store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession (paused): %v", err)
	}
	if persisted.Status != session.StatusPaused {
		t.Errorf("after pause: Status want %v, got %v", session.StatusPaused, persisted.Status)
	}

	// Resume.
	if err := sess.Transition(session.StatusRunning); err != nil {
		t.Fatalf("Transition Paused -> Running: %v", err)
	}
	if err := store.UpdateSession(context.Background(), sess); err != nil {
		t.Fatalf("UpdateSession (resumed): %v", err)
	}
	persisted, err = store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession (resumed): %v", err)
	}
	if persisted.Status != session.StatusRunning {
		t.Errorf("after resume: Status want %v, got %v", session.StatusRunning, persisted.Status)
	}

	// Complete (terminal).
	if err := sess.Transition(session.StatusCompleted); err != nil {
		t.Fatalf("Transition Running -> Completed: %v", err)
	}
	if err := store.UpdateSession(context.Background(), sess); err != nil {
		t.Fatalf("UpdateSession (completed): %v", err)
	}
	persisted, err = store.GetSession(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSession (completed): %v", err)
	}
	if persisted.Status != session.StatusCompleted {
		t.Errorf("after complete: Status want %v, got %v", session.StatusCompleted, persisted.Status)
	}

	// Verify a re-load of a Completed session does NOT silently
	// revive it: loadOrCreateSession returns the existing row
	// unchanged when it is already in a non-Running state, so the
	// Completed status survives a second daemon start.
	reloaded, err := loadOrCreateSession(context.Background(), store, workspace, nil, logger)
	if err != nil {
		t.Fatalf("second loadOrCreateSession: %v", err)
	}
	if reloaded.Status != session.StatusCompleted {
		t.Errorf("after re-load: Status want %v, got %v", session.StatusCompleted, reloaded.Status)
	}
}

// waitForSocket polls for the Unix socket file to appear. It is the
// test-side counterpart of daemon.Serve's bind step.
func waitForSocket(t *testing.T, path string, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %q did not appear within %s", path, max)
}

// shortTempDirUnderTmp creates a temp directory under /tmp with a
// short prefix, so the resulting Unix socket path stays well under
// the macOS sun_path limit of 104 bytes. Go's default $TMPDIR on
// macOS resolves to /var/folders/.../T/, which produces paths long
// enough that some test names blow the limit when concatenated with
// "/x.sock".
//
// The returned directory is registered for cleanup via t.Cleanup.
func shortTempDirUnderTmp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mortise")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
