// Package daemon — bind.go performs the OS-level Unix domain socket
// bind, handling stale-socket cleanup, parent-directory creation, and
// collision detection (refuse to overwrite a non-socket file).
//
// Responsibilities:
//   - Create parent directories for the socket path
//   - Detect and remove stale socket files from crashed daemons
//   - Refuse to clobber regular files that happen to share the socket path
//   - Bind the socket and return a net.Listener
//
// Is NOT responsible for:
//   - HTTP/Connect-RPC handler setup (daemon.go)
//   - Permission locking on the bound socket (socket.go)
//   - Socket lifecycle (listening, accepting, shutdown — daemon.go)
package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// bindUnixListener performs the actual OS-level bind with stale-socket
// cleanup. It is split out from newUnixListener so the post-bind
// permission lock can be applied uniformly.
func bindUnixListener(path string) (net.Listener, error) {
	if path == "" {
		return nil, errors.New("unix listener: empty path")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("unix listener: mkdir %q: %w", dir, err)
		}
	}

	// Probe for a stale socket file. We only unlink when we can
	// confirm the existing entry is a socket (stat returns a S_IFSOCK
	// mode bit) — this avoids clobbering a regular file the user
	// happened to name "mortise.sock".
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket != 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("unix listener: remove stale socket %q: %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("unix listener: %q exists and is not a socket", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("unix listener: stat %q: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		// EADDRINUSE on a path we just inspected usually means a
		// concurrent process bound the path between our lstat and
		// listen. Surface a clear error.
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("unix listener: %q already in use", path)
		}
		// Invalid path (e.g. embedded NUL byte) shows up here.
		if strings.Contains(err.Error(), "invalid argument") {
			return nil, fmt.Errorf("unix listener: invalid path %q: %w", path, err)
		}
		return nil, fmt.Errorf("unix listener: bind %q: %w", path, err)
	}
	return listener, nil
}
