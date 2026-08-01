// Package main — bind.go owns OS-level binding helpers used by main.
//
// The mortised entrypoint delegates Unix-socket binding to the
// daemon package's exported NewUnixListener so the daemon and
// entrypoint share one binding policy (stale-socket cleanup, mode
// locking, parent-dir creation).
package main

import (
	"net"

	mortisedaemon "github.com/AzeemWorsdorfer/Mortise/daemon/daemon"
)

// newListener binds the requested Unix socket. Delegates to the
// daemon package so socket-binding policy lives in one place.
func newListener(socketPath string) (net.Listener, error) {
	if socketPath == "" {
		socketPath = defaultDir() + "/mortise.sock"
	}
	return mortisedaemon.NewUnixListener(socketPath)
}
