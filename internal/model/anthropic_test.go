package model

import (
	"encoding/json"
	"testing"
)

func TestAnthropicRequestShape(t *testing.T) {
	a := NewAnthropic("", "k", "claude-opus-5", Profile{})
	req := Request{
		System: "you are titan",
		Messages: []Message{
			{Role: RoleUser, Content: "read the file"},
			{Role: RoleAssistant, Content: "sure", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read", Args: json.RawMessage(`{"path":"a.go"}`)},
			}},
			{Role: RoleTool, ToolCallID: "call_1", Content: "package main"},
		},
		Tools:  []ToolDef{{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Params: Params{Temperature: ptr(0.3), MaxTokens: 1024},
	}
	got := a.buildRequest(req)

	// The system prompt is a top-level field, not a message, and it carries the
	// cache marker that makes the stable prefix a cache read.
	if len(got.System) != 1 || got.System[0].Text != "you are titan" {
		t.Fatalf("system = %+v", got.System)
	}
	if got.System[0].CacheControl == nil || got.System[0].CacheControl.Type != "ephemeral" {
		t.Error("the system block must carry cache_control, or the prefix is re-billed every turn")
	}

	if len(got.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(got.Messages))
	}
	// A tool result is a user message carrying a tool_result block.
	last := got.Messages[2]
	if last.Role != "user" || last.Content[0].Type != "tool_result" {
		t.Errorf("tool result shape = %s/%s", last.Role, last.Content[0].Type)
	}
	if last.Content[0].ToolUseID != "call_1" {
		t.Errorf("tool_use_id = %q, want call_1", last.Content[0].ToolUseID)
	}
	// The assistant turn carries text and tool_use as separate blocks.
	asst := got.Messages[1]
	if len(asst.Content) != 2 || asst.Content[1].Type != "tool_use" {
		t.Errorf("assistant blocks = %+v", asst.Content)
	}
	if got.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", got.MaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.3 {
		t.Error("temperature did not reach the request")
	}
}

// max_tokens is required by the API, so it must never be sent as zero.
func TestAnthropicAlwaysSendsMaxTokens(t *testing.T) {
	a := NewAnthropic("", "k", "m", Profile{})
	if got := a.buildRequest(Request{}); got.MaxTokens <= 0 {
		t.Fatalf("max_tokens = %d, want a positive default", got.MaxTokens)
	}
	withProfile := NewAnthropic("", "k", "m", Profile{MaxOutputTokens: 2048})
	if got := withProfile.buildRequest(Request{}); got.MaxTokens != 2048 {
		t.Errorf("max_tokens = %d, want the profile's 2048", got.MaxTokens)
	}
}

// budget_tokens is rejected by current models; it must only be sent when the
// operator configured one explicitly.
func TestAnthropicThinkingForm(t *testing.T) {
	a := NewAnthropic("", "k", "m", Profile{})

	if got := a.buildRequest(Request{Params: Params{Effort: EffortHigh}}); got.Thinking == nil ||
		got.Thinking.Type != "adaptive" || got.Thinking.BudgetTokens != nil {
		t.Errorf("effort should request adaptive thinking with no budget, got %+v", got.Thinking)
	}
	if got := a.buildRequest(Request{Params: Params{ThinkingBudget: ptr(4096)}}); got.Thinking == nil ||
		got.Thinking.Type != "enabled" || got.Thinking.BudgetTokens == nil {
		t.Errorf("an explicit budget should use the enabled form, got %+v", got.Thinking)
	}
	if got := a.buildRequest(Request{}); got.Thinking != nil {
		t.Errorf("thinking must be omitted when nothing asked for it, got %+v", got.Thinking)
	}
}

// An assistant turn with neither text nor tool calls is not a legal message.
func TestAnthropicDropsEmptyAssistantTurn(t *testing.T) {
	a := NewAnthropic("", "k", "m", Profile{})
	got := a.buildRequest(Request{Messages: []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: ""},
	}})
	if len(got.Messages) != 1 {
		t.Fatalf("want the empty assistant turn dropped, got %d messages", len(got.Messages))
	}
}
