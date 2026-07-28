// Package main — branch.go owns git introspection for SystemStatus.
//
// Kept separate from main.go so the exec.Command can be replaced in
// tests without touching the entrypoint wiring.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// gitBranch returns the current git branch for workspace, or an error
// when workspace is not inside a working tree or git is unavailable.
// A 5-second deadline prevents hanging on a corrupted repo or NFS stall.
func gitBranch(workspace string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
