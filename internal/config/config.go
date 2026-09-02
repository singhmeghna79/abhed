// Package config loads Titan's layered configuration.
//
// Precedence, lowest to highest: built-in defaults, user config, project
// config, environment, flags — except managed config (/etc/titan), which
// always wins so local settings cannot escalate past org policy (docs P7).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Model       ModelConfig       `json:"model"`
	Permissions PermissionsConfig `json:"permissions"`
	Context     ContextConfig     `json:"context"`
	Limits      LimitsConfig      `json:"limits"`
	Sandbox     SandboxConfig     `json:"sandbox"`
	MCP         MCPConfig         `json:"mcp"`
	Retrieval   RetrievalConfig   `json:"retrieval"`

	// Managed is set when the config came from the org-managed path.
	Managed bool `json:"-"`
}

type ModelConfig struct {
	Default   string                    `json:"default"`
	Providers map[string]ProviderConfig `json:"providers"`
}

type ProviderConfig struct {
	Type            string   `json:"type"` // openai-compatible
	BaseURL         string   `json:"base_url"`
	Model           string   `json:"model"`
	APIKey          string   `json:"api_key,omitempty"`
	APIKeyEnv       string   `json:"api_key_env,omitempty"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	ToolCallFormat  string   `json:"tool_call_format,omitempty"`
	ReasoningTags   []string `json:"reasoning_tags,omitempty"`
}

type PermissionsConfig struct {
	Mode  string   `json:"mode"`
	Deny  []string `json:"deny"`
	Ask   []string `json:"ask"`
	Allow []string `json:"allow"`
}

type ContextConfig struct {
	CompactAt   float64  `json:"compact_at"`
	MemoryFiles []string `json:"memory_files"`
}

// RetrievalConfig controls the on-prem index. Retrieval is an accelerator over
// agentic grep, not a replacement: the evidence favouring one over the other is
// contested, so Titan builds both and measures (docs §02 §4).
type RetrievalConfig struct {
	// Enabled builds an index at startup and exposes the search tool.
	Enabled bool `json:"enabled"`
	// Embed adds the vector tier. Off by default - the symbol and BM25 tiers
	// answer most code questions without the cost of embedding a monorepo.
	Embed        bool   `json:"embed"`
	EmbedBaseURL string `json:"embed_base_url,omitempty"`
	EmbedModel   string `json:"embed_model,omitempty"`
	EmbedDims    int    `json:"embed_dims,omitempty"`
}

// MCPConfig registers Model Context Protocol servers. A server not listed here
// does not run: discovery does not imply trust (docs §03 T4).
type MCPConfig struct {
	Servers []MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Env        []string `json:"env,omitempty"`
	Enabled    bool     `json:"enabled"`
	AllowTools []string `json:"allow_tools,omitempty"`
	Digest     string   `json:"digest,omitempty"`
}

// SandboxConfig controls execution isolation. Defaults deny egress, because a
// successful prompt injection then has no channel to exfiltrate through.
type SandboxConfig struct {
	MinTier       string   `json:"min_tier"` // none|process|container|vm
	AllowNetwork  bool     `json:"allow_network"`
	ReadOnlyPaths []string `json:"read_only_paths,omitempty"`
	MaxMemoryMB   int      `json:"max_memory_mb"`
	MaxProcs      int      `json:"max_procs"`
}

type LimitsConfig struct {
	MaxTurns  int `json:"max_turns"`
	MaxTokens int `json:"max_tokens"`
	// MaxBudgetTokens caps total spend across a session AND its subagents.
	// Zero means unlimited.
	MaxBudgetTokens int  `json:"max_budget_tokens"`
	MaxSubagents    int  `json:"max_subagents"`
	NestedSubagents bool `json:"nested_subagents"`
}

func Default() Config {
	return Config{
		Model: ModelConfig{
			Default: "local",
			Providers: map[string]ProviderConfig{
				"local": {
					Type:          "openai-compatible",
					BaseURL:       "http://localhost:11434/v1", // Ollama's default
					Model:         "qwen2.5-coder:7b",
					ContextWindow: 32768,
				},
			},
		},
		Permissions: PermissionsConfig{
			Mode: "default",
			// Commands with no undo. Denied outright rather than merely asked,
			// because a tired user clicks yes.
			Deny: []string{
				"bash(rm -rf /*)",
				"bash(*mkfs*)",
				"bash(*shutdown*)",
				"write(/etc/**)",
			},
			Allow: []string{
				"bash(git status*)", "bash(git diff*)", "bash(git log*)",
				"bash(ls*)", "bash(pwd)", "bash(cat *)",
			},
		},
		Context: ContextConfig{
			CompactAt:   0.90,
			MemoryFiles: []string{"TITAN.md", "TITAN.local.md"},
		},
		Limits: LimitsConfig{
			MaxTurns: 100, MaxTokens: 8192, MaxBudgetTokens: 0,
			MaxSubagents: 20, NestedSubagents: false,
		},
		Sandbox: SandboxConfig{
			MinTier:      "process",
			AllowNetwork: false,
			MaxMemoryMB:  4096,
			MaxProcs:     512,
		},
	}
}

// Load assembles configuration from all sources in precedence order.
func Load(workspace string) (Config, error) {
	cfg := Default()

	if home, err := os.UserHomeDir(); err == nil {
		if err := mergeFile(&cfg, filepath.Join(home, ".titan", "config.json")); err != nil {
			return cfg, err
		}
	}
	if err := mergeFile(&cfg, filepath.Join(workspace, ".titan", "config.json")); err != nil {
		return cfg, err
	}

	// Managed config is applied last and marks the engine as org-controlled.
	managed := filepath.Join("/etc", "titan", "config.json")
	if _, err := os.Stat(managed); err == nil {
		if err := mergeFile(&cfg, managed); err != nil {
			return cfg, err
		}
		cfg.Managed = true
	}

	applyEnv(&cfg)
	return cfg, cfg.Validate()
}

func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	// Unmarshalling onto the existing struct merges: fields absent from the
	// file keep their current value, and lists are replaced wholesale.
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// applyEnv lets a deployment override the endpoint without editing files,
// which is what container and CI environments need.
func applyEnv(cfg *Config) {
	name := cfg.Model.Default
	p, found := cfg.Model.Providers[name]
	if !found {
		return
	}
	if v := os.Getenv("TITAN_BASE_URL"); v != "" {
		p.BaseURL = v
	}
	if v := os.Getenv("TITAN_MODEL"); v != "" {
		p.Model = v
	}
	if v := os.Getenv("TITAN_API_KEY"); v != "" {
		p.APIKey = v
	}
	cfg.Model.Providers[name] = p
}

// Provider returns the active provider with its API key resolved.
func (c Config) Provider() (ProviderConfig, error) {
	p, found := c.Model.Providers[c.Model.Default]
	if !found {
		return ProviderConfig{}, fmt.Errorf(
			"model %q is not defined. Available: %s",
			c.Model.Default, strings.Join(providerNames(c.Model.Providers), ", "))
	}
	if p.APIKey == "" && p.APIKeyEnv != "" {
		p.APIKey = os.Getenv(p.APIKeyEnv)
	}
	return p, nil
}

func providerNames(m map[string]ProviderConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (c Config) Validate() error {
	p, err := c.Provider()
	if err != nil {
		return err
	}
	if p.BaseURL == "" {
		return fmt.Errorf("provider %q has no base_url", c.Model.Default)
	}
	if p.Model == "" {
		return fmt.Errorf("provider %q has no model", c.Model.Default)
	}
	switch c.Permissions.Mode {
	case "default", "accept-edits", "plan", "auto", "bypass", "":
	default:
		return fmt.Errorf("unknown permission mode %q", c.Permissions.Mode)
	}
	if c.Context.CompactAt <= 0 || c.Context.CompactAt > 1 {
		return fmt.Errorf("context.compact_at must be between 0 and 1, got %v", c.Context.CompactAt)
	}
	switch c.Sandbox.MinTier {
	case "none", "process", "container", "vm", "":
	default:
		return fmt.Errorf("unknown sandbox.min_tier %q (want none|process|container|vm)", c.Sandbox.MinTier)
	}
	return nil
}

// WriteDefault creates a starter config, so `titan init` produces something
// the user can edit rather than a blank file.
func WriteDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
