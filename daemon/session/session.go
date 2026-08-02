// Package session — session.go owns the Session domain type and its
// lifecycle state machine.
//
// This file deliberately does NOT import the database/sql layer. The
// state machine and ID derivation are pure-Go logic that can be
// unit-tested without SQLite. Persistence lives in store.go.
//
// Responsibilities:
//   - Define the Session struct and SessionStatus enum.
//   - Enforce the documented state-machine transitions via Transition.
//   - Derive deterministic session IDs (UUIDv5) from workspace paths.
//
// Is NOT responsible for:
//   - Persisting sessions to SQLite (store.go).
//   - Loading sessions on daemon startup (cmd/mortised/main.go).
//
// See: docs/specs/01-core-agent-harness.md §3.2, §3.3
package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// SessionStatus enumerates the lifecycle states a Session can be in.
//
// Numeric values are part of the on-disk schema (the `status` column
// is INTEGER), so the relative ordering is stable. New statuses must
// be appended at the end to keep existing rows interpretable.
type SessionStatus int

const (
	// StatusCreated is the initial state for a freshly created session
	// that has not yet been activated.
	StatusCreated SessionStatus = iota
	// StatusRunning is an active session accepting turns.
	StatusRunning
	// StatusPaused is a session the user has interrupted; the agent
	// loop is held and no new model calls are issued.
	StatusPaused
	// StatusCompleted is a terminal state for a session whose task is
	// finished. Cannot transition out.
	StatusCompleted
	// StatusErrored is a session that hit an error (e.g., provider
	// failure). Resumable to StatusRunning after the user fixes the
	// underlying problem.
	StatusErrored
	// StatusCrashed is a session whose owning daemon died. Resumable
	// to StatusRunning on the next daemon startup.
	StatusCrashed
)

// ErrInvalidTransition is returned by Session.Transition when the
// requested state change is not allowed by the lifecycle state
// machine. Callers compare with errors.Is.
var ErrInvalidTransition = errors.New("session: invalid status transition")

// Session is a single Mortise agent-coding session. It is keyed by a
// deterministic ID derived from WorkspacePath, which means reopening
// the same workspace recovers the same session.
type Session struct {
	ID            string
	Name          string
	Status        SessionStatus
	WorkspacePath string
	Branch        string
	TurnCount     int
	CreatedAt     time.Time
	LastActiveAt  time.Time
	ConfigJSON    string
}

// Transition changes s.Status to newStatus when the move is permitted
// by the documented lifecycle. On an invalid transition the receiver
// is left unchanged and ErrInvalidTransition is returned (wrapped with
// the from/to states for diagnostic logging).
//
// The state machine is:
//
//	Created  → Running
//	Running  → Paused    → Running
//	Running  → Completed
//	Running  → Errored   → Running
//	Running  → Crashed   → Running
//
// Paused, Errored, and Crashed are all resumable back to Running.
// Completed is terminal.
func (s *Session) Transition(newStatus SessionStatus) error {
	if !validTransition(s.Status, newStatus) {
		return fmt.Errorf("%w: %s -> %s",
			ErrInvalidTransition, s.Status.String(), newStatus.String())
	}
	s.Status = newStatus
	return nil
}

// validTransition reports whether moving from → to is allowed.
func validTransition(from, to SessionStatus) bool {
	if from == to {
		return false
	}
	switch from {
	case StatusCreated:
		return to == StatusRunning
	case StatusRunning:
		switch to {
		case StatusPaused, StatusCompleted, StatusErrored, StatusCrashed:
			return true
		}
	case StatusPaused:
		return to == StatusRunning
	case StatusErrored, StatusCrashed:
		return to == StatusRunning
	}
	return false
}

// String returns a human-readable name for the status. The returned
// string is stable; do not localize.
func (s SessionStatus) String() string {
	switch s {
	case StatusCreated:
		return "created"
	case StatusRunning:
		return "running"
	case StatusPaused:
		return "paused"
	case StatusCompleted:
		return "completed"
	case StatusErrored:
		return "errored"
	case StatusCrashed:
		return "crashed"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// NewSessionID returns a deterministic UUIDv5 derived from workspace.
// Two calls with the same workspace return the same ID; different
// workspaces return different IDs. This is the keystone of crash
// recovery: the same workspace path on daemon restart resolves to the
// same session row.
//
// The DNS namespace is used for global uniqueness per RFC 4122 §4.3.
// The algorithm (SHA-1, version 5) is fixed and stable across Go
// versions; tests assert the known-good value to guard against
// accidental namespace or algorithm swaps.
func NewSessionID(workspace string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(workspace)).String()
}

// DefaultName returns the conventional display name for a session
// based on its workspace path: the directory basename. Returns "" for
// an empty workspace so callers can distinguish "no workspace" from
// "a workspace named '.'".
func DefaultName(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}
