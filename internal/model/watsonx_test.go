package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWatsonXBuildsRequest(t *testing.T) {
	w := NewWatsonX(WatsonXConfig{
		BaseURL: "https://x", APIKey: "k", SpaceID: "sp", ModelID: "openai/gpt-oss-120b"})

	req := w.buildRequest(Request{
		System:   "You are Abhed.",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools: []ToolDef{{Name: "glob", Description: "Find files.",
			InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 512,
	})

	// The system prompt is a separate field on Request, not a message.
	// Dropping it was silent: the model still answered, without any of the
	// working method or tool guidance it was supposed to have.
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
		t.Fatalf("system prompt not sent as the first message: %+v", req.Messages)
	}
	if req.Messages[0].Content != "You are Abhed." {
		t.Errorf("system content = %v", req.Messages[0].Content)
	}
	if req.SpaceID != "sp" || req.ProjectID != "" {
		t.Errorf("scope wrong: space=%q project=%q", req.SpaceID, req.ProjectID)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "glob" {
		t.Errorf("tools not carried: %+v", req.Tools)
	}
}

// watsonx rejects a tool_calls entry with no id, so a salvaged call — which
// never had one — must be given one rather than failing the turn.
func TestWatsonXSynthesisesMissingToolCallID(t *testing.T) {
	w := NewWatsonX(WatsonXConfig{BaseURL: "https://x", ModelID: "m", SpaceID: "s"})
	req := w.buildRequest(Request{Messages: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Name: "read",
			Args: json.RawMessage(`{"path":"/x"}`)}}},
	}})
	if len(req.Messages) != 1 || len(req.Messages[0].ToolCalls) != 1 {
		t.Fatalf("unexpected shape: %+v", req.Messages)
	}
	if req.Messages[0].ToolCalls[0].ID == "" {
		t.Error("a tool call went out with no id; watsonx returns 400")
	}
}

// gpt-oss-120b sometimes ends its reasoning with the tool ARGUMENTS and never
// emits a tool_calls delta — leaving the loop nothing to dispatch and the turn
// silently empty. Observed live with Abhed's real system prompt.
func TestSalvageBareArgsFromReasoning(t *testing.T) {
	tools := []ToolDef{
		{Name: "read", InputSchema: json.RawMessage(
			`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		{Name: "glob", InputSchema: json.RawMessage(
			`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`)},
	}
	reasoning := `We need to look at the file. Let's read it.{"path":"store/store.go"}`

	tc, found := salvageToolCall(reasoning, tools)
	if !found {
		t.Fatal("did not recover arguments left in the reasoning channel")
	}
	if tc.Name != "read" {
		t.Errorf("matched %q, want read — the schema identifies it", tc.Name)
	}
	if tc.ID == "" {
		t.Error("salvaged call has no id; watsonx would reject the next turn")
	}
}

// Ambiguity must NOT be resolved by guessing: inventing a call the model did
// not make is worse than the stall it replaces.
func TestSalvageBareArgsRefusesAmbiguity(t *testing.T) {
	tools := []ToolDef{
		{Name: "a", InputSchema: json.RawMessage(
			`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)},
		{Name: "b", InputSchema: json.RawMessage(
			`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)},
	}
	if _, found := salvageToolCall(`thinking...{"x":"1"}`, tools); found {
		t.Error("guessed between two tools that both fit the arguments")
	}
}

func TestSalvageBareArgsIgnoresUnrelatedJSON(t *testing.T) {
	tools := []ToolDef{{Name: "read", InputSchema: json.RawMessage(
		`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}}
	for _, text := range []string{
		`the config looked like {"timeout":30}`, // no matching schema
		`{"path":"/x","extra":"y"}`,             // key not in the schema
		`no json at all`,
	} {
		if _, found := salvageToolCall(text, tools); found {
			t.Errorf("invented a call from %q", text)
		}
	}
}

func TestWatsonXAuthErrorIsActionable(t *testing.T) {
	msg := wxErrorMessage([]byte(`{"errors":[{"code":"invalid_request_entity","message":"bad field"}]}`))
	if !strings.Contains(msg, "invalid_request_entity") || !strings.Contains(msg, "bad field") {
		t.Errorf("error message lost detail: %q", msg)
	}
}
