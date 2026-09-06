package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Todo is the agent's task list.
//
// A long task goes wrong in a specific way without one: the model completes the
// first half of a request, produces a confident summary, and never returns to
// the rest — and because the summary reads as finished work, nobody notices.
// Writing the list down turns "what was I doing" from something the model has
// to reconstruct from a compacted history into something it can read.
//
// The list is state, not prose. It is recorded as an event on every change, so
// a replay shows what the agent believed the plan was at each point, and the UI
// can render progress without parsing the reply for intentions.
type Todo struct {
	// OnUpdate is called with the new list so the caller can record an event.
	OnUpdate func(items []TodoItem, note string)
	// State holds the current list between calls.
	State *[]TodoItem
}

// TodoItem is one task.
type TodoItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

func (Todo) Name() string { return "todo" }

func (Todo) Description() string {
	return "Record and update the task list for multi-step work. Write the list " +
		"once when a request has several distinct parts, then update an item's " +
		"status as you go. Use it when a task has more than about three steps, " +
		"when the user listed several things, or when you are about to say you " +
		"have finished part of the work — the list is what stops the rest being " +
		"forgotten. Do not use it for a single-step request."
}

func (Todo) Mutates() bool { return false }

func (Todo) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "items":{
      "type":"array",
      "description":"The complete list, in order. Send every item each time, not just the changed one.",
      "items":{
        "type":"object",
        "properties":{
          "id":{"type":"string","description":"Short stable id, e.g. \"1\"."},
          "text":{"type":"string","description":"What is to be done."},
          "status":{"type":"string","enum":["pending","in_progress","done","cancelled"]}
        },
        "required":["id","text","status"]
      }
    },
    "note":{"type":"string","description":"Optional one line on what changed and why."}
  },
  "required":["items"]
}`)
}

type todoArgs struct {
	Items []TodoItem `json:"items"`
	Note  string     `json:"note"`
}

const maxTodoItems = 40

func (t Todo) Run(_ context.Context, _ *Session, raw json.RawMessage) Result {
	var a todoArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return Result{Content: fmt.Sprintf("Invalid arguments for todo: %v", err), IsError: true}
	}
	if len(a.Items) == 0 {
		return Result{Content: "The todo list needs at least one item.", IsError: true}
	}
	if len(a.Items) > maxTodoItems {
		return Result{Content: fmt.Sprintf(
			"A %d-item list is a sign the work was split too finely; keep it under %d.",
			len(a.Items), maxTodoItems), IsError: true}
	}

	seen := map[string]bool{}
	inProgress := 0
	for i, item := range a.Items {
		if strings.TrimSpace(item.Text) == "" {
			return Result{Content: fmt.Sprintf("Item %d has no text.", i+1), IsError: true}
		}
		if item.ID == "" {
			return Result{Content: fmt.Sprintf("Item %q has no id.", item.Text), IsError: true}
		}
		if seen[item.ID] {
			return Result{Content: fmt.Sprintf("Duplicate id %q.", item.ID), IsError: true}
		}
		seen[item.ID] = true
		switch item.Status {
		case "pending", "in_progress", "done", "cancelled":
		default:
			return Result{Content: fmt.Sprintf(
				"Item %q has status %q; use pending, in_progress, done or cancelled.",
				item.ID, item.Status), IsError: true}
		}
		if item.Status == "in_progress" {
			inProgress++
		}
	}
	// More than one task in progress means the list is describing intent
	// rather than tracking work, which is how it stops being trustworthy.
	if inProgress > 1 {
		return Result{Content: fmt.Sprintf(
			"%d items are in_progress. Exactly one task is in progress at a time; "+
				"mark the others pending.", inProgress), IsError: true}
	}

	if t.State != nil {
		*t.State = a.Items
	}
	if t.OnUpdate != nil {
		t.OnUpdate(a.Items, a.Note)
	}
	return Result{Content: render(a.Items)}
}

func render(items []TodoItem) string {
	var b strings.Builder
	done := 0
	for _, it := range items {
		mark := " "
		switch it.Status {
		case "done":
			mark, done = "x", done+1
		case "in_progress":
			mark = "»"
		case "cancelled":
			mark = "-"
		}
		fmt.Fprintf(&b, "[%s] %s %s\n", mark, it.ID, it.Text)
	}
	fmt.Fprintf(&b, "\n%d of %d done.", done, len(items))
	return b.String()
}
