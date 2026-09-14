package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yuvrajsingh/abhed/internal/model"
	"github.com/yuvrajsingh/abhed/internal/policy"
	"github.com/yuvrajsingh/abhed/internal/tools"
)

// failingTool always errors, which is what drives the repeatedFailures map that
// the parallel path writes to from several goroutines at once.
type failingTool struct{}

func (failingTool) Name() string            { return "failer" }
func (failingTool) Description() string     { return "always fails" }
func (failingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (failingTool) Mutates() bool           { return false }
func (failingTool) Run(context.Context, *tools.Session, json.RawMessage) tools.Result {
	return tools.Result{Content: "boom", IsError: true}
}

// Independent tool calls run concurrently, and a failing one writes to the
// loop's repeated-failure counter. Concurrent writes to a Go map do not corrupt
// a count — they abort the process — so this runs eight failing calls in one
// turn under -race. It found exactly that bug when the parallel path was added.
func TestParallelFailuresDoNotRaceOnSharedState(t *testing.T) {
	dir := tempDir(t)
	sess, err := tools.NewSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemStore()
	rec := NewRecorder(store, "sess1", "")
	reg := tools.NewRegistry(failingTool{})
	pol := policy.New(policy.ModeAuto)
	if err := pol.AddAllow("*"); err != nil {
		t.Fatal(err)
	}

	var calls []model.ToolCall
	for i := 0; i < 8; i++ {
		calls = append(calls, model.ToolCall{
			ID: fmt.Sprintf("c%d", i), Name: "failer",
			Args: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
		})
	}
	loop := NewLoop(&scriptedAdapter{turns: []scriptedTurn{
		{calls: calls}, {text: "done"},
	}}, reg, pol, AutoApprove{Yes: true}, sess, rec, DefaultConfig())

	if _, err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
}
