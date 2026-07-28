package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir returns a temp directory rooted at $TMPDIR (or
// os.TempDir() on non-macOS) but with a short name. macOS caps
// Unix socket paths at 104 bytes; the default t.TempDir() names
// can exceed that once a long test name is appended.
func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "mort")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestNewUnixListener_CreatesSocket(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")
	listener, err := newUnixListener(sockPath)
	if err != nil {
		t.Fatalf("newUnixListener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("socket file should exist at %q, stat err: %v", sockPath, err)
	}
}

func TestNewUnixListener_RejectsRegularFileAtPath(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")
	// Create a regular file at the target path. The daemon must
	// refuse to clobber it (clobbering could nuke user data).
	if err := os.WriteFile(sockPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := newUnixListener(sockPath); err == nil {
		t.Fatal("newUnixListener: want error when path is a regular file, got nil")
	}
}

func TestNewUnixListener_ReplacesStaleSocketFile(t *testing.T) {
	t.Parallel()

	sockPath := filepath.Join(shortTempDir(t), "x.sock")

	// Bind and close a real socket so a stale socket file exists.
	first, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	// A second listener on the same path must succeed: the helper
	// unlinks the stale socket first.
	listener, err := newUnixListener(sockPath)
	if err != nil {
		t.Fatalf("newUnixListener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}

func TestNewUnixListener_CreatesMissingParentDir(t *testing.T) {
	t.Parallel()

	// newUnixListener should auto-create the parent dir so a fresh
	// install doesn't require the user to mkdir ~/.mortise first.
	sockPath := filepath.Join(shortTempDir(t), "missing", "subdir", "x.sock")
	listener, err := newUnixListener(sockPath)
	if err != nil {
		t.Fatalf("newUnixListener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if _, err := os.Stat(sockPath); err != nil {
		t.Errorf("expected socket file at %q, stat err: %v", sockPath, err)
	}
}

func TestNewUnixListener_PropagatesOtherErrors(t *testing.T) {
	t.Parallel()

	// A path with a NUL byte is invalid on every platform; the OS
	// returns an error that should NOT be classified as "stale socket".
	bad := string([]byte{0})
	if _, err := newUnixListener(filepath.Join(shortTempDir(t), bad)); err == nil {
		t.Fatal("newUnixListener: want error for invalid path, got nil")
	}
}
