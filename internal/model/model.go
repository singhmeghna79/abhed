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
	System   string // the cached prefix (docs §07 layers 1-4)
	Messages []Message
	Tools    []ToolDef
	// Params carries sampling and decoding controls. MaxTokens, Stop and
	// Effort live here too; the older top-level fields are kept as the
	// per-request override that composes over the configured defaults.
	Params Params

	MaxTokens   int
	Effort      EffortLevel
	Stop        []string
	Temperature *float64
}

// Sampling resolves the effective parameters for this request: the configured
// Params, with any per-request field set on the legacy top-level fields taking
// precedence.
func (r Request) Sampling() Params {
	out := r.Params
	if r.MaxTokens != 0 {
		out.MaxTokens = r.MaxTokens
	}
	if r.Effort != EffortNone {
		out.Effort = r.Effort
	}
	if len(r.Stop) > 0 {
		out.Stop = r.Stop
	}
	if r.Temperature != nil {
		out.Temperature = r.Temperature
	}
	return out
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
	// Sampling reports which decoding knobs this provider honours, so a
	// config naming one it does not is rejected rather than ignored.
	Sampling Sampling `json:"-"`
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


// estimateTokens approximates prompt size from character counts.
//
// Every adapter needs this for the same decision — when to compact — and none
// of them needs it to be exact. A real tokenizer would mean shipping vocabulary
// files per model family, and the counting endpoints that exist cost a round
// trip on a question asked several times a session. An estimate that is close
// and free is the right trade for a threshold check; the model reports true
// usage afterwards, which is what the budget is actually reconciled against.
func estimateTokens(req Request) int {
	n := len(req.System)
	for _, m := range req.Messages {
		n += len(m.Content) + 16 // per-message framing overhead
		for _, tc := range m.ToolCalls {
			n += len(tc.Name) + len(tc.Args) + 16
		}
	}
	for _, t := range req.Tools {
		n += len(t.Name) + len(t.Description) + len(t.InputSchema)
	}
	// ~3.6 chars/token is a reasonable average for code-heavy English text.
	return n * 10 / 36
}
