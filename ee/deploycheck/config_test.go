package deploycheck

// Package deploycheck asserts that the configuration actually deployed to
// abhed.zybuu.com is safe for people who are not the operator.
//
// The console issues invites to strangers, so its config is not a preference
// file any more: it is the boundary. These tests read the shipped config and
// fail when that boundary weakens.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zybuu-ai/abhed/internal/policy"
)

func TestDeployedDenyRulesMatchRealPaths(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config.json"))
	if err != nil {
		t.Fatalf("deploy/config.json unreadable, so nothing was checked: %v", err)
	}
	var cfg struct {
		Permissions struct {
			Deny  []string `json:"deny"`
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Sandbox struct {
			MinTier string `json:"min_tier"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("deploy/config.json does not parse: %v", err)
	}

	// The sandbox is the boundary that makes everything else survivable.
	// "none" runs the agent on the host as the operating user, which is
	// correct for a laptop you drive yourself and wrong for a console
	// strangers can sign in to.
	if cfg.Sandbox.MinTier == "none" || cfg.Sandbox.MinTier == "" {
		t.Errorf("deployed sandbox min_tier is %q — an invited user's agent "+
			"would run shell commands on the host with no isolation",
			cfg.Sandbox.MinTier)
	}

	// An allow rule runs with no approval at all, so an allow rule that can
	// read an arbitrary file is a credential-disclosure primitive no deny
	// list can contain: the interpreter reads the file, not the tool.
	for _, a := range cfg.Permissions.Allow {
		for _, banned := range []string{"python", "perl", "ruby", "node",
			"cat ", "head", "tail", "less", "more", "xxd", "od ", "strings",
			"base64", "curl", "wget", "nc ", "ssh", "scp", "eval"} {
			if strings.Contains(strings.ToLower(a), banned) {
				t.Errorf("allow rule %q pre-approves %q, which can read any "+
					"file the process can — deny rules cannot contain it",
					a, banned)
			}
		}
	}

	deny := cfg.Permissions.Deny
	var rules []policy.Rule
	for _, d := range deny {
		r, err := policy.ParseRule(d)
		if err != nil {
			t.Fatalf("rule %q does not parse: %v", d, err)
		}
		rules = append(rules, r)
	}
	blocked := func(path string) bool {
		for _, r := range rules {
			if r.Matches("read", path) {
				return true
			}
		}
		return false
	}
	for _, p := range []string{
		"/Users/yuvrajsingh/.ssh/id_rsa",
		"/Users/yuvrajsingh/.ssh/config",
		"/Users/yuvrajsingh/.aws/credentials",
		"/Users/yuvrajsingh/.config/gcloud/credentials.db",
		"/Users/yuvrajsingh/.kube/config",
		"/workspace/.env",
		"/workspace/.env.production",
		"/Users/yuvrajsingh/id_rsa",
		"/tmp/server.pem",
		"/Users/yuvrajsingh/.git-credentials",
		"/Users/yuvrajsingh/.netrc",
		"/Users/yuvrajsingh/Library/Keychains/login.keychain-db",
		"/Users/yuvrajsingh/titan/.abhed/config.json",
	} {
		if !blocked(p) {
			t.Errorf("NOT BLOCKED: %s", p)
		}
	}
	for _, p := range []string{
		"/workspace/main.go", "/workspace/README.md", "/workspace/src/app.py",
	} {
		if blocked(p) {
			t.Errorf("wrongly blocked ordinary file: %s", p)
		}
	}
}
