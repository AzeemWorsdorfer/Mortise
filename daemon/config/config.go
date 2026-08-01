// Package config — config.go contains the type definitions, file
// loading, schema merging, and validation logic for Mortise config.
//
// Splitting loading from validation is deliberate: tests can construct a
// Config in memory and call Validate without touching the filesystem.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the merged Mortise configuration. It is the authoritative
// input to the agent loop, tool registry, provider, and persona system.
//
// Field names mirror the JSON schema in docs/specs/01-core-agent-harness.md §4.3.
// JSON tags drive both decoding and (in future tickets) re-serialization.
type Config struct {
	// Version is the schema version. Must be 1.
	Version int `json:"version"`

	// Persona is the default persona name; resolved via Personas map.
	Persona string `json:"persona,omitempty"`

	// Tools holds tool registry and approval policy.
	Tools ToolsConfig `json:"tools"`

	// Providers holds default/fallback provider + per-provider model selection.
	Providers ProvidersConfig `json:"providers"`

	// Personas maps persona name to system prompt text.
	Personas map[string]string `json:"personas,omitempty"`

	// Skills maps skill name to skill definition (CH-15).
	Skills map[string]SkillConfig `json:"skills,omitempty"`

	// MCPServers lists MCP servers to spawn at session start (CH-14).
	MCPServers []MCPServerConfig `json:"mcp_servers,omitempty"`

	// Context controls project context loading (CH-06).
	Context ContextConfig `json:"context"`

	// Logging configures log level and per-session event log directory.
	Logging LoggingConfig `json:"logging"`
}

// ToolsConfig is the inner tools.* subtree.
type ToolsConfig struct {
	// Approval maps tool name (file_read, file_write, shell_safe, shell_destructive)
	// to either "auto" or "confirm".
	Approval map[string]string `json:"approval,omitempty"`

	// Shell holds the shell-tool allowlist/denylist/timeout.
	Shell ShellConfig `json:"shell"`

	// MaxParallelTools caps concurrent tool execution (default 1).
	MaxParallelTools int `json:"max_parallel_tools,omitempty"`

	// UndoStackSize caps in-memory file-write undo entries (default 10).
	UndoStackSize int `json:"undo_stack_size,omitempty"`

	// Custom lists user-defined tools (CH-11).
	Custom []CustomToolConfig `json:"custom,omitempty"`
}

// ShellConfig governs shell command execution.
type ShellConfig struct {
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	BlockedPatterns []string `json:"blocked_patterns,omitempty"`
	Sandbox         bool     `json:"sandbox,omitempty"`
	TimeoutSec      int      `json:"timeout_sec,omitempty"`
}

// CustomToolConfig describes a user-defined tool that wraps a shell command.
type CustomToolConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  map[string]any    `json:"parameters,omitempty"`
	Command     string            `json:"command"`
	WorkingDir  string            `json:"working_dir,omitempty"`
	Approval    string            `json:"approval,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// ProvidersConfig holds provider selection.
type ProvidersConfig struct {
	Default  string                    `json:"default,omitempty"`
	Fallback string                    `json:"fallback,omitempty"`
	Models   map[string]ProviderConfig `json:"models,omitempty"`
}

// ProviderConfig is per-provider model + credentials pointer.
type ProviderConfig struct {
	Model    string `json:"model,omitempty"`
	APIKeyID string `json:"api_key_id,omitempty"`
}

// SkillConfig describes a named skill (CH-15).
type SkillConfig struct {
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// MCPServerConfig describes an MCP server to spawn (CH-14).
type MCPServerConfig struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	Transport string   `json:"transport,omitempty"`
}

// ContextConfig controls project context loading (CH-06).
type ContextConfig struct {
	MaxTurnsBeforeHandoffHint int  `json:"max_turns_before_handoff_hint,omitempty"`
	IncludeGitDiff            bool `json:"include_git_diff,omitempty"`
	MaxFilesInContext         int  `json:"max_files_in_context,omitempty"`
	RespectGitignore          bool `json:"respect_gitignore,omitempty"`
}

// LoggingConfig configures log level and event log location.
type LoggingConfig struct {
	Level         string `json:"level,omitempty"`
	SessionLogDir string `json:"session_log_dir,omitempty"`
}

// Load reads the project and global config files from disk, merges them
// (project wins on field-level conflicts), and returns the result.
//
// A missing file is not an error — Mortise may run with only a global
// config, only a project config, or neither. A present but malformed
// file is always an error.
//
// Load does not validate the schema; call Validate separately. This
// split lets tests construct Configs in memory.
func Load(projectPath, globalPath string) (*Config, error) {
	global, err := readIfPresent(globalPath)
	if err != nil {
		return nil, fmt.Errorf("reading global config %q: %w", globalPath, err)
	}
	project, err := readIfPresent(projectPath)
	if err != nil {
		return nil, fmt.Errorf("reading project config %q: %w", projectPath, err)
	}
	return Merge(global, project), nil
}

// Merge returns a *Config that is the field-level merge of global
// (base) and project (override). Non-zero project fields win; project
// maps and slices replace global entries for matching keys.
//
// A nil input is treated as an empty Config.
//
// Known limitation (v1): Boolean fields (Sandbox, IncludeGitDiff,
// RespectGitignore) use zero-value checks. A project config setting
// false cannot override a global config setting true because Go
// cannot distinguish "unset" from "explicitly set to false". This
// will be addressed when config moves to explicit presence tracking.
func Merge(global, project *Config) *Config {
	out := &Config{}
	if global != nil {
		*out = *global
	}
	if project == nil {
		return out
	}
	// Scalar fields: project wins if set.
	if project.Version != 0 {
		out.Version = project.Version
	}
	if project.Persona != "" {
		out.Persona = project.Persona
	}
	// Nested struct: project wins wholesale unless it equals zero value.
	if !isZeroTools(project.Tools) {
		out.Tools = mergeTools(out.Tools, project.Tools)
	}
	// Providers: merge models map; other scalars project wins.
	out.Providers = mergeProvidersSection(out.Providers, project.Providers)
	if len(project.Personas) > 0 {
		if out.Personas == nil {
			out.Personas = map[string]string{}
		}
		for k, v := range project.Personas {
			out.Personas[k] = v
		}
	}
	if len(project.Skills) > 0 {
		if out.Skills == nil {
			out.Skills = map[string]SkillConfig{}
		}
		for k, v := range project.Skills {
			out.Skills[k] = v
		}
	}
	if len(project.MCPServers) > 0 {
		out.MCPServers = append([]MCPServerConfig(nil), project.MCPServers...)
	}
	if !isZeroContext(project.Context) {
		out.Context = mergeContext(out.Context, project.Context)
	}
	if project.Logging.Level != "" || project.Logging.SessionLogDir != "" {
		out.Logging = mergeLogging(out.Logging, project.Logging)
	}
	return out
}

// mergeProvidersSection merges the ProvidersConfig from project into
// base. Scalar fields (Default, Fallback) win if non-empty; the Models
// map merges key-by-key with project taking precedence.
func mergeProvidersSection(base, override ProvidersConfig) ProvidersConfig {
	out := base
	if override.Default != "" {
		out.Default = override.Default
	}
	if override.Fallback != "" {
		out.Fallback = override.Fallback
	}
	if len(override.Models) > 0 {
		if out.Models == nil {
			out.Models = map[string]ProviderConfig{}
		}
		for k, v := range override.Models {
			out.Models[k] = v
		}
	}
	return out
}

// Validate checks the merged config for the fields the daemon depends
// on at startup. Returns an error explaining the first violation found.
//
// Per the spec, the minimum required fields are version, providers.default,
// tools.approval, and tools.shell. As the daemon grows (ticket 04+), more
// fields will be enforced here.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config: nil")
	}
	if cfg.Version != 1 {
		return fmt.Errorf("config: unsupported version %d (want 1)", cfg.Version)
	}
	if cfg.Providers.Default == "" {
		return fmt.Errorf("config: providers.default is required")
	}
	// TODO(ticket-04): enforce tools.approval and tools.shell sub-fields
	// once the tool registry and shell executor are wired.
	return nil
}

// readIfPresent decodes a JSON file. Missing file returns (nil, nil);
// present-but-malformed returns (nil, error).
func readIfPresent(path string) (*Config, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func isZeroTools(t ToolsConfig) bool {
	return len(t.Approval) == 0 && isZeroShell(t.Shell) && t.MaxParallelTools == 0 &&
		t.UndoStackSize == 0 && len(t.Custom) == 0
}

func isZeroShell(s ShellConfig) bool {
	return len(s.AllowedCommands) == 0 && len(s.BlockedPatterns) == 0 && !s.Sandbox && s.TimeoutSec == 0
}

func isZeroContext(c ContextConfig) bool {
	return c.MaxTurnsBeforeHandoffHint == 0 && !c.IncludeGitDiff &&
		c.MaxFilesInContext == 0 && !c.RespectGitignore
}

func mergeTools(base, override ToolsConfig) ToolsConfig {
	out := base
	if len(override.Approval) > 0 {
		if out.Approval == nil {
			out.Approval = map[string]string{}
		}
		for k, v := range override.Approval {
			out.Approval[k] = v
		}
	}
	if !isZeroShell(override.Shell) {
		out.Shell = mergeShell(base.Shell, override.Shell)
	}
	if override.MaxParallelTools != 0 {
		out.MaxParallelTools = override.MaxParallelTools
	}
	if override.UndoStackSize != 0 {
		out.UndoStackSize = override.UndoStackSize
	}
	if len(override.Custom) > 0 {
		out.Custom = append([]CustomToolConfig(nil), override.Custom...)
	}
	return out
}

func mergeShell(base, override ShellConfig) ShellConfig {
	out := base
	if len(override.AllowedCommands) > 0 {
		out.AllowedCommands = append([]string(nil), override.AllowedCommands...)
	}
	if len(override.BlockedPatterns) > 0 {
		out.BlockedPatterns = append([]string(nil), override.BlockedPatterns...)
	}
	if override.Sandbox {
		out.Sandbox = true
	}
	if override.TimeoutSec != 0 {
		out.TimeoutSec = override.TimeoutSec
	}
	return out
}

func mergeContext(base, override ContextConfig) ContextConfig {
	out := base
	if override.MaxTurnsBeforeHandoffHint != 0 {
		out.MaxTurnsBeforeHandoffHint = override.MaxTurnsBeforeHandoffHint
	}
	if override.IncludeGitDiff {
		out.IncludeGitDiff = true
	}
	if override.MaxFilesInContext != 0 {
		out.MaxFilesInContext = override.MaxFilesInContext
	}
	if override.RespectGitignore {
		out.RespectGitignore = true
	}
	return out
}

func mergeLogging(base, override LoggingConfig) LoggingConfig {
	out := base
	if override.Level != "" {
		out.Level = override.Level
	}
	if override.SessionLogDir != "" {
		out.SessionLogDir = override.SessionLogDir
	}
	return out
}
