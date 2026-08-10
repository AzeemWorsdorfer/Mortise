package session

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestMigrate_AllColumnsPresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "mortise.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	got := columnNames(t, store.DB(), "sessions")
	want := []string{
		"id", "status", "workspace_path", "created_at",
		"name", "branch", "turn_count", "last_active_at", "config_json",
	}
	if len(got) != len(want) {
		t.Fatalf("sessions table column count: want %d (%v), got %d (%v)",
			len(want), want, len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("sessions column %d: want %q, got %q", i, w, got[i])
		}
	}
}

func TestMigrate_UpgradesLegacySchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mortise.db")

	// 1. Simulate a database created by ticket 02: 4-column schema.
	{
		raw, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("open raw: %v", err)
		}
		_, err = raw.ExecContext(context.Background(), `
CREATE TABLE sessions (
    id            TEXT PRIMARY KEY,
    status        INTEGER NOT NULL DEFAULT 0,
    workspace_path TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
`)
		if err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
		// Insert a legacy row to confirm it survives the migration.
		_, err = raw.ExecContext(context.Background(), `
INSERT INTO sessions (id, status, workspace_path, created_at)
VALUES ('legacy-id', 1, '/legacy/workspace', 1700000000);
`)
		if err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
		if err := raw.Close(); err != nil {
			t.Fatalf("close raw: %v", err)
		}
	}

	// 2. Open with the production Store; Migrate should add the
	//    missing columns without disturbing the legacy row.
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 3. All 9 columns exist.
	got := columnNames(t, store.DB(), "sessions")
	if len(got) != 9 {
		t.Fatalf("upgraded sessions table column count: want 9, got %d (%v)", len(got), got)
	}

	// 4. The legacy row still reads back with its original id and
	//    workspace_path; new columns take their declared defaults.
	var id, ws string
	var status int
	var createdAt int64
	row := store.DB().QueryRowContext(context.Background(),
		"SELECT id, status, workspace_path, created_at FROM sessions WHERE id = 'legacy-id'")
	if err := row.Scan(&id, &status, &ws, &createdAt); err != nil {
		t.Fatalf("scan legacy row: %v", err)
	}
	if id != "legacy-id" || ws != "/legacy/workspace" || status != 1 || createdAt != 1700000000 {
		t.Errorf("legacy row mutated: id=%q status=%d ws=%q createdAt=%d",
			id, status, ws, createdAt)
	}
}

func TestCreateSession_AndGetSession(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	s := &Session{
		ID:            NewSessionID("/tmp/project-a"),
		Name:          "project-a",
		Status:        StatusRunning,
		WorkspacePath: "/tmp/project-a",
		Branch:        "main",
		TurnCount:     0,
		CreatedAt:     now,
		LastActiveAt:  now,
		ConfigJSON:    `{"providers":{"default":"openai"}}`,
	}
	if err := store.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := store.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	assertSessionEqual(t, got, s)
}

func TestGetSession_NotFoundReturnsWrappedErr(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	_, err := store.GetSession(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("GetSession: want error, got nil")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("GetSession: want ErrSessionNotFound, got %v", err)
	}
}

func TestUpdateSession_RefreshesMutableFields(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	s := sampleSession("/tmp/project-b", StatusRunning)
	if err := store.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Mutate every mutable field, then Update.
	s.Name = "renamed"
	s.Branch = "feature/x"
	s.TurnCount = 7
	s.Status = StatusPaused
	s.ConfigJSON = `{"providers":{"default":"anthropic"}}`
	originalCreated := s.CreatedAt
	originalID := s.ID
	originalWorkspace := s.WorkspacePath
	if err := store.UpdateSession(ctx, s); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, err := store.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name: want %q, got %q", "renamed", got.Name)
	}
	if got.Branch != "feature/x" {
		t.Errorf("Branch: want %q, got %q", "feature/x", got.Branch)
	}
	if got.TurnCount != 7 {
		t.Errorf("TurnCount: want 7, got %d", got.TurnCount)
	}
	if got.Status != StatusPaused {
		t.Errorf("Status: want StatusPaused, got %v", got.Status)
	}
	if got.ConfigJSON != `{"providers":{"default":"anthropic"}}` {
		t.Errorf("ConfigJSON: %q", got.ConfigJSON)
	}
	if !got.LastActiveAt.After(originalCreated) && !got.LastActiveAt.Equal(originalCreated) {
		t.Errorf("LastActiveAt: want updated, got %v (created %v)", got.LastActiveAt, originalCreated)
	}
	// Immutable fields must not change.
	if got.ID != originalID {
		t.Errorf("ID mutated: want %q, got %q", originalID, got.ID)
	}
	if got.WorkspacePath != originalWorkspace {
		t.Errorf("WorkspacePath mutated: want %q, got %q", originalWorkspace, got.WorkspacePath)
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Errorf("CreatedAt mutated: want %v, got %v", originalCreated, got.CreatedAt)
	}
}

func TestUpdateSessionStatus_Atomic(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	s := sampleSession("/tmp/project-c", StatusRunning)
	if err := store.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := store.UpdateSessionStatus(ctx, s.ID, StatusPaused); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}

	got, err := store.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != StatusPaused {
		t.Errorf("Status: want StatusPaused, got %v", got.Status)
	}
	// Other fields preserved.
	if got.Name != s.Name {
		t.Errorf("Name mutated by UpdateSessionStatus: want %q, got %q", s.Name, got.Name)
	}
}

func TestCreateSession_DuplicateIDFails(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	s := sampleSession("/tmp/project-d", StatusRunning)
	if err := store.CreateSession(ctx, s); err != nil {
		t.Fatalf("CreateSession (first): %v", err)
	}
	// Re-create with the same ID; the PRIMARY KEY constraint should
	// reject the second insert.
	dup := sampleSession("/tmp/project-d", StatusRunning)
	dup.ID = s.ID
	err := store.CreateSession(ctx, dup)
	if err == nil {
		t.Fatal("CreateSession (duplicate): want error, got nil")
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

// --- helpers ---

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "mortise.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sampleSession(workspace string, status SessionStatus) *Session {
	now := time.Now().UTC().Truncate(time.Second)
	return &Session{
		ID:            NewSessionID(workspace),
		Name:          DefaultName(workspace),
		Status:        status,
		WorkspacePath: workspace,
		Branch:        "main",
		TurnCount:     0,
		CreatedAt:     now,
		LastActiveAt:  now,
		ConfigJSON:    "",
	}
}

func assertSessionEqual(t *testing.T, got, want *Session) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("ID: want %q, got %q", want.ID, got.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name: want %q, got %q", want.Name, got.Name)
	}
	if got.Status != want.Status {
		t.Errorf("Status: want %v, got %v", want.Status, got.Status)
	}
	if got.WorkspacePath != want.WorkspacePath {
		t.Errorf("WorkspacePath: want %q, got %q", want.WorkspacePath, got.WorkspacePath)
	}
	if got.Branch != want.Branch {
		t.Errorf("Branch: want %q, got %q", want.Branch, got.Branch)
	}
	if got.TurnCount != want.TurnCount {
		t.Errorf("TurnCount: want %d, got %d", want.TurnCount, got.TurnCount)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt: want %v, got %v", want.CreatedAt, got.CreatedAt)
	}
	if !got.LastActiveAt.Equal(want.LastActiveAt) {
		t.Errorf("LastActiveAt: want %v, got %v", want.LastActiveAt, got.LastActiveAt)
	}
	if got.ConfigJSON != want.ConfigJSON {
		t.Errorf("ConfigJSON: want %q, got %q", want.ConfigJSON, got.ConfigJSON)
	}
}

func columnNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("PRAGMA %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return names
}
