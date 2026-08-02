// Package session — session_test.go exercises the Session domain
// type and its lifecycle state machine without touching the database.
package session

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSession_Transition_Valid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from SessionStatus
		to   SessionStatus
	}{
		{"created_to_running", StatusCreated, StatusRunning},
		{"running_to_paused", StatusRunning, StatusPaused},
		{"paused_to_running", StatusPaused, StatusRunning},
		{"running_to_completed", StatusRunning, StatusCompleted},
		{"running_to_errored", StatusRunning, StatusErrored},
		{"errored_to_running", StatusErrored, StatusRunning},
		{"running_to_crashed", StatusRunning, StatusCrashed},
		{"crashed_to_running", StatusCrashed, StatusRunning},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Status: tc.from}
			if err := s.Transition(tc.to); err != nil {
				t.Fatalf("Transition(%v -> %v): want nil, got %v", tc.from, tc.to, err)
			}
			if s.Status != tc.to {
				t.Errorf("Status after Transition: want %v, got %v", tc.to, s.Status)
			}
		})
	}
}

func TestSession_Transition_Invalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		from SessionStatus
		to   SessionStatus
	}{
		{"completed_to_running", StatusCompleted, StatusRunning},
		{"completed_to_paused", StatusCompleted, StatusPaused},
		{"completed_to_errored", StatusCompleted, StatusErrored},
		{"paused_to_completed", StatusPaused, StatusCompleted},
		{"created_to_paused", StatusCreated, StatusPaused},
		{"created_to_completed", StatusCreated, StatusCompleted},
		{"errored_to_paused", StatusErrored, StatusPaused},
		{"errored_to_completed", StatusErrored, StatusCompleted},
		{"crashed_to_paused", StatusCrashed, StatusPaused},
		{"crashed_to_completed", StatusCrashed, StatusCompleted},
		{"running_to_running", StatusRunning, StatusRunning},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Status: tc.from}
			err := s.Transition(tc.to)
			if err == nil {
				t.Fatalf("Transition(%v -> %v): want error, got nil", tc.from, tc.to)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Transition: want ErrInvalidTransition, got %v", err)
			}
			if s.Status != tc.from {
				t.Errorf("Status after failed Transition: want %v (unchanged), got %v", tc.from, s.Status)
			}
		})
	}
}

func TestNewSessionID_Deterministic(t *testing.T) {
	t.Parallel()

	workspace := "/tmp/some/workspace"
	id1 := NewSessionID(workspace)
	id2 := NewSessionID(workspace)

	if id1 == "" {
		t.Fatal("NewSessionID: want non-empty UUID, got empty string")
	}
	if id1 != id2 {
		t.Errorf("NewSessionID: want deterministic for same workspace, got %q vs %q", id1, id2)
	}
}

func TestNewSessionID_UniquePerWorkspace(t *testing.T) {
	t.Parallel()

	a := NewSessionID("/workspace/a")
	b := NewSessionID("/workspace/b")

	if a == b {
		t.Errorf("NewSessionID: want unique IDs for different workspaces, got same %q", a)
	}
}

func TestNewSessionID_DeterministicUUIDv5(t *testing.T) {
	t.Parallel()

	// The known-good UUIDv5 for "/tmp/some/workspace" with the DNS
	// namespace is fixed by RFC 4122. This guards against the helper
	// accidentally swapping to a different namespace or algorithm.
	const want = "b0e130a8-4f72-5192-9828-10e46267b271"
	got := NewSessionID("/tmp/some/workspace")
	if got != want {
		t.Errorf("NewSessionID: want %q, got %q", want, got)
	}
}

func TestDefaultName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		workspace string
		want      string
	}{
		{"/tmp/my-project", "my-project"},
		{"/tmp/nested/path/foo", "foo"},
		{"", ""},
		{".", filepath.Base(".")},
	}

	for _, tc := range cases {
		t.Run(tc.workspace, func(t *testing.T) {
			if got := DefaultName(tc.workspace); got != tc.want {
				t.Errorf("DefaultName(%q): want %q, got %q", tc.workspace, tc.want, got)
			}
		})
	}
}
