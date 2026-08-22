package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// newTestWorkspace creates a temp workspace root containing
// package.json and a sub directory.
func newTestWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"mortise"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFileReadRiskIsSafe(t *testing.T) {
	tool := NewFileRead(newTestWorkspace(t))
	if got := tool.Risk(); got != mortisev1.RiskLevel_SAFE {
		t.Errorf("file_read Risk() = %v, want SAFE", got)
	}
}

func TestFileReadReturnsContentsForValidPath(t *testing.T) {
	tool := NewFileRead(newTestWorkspace(t))

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"package.json"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Error("res.Success = false, want true")
	}
	if res.Output != `{"name":"mortise"}` {
		t.Errorf("res.Output = %q, want file contents", res.Output)
	}
}

func TestFileReadErrors(t *testing.T) {
	outsideFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		params     string
		setup      func(root string)
		wantErrSub string
	}{
		{
			name:       "path outside workspace root",
			params:     `{"path":"../secret.txt"}`,
			wantErrSub: "outside the workspace",
		},
		{
			name:   "symlink escaping the workspace",
			params: `{"path":"link.txt"}`,
			setup: func(root string) {
				if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
			wantErrSub: "outside the workspace",
		},
		{
			name:       "path is the workspace root itself",
			params:     `{"path":"."}`,
			wantErrSub: "workspace root",
		},
		{
			name:       "non-existent file",
			params:     `{"path":"missing.txt"}`,
			wantErrSub: "does not exist",
		},
		{
			name:       "directory instead of file",
			params:     `{"path":"sub"}`,
			wantErrSub: "directory",
		},
		{
			name:       "missing path parameter",
			params:     `{}`,
			wantErrSub: "path",
		},
		{
			name:       "empty path parameter",
			params:     `{"path":""}`,
			wantErrSub: "path",
		},
		{
			name:       "malformed params JSON",
			params:     `not json`,
			wantErrSub: "path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestWorkspace(t)
			if tt.setup != nil {
				tt.setup(root)
			}
			tool := NewFileRead(root)

			res, err := tool.Execute(context.Background(), json.RawMessage(tt.params))
			if err == nil {
				t.Fatalf("Execute(%s) returned nil error, want error containing %q", tt.params, tt.wantErrSub)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErrSub)
			}
			if res != nil && res.Success {
				t.Error("res.Success = true on error, want false")
			}
		})
	}
}

func TestFileReadReadsThroughWorkspaceSymlinkedDir(t *testing.T) {
	// A symlink INSIDE the workspace pointing to another file INSIDE
	// the workspace is legitimate and must still be readable.
	root := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	tool := NewFileRead(root)

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"alias.txt"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Output != "inside" {
		t.Errorf("res.Output = %q, want %q", res.Output, "inside")
	}
}
