// Package tools stores the shared in-memory history used by file tools.
//
// Responsibilities:
//   - Share per-workspace previous file contents between file_write and file_diff
//   - Enforce the bounded undo-history limit
//
// Is NOT responsible for filesystem access, diff generation, approval policy,
// or persistent storage.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package tools

import (
	"path/filepath"
	"sync"
)

const (
	defaultUndoStackSize = 10
	maxUndoStackSize     = 10
)

// FileHistory stores previous file contents in memory. It is safe for
// concurrent use and is shared by the write and diff tools for a workspace.
type FileHistory struct {
	mu         sync.Mutex
	entries    map[string][]historyEntry
	maxEntries int
	locksMu    sync.Mutex
	locks      map[string]*sync.Mutex
}

type historyEntry struct {
	content string
	exists  bool
}

var histories sync.Map

func workspaceHistory(root string, stackSize int) *FileHistory {
	key, err := filepath.Abs(root)
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(key); resolveErr == nil {
			key = resolved
		}
	}
	if err != nil {
		key = root
	}
	if value, ok := histories.Load(key); ok {
		history := value.(*FileHistory)
		history.mu.Lock()
		if stackSize < history.maxEntries {
			history.maxEntries = stackSize
			for path, entries := range history.entries {
				if len(entries) > stackSize {
					history.entries[path] = entries[len(entries)-stackSize:]
				}
			}
		}
		history.mu.Unlock()
		return history
	}
	history := &FileHistory{entries: make(map[string][]historyEntry), maxEntries: stackSize, locks: make(map[string]*sync.Mutex)}
	actual, loaded := histories.LoadOrStore(key, history)
	if loaded {
		return actual.(*FileHistory)
	}
	return history
}

func historyTargetPath(_ string, target string) string {
	if absolute, err := filepath.Abs(target); err == nil {
		target = absolute
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	return filepath.Clean(target)
}

func historyLockPath(root, requested string) string {
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return historyTargetPath(root, target)
}

func (h *FileHistory) withFileLock(path string, fn func() error) error {
	h.locksMu.Lock()
	if h.locks == nil {
		h.locks = make(map[string]*sync.Mutex)
	}
	lock, ok := h.locks[path]
	if !ok {
		lock = &sync.Mutex{}
		h.locks[path] = lock
	}
	h.locksMu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (h *FileHistory) push(path, content string, exists bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := append(h.entries[path], historyEntry{content: content, exists: exists})
	if len(entries) > h.maxEntries {
		entries = entries[len(entries)-h.maxEntries:]
	}
	h.entries[path] = entries
}

func (h *FileHistory) last(path string) (string, bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := h.entries[path]
	if len(entries) == 0 {
		return "", false, false
	}
	entry := entries[len(entries)-1]
	return entry.content, entry.exists, true
}
