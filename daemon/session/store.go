package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	// Pure-Go SQLite driver. Imported with the blank identifier so its
	// init() registers the "sqlite" driver with database/sql. We avoid
	// mattn/go-sqlite3 because that needs CGO, which would break the
	// static-linking NFR (binary < 30MB).
	_ "modernc.org/sqlite"
)

// Store is the Mortise session persistence layer.
//
// Responsibilities:
//   - Own the SQLite connection pool.
//   - Run schema migrations on first use (idempotent across schema
//     versions).
//   - Provide narrow CRUD methods over the sessions table.
//   - Translate database/sql rows into *Session domain values.
//
// Is NOT responsible for:
//   - In-memory caching of sessions (callers may wrap a Store in a
//     cache; ticket 04 does not need one).
//   - Event log writes — see the eventlog package (later ticket).
//   - Agent-loop state — sessions are passive records here.
//
// See: docs/specs/01-core-agent-harness.md §3.2, §3.3
type Store struct {
	db        *sql.DB
	closeOnce sync.Once
}

// Open opens (or creates) a SQLite database at path, applies the
// schema, and returns a ready-to-use Store.
//
// The parent directory of path is created if missing. The connection
// pool is sized for a single-writer daemon: one writer, many readers
// (the connect handler runs in its own goroutine).
func Open(ctx context.Context, path string) (*Store, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, fmt.Errorf("session store: prepare dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("session store: open %q: %w", path, err)
	}
	// SQLite is single-writer; a small pool is plenty for the daemon
	// and avoids spurious "database is locked" errors on reconnect.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close() //nolint:errcheck // caller already has a failure reason
		return nil, fmt.Errorf("session store: ping %q: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close() //nolint:errcheck // caller already has a failure reason
		return nil, fmt.Errorf("session store: migrate: %w", err)
	}
	return s, nil
}

// Close releases the database connection. Safe to call multiple times
// (idempotent via sync.Once).
func (s *Store) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.db != nil {
			closeErr = s.db.Close()
		}
	})
	return closeErr
}

// DB exposes the underlying *sql.DB for tests. Production code should
// prefer Store's own methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// ErrSessionNotFound is returned by GetSession when no row matches the
// requested id. It wraps database/sql.ErrNoRows so callers can use
// either error in errors.Is.
var ErrSessionNotFound = fmt.Errorf("session: not found: %w", sql.ErrNoRows)

// columnAdditions lists the columns added after the initial 4-column
// schema from ticket 02. Each entry is (column_name, full_alter_clause)
// for the idempotent migration loop in Migrate.
var columnAdditions = []struct {
	name string
	ddl  string
}{
	{"name", "ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT ''"},
	{"branch", "ALTER TABLE sessions ADD COLUMN branch TEXT NOT NULL DEFAULT ''"},
	{"turn_count", "ALTER TABLE sessions ADD COLUMN turn_count INTEGER NOT NULL DEFAULT 0"},
	{"last_active_at", "ALTER TABLE sessions ADD COLUMN last_active_at INTEGER NOT NULL DEFAULT 0"},
	{"config_json", "ALTER TABLE sessions ADD COLUMN config_json TEXT NOT NULL DEFAULT ''"},
}

// Migrate creates the sessions table if it does not exist, applying
// the full 9-column schema. If the table already exists with the
// older 4-column schema, Migrate adds the missing columns one at a
// time. Each ADD COLUMN is gated by a PRAGMA table_info check, so
// running Migrate on an already-upgraded database is a no-op.
//
// This idempotency is required because users upgrading from ticket
// 02 already have a populated sessions table that we must not lose.
func (s *Store) Migrate(ctx context.Context) error {
	const createStmt = `
CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    status        INTEGER NOT NULL DEFAULT 0,
    workspace_path TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    branch        TEXT NOT NULL DEFAULT '',
    turn_count    INTEGER NOT NULL DEFAULT 0,
    last_active_at INTEGER NOT NULL DEFAULT 0,
    config_json   TEXT NOT NULL DEFAULT ''
);
`
	if _, err := s.db.ExecContext(ctx, createStmt); err != nil {
		return fmt.Errorf("create sessions table: %w", err)
	}

	for _, col := range columnAdditions {
		present, err := s.hasColumn(ctx, "sessions", col.name)
		if err != nil {
			return fmt.Errorf("check column %q: %w", col.name, err)
		}
		if present {
			continue
		}
		if _, err := s.db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("add column %q: %w", col.name, err)
		}
	}
	return nil
}

// hasColumn reports whether table contains a column named name.
func (s *Store) hasColumn(ctx context.Context, table, name string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var colName, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if colName == name {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// CreateSession inserts a new row. The caller is responsible for
// populating ID (use NewSessionID), Status, WorkspacePath, CreatedAt,
// and any optional fields. The LastActiveAt field on the receiver is
// NOT consulted; the row is written with CreatedAt for last_active_at
// as a sane default.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	if sess == nil {
		return errors.New("session store: CreateSession: nil session")
	}
	if sess.ID == "" {
		return errors.New("session store: CreateSession: empty id")
	}
	const stmt = `
INSERT INTO sessions
    (id, status, workspace_path, branch, turn_count,
     created_at, last_active_at, name, config_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`
	lastActive := sess.LastActiveAt
	if lastActive.IsZero() {
		lastActive = sess.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, stmt,
		sess.ID,
		int(sess.Status),
		sess.WorkspacePath,
		sess.Branch,
		sess.TurnCount,
		sess.CreatedAt.Unix(),
		lastActive.Unix(),
		sess.Name,
		sess.ConfigJSON,
	)
	if err != nil {
		return fmt.Errorf("session store: insert %q: %w", sess.ID, err)
	}
	return nil
}

// GetSession fetches the row with the given id and returns it as a
// *Session. If no row matches, the returned error wraps
// ErrSessionNotFound (which itself wraps sql.ErrNoRows).
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	const stmt = `
SELECT id, status, workspace_path, branch, turn_count,
       created_at, last_active_at, name, config_json
FROM sessions WHERE id = ?
`
	row := s.db.QueryRowContext(ctx, stmt, id)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session store: get %q: %w", id, ErrSessionNotFound)
		}
		return nil, fmt.Errorf("session store: get %q: %w", id, err)
	}
	return sess, nil
}

// UpdateSession writes all mutable fields (name, status, branch,
// turn_count, config_json) and refreshes last_active_at to the
// current time. ID, workspace_path, and created_at are immutable and
// are NOT updated even if the receiver carries different values.
func (s *Store) UpdateSession(ctx context.Context, sess *Session) error {
	if sess == nil {
		return errors.New("session store: UpdateSession: nil session")
	}
	if sess.ID == "" {
		return errors.New("session store: UpdateSession: empty id")
	}
	const stmt = `
UPDATE sessions SET
    status        = ?,
    branch        = ?,
    turn_count    = ?,
    last_active_at = ?,
    name          = ?,
    config_json   = ?
WHERE id = ?
`
	res, err := s.db.ExecContext(ctx, stmt,
		int(sess.Status),
		sess.Branch,
		sess.TurnCount,
		time.Now().UTC().Unix(),
		sess.Name,
		sess.ConfigJSON,
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("session store: update %q: %w", sess.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("session store: update %q: rows affected: %w", sess.ID, err)
	}
	if rows == 0 {
		return fmt.Errorf("session store: update %q: %w", sess.ID, ErrSessionNotFound)
	}
	return nil
}

// UpdateSessionStatus is an atomic status-only UPDATE. It also
// refreshes last_active_at because changing session status is by
// definition an activity. The transition is not validated against the
// lifecycle state machine; callers that need validation should call
// Session.Transition first.
func (s *Store) UpdateSessionStatus(ctx context.Context, id string, status SessionStatus) error {
	const stmt = `UPDATE sessions SET status = ?, last_active_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, stmt, int(status), time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("session store: update status %q: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("session store: update status %q: rows affected: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("session store: update status %q: %w", id, ErrSessionNotFound)
	}
	return nil
}

// scanSession materializes a *Session from a single row. Column order
// must match the SELECT list used by GetSession.
func scanSession(row *sql.Row) (*Session, error) {
	var (
		sess        Session
		statusInt   int
		createdUnix int64
		lastUnix    int64
	)
	err := row.Scan(
		&sess.ID,
		&statusInt,
		&sess.WorkspacePath,
		&sess.Branch,
		&sess.TurnCount,
		&createdUnix,
		&lastUnix,
		&sess.Name,
		&sess.ConfigJSON,
	)
	if err != nil {
		return nil, err
	}
	sess.Status = SessionStatus(statusInt)
	sess.CreatedAt = time.Unix(createdUnix, 0).UTC()
	sess.LastActiveAt = time.Unix(lastUnix, 0).UTC()
	return &sess, nil
}
