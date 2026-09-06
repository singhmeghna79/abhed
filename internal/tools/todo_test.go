package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A tool that does not satisfy the interface still compiles until something
// registers it, so the mismatch shows up as a missing tool at runtime rather
// than a build failure. Assert it here instead.
var _ Tool = Todo{}

func runTodo(t *testing.T, args string) Result {
	t.Helper()
	return Todo{}.Run(context.Background(), nil, json.RawMessage(args))
}

func TestTodoRendersProgress(t *testing.T) {
	got := runTodo(t, `{"items":[
		{"id":"1","text":"read the code","status":"done"},
		{"id":"2","text":"fix the bug","status":"in_progress"},
		{"id":"3","text":"run the tests","status":"pending"}]}`)
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.Content)
	}
	for _, want := range []string{"[x] 1", "[»] 2", "[ ] 3", "1 of 3 done"} {
		if !strings.Contains(got.Content, want) {
			t.Errorf("output missing %q:\n%s", want, got.Content)
		}
	}
}

// A list where everything is "in progress" is describing intent rather than
// tracking work, and stops being worth reading.
func TestTodoRejectsMultipleInProgress(t *testing.T) {
	got := runTodo(t, `{"items":[
		{"id":"1","text":"a","status":"in_progress"},
		{"id":"2","text":"b","status":"in_progress"}]}`)
	if !got.IsError {
		t.Fatal("two in_progress items must be refused")
	}
	if !strings.Contains(got.Content, "one task is in progress") {
		t.Errorf("the error should say why: %s", got.Content)
	}
}

func TestTodoValidatesItems(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"empty list", `{"items":[]}`, "at least one item"},
		{"no text", `{"items":[{"id":"1","text":"  ","status":"pending"}]}`, "no text"},
		{"no id", `{"items":[{"id":"","text":"a","status":"pending"}]}`, "no id"},
		{"duplicate id", `{"items":[{"id":"1","text":"a","status":"pending"},{"id":"1","text":"b","status":"pending"}]}`, "Duplicate id"},
		{"bad status", `{"items":[{"id":"1","text":"a","status":"maybe"}]}`, "pending, in_progress"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runTodo(t, c.args)
			if !got.IsError {
				t.Fatal("want an error")
			}
			if !strings.Contains(got.Content, c.want) {
				t.Errorf("error = %q, want it to mention %q", got.Content, c.want)
			}
		})
	}
}

// The caller needs the list to record an event and render the UI.
func TestTodoReportsTheListToTheCaller(t *testing.T) {
	var got []TodoItem
	var note string
	tool := Todo{OnUpdate: func(items []TodoItem, n string) { got, note = items, n }}
	res := tool.Run(context.Background(), nil, json.RawMessage(
		`{"items":[{"id":"1","text":"a","status":"done"}],"note":"finished a"}`))
	if res.IsError {
		t.Fatal(res.Content)
	}
	if len(got) != 1 || got[0].Text != "a" {
		t.Fatalf("callback got %+v", got)
	}
	if note != "finished a" {
		t.Errorf("note = %q", note)
	}
}
