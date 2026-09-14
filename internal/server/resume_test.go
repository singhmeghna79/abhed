package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuvrajsingh/abhed/internal/agent"
	"github.com/yuvrajsingh/abhed/internal/config"
	"github.com/yuvrajsingh/abhed/internal/store"
	"github.com/yuvrajsingh/abhed/internal/tools"
)

// durableMem is the memory store plus the two things a durable store gives
// the server: session rows it can list, and an atomic claim on a finished
// session. It is what Postgres does, in a map.
type durableMem struct {
	*agent.MemStore
	mu    sync.Mutex
	rows  map[string]store.SessionRecord
	ended map[string]bool
}

func (d *durableMem) CreateSession(ctx context.Context, r store.SessionRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows[r.ID] = r
	return nil
}
func (d *durableMem) ListSessions(ctx context.Context, limit int) ([]store.SessionRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]store.SessionRecord, 0, len(d.rows))
	for _, r := range d.rows {
		out = append(out, r)
	}
	return out, nil
}
func (d *durableMem) Append(ev agent.Event) error {
	if ev.Type == agent.EvSessionEnded {
		d.mu.Lock()
		d.ended[ev.SessionID] = true
		d.mu.Unlock()
	}
	return d.MemStore.Append(ev)
}
func (d *durableMem) ClaimResume(ctx context.Context, id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.rows[id]; !ok || !d.ended[id] {
		return false, nil
	}
	d.ended[id] = false
	return true, nil
}

// A session that ended with the process that ran it used to be a dead end:
// "session not found — it may have ended with this server process". The
// record is the conversation, so a finished session is continued from it —
// by this process after a restart, or by another node — with one monotonic
// event sequence and the original owner still the only one who can.
func TestFinishedSessionContinuesFromRecord(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.Mode = "proxy"
	st := &durableMem{MemStore: agent.NewMemStore(), rows: map[string]store.SessionRecord{}, ended: map[string]bool{}}
	s := New(Options{Workspace: t.TempDir(), Config: cfg, Adapter: stubAdapter{},
		Registry: tools.NewRegistry(tools.Read{}, tools.Glob{}), Store: st})
	h := s.Handler()

	do := func(method, path, user, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("X-Abhed-User", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	rec := do("POST", "/v1/sessions", "alice", `{"prompt":"first question"}`)
	var out struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.SessionID == "" {
		t.Fatalf("no session: %s", rec.Body.String())
	}
	// Let the stub adapter finish the run and the process forget it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.RLock()
		live := s.running[out.SessionID]
		s.mu.RUnlock()
		live.mu.Lock()
		done := live.State == "done"
		live.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	delete(s.running, out.SessionID) // as a restart, or another node, would see it
	s.mu.Unlock()

	var before []json.RawMessage
	json.Unmarshal(do("GET", "/v1/sessions/"+out.SessionID+"/replay", "alice", "").Body.Bytes(), &before)
	if len(before) == 0 {
		t.Fatal("no events recorded for the first run")
	}

	// Another user cannot continue it.
	if w := do("POST", "/v1/sessions/"+out.SessionID+"/messages", "bob", `{"prompt":"mine now"}`); w.Code != http.StatusNotFound {
		t.Fatalf("bob continued alice's session: %d %s", w.Code, w.Body.String())
	}
	// The owner can, and the process that forgot it picks it up from the record.
	w := do("POST", "/v1/sessions/"+out.SessionID+"/messages", "alice", `{"prompt":"second question"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("owner could not continue a finished session: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(300 * time.Millisecond)

	var after []struct {
		Seq  int64  `json:"seq"`
		Type string `json:"type"`
	}
	json.Unmarshal(do("GET", "/v1/sessions/"+out.SessionID+"/replay", "alice", "").Body.Bytes(), &after)
	if len(after) <= len(before) {
		t.Fatalf("continuation recorded nothing: %d events before, %d after", len(before), len(after))
	}
	for i := 1; i < len(after); i++ {
		if after[i].Seq != after[i-1].Seq+1 {
			t.Fatalf("sequence broke at %d → %d: a continuation must extend the record, not restart it", after[i-1].Seq, after[i].Seq)
		}
	}
	found := false
	for _, ev := range after[len(before):] {
		if ev.Type == "user.message" {
			found = true
		}
	}
	if !found {
		t.Error("the second question was not recorded in the same session")
	}

	// While it is running here, a second continuer — the same request landing
	// on another node — is refused, not doubled.
	if w := do("POST", "/v1/sessions/"+out.SessionID+"/messages", "alice", `{"prompt":"again"}`); w.Code == http.StatusNotFound {
		t.Fatalf("a running continuation went missing: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(300 * time.Millisecond)
	s.mu.Lock()
	delete(s.running, out.SessionID) // this node forgets it…
	s.mu.Unlock()
	st.mu.Lock()
	st.ended[out.SessionID] = false // …while the store says another node holds it
	st.mu.Unlock()
	if w := do("POST", "/v1/sessions/"+out.SessionID+"/messages", "alice", `{"prompt":"third"}`); w.Code != http.StatusConflict {
		t.Fatalf("a session claimed elsewhere was continued twice: %d %s", w.Code, w.Body.String())
	}
}
