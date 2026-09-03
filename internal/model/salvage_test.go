package model

import (
	"encoding/json"
	"testing"
)

func tools() []ToolDef {
	return []ToolDef{
		{Name: "glob", InputSchema: json.RawMessage(`{}`)},
		{Name: "read", InputSchema: json.RawMessage(`{}`)},
	}
}

// The exact text qwen3-coder:30b emitted under Titan's system prompt, which
// ended the session after one turn with no work done.
func TestSalvageQwenFunctionSyntax(t *testing.T) {
	text := "I'll review this codebase.\n\n<function=glob>\n<parameter=pattern>\n**\n</parameter>\n</function>\n</tool_call>"
	tc, found := salvageToolCall(text, tools())
	if !found {
		t.Fatal("did not recover a tool call the model wrote as prose")
	}
	if tc.Name != "glob" {
		t.Errorf("name = %q, want glob", tc.Name)
	}
	var args map[string]string
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("arguments are not valid JSON: %v", err)
	}
	if args["pattern"] != "**" {
		t.Errorf("pattern = %q, want **", args["pattern"])
	}
}

func TestSalvageToolCallTagSyntax(t *testing.T) {
	text := `<tool_call>{"name":"read","arguments":{"path":"/tmp/x.go"}}</tool_call>`
	tc, found := salvageToolCall(text, tools())
	if !found {
		t.Fatal("did not recover the <tool_call> form")
	}
	if tc.Name != "read" {
		t.Errorf("name = %q, want read", tc.Name)
	}
}

// The guard that matters: a tool the request never offered must never be
// invented, however convincing the text looks. Salvage that fabricates calls
// is worse than the stall it replaces.
func TestSalvageRejectsUnknownTool(t *testing.T) {
	text := "<function=rm_rf>\n<parameter=path>\n/\n</parameter>\n</function>"
	if _, found := salvageToolCall(text, tools()); found {
		t.Error("recovered a call to a tool that was never offered")
	}
}

func TestSalvageIgnoresOrdinaryProse(t *testing.T) {
	for _, text := range []string{
		"I will use the glob tool to list files.",
		"The read function takes a path parameter.",
		"",
	} {
		if _, found := salvageToolCall(text, tools()); found {
			t.Errorf("invented a tool call from prose: %q", text)
		}
	}
}

func TestSalvageNeedsOfferedTools(t *testing.T) {
	text := "<function=glob>\n<parameter=pattern>\n**\n</parameter>\n</function>"
	if _, found := salvageToolCall(text, nil); found {
		t.Error("recovered a call when the request offered no tools at all")
	}
}

func TestStripSalvagedRemovesTheBlock(t *testing.T) {
	text := "Let me look.\n<function=glob>\n<parameter=pattern>\n**\n</parameter>\n</function>\n</tool_call>"
	got := stripSalvaged(text)
	if got != "Let me look." {
		t.Errorf("stripSalvaged() = %q, want %q", got, "Let me look.")
	}
}
