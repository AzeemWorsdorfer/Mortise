// Package config loads and merges Mortise configuration files.
//
// The configuration system has two layers:
//
//  1. Global config at ~/.mortise/config.json — user-wide defaults.
//  2. Project config at <workspace>/.mortise.json — workspace overrides.
//
// A successful load produces a single *Config whose fields reflect the
// merged result (project wins on conflicts). The merged config is
// fail-fast validated: the daemon refuses to start with an invalid
// schema.
//
// See: docs/specs/01-core-agent-harness.md §4.3
package config
