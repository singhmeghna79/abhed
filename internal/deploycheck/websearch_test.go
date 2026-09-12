package deploycheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Web search and shell networking are different capabilities and must stay
// that way. The shell gets no network at all, so a compromised session cannot
// fetch a payload or exfiltrate what it found. Web search reaches the internet
// through a Go tool in the server process instead: one narrow call, logged as
// an event, with no way to turn it into an arbitrary request.
//
// Hardening the sandbox must not quietly remove the product's ability to
// search, and enabling search must not quietly give the shell a network.
func TestWebSearchSurvivesSandboxHardening(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config.json"))
	if err != nil {
		t.Fatalf("deploy/config.json unreadable: %v", err)
	}
	var cfg struct {
		WebSearch struct {
			Enabled  bool   `json:"enabled"`
			Provider string `json:"provider"`
		} `json:"web_search"`
		Sandbox struct {
			AllowNetwork bool   `json:"allow_network"`
			MinTier      string `json:"min_tier"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("config does not parse: %v", err)
	}

	if !cfg.WebSearch.Enabled {
		t.Error("web_search is disabled on the deployed config — Titan Chat " +
			"loses the ability to search, which is a product capability and " +
			"not something sandbox hardening should have taken away")
	}
	if cfg.WebSearch.Provider == "" {
		t.Error("web_search is enabled with no provider, so it cannot answer")
	}

	// The other half of the contract. If this ever flips true, the shell can
	// reach the internet and the search tool stops being the only egress.
	if cfg.Sandbox.AllowNetwork {
		t.Error("sandbox.allow_network is true — the shell can now reach the " +
			"network directly, which makes every deny rule bypassable by curl")
	}
}
