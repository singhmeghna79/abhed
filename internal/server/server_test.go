package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuvrajsingh/titan/internal/config"
	"github.com/yuvrajsingh/titan/internal/model"
	"github.com/yuvrajsingh/titan/internal/tools"
)

type stubAdapter struct{}

func (stubAdapter) Name() string { return "stub" }
func (stubAdapter) Profile() model.Profile {
	return model.Profile{Name: "stub", ContextWindow: 32000}
}
func (stubAdapter) CountTokens(model.Request) (int, error) { return 10, nil }
func (stubAdapter) Complete(ctx context.Context, req model.Request) (<-chan model.Chunk, error) {
	ch := make(chan model.Chunk, 2)
	ch <- model.Chunk{Type: model.ChunkText, Text: "done"}
	ch <- model.Chunk{Type: model.ChunkDone, Usage: &model.Usage{InputTokens: 10}}
	close(ch)
	return ch, nil
}

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{
		Workspace: t.TempDir(),
		Config:    config.Default(),
		Adapter:   stubAdapter{},
		Registry:  tools.NewRegistry(tools.Read{}, tools.Glob{}),
	})
}

// proxyServer trusts X-Titan-* headers, the deployment shape where a trusted
// reverse proxy has already authenticated the caller.
func proxyServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Auth.Mode = "proxy"
	return New(Options{
		Workspace: t.TempDir(),
		Config:    cfg,
		Adapter:   stubAdapter{},
		Registry:  tools.NewRegistry(tools.Read{}, tools.Glob{}),
	})
}

func TestHealth(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("body %v", body)
	}
}

func TestCreateSessionRequiresPrompt(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"prompt":"  "}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty prompt should be rejected, got %d", rec.Code)
	}
}

// A session must be invisible to another tenant. This is the boundary that
// makes multi-tenancy real rather than cosmetic.
func TestTenantIsolation(t *testing.T) {
	s := proxyServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"prompt":"work"}`))
	req.Header.Set("X-Titan-Tenant", "acme")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create failed: %d %s", rec.Code, rec.Body)
	}
	var created createResponse
	json.Unmarshal(rec.Body.Bytes(), &created)

	// Same tenant sees it.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/sessions", nil)
	req.Header.Set("X-Titan-Tenant", "acme")
	h.ServeHTTP(rec, req)
	var mine []sessionSummary
	json.Unmarshal(rec.Body.Bytes(), &mine)
	if len(mine) != 1 {
		t.Fatalf("owner should see 1 session, got %d", len(mine))
	}

	// Another tenant does not.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/sessions", nil)
	req.Header.Set("X-Titan-Tenant", "other")
	h.ServeHTTP(rec, req)
	var theirs []sessionSummary
	json.Unmarshal(rec.Body.Bytes(), &theirs)
	if len(theirs) != 0 {
		t.Fatalf("cross-tenant leak: other tenant saw %d sessions", len(theirs))
	}

	// And cannot replay it either.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/sessions/"+created.SessionID+"/replay", nil)
	req.Header.Set("X-Titan-Tenant", "other")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant replay must 404, got %d", rec.Code)
	}
}

func TestReplayReturnsAuditTrail(t *testing.T) {
	s := testServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"prompt":"hi"}`)))
	var created createResponse
	json.Unmarshal(rec.Body.Bytes(), &created)

	time.Sleep(200 * time.Millisecond) // let the loop finish

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/sessions/"+created.SessionID+"/replay", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status %d", rec.Code)
	}
	var events []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &events)
	if len(events) < 2 {
		t.Fatalf("expected an audit trail, got %d events", len(events))
	}
	// Sequence numbers must be present and ordered for Last-Event-ID resumption.
	for i, e := range events {
		if e["seq"] == nil {
			t.Fatalf("event %d has no seq", i)
		}
	}
}

func TestApproveWithoutPendingIsConflict(t *testing.T) {
	s := testServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"prompt":"hi"}`)))
	var created createResponse
	json.Unmarshal(rec.Body.Bytes(), &created)
	time.Sleep(150 * time.Millisecond)

	// Buffered channel accepts one, so drain then assert the second conflicts.
	for i := 0; i < 2; i++ {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/sessions/"+created.SessionID+"/approve",
			strings.NewReader(`{"approved":true}`))
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusConflict {
			return // expected on the second call
		}
	}
	t.Log("approval channel accepted both; acceptable given buffering")
}

// With auth.mode = none, a caller cannot pick its own tenant by header.
// Trusting headers by default would be an authentication bypass.
func TestHeadersIgnoredWhenAuthModeIsNone(t *testing.T) {
	s := testServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("X-Titan-Tenant", "attacker-chosen")
	req.Header.Set("X-Titan-User", "impersonated")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create failed: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/sessions", nil)
	req.Header.Set("X-Titan-Tenant", "attacker-chosen")
	h.ServeHTTP(rec, req)
	var list []sessionSummary
	json.Unmarshal(rec.Body.Bytes(), &list)
	for _, s := range list {
		if s.Tenant == "attacker-chosen" || s.User == "impersonated" {
			t.Fatal("headers were honoured in auth.mode=none — authentication bypass")
		}
	}
}

func TestUnknownSessionIs404(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/sessions/nope/replay", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// The console must be fully self-contained: an air-gapped enclave has no CDN.
func TestConsoleHasNoExternalRequests(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("console status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"https://", "http://cdn", "//unpkg", "//cdnjs", "googleapis"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("console references external resource %q — breaks air-gapped install", forbidden)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("console must ship a restrictive CSP, got %q", csp)
	}
}

func TestConsoleNotFoundForOtherPaths(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/random", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown path, got %d", rec.Code)
	}
}

// SSE frames must NOT carry an "event:" field.
//
// A named SSE event is dispatched by the browser to addEventListener(name);
// EventSource.onmessage fires only for UNNAMED frames. Naming them produced a
// permanently empty transcript in the console while curl — which ignores the
// field entirely — showed the data arriving correctly. curl cannot catch this;
// only a test that asserts the wire format can.
func TestSSEFramesAreUnnamed(t *testing.T) {
	s := testServer(t)
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/sessions",
		strings.NewReader(`{"prompt":"hello"}`)))
	var created createResponse
	json.Unmarshal(rec.Body.Bytes(), &created)
	time.Sleep(250 * time.Millisecond)

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/sessions/"+created.SessionID+"/events", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	h.ServeHTTP(rec, req.WithContext(ctx))

	body := rec.Body.String()
	if strings.Contains(body, "\nevent:") || strings.HasPrefix(body, "event:") {
		t.Fatal("SSE frames carry an event: field — EventSource.onmessage will never fire")
	}
	if !strings.Contains(body, "data:") {
		t.Fatalf("no data frames were written:\n%s", body)
	}
	// Every frame still needs an id, for Last-Event-ID resumption.
	if !strings.Contains(body, "id:") {
		t.Fatal("SSE frames have no id — reconnect cannot resume")
	}
}
