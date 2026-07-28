// Package session — store.go owns the SQLite-backed persistence layer
// for Mortise sessions.
//
// The session store is a thin wrapper around database/sql. It owns the
// connection pool, runs schema migrations on first use, and exposes
// narrow methods for the rest of the daemon. Ticket 02 only needs the
// table-creation migration; full CRUD arrives with ticket 04.
package session
