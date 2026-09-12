// agent/tools.go defines ToolExecutor, the seam between the
// AgentLoop and the tool execution domain. The loop never constructs
// tools itself — it only looks them up and executes them through
// this interface, which *tools.ToolRegistry satisfies in production.
//
// See: ticket 07 — Tool Interface, Registry & File Read.

package agent

import (
	"context"
	"encoding/json"

	"github.com/AzeemWorsdorfer/Mortise/daemon/tools"
)

// ToolExecutor is the seam between the AgentLoop and the tool
// execution domain. In production this is satisfied by
// *tools.ToolRegistry; tests may supply a fake. The AgentLoop never
// constructs tools itself — it only looks them up and executes them.
type ToolExecutor interface {
	// Get returns the named registered tool, or ok=false.
	Get(name string) (tools.Tool, bool)

	// Execute looks up the named tool and runs it, measuring
	// wall-clock duration into the returned ToolResult.
	Execute(ctx context.Context, name string, params json.RawMessage) (*tools.ToolResult, error)
}

// toolDefinitionProvider is the optional capability used to advertise
// registered tools without widening the execution seam used by test fakes.
type toolDefinitionProvider interface {
	ToolDefinitions() (string, error)
}

// Compile-time check that the production registry satisfies the seam.
var _ ToolExecutor = (*tools.ToolRegistry)(nil)
