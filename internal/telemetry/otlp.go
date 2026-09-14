// Package telemetry exports the event stream as OpenTelemetry traces.
//
// A Abhed session is already an ordered, append-only log of everything the
// agent did. That log is the audit trail; this package is the same facts in
// the shape observability tooling expects: one trace per session, a span per
// tool call and per subagent, with tokens, exit codes and denials attached.
// It speaks OTLP/HTTP JSON directly, with no SDK dependency, because the
// mapping is small and the air-gapped installs this product is built for
// cannot pull a dependency tree at build time.
//
// The exporter is a TAP on the store, not a stage in the pipeline. Append
// hands it the event and returns; a slow or absent collector costs the
// session nothing. If the buffer fills, events are dropped and counted — a
// missing span is a smaller failure than a stalled agent.
package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuvrajsingh/abhed/internal/agent"
)

// Config is the operator's side of the exporter.
type Config struct {
	Endpoint    string            // OTLP/HTTP base; /v1/traces is appended
	Headers     map[string]string // sent on every export
	ServiceName string            // resource attribute; defaults to "abhed"

	// FlushEvery and MaxBatch bound how long a span waits and how many go
	// out together. Both have sensible defaults.
	FlushEvery time.Duration
	MaxBatch   int
	// Buffer is how many events may queue before Observe starts dropping.
	Buffer int

	HTTPClient *http.Client
	Log        *slog.Logger
}

// Exporter turns events into spans and ships them.
type Exporter struct {
	cfg    Config
	in     chan agent.Event
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool

	mu   sync.Mutex
	open map[string]*span // keyed by session id, then call id
	out  []*span          // finished, awaiting flush

	// Counters an operator can read to know whether the picture is complete.
	Exported atomic.Int64
	Dropped  atomic.Int64
	Failed   atomic.Int64
}

type span struct {
	TraceID   string
	SpanID    string
	ParentID  string
	Name      string
	Kind      int
	Start     time.Time
	End       time.Time
	Attrs     map[string]any
	Events    []spanEvent
	Status    int // 0 unset, 1 ok, 2 error
	StatusMsg string
}

type spanEvent struct {
	Name  string
	At    time.Time
	Attrs map[string]any
}

// New starts an exporter. Close it to flush what remains.
func New(cfg Config) *Exporter {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "abhed"
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 2 * time.Second
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = 200
	}
	if cfg.Buffer <= 0 {
		cfg.Buffer = 4096
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	e := &Exporter{
		cfg:  cfg,
		in:   make(chan agent.Event, cfg.Buffer),
		done: make(chan struct{}),
		open: map[string]*span{},
	}
	go e.run()
	return e
}

// Observe accepts an event without blocking. This is what the store tap
// calls, on the agent's own goroutine, so it must return immediately.
func (e *Exporter) Observe(ev agent.Event) {
	if e.closed.Load() {
		return
	}
	select {
	case e.in <- ev:
	default:
		e.Dropped.Add(1)
	}
}

// Close stops the exporter and flushes anything pending, bounded by the HTTP
// client's timeout so shutdown cannot hang on an unreachable collector.
func (e *Exporter) Close() {
	e.once.Do(func() {
		e.closed.Store(true)
		close(e.in)
		<-e.done
	})
}

func (e *Exporter) run() {
	defer close(e.done)
	tick := time.NewTicker(e.cfg.FlushEvery)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-e.in:
			if !ok {
				e.finishAll()
				e.flush()
				return
			}
			e.handle(ev)
			if e.pending() >= e.cfg.MaxBatch {
				e.flush()
			}
		case <-tick.C:
			e.flush()
		}
	}
}

func (e *Exporter) pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.out)
}

// ---------------------------------------------------------------- mapping

// Trace and span IDs are derived from the session and call IDs rather than
// generated, so the exporter needs no state to correlate a tool result with
// the request that produced it, and a replayed session produces the same
// trace — which is exactly what an auditor comparing the two wants.
func traceID(sessionID string) string { return hashHex(sessionID, 16) }
func spanID(parts ...string) string   { return hashHex(strings.Join(parts, "|"), 8) }

func hashHex(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:n])
}

func (e *Exporter) handle(ev agent.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tid := traceID(rootSession(ev))
	sessionKey := "s:" + ev.SessionID
	root := e.open[sessionKey]

	switch ev.Type {
	case agent.EvSessionStarted:
		sp := &span{
			TraceID: tid, SpanID: spanID("session", ev.SessionID),
			Name: "abhed.session", Kind: 1, Start: ev.CreatedAt,
			Attrs: map[string]any{
				"abhed.session.id": ev.SessionID,
			},
		}
		if ev.ParentID != "" {
			sp.ParentID = spanID("session", ev.ParentID)
			sp.Name = "abhed.subagent"
			sp.Attrs["abhed.session.parent"] = ev.ParentID
		}
		var p map[string]any
		if json.Unmarshal(ev.Payload, &p) == nil {
			for _, k := range []string{"model", "provider", "mode", "workspace", "tenant"} {
				if v, ok := p[k]; ok {
					sp.Attrs["abhed."+k] = v
				}
			}
		}
		e.open[sessionKey] = sp

	case agent.EvActionRequested:
		var p agent.ActionRequested
		if json.Unmarshal(ev.Payload, &p) != nil {
			return
		}
		sp := &span{
			TraceID: tid, SpanID: spanID("call", ev.SessionID, p.CallID),
			ParentID: spanID("session", ev.SessionID),
			Name:     "tool." + p.Tool, Kind: 3, Start: ev.CreatedAt,
			Attrs: map[string]any{
				"abhed.tool":              p.Tool,
				"abhed.call.id":           p.CallID,
				"abhed.requires_approval": p.RequiresApproval,
			},
		}
		if len(p.Args) > 0 && len(p.Args) <= 2048 {
			sp.Attrs["abhed.tool.args"] = string(p.Args)
		}
		e.open["c:"+ev.SessionID+":"+p.CallID] = sp

	case agent.EvActionDenied:
		var p struct {
			CallID string `json:"call_id"`
			Reason string `json:"reason"`
			Tool   string `json:"tool"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		key := "c:" + ev.SessionID + ":" + p.CallID
		sp := e.open[key]
		if sp == nil {
			sp = &span{TraceID: tid, SpanID: spanID("call", ev.SessionID, p.CallID),
				ParentID: spanID("session", ev.SessionID), Name: "tool." + p.Tool,
				Kind: 3, Start: ev.CreatedAt, Attrs: map[string]any{"abhed.tool": p.Tool}}
		}
		sp.End = ev.CreatedAt
		sp.Status, sp.StatusMsg = 2, "denied: "+p.Reason
		sp.Attrs["abhed.denied"] = true
		sp.Attrs["abhed.denied.reason"] = p.Reason
		delete(e.open, key)
		e.out = append(e.out, sp)

	case agent.EvObservation:
		var p agent.Observation
		if json.Unmarshal(ev.Payload, &p) != nil {
			return
		}
		key := "c:" + ev.SessionID + ":" + p.CallID
		sp := e.open[key]
		if sp == nil {
			// A result with no recorded request — possible after a restart.
			// Still worth a span; its duration is the only thing missing.
			sp = &span{TraceID: tid, SpanID: spanID("call", ev.SessionID, p.CallID),
				ParentID: spanID("session", ev.SessionID), Name: "tool." + p.Tool,
				Kind: 3, Start: ev.CreatedAt.Add(-time.Duration(p.DurationMS) * time.Millisecond),
				Attrs: map[string]any{"abhed.tool": p.Tool}}
		}
		sp.End = ev.CreatedAt
		sp.Attrs["abhed.tool.duration_ms"] = p.DurationMS
		sp.Attrs["abhed.tool.truncated"] = p.Truncated
		if p.ExitCode != nil {
			sp.Attrs["abhed.tool.exit_code"] = *p.ExitCode
		}
		if p.IsError {
			sp.Status, sp.StatusMsg = 2, firstLine(p.Content)
		} else {
			sp.Status = 1
		}
		delete(e.open, key)
		e.out = append(e.out, sp)

	case agent.EvCompactStarted, agent.EvCompactDone, agent.EvSubagentSpawned, agent.EvSubagentReturn:
		if root == nil {
			return
		}
		var p map[string]any
		_ = json.Unmarshal(ev.Payload, &p)
		root.Events = append(root.Events, spanEvent{Name: string(ev.Type), At: ev.CreatedAt, Attrs: flatten(p)})

	case agent.EvSessionEnded:
		if root == nil {
			return
		}
		var p agent.SessionEnded
		_ = json.Unmarshal(ev.Payload, &p)
		root.End = ev.CreatedAt
		root.Attrs["abhed.turns"] = p.Turns
		root.Attrs["abhed.tokens.in"] = p.TokensIn
		root.Attrs["abhed.tokens.out"] = p.TokensOut
		root.Attrs["abhed.tokens.cached"] = p.TokensCached
		root.Attrs["abhed.compactions"] = p.Compactions
		root.Attrs["abhed.reason"] = string(p.Reason)
		if strings.Contains(strings.ToLower(string(p.Reason)), "error") {
			root.Status, root.StatusMsg = 2, string(p.Reason)
		} else {
			root.Status = 1
		}
		delete(e.open, sessionKey)
		e.out = append(e.out, root)
	}
}

// rootSession walks to the top-level session so a subagent's spans join its
// parent's trace instead of starting one of their own.
func rootSession(ev agent.Event) string {
	if ev.ParentID != "" {
		return ev.ParentID
	}
	return ev.SessionID
}

// finishAll closes whatever is still open at shutdown, marked as such, so a
// session cut off by a restart is visible as exactly that rather than absent.
func (e *Exporter) finishAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now().UTC()
	for k, sp := range e.open {
		sp.End = now
		sp.Attrs["abhed.unfinished"] = true
		e.out = append(e.out, sp)
		delete(e.open, k)
	}
}

// ---------------------------------------------------------------- shipping

func (e *Exporter) flush() {
	e.mu.Lock()
	batch := e.out
	e.out = nil
	e.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	body, err := json.Marshal(e.otlp(batch))
	if err != nil {
		e.Failed.Add(int64(len(batch)))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.HTTPClient.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(e.cfg.Endpoint, "/")+"/v1/traces", bytes.NewReader(body))
	if err != nil {
		e.Failed.Add(int64(len(batch)))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		e.Failed.Add(int64(len(batch)))
		e.cfg.Log.Warn("telemetry export failed", "spans", len(batch), "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.Failed.Add(int64(len(batch)))
		e.cfg.Log.Warn("telemetry export refused", "spans", len(batch), "status", resp.StatusCode)
		return
	}
	e.Exported.Add(int64(len(batch)))
}

// otlp renders a batch as OTLP/HTTP JSON (ExportTraceServiceRequest).
func (e *Exporter) otlp(batch []*span) map[string]any {
	spans := make([]map[string]any, 0, len(batch))
	for _, sp := range batch {
		s := map[string]any{
			"traceId":           sp.TraceID,
			"spanId":            sp.SpanID,
			"name":              sp.Name,
			"kind":              sp.Kind,
			"startTimeUnixNano": nanos(sp.Start),
			"endTimeUnixNano":   nanos(sp.End),
			"attributes":        kv(sp.Attrs),
			"status":            map[string]any{"code": sp.Status, "message": sp.StatusMsg},
		}
		if sp.ParentID != "" {
			s["parentSpanId"] = sp.ParentID
		}
		if len(sp.Events) > 0 {
			evs := make([]map[string]any, 0, len(sp.Events))
			for _, se := range sp.Events {
				evs = append(evs, map[string]any{
					"name": se.Name, "timeUnixNano": nanos(se.At), "attributes": kv(se.Attrs),
				})
			}
			s["events"] = evs
		}
		spans = append(spans, s)
	}
	return map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{"attributes": kv(map[string]any{
				"service.name": e.cfg.ServiceName,
			})},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "abhed"},
				"spans": spans,
			}},
		}},
	}
}

// kv renders attributes in OTLP's typed form.
func kv(m map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(m))
	for k, v := range m {
		var val map[string]any
		switch x := v.(type) {
		case string:
			val = map[string]any{"stringValue": x}
		case bool:
			val = map[string]any{"boolValue": x}
		case int:
			val = map[string]any{"intValue": x}
		case int64:
			val = map[string]any{"intValue": x}
		case float64:
			if x == float64(int64(x)) {
				val = map[string]any{"intValue": int64(x)}
			} else {
				val = map[string]any{"doubleValue": x}
			}
		default:
			b, _ := json.Marshal(x)
			val = map[string]any{"stringValue": string(b)}
		}
		out = append(out, map[string]any{"key": k, "value": val})
	}
	return out
}

func nanos(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return jsonInt(t.UnixNano())
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func flatten(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		switch v.(type) {
		case string, bool, float64, int, int64:
			out[k] = v
		default:
			b, _ := json.Marshal(v)
			if len(b) <= 512 {
				out[k] = string(b)
			}
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
