package model

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Salvaging tool calls that a model wrote as prose.
//
// A model that has been fine-tuned on a particular tool-call syntax sometimes
// falls back to emitting that syntax as ordinary text instead of using the
// structured tool_calls field — most often under a long system prompt, where
// the in-context examples outweigh the API contract.
//
// Observed with qwen3-coder:30b under Titan's full prompt: it produced
//
//	<function=glob>
//	<parameter=pattern>
//	**
//	</parameter>
//	</function>
//
// as message content. The loop had nothing to dispatch, so the session ended
// after one turn with no work done and no error — the worst kind of failure,
// because it looks like the model simply chose to stop.
//
// Recovering the call is the difference between a stalled agent and a working
// one. This is deliberately conservative: it runs only when the model emitted
// NO structured calls, it requires the tool name to be one actually offered in
// the request, and it never guesses at arguments it cannot parse. A false
// positive would invent a tool call the model did not make, which is worse
// than the stall it is fixing.

var (
	// Qwen's fallback syntax: <function=name>...<parameter=k>v</parameter>...
	qwenFuncRe  = regexp.MustCompile(`(?s)<function=([a-zA-Z0-9_-]+)>(.*?)</function>`)
	qwenParamRe = regexp.MustCompile(`(?s)<parameter=([a-zA-Z0-9_-]+)>(.*?)</parameter>`)

	// A JSON object inside a <tool_call> tag, used by several other tunes.
	toolCallTagRe = regexp.MustCompile(`(?s)<tool_call>\s*(\{.*?\})\s*</tool_call>`)
)

// salvageToolCall looks for a tool call written as text. It returns the call
// and true only when it is confident; allowed names the tools the request
// actually offered, so a stray mention of a word cannot become a call.
func salvageToolCall(text string, allowed []ToolDef) (ToolCall, bool) {
	if text == "" || len(allowed) == 0 {
		return ToolCall{}, false
	}
	known := make(map[string]bool, len(allowed))
	for _, t := range allowed {
		known[t.Name] = true
	}

	// Form 1: <tool_call>{"name":..., "arguments":{...}}</tool_call>
	if m := toolCallTagRe.FindStringSubmatch(text); m != nil {
		var payload struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Args      json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal([]byte(m[1]), &payload); err == nil && known[payload.Name] {
			args := payload.Arguments
			if len(args) == 0 {
				args = payload.Args
			}
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			return ToolCall{Name: payload.Name, Args: args}, true
		}
	}

	// Form 2: Qwen's <function=name><parameter=k>v</parameter></function>
	if m := qwenFuncRe.FindStringSubmatch(text); m != nil && known[m[1]] {
		args := map[string]any{}
		for _, p := range qwenParamRe.FindAllStringSubmatch(m[2], -1) {
			// Values arrive with the surrounding newlines of the block form.
			args[p[1]] = strings.TrimSpace(p[2])
		}
		encoded, err := json.Marshal(args)
		if err != nil {
			return ToolCall{}, false
		}
		return ToolCall{Name: m[1], Args: encoded}, true
	}

	return ToolCall{}, false
}

// stripSalvaged removes the text form of a call from the visible content, so a
// recovered call does not also appear as prose in the transcript.
func stripSalvaged(text string) string {
	text = toolCallTagRe.ReplaceAllString(text, "")
	text = qwenFuncRe.ReplaceAllString(text, "")
	// Models often wrap the block in a stray </tool_call> or fence.
	text = strings.ReplaceAll(text, "</tool_call>", "")
	return strings.TrimSpace(text)
}
