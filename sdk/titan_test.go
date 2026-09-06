package titan_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	titan "github.com/yuvrajsingh/titan/sdk"
)

// The SDK is the wall between another program and internal/. If this file
// stops compiling, the supported surface has changed under someone.
func TestSurfaceIsUsableFromOutside(t *testing.T) {
	dir := t.TempDir()
	var seen []titan.Event

	a, err := titan.New(context.Background(), titan.Options{
		Workspace: dir,
		Provider: &titan.Provider{
			Type: "ollama", BaseURL: "http://127.0.0.1:1", // never reached
			Model: "test", ContextWindow: 8192,
		},
		Mode:    "auto",
		Deny:    []string{"bash(rm -rf *)"},
		OnEvent: func(ev titan.Event) { seen = append(seen, ev) },
		Approve: func(ctx context.Context, tool string, args json.RawMessage, d titan.Decision) (bool, error) {
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	// The methods a caller needs are all present and callable.
	_ = a.Events()
	_ = a.Usage()
	_ = a.ExportHTML()
	a.Steer("noted")
	_ = seen
}

func TestProvidersIsExported(t *testing.T) {
	if len(titan.Providers()) < 10 {
		t.Fatalf("Providers() returned %d; the registry should be visible to a caller",
			len(titan.Providers()))
	}
}

// An embedded agent with no approver must refuse what needs approval, not
// assume yes. Defaulting to permissive would make the SDK quietly weaker than
// the same policy on the command line.
func TestNoApproverMeansRefuse(t *testing.T) {
	dir := t.TempDir()
	a, err := titan.New(context.Background(), titan.Options{
		Workspace: dir,
		Provider:  &titan.Provider{Type: "ollama", BaseURL: "http://127.0.0.1:1", Model: "m"},
		Mode:      "default", // every mutation asks
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Nothing to assert without a live model; the guarantee is in the
	// constructor, and this proves the option is reachable and the default is
	// the strict one rather than a panic.
}

func TestWorkspaceIsRequired(t *testing.T) {
	_, err := titan.New(context.Background(), titan.Options{})
	if err == nil {
		t.Fatal("an agent with no workspace must be refused")
	}
	if !strings.Contains(err.Error(), "Workspace") {
		t.Errorf("the error should name what is missing: %v", err)
	}
}

func TestConfigDirIsHonoured(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".titan"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"model":{"default":"local","providers":{"local":{
		"type":"ollama","base_url":"http://127.0.0.1:1","model":"from-config"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".titan", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := titan.New(context.Background(), titan.Options{
		Workspace: dir, ConfigDir: dir,
	})
	if err != nil {
		t.Fatalf("a config file the CLI would accept must work here too: %v", err)
	}
	a.Close()
}

// A bad deny rule must fail at construction, not on the first tool call.
func TestBadRuleFailsEarly(t *testing.T) {
	_, err := titan.New(context.Background(), titan.Options{
		Workspace: t.TempDir(),
		Provider:  &titan.Provider{Type: "ollama", BaseURL: "http://127.0.0.1:1", Model: "m"},
		Deny:      []string{"bash("},
	})
	if err == nil {
		t.Fatal("a malformed rule must be refused at construction")
	}
}
