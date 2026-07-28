package daemon

import (
	"fmt"
	"net"
	"os"
)

// newUnixListener binds a Unix domain socket at path, creating the
// parent directory if needed. If a stale socket file already exists
// at path (left behind by a crashed previous daemon), it is unlinked
// first so the bind succeeds.
//
// The exported alias NewUnixListener exists so the cmd/mortised
// entrypoint can reuse the binding logic without duplicating
// socket-plumbing code.
func newUnixListener(path string) (net.Listener, error) {
	listener, err := bindUnixListener(path)
	if err != nil {
		return nil, err
	}
	// Belt-and-suspenders: a previous daemon could have left the
	// socket with overly-permissive mode. Lock it down.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("unix listener: chmod %q: %w", path, err)
	}
	return listener, nil
}

// NewUnixListener is the exported form of newUnixListener, used by
// the cmd/mortised entrypoint.
func NewUnixListener(path string) (net.Listener, error) { return newUnixListener(path) }
