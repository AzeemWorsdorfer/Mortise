// Package session — path.go contains filesystem helpers for directory
// creation and path resolution used by the session store.
//
// Responsibilities:
//   - Create parent directories for the SQLite database file
//
// Is NOT responsible for:
//   - SQLite connection or schema management (store.go)
//   - Session lifecycle or workspace path discovery
package session

import (
	"os"
	"path/filepath"
)

// DefaultDirPerm is the filesystem permission (0755) used when creating
// Mortise state directories. It is shared between the session store and
// the daemon entrypoint.
const DefaultDirPerm = 0o755

// ensureParentDir creates the parent directory of path (idempotent).
func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, DefaultDirPerm)
}
