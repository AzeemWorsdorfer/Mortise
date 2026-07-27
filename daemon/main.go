// Package main is the Mortise daemon entry point.
//
// As of ticket 01 (project foundation), this is a minimal Go program that
// imports the generated protobuf stubs to verify the proto build pipeline.
// The actual daemon (agent loop, tool execution, session persistence) is
// implemented across tickets 02-15.
package main

import (
	"fmt"

	mortisev1 "github.com/AzeemWorsdorfer/Mortise/daemon/gen/mortise/v1"
)

func main() {
	// Sanity check: enum values are reachable through the generated stubs.
	// If `make proto` produced broken stubs, this won't compile.
	fmt.Printf("Mortise daemon — scaffold (ticket 01)\n")
	fmt.Printf("Agent phase enum: %v\n", mortisev1.AgentPhase_name)
	fmt.Printf("Risk level enum: %v\n", mortisev1.RiskLevel_name)
}
