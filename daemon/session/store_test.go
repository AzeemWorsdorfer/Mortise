package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpen_CreatesDatabaseFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir", "mortise.db")

	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("Open: expected database file at %q, stat: %v", dbPath, err)
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mortise.db")

	first, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
}

func TestMigrate_SessionsTableExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	row := store.DB().QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sessions'",
	)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("sessions table count: want 1, got %d", count)
	}
}

func TestMigrate_SessionsTableSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rows, err := store.DB().QueryContext(context.Background(), "PRAGMA table_info(sessions)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	for _, want := range []string{"id", "status", "workspace_path", "created_at"} {
		if !cols[want] {
			t.Errorf("sessions table missing column %q", want)
		}
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Errorf("second Close: want nil, got %v", err)
	}
}
