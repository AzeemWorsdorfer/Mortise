// Package agent tests for provider tool discovery: the AgentLoop must
// advertise every registered tool to the provider on each turn so real
// providers can discover file_read, file_write, and file_diff.
//
// Responsibilities:
//   - Verify ProviderRequest.ToolDefinitions carries the registry's
//     tool definitions on the normal sendTurn path
//
// Is NOT responsible for tool execution or registry ordering; those
// concerns belong to tool_execution_test.go and the tools package.
//
// See: docs/specs/01-core-agent-harness.md §3.3, ticket 08.
package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// newFileToolsRegistry returns a registry wired exactly like the
// daemon's Serve: the three built-in file tools bound to a temp
// workspace.
func newFileToolsRegistry(t *testing.T) *tools.ToolRegistry {
	t.Helper()
	root := t.TempDir()
	reg := tools.NewRegistry()
	reg.Register(tools.NewFileRead(root))
	fileWrite := tools.NewFileWrite(root, 10)
	reg.Register(fileWrite)
	reg.Register(tools.NewFileDiff(root, fileWrite.History()))
	return reg
}

// decodeToolNames parses a ProviderRequest.ToolDefinitions JSON array
// and returns the advertised tool names.
func decodeToolNames(t *testing.T, raw string) []string {
	t.Helper()
	var defs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		t.Fatalf("ToolDefinitions is not valid JSON: %v\n%s", err, raw)
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func TestAgentLoop_AdvertisesRegisteredToolsToProvider(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg := newFileToolsRegistry(t)
	recorder := &reqRecorder{inner: &stubProvider{
		events: []ProviderEvent{
			{Type: EventToolCall, ToolName: "file_read", ParametersJSON: `{"path":"package.json"}`},
			{Type: EventDone},
		},
	}}
	loop := NewAgentLoop(Options{Provider: recorder, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "read the project"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := recorder.Requests()
	if len(reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	for i, req := range reqs {
		if req.ToolDefinitions == "" {
			t.Fatalf("request %d carried no ToolDefinitions", i)
		}
	}

	// The first request (before any tool ran) must already advertise
	// every registered tool so the provider can propose them.
	advertised := decodeToolNames(t, reqs[0].ToolDefinitions)
	advertisedNames := make(map[string]bool, len(advertised))
	for _, name := range advertised {
		advertisedNames[name] = true
	}
	for _, name := range []string{"file_read", "file_write", "file_diff"} {
		if !advertisedNames[name] {
			t.Errorf("ToolDefinitions missing %q: %v", name, advertised)
		}
	}
	if len(advertised) != 3 {
		t.Errorf("advertised %d tools, want 3: %v", len(advertised), advertised)
	}
}

func TestAgentLoop_AdvertisesToolsWithSchemas(t *testing.T) {
	t.Parallel()

	spy := &eventSpy{}
	reg := newFileToolsRegistry(t)
	recorder := &reqRecorder{inner: &stubProvider{
		events: []ProviderEvent{{Type: EventDone}},
	}}
	loop := NewAgentLoop(Options{Provider: recorder, Bus: spy, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := loop.Run(ctx, "noop"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := recorder.Requests()
	if len(reqs) == 0 {
		t.Fatal("provider received no requests")
	}
	var defs []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(reqs[0].ToolDefinitions), &defs); err != nil {
		t.Fatalf("ToolDefinitions is not valid JSON: %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("advertised %d tools, want 3", len(defs))
	}
	for _, def := range defs {
		if def.Description == "" {
			t.Errorf("tool %q advertised without a description", def.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(def.Parameters, &schema); err != nil {
			t.Errorf("tool %q parameters is not a JSON object: %v", def.Name, err)
		}
	}
}
