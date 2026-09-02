// Package model abstracts over reasoning models.
//
// Everything model-specific lives behind the Adapter interface: tool-call
// parsing, reasoning-token handling, chat templates, structured-output
// strategy. If you find yourself forking the *prompt* per model, the
// difference belongs here instead (docs §07 anti-patterns).
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of conversation. ToolCalls and ToolResults are kept
// structured rather than flattened into text so adapters can render them in
// whatever format their model family expects.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string // set when Role == RoleTool
	IsError    bool   // tool result was an error
}

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// EffortLevel controls reasoning budget. Per docs P11 this is a measured,
// per-model, per-task-class parameter — never assume more is better.
type EffortLevel string

const (
	EffortNone   EffortLevel = ""
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
)

type Request struct {
	System      string // the cached prefix (docs §07 layers 1-4)
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Effort      EffortLevel
	Stop        []string
	Temperature *float64
}

// ChunkType distinguishes the pieces of a streamed response.
type ChunkType string

const (
	ChunkText      ChunkType = "text"
	ChunkReasoning ChunkType = "reasoning" // never fed back as tool input
	ChunkToolCall  ChunkType = "tool_call"
	ChunkDone      ChunkType = "done"
	ChunkError     ChunkType = "error"
)

type Chunk struct {
	Type       ChunkType
	Text       string
	ToolCall   *ToolCall
	Usage      *Usage
	Err        error
	StopReason string
}

type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int // drives the cache-hit metric (docs P8)
	ReasoningTokens   int
}

// Profile describes what a model can do. Populated by the conformance suite
// at registration time (docs §08 L2) and consulted by the harness.
type Profile struct {
	Name            string             `json:"name"`
	ContextWindow   int                `json:"context_window"`
	MaxOutputTokens int                `json:"max_output_tokens"`
	SupportsTools   bool               `json:"supports_tools"`
	SupportsStream  bool               `json:"supports_stream"`
	ToolCallFormat  string             `json:"tool_call_format"` // json | xml | pythonic | harmony
	ReasoningTokens bool               `json:"reasoning_tokens"`
	GuidedDecoding  bool               `json:"guided_decoding"`
	CachePrefix     bool               `json:"cache_prefix"`
	Conformance     map[string]float64 `json:"conformance,omitempty"`
}

// Adapter is the single seam that makes Titan model-agnostic.
type Adapter interface {
	Name() string
	Profile() Profile
	// Complete streams a response. The channel closes when the turn ends.
	Complete(ctx context.Context, req Request) (<-chan Chunk, error)
	// CountTokens estimates prompt size for budget and compaction decisions.
	CountTokens(req Request) (int, error)
}
