package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/yuvrajsingh/titan/internal/model"
)

// Compaction summarizes a conversation approaching the context limit and
// restarts from the summary (docs P3, P4).
//
// The economics matter as much as the quality here. Compaction invalidates the
// prefix cache by construction: the summarized history replaces tokens the
// serving layer had already cached, so the next turn pays cold prefill again.
// That makes compaction frequency a capacity variable, not just a quality knob,
// which is why every compaction emits an event carrying its token accounting.
type Compactor struct {
	Adapter model.Adapter
	// Threshold is the fraction of the context window at which compaction
	// fires. Below 1.0 with real margin: hitting the hard limit mid-turn is an
	// unrecoverable error, and the estimate is approximate.
	Threshold float64
	// KeepRecentTurns are preserved verbatim after the summary. The most recent
	// exchanges carry the working state the model needs to continue.
	KeepRecentTurns int
	// PreCompact runs before summarization, receiving the trigger ("auto" or
	// "manual"). Operators use it to archive the full transcript before it is
	// discarded (docs §07).
	PreCompact func(trigger string, messages []model.Message) error
}

func NewCompactor(a model.Adapter, threshold float64) *Compactor {
	if threshold <= 0 || threshold > 1 {
		threshold = 0.90
	}
	return &Compactor{Adapter: a, Threshold: threshold, KeepRecentTurns: 4}
}

// ShouldCompact reports whether the conversation has grown past the threshold.
func (c *Compactor) ShouldCompact(system string, messages []model.Message, tools []model.ToolDef) (bool, int, error) {
	window := c.Adapter.Profile().ContextWindow
	if window <= 0 {
		return false, 0, nil // unknown window: never auto-compact
	}
	used, err := c.Adapter.CountTokens(model.Request{
		System: system, Messages: messages, Tools: tools,
	})
	if err != nil {
		return false, 0, err
	}
	return float64(used) >= float64(window)*c.Threshold, used, nil
}

const summaryPrompt = `Summarize the conversation so far so that another engineer could pick up exactly where it left off.

Preserve, in this order:
1. The user's original goal, stated verbatim if short.
2. What has been done: files created or modified, with paths.
3. What was learned about the codebase that is not obvious from reading it.
4. Current state: what works, what is broken, what was just attempted.
5. The immediate next step.

Omit: tool call mechanics, file contents already written to disk, and reasoning
that led nowhere. Be specific about paths and identifiers — a vague summary
forces the work to be redone.

Write it as notes to a colleague, not prose.`

// Compact replaces history with a summary plus the most recent turns.
//
// Returns the new message list and the token accounting for the event. The
// system prompt and memory file are NOT summarized — they are re-injected whole
// by the caller, since compaction discarding the operating rules is exactly the
// failure P4 warns about.
func (c *Compactor) Compact(ctx context.Context, trigger string, system string,
	messages []model.Message, beforeTokens int) ([]model.Message, Compaction, error) {

	if c.PreCompact != nil {
		if err := c.PreCompact(trigger, messages); err != nil {
			return messages, Compaction{}, fmt.Errorf("pre-compact hook: %w", err)
		}
	}

	keep := c.KeepRecentTurns
	if keep < 1 {
		keep = 1
	}
	// Split at a message boundary that keeps tool calls with their results:
	// an assistant turn whose tool results were summarized away leaves the
	// model referencing a call it cannot see.
	split := boundaryBefore(messages, keep)
	older, recent := messages[:split], messages[split:]

	if len(older) == 0 {
		return messages, Compaction{}, nil // nothing worth summarizing yet
	}

	summary, err := c.summarize(ctx, system, older)
	if err != nil {
		return messages, Compaction{}, err
	}

	compacted := make([]model.Message, 0, len(recent)+1)
	compacted = append(compacted, model.Message{
		Role: model.RoleUser,
		Content: "[Earlier conversation was compacted to stay within the context " +
			"window. Summary of what happened:]\n\n" + summary,
	})
	compacted = append(compacted, recent...)

	after, _ := c.Adapter.CountTokens(model.Request{System: system, Messages: compacted})
	return compacted, Compaction{
		BeforeTokens: beforeTokens,
		AfterTokens:  after,
		Summary:      summary,
		Trigger:      trigger,
	}, nil
}

func (c *Compactor) summarize(ctx context.Context, system string, older []model.Message) (string, error) {
	// Render the history as text rather than replaying it as messages: the
	// summarizer is doing a different job than the agent, and giving it the
	// tool schemas would invite it to call them.
	var transcript strings.Builder
	for _, m := range older {
		switch m.Role {
		case model.RoleUser:
			fmt.Fprintf(&transcript, "USER: %s\n\n", m.Content)
		case model.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&transcript, "ASSISTANT: %s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&transcript, "ASSISTANT called %s(%s)\n", tc.Name, truncate(string(tc.Args), 200))
			}
			transcript.WriteString("\n")
		case model.RoleTool:
			status := "result"
			if m.IsError {
				status = "ERROR"
			}
			fmt.Fprintf(&transcript, "TOOL %s: %s\n\n", status, truncate(m.Content, 400))
		}
	}

	stream, err := c.Adapter.Complete(ctx, model.Request{
		System: "You summarize engineering work accurately and concisely.",
		Messages: []model.Message{{
			Role:    model.RoleUser,
			Content: summaryPrompt + "\n\n---\n\n" + transcript.String(),
		}},
		MaxTokens: 2048,
	})
	if err != nil {
		return "", fmt.Errorf("summarization failed: %w", err)
	}

	var out strings.Builder
	for chunk := range stream {
		switch chunk.Type {
		case model.ChunkText:
			out.WriteString(chunk.Text)
		case model.ChunkError:
			return "", fmt.Errorf("summarization failed: %w", chunk.Err)
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("summarization produced no output")
	}
	return strings.TrimSpace(out.String()), nil
}

// boundaryBefore finds a split index that keeps the last `keep` user turns and
// never separates an assistant's tool calls from their results.
func boundaryBefore(messages []model.Message, keep int) int {
	if len(messages) == 0 {
		return 0
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleUser {
			seen++
			if seen >= keep {
				return i
			}
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
