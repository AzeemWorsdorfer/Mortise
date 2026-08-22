package tools

import (
	"context"
	"encoding/json"
	"testing"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

// fakeTool is a minimal Tool used to exercise the registry without
// touching the real file system.
type fakeTool struct {
	name   string
	risk   mortisev1.RiskLevel
	output string
	err    error
}

func (f *fakeTool) Name() string                     { return f.name }
func (f *fakeTool) Description() string              { return "fake tool for tests" }
func (f *fakeTool) ParameterSchema() json.RawMessage { return json.RawMessage(`{}`) }
func (f *fakeTool) Risk() mortisev1.RiskLevel        { return f.risk }
func (f *fakeTool) Execute(_ context.Context, _ json.RawMessage) (*ToolResult, error) {
	return &ToolResult{Success: f.err == nil, Output: f.output}, f.err
}

func TestRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	tool := &fakeTool{name: "file_read", risk: mortisev1.RiskLevel_SAFE}

	reg.Register(tool)

	got, ok := reg.Get("file_read")
	if !ok {
		t.Fatal("Get(file_read) returned ok=false, want true")
	}
	if got.Name() != "file_read" {
		t.Errorf("Get(file_read).Name() = %q, want %q", got.Name(), "file_read")
	}

	if _, ok := reg.Get("missing"); ok {
		t.Error("Get(missing) returned ok=true, want false")
	}
}

func TestRegisterPanicsOnDuplicateName(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "file_read"})

	defer func() {
		if recover() == nil {
			t.Error("Register with duplicate name did not panic")
		}
	}()
	reg.Register(&fakeTool{name: "file_read"})
}

func TestListReturnsAllTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "file_read"})
	reg.Register(&fakeTool{name: "file_write", risk: mortisev1.RiskLevel_MODIFIES_FILES})

	defs := reg.List()
	if len(defs) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(defs))
	}

	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["file_read"] || !names["file_write"] {
		t.Errorf("List() names = %v, want file_read and file_write", names)
	}
}

func TestExecuteRunsToolAndMeasuresDuration(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "file_read", output: "hello"})

	res, err := reg.Execute(context.Background(), "file_read", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Error("res.Success = false, want true")
	}
	if res.Output != "hello" {
		t.Errorf("res.Output = %q, want %q", res.Output, "hello")
	}
	if res.DurationMs < 0 {
		t.Errorf("res.DurationMs = %d, want >= 0", res.DurationMs)
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	reg := NewRegistry()

	if _, err := reg.Execute(context.Background(), "nope", json.RawMessage(`{}`)); err == nil {
		t.Error("Execute(unknown tool) returned nil error, want error")
	}
}
