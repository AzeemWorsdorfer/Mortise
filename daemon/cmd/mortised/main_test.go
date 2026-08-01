// Package main — main_test.go exercises the mortised entrypoint without
// touching the listener. It covers the validate-fail-fast path
// (config version missing or wrong) and the merge-correct path
// (project wins over global).
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
