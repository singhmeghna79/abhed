package pipeline

import (
	"context"
	"encoding/json"
	"testing"
)

// A tool step's args are a JSON template and {{input}} is often untrusted
// (a prior step's output or a RAG result). It must land as a single string
// value, never as a chance to add fields to the call. render() returned
// strings verbatim, so this payload used to close the JSON string and inject
// an "injected" key into the arguments the tool received.
func TestRenderJSONEscapesStringSubstitutions(t *testing.T) {
	payload := `x","injected":"pwned`
	out, err := renderJSON(`{"query":"{{input}}"}`, map[string]any{"input": payload})
	if err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("rendered args are not valid JSON: %v (%s)", err, out)
	}
	if _, injected := got["injected"]; injected {
		t.Fatalf("injection succeeded: %s", out)
	}
	if got["query"] != payload {
		t.Fatalf("query = %q, want the payload verbatim as one string", got["query"])
	}
}

// Non-string values keep flowing through as JSON, so numbers and arrays are
// not turned into strings.
func TestRenderJSONKeepsNonStringValues(t *testing.T) {
	out, err := renderJSON(`{"n":{{count}},"items":{{list}}}`, map[string]any{
		"count": 3,
		"list":  []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var got struct {
		N     int      `json:"n"`
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not valid JSON: %v (%s)", err, out)
	}
	if got.N != 3 || len(got.Items) != 2 {
		t.Fatalf("got %+v, want n=3 and two items", got)
	}
}

// A template that renders to something no tool could parse is refused rather
// than sent: a missing reference in a value position leaves a hole.
func TestRenderJSONRejectsInvalidResult(t *testing.T) {
	if _, err := renderJSON(`{"n":{{missing}}}`, map[string]any{}); err == nil {
		t.Fatal("expected an error for arguments that do not render to valid JSON")
	}
}

func TestRenderJSONEmptyTemplateIsEmptyObject(t *testing.T) {
	out, err := renderJSON("", nil)
	if err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if string(out) != "{}" {
		t.Fatalf("empty template rendered to %q, want {}", out)
	}
}

// End to end: the arguments a tool actually receives carry no injected field.
func TestToolStepArgsAreNotInjectable(t *testing.T) {
	var seen json.RawMessage
	tool := func(_ context.Context, _ string, args json.RawMessage) (string, error) {
		seen = args
		return `"ok"`, nil
	}
	model := func(context.Context, string, json.RawMessage) (string, error) { return "", nil }

	p := Pipeline{Stages: []Stage{{
		Name: "call",
		Steps: []Step{{
			Kind: "tool", Tool: "search", Output: "r",
			Args: json.RawMessage(`{"query":"{{input}}"}`),
		}},
	}}}
	_, err := runner(tool, model).Run(context.Background(), p, `a","danger":"b`)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(seen, &got); err != nil {
		t.Fatalf("tool received invalid JSON: %v (%s)", err, seen)
	}
	if _, injected := got["danger"]; injected {
		t.Fatalf("tool received injected field: %s", seen)
	}
}
