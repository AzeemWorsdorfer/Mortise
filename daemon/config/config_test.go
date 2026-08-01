package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.json"), filepath.Join(dir, "also-nope.json"))
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load: returned nil config without error")
	}
	if cfg.Version != 0 {
		t.Errorf("Version: want 0 (unset), got %d", cfg.Version)
	}
}

func TestLoad_ProjectOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".mortise.json")
	writeJSON(t, projectPath, `{"version":1,"persona":"concise"}`)

	cfg, err := Load(projectPath, filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona != "concise" {
		t.Errorf("Persona: want %q, got %q", "concise", cfg.Persona)
	}
	if cfg.Version != 1 {
		t.Errorf("Version: want 1, got %d", cfg.Version)
	}
}

func TestLoad_GlobalOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	globalPath := filepath.Join(dir, "config.json")
	writeJSON(t, globalPath, `{"version":1,"persona":"architect"}`)

	cfg, err := Load(filepath.Join(dir, "missing.json"), globalPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Persona != "architect" {
		t.Errorf("Persona: want %q, got %q", "architect", cfg.Persona)
	}
}

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".mortise.json")
	globalPath := filepath.Join(dir, "config.json")
	writeJSON(t, projectPath, `{"version":1,"persona":"concise","providers":{"default":"openai","models":{"openai":{"model":"gpt-4o"}}}}`)
	writeJSON(t, globalPath, `{"version":1,"persona":"architect","providers":{"default":"anthropic","models":{"anthropic":{"model":"claude-sonnet-4-20250514"},"openai":{"model":"gpt-4o-mini"}}}}`)

	cfg, err := Load(projectPath, globalPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Persona != "concise" {
		t.Errorf("Persona: project should win, want %q got %q", "concise", cfg.Persona)
	}
	if cfg.Providers.Default != "openai" {
		t.Errorf("Providers.Default: project should win, want %q got %q", "openai", cfg.Providers.Default)
	}
	if got := cfg.Providers.Models["openai"].Model; got != "gpt-4o" {
		t.Errorf("openai model: project should win, want %q got %q", "gpt-4o", got)
	}
	if got := cfg.Providers.Models["anthropic"].Model; got != "claude-sonnet-4-20250514" {
		t.Errorf("anthropic model: should inherit from global, want %q got %q", "claude-sonnet-4-20250514", got)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".mortise.json")
	if err := os.WriteFile(projectPath, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(projectPath, ""); err == nil {
		t.Fatal("Load: want error for malformed JSON, got nil")
	}
}

func TestValidate_MissingVersion(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate: want error for missing version, got nil")
	}
}

func TestValidate_UnsupportedVersion(t *testing.T) {
	t.Parallel()

	cfg := &Config{Version: 2}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate: want error for version 2, got nil")
	}
}

func TestValidate_Version1(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Version: 1,
		Providers: ProvidersConfig{
			Default: "openai",
		},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: unexpected error: %v", err)
	}
}

func TestValidate_MissingProviderDefault(t *testing.T) {
	t.Parallel()

	cfg := &Config{Version: 1}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate: want error for missing providers.default, got nil")
	}
}

func TestMerge_NilSafe(t *testing.T) {
	t.Parallel()

	got := Merge(nil, nil)
	if got == nil {
		t.Fatal("Merge: want non-nil result for nil inputs")
	}
	if got.Version != 0 {
		t.Errorf("Version: want 0, got %d", got.Version)
	}
}

// writeJSON is a small test helper to write a string and fail on error.
func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
