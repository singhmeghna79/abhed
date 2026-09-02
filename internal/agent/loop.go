package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
	"github.com/yuvrajsingh/titan/internal/tools"
)

// Approver decides on a tool call that policy routed to Ask. Returning false
// feeds a denial back to the model so it can adapt rather than retry.
type Approver interface {
	Approve(ctx context.Context, tool string, args json.RawMessage, res policy.Result) (bool, error)
}

// AutoApprove is for headless runs and tests where policy alone decides.
type AutoApprove struct{ Yes bool }

func (a AutoApprove) Approve(context.Context, string, json.RawMessage, policy.Result) (bool, error) {
	return a.Yes, nil
}

type Config struct {
	MaxTurns     int
	MaxTokens    int
	CompactAt    float64 // fraction of the context window
	SystemPrompt string
	Temperature  *float64
	Effort       model.EffortLevel
}

func DefaultConfig() Config {
	return Config{MaxTurns: 100, MaxTokens: 8192, CompactAt: 0.90}
}

// Loop is the agent's execution loop: a turn-based cycle that terminates
// normally on a response with no tool calls, and abnormally through roughly
// ten other exits — each a distinct, logged terminal event (docs §02).
type Loop struct {
	Adapter   model.Adapter
	Tools     *tools.Registry
	Policy    *policy.Engine
	Approver  Approver
	Session   *tools.Session
	Recorder  *Recorder
	Config    Config
	Compactor *Compactor

	messages []model.Message
	usage    Usage
	turns    int

	// repeatedFailures counts consecutive identical tool calls that returned an
	// error. A model that ignores an error message and retries verbatim will
	// otherwise burn the entire turn budget on one mistake.
	repeatedFailures map[string]int
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
	Turns        int
	// Compactions is a capacity metric, not a curiosity: each one invalidates
	// the prefix cache and pays cold prefill again (docs P8).
	Compactions int
	// ColdPrefillTokens counts input tokens that missed the cache. This is the
	// quantity the 17x prefix-cache claim is about, measured rather than assumed.
	ColdPrefillTokens int
}

// CacheHitRate reports the fraction of input tokens served from the prefix
// cache. Per docs P8 this is both a capacity and a UX metric, so it is surfaced
// rather than buried.
func (u Usage) CacheHitRate() float64 {
	if u.InputTokens == 0 {
		return 0
	}
	return float64(u.CachedTokens) / float64(u.InputTokens)
}

// PrefillSavings estimates the multiple by which prefix caching reduced prefill
// work this session: total input tokens over the tokens actually prefilled cold.
// This is the measured analogue of the computed 17x in docs/architecture/04-sizing.md.
func (u Usage) PrefillSavings() float64 {
	if u.ColdPrefillTokens == 0 {
		return 0
	}
	return float64(u.InputTokens) / float64(u.ColdPrefillTokens)
}

func NewLoop(a model.Adapter, reg *tools.Registry, pol *policy.Engine,
	appr Approver, sess *tools.Session, rec *Recorder, cfg Config) *Loop {
	return &Loop{
		Adapter: a, Tools: reg, Policy: pol, Approver: appr,
		Session: sess, Recorder: rec, Config: cfg,
	}
}

// Run executes turns until termination and returns the reason.
func (l *Loop) Run(ctx context.Context, userPrompt string) (TerminalReason, error) {
	if _, err := l.Recorder.Record(EvUserMessage, ActorUser, Trusted, Message{Text: userPrompt}); err != nil {
		return TermError, err
	}
	l.messages = append(l.messages, model.Message{Role: model.RoleUser, Content: userPrompt})

	for {
		if ctx.Err() != nil {
			return l.finish(TermUserInterrupt), nil
		}
		if l.turns >= l.Config.MaxTurns {
			return l.finish(TermMaxTurns), nil
		}
		l.turns++

		if err := l.maybeCompact(ctx); err != nil {
			// Compaction failure is not fatal on its own; the turn may still
			// fit. If it does not, the model call will say so.
			l.Recorder.Record(EvCompactDone, ActorSystem, Trusted, map[string]string{
				"error": err.Error(),
			})
		}

		reason, done, err := l.turn(ctx)
		if err != nil {
			l.finish(TermError)
			return TermError, err
		}
		if done {
			return l.finish(reason), nil
		}
	}
}

// maybeCompact summarizes history when it approaches the context limit.
//
// The system prompt and memory file are re-injected whole rather than
// summarized: compaction discarding the operating rules is exactly the failure
// docs P4 warns about.
func (l *Loop) maybeCompact(ctx context.Context) error {
	if l.Compactor == nil {
		return nil
	}
	should, used, err := l.Compactor.ShouldCompact(l.Config.SystemPrompt, l.messages, toolDefs(l.Tools))
	if err != nil || !should {
		return err
	}

	l.Recorder.Record(EvCompactStarted, ActorSystem, Trusted, Compaction{
		BeforeTokens: used, Trigger: "auto",
	})

	compacted, info, err := l.Compactor.Compact(ctx, "auto", l.Config.SystemPrompt, l.messages, used)
	if err != nil {
		return err
	}
	if len(compacted) == len(l.messages) {
		return nil // nothing was summarized
	}

	l.messages = compacted
	l.usage.Compactions++
	l.Recorder.Record(EvCompactDone, ActorSystem, Trusted, info)
	return nil
}

// Compact forces compaction now, for the /compact command.
func (l *Loop) Compact(ctx context.Context) (Compaction, error) {
	if l.Compactor == nil {
		return Compaction{}, fmt.Errorf("compaction is not configured")
	}
	used, _ := l.Adapter.CountTokens(model.Request{
		System: l.Config.SystemPrompt, Messages: l.messages,
	})
	compacted, info, err := l.Compactor.Compact(ctx, "manual", l.Config.SystemPrompt, l.messages, used)
	if err != nil {
		return Compaction{}, err
	}
	l.messages = compacted
	l.usage.Compactions++
	l.Recorder.Record(EvCompactDone, ActorSystem, Trusted, info)
	return info, nil
}

// Messages exposes the current history for inspection and testing.
func (l *Loop) Messages() []model.Message { return l.messages }

// turn runs one round trip: model output plus any tool executions.
func (l *Loop) turn(ctx context.Context) (TerminalReason, bool, error) {
	req := model.Request{
		System:      l.Config.SystemPrompt,
		Messages:    l.messages,
		Tools:       toolDefs(l.Tools),
		MaxTokens:   l.Config.MaxTokens,
		Temperature: l.Config.Temperature,
		Effort:      l.Config.Effort,
	}

	stream, err := l.Adapter.Complete(ctx, req)
	if err != nil {
		return TermError, true, fmt.Errorf("model call failed: %w", err)
	}

	var text strings.Builder
	var calls []model.ToolCall
	var streamErr error

	for chunk := range stream {
		switch chunk.Type {
		case model.ChunkText:
			text.WriteString(chunk.Text)
		case model.ChunkReasoning:
			// Reasoning is observed but never fed back as history: it is not
			// part of the conversation the model should condition on.
		case model.ChunkToolCall:
			calls = append(calls, *chunk.ToolCall)
		case model.ChunkError:
			streamErr = chunk.Err
		case model.ChunkDone:
			if chunk.Usage != nil {
				l.usage.InputTokens += chunk.Usage.InputTokens
				l.usage.OutputTokens += chunk.Usage.OutputTokens
				l.usage.CachedTokens += chunk.Usage.CachedInputTokens
				l.usage.ColdPrefillTokens += chunk.Usage.InputTokens - chunk.Usage.CachedInputTokens
			}
		}
	}

	if ctx.Err() != nil {
		return TermUserInterrupt, true, nil
	}

	// A malformed tool call is recoverable: tell the model what was wrong and
	// let it retry rather than aborting the session.
	if streamErr != nil && len(calls) == 0 {
		if text.Len() == 0 {
			l.messages = append(l.messages,
				model.Message{Role: model.RoleAssistant, Content: ""},
				model.Message{Role: model.RoleUser, Content: "Your previous response could not be parsed: " +
					streamErr.Error() + "\nPlease retry with valid tool arguments."})
			return "", false, nil
		}
	}

	if body := text.String(); body != "" {
		if _, err := l.Recorder.Record(EvAgentMessage, ActorAgent, Trusted, Message{Text: body}); err != nil {
			return TermError, true, err
		}
	}

	// Normal termination: a response with no tool calls.
	if len(calls) == 0 {
		l.messages = append(l.messages, model.Message{
			Role: model.RoleAssistant, Content: text.String(),
		})
		return TermCompleted, true, nil
	}

	l.messages = append(l.messages, model.Message{
		Role: model.RoleAssistant, Content: text.String(), ToolCalls: calls,
	})

	for _, call := range calls {
		result, terminal := l.execute(ctx, call)
		l.messages = append(l.messages, model.Message{
			Role:       model.RoleTool,
			ToolCallID: call.ID,
			Content:    result.Content,
			IsError:    result.IsError,
		})
		if terminal != "" {
			return terminal, true, nil
		}
	}

	// If one call has failed identically far past the point of escalation, the
	// model is stuck. Ending is better than spending the remaining budget.
	for key, n := range l.repeatedFailures {
		if n >= repeatedFailureAbort {
			l.messages = append(l.messages, model.Message{
				Role: model.RoleUser,
				Content: fmt.Sprintf(
					"Stopping: the same call failed %d times without adaptation (%s).",
					n, truncateKey(key)),
			})
			return TermRetryExhausted, true, nil
		}
	}
	return "", false, nil
}

const (
	// After this many identical failures the error message is escalated.
	repeatedFailureLimit = 3
	// After this many, the loop gives up rather than burning the budget.
	repeatedFailureAbort = 6
)

func truncateKey(k string) string {
	if len(k) > 80 {
		return k[:80] + "…"
	}
	return k
}

// execute runs one tool call through policy, approval, and the tool itself.
func (l *Loop) execute(ctx context.Context, call model.ToolCall) (tools.Result, TerminalReason) {
	tool, found := l.Tools.Get(call.Name)
	if !found {
		return tools.Result{
			Content: fmt.Sprintf("Unknown tool %q. Available tools: %s.",
				call.Name, strings.Join(l.Tools.Names(), ", ")),
			IsError: true,
		}, ""
	}

	decision := l.Policy.Evaluate(call.Name, tool.Mutates(), call.Args)

	if _, err := l.Recorder.Record(EvActionRequested, ActorAgent, Trusted, ActionRequested{
		CallID:           call.ID,
		Tool:             call.Name,
		Args:             call.Args,
		RequiresApproval: decision.Decision == policy.Ask,
		Reason:           decision.Reason,
	}); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}, TermError
	}

	switch decision.Decision {
	case policy.Deny:
		l.Recorder.Record(EvActionDenied, ActorSystem, Trusted, map[string]string{
			"call_id": call.ID, "reason": decision.Reason,
		})
		// Feed the denial back so the model can choose another approach.
		return tools.Result{
			Content: fmt.Sprintf("Denied: %s. Choose a different approach.", decision.Reason),
			IsError: true,
		}, ""

	case policy.Ask:
		approved, err := l.Approver.Approve(ctx, call.Name, call.Args, decision)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return tools.Result{Content: "Interrupted.", IsError: true}, TermUserInterrupt
			}
			return tools.Result{Content: err.Error(), IsError: true}, TermError
		}
		if !approved {
			l.Recorder.Record(EvActionDenied, ActorUser, Trusted, map[string]string{
				"call_id": call.ID, "reason": "rejected by user",
			})
			return tools.Result{
				Content: "The user rejected this action. Do not retry it; ask what they would prefer.",
				IsError: true,
			}, ""
		}
	}

	l.Recorder.Record(EvActionApproved, ActorSystem, Trusted, map[string]string{
		"call_id": call.ID, "reason": decision.Reason,
	})

	start := time.Now()
	result := tool.Run(ctx, l.Session, call.Args)
	elapsed := time.Since(start)

	// An identical call that keeps failing means the model is not reading the
	// error. Escalate the message rather than letting it consume every turn:
	// the error text alone has demonstrably not worked.
	if result.IsError {
		if l.repeatedFailures == nil {
			l.repeatedFailures = map[string]int{}
		}
		key := call.Name + string(call.Args)
		l.repeatedFailures[key]++
		if n := l.repeatedFailures[key]; n >= repeatedFailureLimit {
			result.Content = fmt.Sprintf(
				"%s\n\n[This exact call has now failed %d times. Repeating it will not "+
					"work. Read the error above and do something different — or explain "+
					"what is blocking you and stop.]", result.Content, n)
		}
	} else {
		delete(l.repeatedFailures, call.Name+string(call.Args))
	}

	// Tool output is untrusted: it may contain text that looks like
	// instructions. The trust tag travels with the event (docs arch §6).
	trust := Untrusted
	if _, err := l.Recorder.Record(EvObservation, ActorTool, trust, Observation{
		CallID:     call.ID,
		Tool:       call.Name,
		Content:    result.Content,
		IsError:    result.IsError,
		Truncated:  result.Truncated,
		ExitCode:   result.ExitCode,
		DurationMS: elapsed.Milliseconds(),
	}); err != nil {
		return result, TermError
	}
	return result, ""
}

func (l *Loop) finish(reason TerminalReason) TerminalReason {
	l.usage.Turns = l.turns
	l.Recorder.Record(EvSessionEnded, ActorSystem, Trusted, SessionEnded{
		Reason:       reason,
		Turns:        l.turns,
		TokensIn:     l.usage.InputTokens,
		TokensOut:    l.usage.OutputTokens,
		TokensCached: l.usage.CachedTokens,
		Compactions:  l.usage.Compactions,
	})
	return reason
}

func (l *Loop) Usage() Usage { return l.usage }

func toolDefs(r *tools.Registry) []model.ToolDef {
	defs := r.Definitions()
	out := make([]model.ToolDef, len(defs))
	for i, d := range defs {
		out[i] = model.ToolDef{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
	}
	return out
}
