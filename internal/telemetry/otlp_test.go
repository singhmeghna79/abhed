package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yuvrajsingh/titan/internal/agent"
)

// A collector that records what it is sent.
type collector struct {
	mu    sync.Mutex
	spans []map[string]any
	reqs  int
	hdr   http.Header
}

func (c *collector) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reqs++
		c.hdr = r.Header.Clone()
		for _, rs := range req["resourceSpans"].([]any) {
			for _, ss := range rs.(map[string]any)["scopeSpans"].([]any) {
				for _, sp := range ss.(map[string]any)["spans"].([]any) {
					c.spans = append(c.spans, sp.(map[string]any))
				}
			}
		}
		w.WriteHeader(200)
	})
}

func (c *collector) find(name string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.spans {
		if s["name"] == name {
			return s
		}
	}
	return nil
}

func attr(sp map[string]any, key string) any {
	for _, a := range sp["attributes"].([]any) {
		m := a.(map[string]any)
		if m["key"] == key {
			for _, v := range m["value"].(map[string]any) {
				return v
			}
		}
	}
	return nil
}

func ev(t agent.EventType, session, parent string, at time.Time, payload any) agent.Event {
	raw, _ := json.Marshal(payload)
	return agent.Event{ID: "e", SessionID: session, ParentID: parent, Type: t,
		Payload: raw, CreatedAt: at}
}

// A session with one tool call, one denial and an end becomes a trace with
// the right shape: the session is the root, calls are children, and the
// numbers an operator actually asks for are attributes rather than log lines.
func TestSessionBecomesATrace(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()

	e := New(Config{Endpoint: srv.URL, ServiceName: "titan-test",
		Headers: map[string]string{"Authorization": "Bearer x"}, FlushEvery: 20 * time.Millisecond})

	t0 := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	code := 0
	e.Observe(ev(agent.EvSessionStarted, "s1", "", t0, map[string]any{"model": "gemma4:26b"}))
	e.Observe(ev(agent.EvActionRequested, "s1", "", t0.Add(time.Second),
		agent.ActionRequested{CallID: "c1", Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)}))
	e.Observe(ev(agent.EvObservation, "s1", "", t0.Add(3*time.Second),
		agent.Observation{CallID: "c1", Tool: "bash", ExitCode: &code, DurationMS: 2000}))
	e.Observe(ev(agent.EvActionRequested, "s1", "", t0.Add(4*time.Second),
		agent.ActionRequested{CallID: "c2", Tool: "bash"}))
	e.Observe(ev(agent.EvActionDenied, "s1", "", t0.Add(4*time.Second),
		map[string]any{"call_id": "c2", "tool": "bash", "reason": "deny rule"}))
	e.Observe(ev(agent.EvSessionEnded, "s1", "", t0.Add(9*time.Second),
		agent.SessionEnded{Reason: "completed", Turns: 3, TokensIn: 1200, TokensOut: 300}))
	e.Close()

	root := c.find("titan.session")
	if root == nil {
		t.Fatalf("no session span exported; got %d spans", len(c.spans))
	}
	call := c.find("tool.bash")
	if call == nil {
		t.Fatal("no tool span exported")
	}
	if call["parentSpanId"] != root["spanId"] {
		t.Errorf("tool span parent %v != session span %v", call["parentSpanId"], root["spanId"])
	}
	if call["traceId"] != root["traceId"] {
		t.Error("tool span is not in the session's trace")
	}
	if got := attr(root, "titan.tokens.in"); got != float64(1200) {
		t.Errorf("tokens.in attribute = %v, want 1200", got)
	}
	if got := attr(root, "titan.turns"); got != float64(3) {
		t.Errorf("turns attribute = %v, want 3", got)
	}
	if got := call["endTimeUnixNano"]; got != jsonInt(t0.Add(3*time.Second).UnixNano()) {
		t.Errorf("tool span end = %v", got)
	}

	// The denied call is a span with error status and the reason attached —
	// a policy refusal is the most important thing a trace can show.
	c.mu.Lock()
	var denied map[string]any
	for _, s := range c.spans {
		if attr(s, "titan.denied") == true {
			denied = s
		}
	}
	c.mu.Unlock()
	if denied == nil {
		t.Fatal("denied action produced no span")
	}
	if denied["status"].(map[string]any)["code"] != float64(2) {
		t.Error("denied span does not carry error status")
	}
	if c.hdr.Get("Authorization") != "Bearer x" {
		t.Error("configured header was not sent")
	}
}

// A subagent joins its parent's trace, under the parent's session span,
// instead of starting a trace of its own — otherwise a delegated task is
// invisible from the run that delegated it.
func TestSubagentJoinsParentTrace(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	e := New(Config{Endpoint: srv.URL, FlushEvery: 20 * time.Millisecond})

	t0 := time.Now().UTC()
	e.Observe(ev(agent.EvSessionStarted, "parent", "", t0, nil))
	e.Observe(ev(agent.EvSessionStarted, "child", "parent", t0.Add(time.Second), nil))
	e.Observe(ev(agent.EvSessionEnded, "child", "parent", t0.Add(2*time.Second), agent.SessionEnded{Reason: "completed"}))
	e.Observe(ev(agent.EvSessionEnded, "parent", "", t0.Add(3*time.Second), agent.SessionEnded{Reason: "completed"}))
	e.Close()

	parent, child := c.find("titan.session"), c.find("titan.subagent")
	if parent == nil || child == nil {
		t.Fatalf("parent=%v child=%v", parent != nil, child != nil)
	}
	if child["traceId"] != parent["traceId"] {
		t.Error("subagent started its own trace")
	}
	if child["parentSpanId"] != parent["spanId"] {
		t.Error("subagent span is not a child of the parent session span")
	}
}

// The property that makes this safe to turn on: Observe never blocks, and a
// collector that is down costs nothing but the spans.
func TestObserveNeverBlocksWhenCollectorIsDown(t *testing.T) {
	// A port nothing listens on.
	e := New(Config{Endpoint: "http://127.0.0.1:1", Buffer: 8, FlushEvery: time.Hour,
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	defer e.Close()

	done := make(chan struct{})
	go func() {
		for range 10_000 {
			e.Observe(ev(agent.EvActionRequested, "s", "", time.Now(),
				agent.ActionRequested{CallID: "x", Tool: "bash"}))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked with the collector unreachable — a session would have stalled")
	}
	if e.Dropped.Load() == 0 {
		t.Error("buffer of 8 absorbed 10,000 events without dropping — the counter is not wired")
	}
}

// Same session, same trace, every time. This is what lets an auditor put a
// replay beside the original.
func TestTraceIDIsDeterministic(t *testing.T) {
	first := traceID("s-abc")
	again := traceID("s-abc")
	if first != again {
		t.Error("trace id is not a stable function of the session id")
	}
	if first == traceID("s-abd") {
		t.Error("different sessions produced the same trace id")
	}
	if len(traceID("x")) != 32 || len(spanID("x")) != 16 {
		t.Errorf("id lengths: trace %d span %d, want 32/16 hex chars", len(traceID("x")), len(spanID("x")))
	}
}
