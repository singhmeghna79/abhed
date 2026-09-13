package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/policy"
)

const answerSchema = `{
  "type":"object","required":["verdict","confidence"],"additionalProperties":false,
  "properties":{
    "verdict":{"type":"string","enum":["safe","unsafe"]},
    "confidence":{"type":"number","minimum":0,"maximum":1},
    "notes":{"type":"array","items":{"type":"string"}}
  }}`

// The model delivers the answer by calling `result`; the run ends there,
// completed, and the caller gets exactly the validated JSON.
func TestStructuredRunReturnsValidatedJSONAndEnds(t *testing.T) {
	turns := []scriptedTurn{
		{text: "Looking.", calls: []model.ToolCall{call("result",
			map[string]any{"verdict": "safe", "confidence": 0.9, "notes": []string{"ok"}})}},
		// Never reached: a Final result ends the run before the next turn.
		{text: "I should not be asked again."},
	}
	l, _, _ := harness(t, turns, policy.ModeDefault, true)
	raw, reason, err := RunStructured(context.Background(), l, l.Tools, "Is it safe?", json.RawMessage(answerSchema))
	if err != nil {
		t.Fatal(err)
	}
	if reason != TermCompleted {
		t.Errorf("reason = %s, want completed", reason)
	}
	var out struct {
		Verdict    string  `json:"verdict"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Verdict != "safe" || out.Confidence != 0.9 {
		t.Errorf("got %s", raw)
	}
	// The result tool is gone again: a later plain run must not see it.
	if _, still := l.Tools.Get(ResultToolName); still {
		t.Error("result tool left in the registry after the run")
	}
}

// A rejected answer comes back with the violations named, and the model's
// corrected call is accepted. This is the whole point of validating inside
// the loop rather than after it: the model gets to fix its own output.
func TestStructuredRunFeedsViolationsBackAndAcceptsTheFix(t *testing.T) {
	turns := []scriptedTurn{
		{calls: []model.ToolCall{call("result", map[string]any{"verdict": "maybe", "confidence": 3})}},
		{calls: []model.ToolCall{call("result", map[string]any{"verdict": "unsafe", "confidence": 0.4})}},
	}
	l, store, _ := harness(t, turns, policy.ModeDefault, true)
	raw, _, err := RunStructured(context.Background(), l, l.Tools, "?", json.RawMessage(answerSchema))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"unsafe"`) {
		t.Errorf("accepted the wrong answer: %s", raw)
	}
	// The first observation told the model what was wrong, by path.
	evs, _ := store.Events("sess1")
	var firstObs string
	for _, ev := range evs {
		if ev.Type == EvObservation {
			var o Observation
			_ = json.Unmarshal(ev.Payload, &o)
			firstObs = o.Content
			break
		}
	}
	for _, want := range []string{"$.verdict: must be one of", "$.confidence: above maximum 1"} {
		if !strings.Contains(firstObs, want) {
			t.Errorf("rejection did not name %q:\n%s", want, firstObs)
		}
	}
}

// A run that ends without calling result is an error the caller can
// distinguish, carrying the model's last words.
func TestStructuredRunWithoutResultIsAnError(t *testing.T) {
	l, _, _ := harness(t, []scriptedTurn{{text: "Here is my answer in prose: safe."}}, policy.ModeDefault, true)
	_, _, err := RunStructured(context.Background(), l, l.Tools, "?", json.RawMessage(answerSchema))
	var nr ErrNoResult
	if !errors.As(err, &nr) {
		t.Fatalf("got %v, want ErrNoResult", err)
	}
	if !strings.Contains(nr.Last, "prose") {
		t.Errorf("ErrNoResult did not carry the last message: %q", nr.Last)
	}
}

func TestStructuredRunRefusesABadSchemaUpFront(t *testing.T) {
	l, _, _ := harness(t, nil, policy.ModeDefault, true)
	_, _, err := RunStructured(context.Background(), l, l.Tools, "?", json.RawMessage(`{"type":"object","if":{}}`))
	if err == nil || !strings.Contains(err.Error(), `"if"`) {
		t.Errorf("unsupported keyword accepted: %v", err)
	}
	if len(l.Messages()) != 0 {
		t.Error("a bad schema still spent a turn")
	}
}
