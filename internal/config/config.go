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
	// AdditionalDirs are directories the agent may reach beyond the workspace
	// root it was started in. Set by the operator — from config or --add-dir —
	// and never by the model: see tools.Session.Roots for why the boundary
	// itself is not negotiable.
	AdditionalDirs []string `json:"additional_dirs,omitempty"`

	Model       ModelConfig       `json:"model"`
	Permissions PermissionsConfig `json:"permissions"`
	Context     ContextConfig     `json:"context"`
	RAG         RAGConfig         `json:"rag,omitempty"`
	Skills      SkillsConfig      `json:"skills,omitempty"`
	K8s         K8sConfig         `json:"k8s,omitempty"`
	SSH         SSHConfig         `json:"ssh,omitempty"`
	Limits      LimitsConfig      `json:"limits"`
	Sandbox     SandboxConfig     `json:"sandbox"`
	MCP         MCPConfig         `json:"mcp"`
	Retrieval   RetrievalConfig   `json:"retrieval"`
	WebSearch   WebSearchConfig   `json:"web_search"`
	Storage     StorageConfig     `json:"storage"`
	Auth        AuthConfig        `json:"auth"`

	// Managed is set when the config came from the org-managed path.
	Managed bool `json:"-"`
}

type ModelConfig struct {
	Default   string                    `json:"default"`
	Providers map[string]ProviderConfig `json:"providers"`
}

type ProviderConfig struct {
	// Type is "openai-compatible" (vLLM, Ollama, OpenAI) or "watsonx".
	Type            string   `json:"type"`
	BaseURL         string   `json:"base_url"`
	Model           string   `json:"model"`
	APIKey          string   `json:"api_key,omitempty"`
	APIKeyEnv       string   `json:"api_key_env,omitempty"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	ToolCallFormat  string   `json:"tool_call_format,omitempty"`
	ReasoningTags   []string `json:"reasoning_tags,omitempty"`
	// Think turns a hybrid-reasoning model's thinking phase on or off.
	// Omit to leave the server's default alone. Ollama reads this; an
	// OpenAI-style server uses reasoning_effort instead.
	Think *bool `json:"think,omitempty"`

	// watsonx only. A deployment is scoped by EITHER a project or a space;
	// sending both is rejected by the API.
	ProjectID string `json:"project_id,omitempty"`
	SpaceID   string `json:"space_id,omitempty"`
	// APIVersion is watsonx's date-versioned API. Defaults to 2024-05-01.
	APIVersion string `json:"api_version,omitempty"`
	// IAMURL overrides the token endpoint, for CPD which mints its own.
	IAMURL string `json:"iam_url,omitempty"`
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

// AuthConfig controls how callers are identified. Default is "none", which is
// single-tenant local development. Production should use "oidc"; "proxy" is
// only safe when a trusted proxy is the sole route to the port.
type AuthConfig struct {
	// Mode is none | local | proxy | oidc.
	//   local — Titan holds the accounts: email and password, no external IdP
	//   oidc  — delegate to an identity provider, including Google/Microsoft
	Mode string `json:"mode"`
	// Provider fills in issuer, scopes and tenant claim for a known IdP:
	// "google" for Gmail, "microsoft" for Outlook.
	Provider string `json:"provider,omitempty"`
	// AllowSignup lets anyone create a local account. Off by default: an
	// open signup on an internal tool is rarely what an operator intends.
	AllowSignup bool `json:"allow_signup,omitempty"`
	// DefaultTenant is assigned to accounts created without one.
	DefaultTenant string `json:"default_tenant,omitempty"`
	Issuer        string `json:"issuer,omitempty"`
	Audience      string `json:"audience,omitempty"`
	JWKSURL       string `json:"jwks_url,omitempty"`
	TenantClaim   string `json:"tenant_claim,omitempty"`
	GroupsClaim   string `json:"groups_claim,omitempty"`
	// RequireGroup gates all access on membership, above tenancy.
	RequireGroup string `json:"require_group,omitempty"`

	// Browser sign-in. Without these, OIDC still validates bearer tokens for
	// API clients, but a person opening the console has no way to sign in.
	ClientID        string `json:"client_id,omitempty"`
	ClientSecret    string `json:"client_secret,omitempty"`
	ClientSecretEnv string `json:"client_secret_env,omitempty"`
	RedirectURL     string `json:"redirect_url,omitempty"`
	// PostLogoutURL is where the IdP returns after ending its own session.
	// Providers require it to be pre-registered, and ignore RP-initiated
	// logout without it — which leaves the user still signed in.
	PostLogoutURL string   `json:"post_logout_redirect_url,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	// CookieSecure should be true anywhere but local HTTP development.
	CookieSecure bool `json:"cookie_secure,omitempty"`
	SessionHours int  `json:"session_hours,omitempty"`
}

// WebSearchConfig controls the agent's access to the public web.
//
// OFF by default: Titan is built to run air-gapped, and this is the one tool
// that deliberately crosses the boundary. Enabling it is a decision an operator
// makes, not a default they inherit.
type WebSearchConfig struct {
	Enabled bool `json:"enabled"`
	// Provider: duckduckgo (free, no key, the default) | brave | tavily |
	// serper | searxng (self-hosted).
	Provider string `json:"provider,omitempty"`
	// APIKeyEnv names the environment variable holding the key, so a
	// credential never sits in a config file.
	APIKeyEnv string `json:"api_key_env,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	// BaseURL points at a self-hosted instance or an egress broker.
	BaseURL    string `json:"base_url,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

// StorageConfig selects the event store. Memory is fine for a CLI session;
// audit and replay across restarts need Postgres (docs §10).
type StorageConfig struct {
	// Driver is "memory" or "postgres".
	Driver string `json:"driver"`
	// DSN may also come from TITAN_DATABASE_URL, so a deployment need not put
	// a credential in a config file.
	DSN string `json:"dsn,omitempty"`
	// Tenant scopes every row; row-level security enforces it.
	Tenant   string `json:"tenant,omitempty"`
	MaxConns int    `json:"max_conns,omitempty"`
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

// SkillsConfig points at directories holding skills.
//
// Deliberately NOT the workspace: a skill body is instructions by
// construction, so reading them from the repository the agent is editing would
// let any cloned project carry its own orders to the agent reading it. Skills
// come from where the operator put them.
type SkillsConfig struct {
	// Dirs each contain one directory per skill, holding a SKILL.md.
	// Defaults to ~/.titan/skills when unset.
	Dirs []string `json:"dirs,omitempty"`
	// Disabled turns skills off entirely, including the default directory.
	Disabled bool `json:"disabled,omitempty"`
}

// K8sConfig enables cluster access. Off by default: reaching a cluster is an
// authorization decision, and the credentials already on the machine are not
// a reason to hand them to an agent without being asked.
type K8sConfig struct {
	Enabled bool `json:"enabled"`
	// Kubeconfig path; empty uses $KUBECONFIG then ~/.kube/config.
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// Context pins which cluster is the default. Empty uses current-context.
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// AllowWrites exposes k8s_apply. Even then every call needs approval;
	// this decides whether the capability exists at all.
	AllowWrites bool `json:"allow_writes,omitempty"`
}

// SSHConfig declares reachable machines. The agent can only name a host from
// this list, so the operator decides the blast radius, not the model.
type SSHConfig struct {
	Enabled bool            `json:"enabled"`
	Hosts   []SSHHostConfig `json:"hosts,omitempty"`
}

type SSHHostConfig struct {
	Name         string `json:"name"`
	Addr         string `json:"addr"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file,omitempty"`
	// PasswordEnv names an environment variable, never the password itself.
	PasswordEnv              string `json:"password_env,omitempty"`
	KnownHostsFile           string `json:"known_hosts_file,omitempty"`
	InsecureSkipHostKeyCheck bool   `json:"insecure_skip_host_key_check,omitempty"`
}

// RAGConfig registers external retrieval corpora. Each becomes a rag_<name>
// tool. Empty by default: reaching a corpus is a network egress and an
// authorization decision, not something a default should make.
type RAGConfig struct {
	Corpora []RAGCorpusConfig `json:"corpora,omitempty"`
}

type RAGCorpusConfig struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	Method      string `json:"method,omitempty"`

	Headers    map[string]string `json:"headers,omitempty"`
	HeadersEnv map[string]string `json:"headers_env,omitempty"`

	QueryField string         `json:"query_field,omitempty"`
	QueryParam string         `json:"query_param,omitempty"`
	TopKField  string         `json:"top_k_field,omitempty"`
	TopK       int            `json:"top_k,omitempty"`
	Body       map[string]any `json:"body,omitempty"`

	ResultsPath string `json:"results_path,omitempty"`
	TextField   string `json:"text_field,omitempty"`
	SourceField string `json:"source_field,omitempty"`
	TitleField  string `json:"title_field,omitempty"`
	ScoreField  string `json:"score_field,omitempty"`

	Enabled bool `json:"enabled"`
}

// MCPConfig registers Model Context Protocol servers. A server not listed here
// does not run: discovery does not imply trust (docs §03 T4).
type MCPConfig struct {
	Servers []MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	Name string `json:"name"`
	// Command spawns the server locally; URL reaches one that already runs.
	// Exactly one of the two.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
	// Headers are sent on every request to a URL server. Prefer headers_env
	// for anything secret: it names an environment variable to read instead
	// of putting the credential in a file the agent itself can read.
	Headers    map[string]string `json:"headers,omitempty"`
	HeadersEnv map[string]string `json:"headers_env,omitempty"`
	Enabled    bool              `json:"enabled"`
	AllowTools []string          `json:"allow_tools,omitempty"`
	Digest     string            `json:"digest,omitempty"`
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
					Type:    "openai-compatible",
					BaseURL: "http://localhost:11434/v1", // Ollama's default
					// gemma4:26b, chosen by measurement rather than benchmark
					// (docs/ops/model-selection.md). It is MoE — 128 experts,
					// 8 active — so it decodes at ~35 tok/s on an M3 Pro where
					// a dense 27B manages 3.
					//
					// The reason it beats the faster qwen3-coder:30b is not
					// speed but honesty: on a planted two-bug review task it
					// found both, verified its own work, and reported the real
					// command output. qwen3-coder fixed one, misidentified the
					// other, and twice claimed environment restrictions that
					// did not exist. A model that misreports whether it
					// verified something is the worst failure mode in an
					// autonomous agent, because every later decision inherits
					// the false premise.
					Model:         "gemma4:26b",
					ContextWindow: 65536,
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
		Storage: StorageConfig{Driver: "memory", Tenant: "default", MaxConns: 10},
		Auth:    AuthConfig{Mode: "none"},
		Sandbox: SandboxConfig{
			MinTier:      "process",
			AllowNetwork: false,
			MaxMemoryMB:  4096,
			MaxProcs:     512,
		},
		// Off by default: Titan runs air-gapped, and web search is the one tool
		// that deliberately crosses the boundary.
		WebSearch: WebSearchConfig{Enabled: false, Provider: "duckduckgo", MaxResults: 5},
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

	if v := os.Getenv("TITAN_DATABASE_URL"); v != "" {
		cfg.Storage.DSN = v
		if cfg.Storage.Driver == "" || cfg.Storage.Driver == "memory" {
			cfg.Storage.Driver = "postgres"
		}
	}
}

// Provider returns the active provider with its API key resolved.
// ValidateProvider checks a provider's settings are internally consistent, so
// a misconfiguration is a startup error rather than a failed request.
func ValidateProvider(p ProviderConfig) error {
	if p.Type != "watsonx" {
		return nil
	}
	if p.BaseURL == "" {
		return fmt.Errorf("watsonx needs base_url (e.g. https://us-south.ml.cloud.ibm.com)")
	}
	if p.Model == "" {
		return fmt.Errorf("watsonx needs model (e.g. openai/gpt-oss-120b)")
	}
	if p.APIKey == "" && p.APIKeyEnv == "" {
		return fmt.Errorf("watsonx needs api_key_env naming the variable holding the key")
	}
	if p.ProjectID == "" && p.SpaceID == "" {
		return fmt.Errorf("watsonx needs either project_id or space_id")
	}
	if p.ProjectID != "" && p.SpaceID != "" {
		return fmt.Errorf("watsonx takes project_id OR space_id, not both — " +
			"the API rejects a request carrying each")
	}
	return nil
}

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
	if err := ValidateProvider(p); err != nil {
		return p, fmt.Errorf("model %q: %w", c.Model.Default, err)
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
	switch c.Auth.Mode {
	case "none", "proxy", "oidc", "local", "":
	default:
		return fmt.Errorf("unknown auth.mode %q (want none, local, proxy or oidc)", c.Auth.Mode)
	}
	if c.Auth.Mode == "oidc" && c.Auth.Issuer == "" && c.Auth.Provider == "" {
		return fmt.Errorf("auth.mode is oidc but neither auth.issuer nor " +
			"auth.provider (google, microsoft) is set")
	}
	switch strings.ToLower(c.WebSearch.Provider) {
	case "", "duckduckgo", "ddg", "brave", "tavily", "serper", "searxng":
	default:
		return fmt.Errorf("unknown web_search.provider %q "+
			"(want duckduckgo, brave, tavily, serper or searxng)", c.WebSearch.Provider)
	}
	switch c.Storage.Driver {
	case "memory", "postgres", "":
	default:
		return fmt.Errorf("unknown storage.driver %q (want memory or postgres)", c.Storage.Driver)
	}
	if c.Storage.Driver == "postgres" && c.Storage.DSN == "" && os.Getenv("TITAN_DATABASE_URL") == "" {
		return fmt.Errorf("storage.driver is postgres but no DSN is set (use storage.dsn or TITAN_DATABASE_URL)")
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
