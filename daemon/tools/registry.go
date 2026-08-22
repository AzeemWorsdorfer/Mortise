package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ToolDefinition is the tool description in the format the model
// expects: name, description, and parameter JSON Schema.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"parameters"`
}

// ToolRegistry holds all registered tools (built-in, custom,
// MCP-discovered) and executes them by name. It is safe for
// concurrent use.
type ToolRegistry struct {
	builtIn map[string]Tool
	mu      sync.RWMutex
}

// NewRegistry returns an empty ToolRegistry.
func NewRegistry() *ToolRegistry {
	return &ToolRegistry{builtIn: make(map[string]Tool)}
}

// Register adds a built-in tool. It panics on a duplicate name —
// a programming error caught at startup, not at call time.
func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.builtIn[name]; exists {
		panic(fmt.Sprintf("tools: duplicate registration of tool %q", name))
	}
	r.builtIn[name] = tool
}

// Get looks up a tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.builtIn[name]
	return tool, ok
}

// List returns every registered tool in the format the model
// expects.
func (r *ToolRegistry) List() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.builtIn))
	for _, tool := range r.builtIn {
		defs = append(defs, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Schema:      tool.ParameterSchema(),
		})
	}
	return defs
}

// Execute looks up the named tool and runs it, measuring wall-clock
// duration from just before execution to return. The returned
// ToolResult.DurationMs reflects that measurement; an unknown tool
// name is an error.
func (r *ToolRegistry) Execute(ctx context.Context, name string, paramsJSON json.RawMessage) (*ToolResult, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tools: unknown tool %q", name)
	}

	start := time.Now()
	res, err := tool.Execute(ctx, paramsJSON)
	if res == nil {
		res = &ToolResult{}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil && res.Error == nil {
		res.Error = err
	}
	return res, err
}
