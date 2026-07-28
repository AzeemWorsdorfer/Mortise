package session

import (
	"context"
	"database/sql"
	"fmt"
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
//   - Run schema migrations on first use.
//   - Provide narrow CRUD methods over the sessions table.
//
// Is NOT responsible for:
//   - Caching sessions in memory (callers may wrap a Store in a cache).
//   - Translating rows into domain types for any caller other than this
//     package's tests (that arrives in ticket 04).
//   - Event log writes — see the eventlog package.
//
// See: docs/specs/01-core-agent-harness.md §3.2, §3.3
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path, applies the
// embedded schema, and returns a ready-to-use Store.
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
		_ = db.Close()
		return nil, fmt.Errorf("session store: ping %q: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("session store: migrate: %w", err)
	}
	return s, nil
}

// Close releases the database connection. Safe to call multiple times.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for tests. Production code should
// prefer Store's own methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Migrate creates the sessions table if it does not already exist.
//
// Schema fields (id, status, workspace_path, created_at) are the minimum
// required by ticket 02. Ticket 04 will add columns for name, branch,
// turn_count, last_active_at, and config_json.
func (s *Store) Migrate(ctx context.Context) error {
	const stmt = `
CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    status        INTEGER NOT NULL DEFAULT 0,
    workspace_path TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
`
	if _, err := s.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("create sessions table: %w", err)
	}
	return nil
}
