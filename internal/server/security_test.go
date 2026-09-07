package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests exist because the server is reachable from the public internet.
// Each one pins a control that, if it silently regressed, would not break any
// feature — which is exactly why a test has to be the thing that notices.

// A client asking for a weaker permission mode is asking to disable the
// approval gate on an agent that runs shell commands. The request body is
// attacker-controlled, so the mode in it is a request, not an instruction.
func TestClientCannotEscalatePermissionMode(t *testing.T) {
	for _, mode := range []string{"bypass", "auto", "accept-edits"} {
		t.Run(mode, func(t *testing.T) {
			s := testServer(t)
			body := fmt.Sprintf(`{"prompt":"hi","mode":%q}`, mode)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(
				"POST", "/v1/sessions", strings.NewReader(body)))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("mode %q was accepted with status %d — "+
					"a caller can disable the approval gate", mode, rec.Code)
			}
		})
	}
}

// plan is strictly read-only, so a client narrowing itself to it is safe and
// must keep working: the rule is "never widen", not "never choose".
func TestClientMayNarrowToPlanMode(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(
		"POST", "/v1/sessions", strings.NewReader(`{"prompt":"hi","mode":"plan"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("plan mode rejected with %d; narrowing must stay allowed", rec.Code)
	}
}

// An unknown mode must be refused rather than quietly ignored: falling back to
// the configured mode would let a typo read as success while running with
// permissions the caller did not ask for.
func TestUnknownModeIsRejected(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(
		"POST", "/v1/sessions", strings.NewReader(`{"prompt":"hi","mode":"paln"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unknown mode accepted with %d", rec.Code)
	}
}

// Session IDs used to be a nanosecond timestamp in base36, which is monotonic
// and therefore enumerable: one ID narrows the search for the next.
func TestSessionIDsAreNotGuessable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newSessionID()
		if seen[id] {
			t.Fatalf("duplicate session id %q", id)
		}
		seen[id] = true
	}

	// Two IDs minted back to back must not share a long prefix; a timestamp
	// scheme would differ only in the last character or two.
	a, b := newSessionID(), newSessionID()
	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	if common > 6 {
		t.Fatalf("consecutive ids share a %d-char prefix (%q, %q) — "+
			"they look sequential, not random", common, a, b)
	}
}

// The session ID becomes a directory name. filepath.Join CLEANS "..", which
// resolves the traversal rather than refusing it, so the segment must be
// rejected before it is ever joined.
func TestUploadRejectsTraversalInSessionID(t *testing.T) {
	for _, id := range []string{"../escape", "../../etc", "a/b", ".", "..", "a b"} {
		if validSessionID(id) {
			t.Errorf("session id %q accepted as a path segment — "+
				"uploads could be written outside the upload directory", id)
		}
	}
	for _, id := range []string{"s-abc123", "staged-9f8e7d", "A_b-C"} {
		if !validSessionID(id) {
			t.Errorf("legitimate session id %q rejected", id)
		}
	}
}

// A cookie-authenticated console is only safe from cross-site writes if
// something checks where the request came from. SameSite=Lax does it today, but
// it is one flag change away from not doing so, and proxy-header deployments
// have no cookie to attach it to.
func TestCrossOriginWritesAreRejected(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("POST", "/v1/sessions",
		strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST accepted with %d — CSRF is possible", rec.Code)
	}
}

// A request from the console's own origin is the normal case and must pass.
func TestSameOriginWritesAreAllowed(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("POST", "/v1/sessions",
		strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("same-origin POST rejected with %d", rec.Code)
	}
}

// curl, the SDK and CI send no Origin at all. They are not the threat — CSRF
// needs a browser — so omitting the header must not break them.
func TestRequestsWithoutOriginAreAllowed(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(
		"POST", "/v1/sessions", strings.NewReader(`{"prompt":"hi"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("origin-less POST rejected with %d — API clients would break", rec.Code)
	}
}

// Headers must be present on every response, errors included: a 404 is still a
// response a browser will act on.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/", "/console", "/v1/health", "/nope"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
	}
}

// HSTS pins a browser to HTTPS for a year. Sent from a laptop on plain HTTP it
// would pin localhost to a scheme that does not answer, and the pin outlives
// the mistake — so it is only emitted when the operator says TLS is in front.
func TestHSTSOnlyWhenConfigured(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if h := rec.Header().Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HSTS sent without TLS configured: %q", h)
	}

	s.opts.Config.Server.HSTS = true
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if h := rec.Header().Get("Strict-Transport-Security"); h == "" {
		t.Error("HSTS not sent even though TLS is configured")
	}
}

// Sign-in is public by necessity and costs a bcrypt comparison per attempt, so
// unthrottled it is both a credential-stuffing endpoint and a way to burn the
// server's CPU for free.
func TestSignInIsRateLimited(t *testing.T) {
	l := newLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("attempt %d blocked while under the limit", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("limiter allowed a 4th attempt past a limit of 3")
	}
	// The limit is per-key, so one attacker must not lock out everyone else.
	if !l.allow("5.6.7.8") {
		t.Fatal("a different address was blocked by another's attempts")
	}
}

// X-Forwarded-For is attacker-controlled unless a proxy we trust set it. If it
// were read by default, a client could rotate its own limiter key and never be
// throttled at all.
func TestForwardedForIgnoredUnlessProxyTrusted(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/signin", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")

	if got := clientIP(r, false); got != "10.0.0.1" {
		t.Errorf("untrusted XFF was honoured: got %q, want the socket address", got)
	}
	if got := clientIP(r, true); got != "1.1.1.1" {
		t.Errorf("trusted XFF ignored: got %q", got)
	}
}

// A body with no cap can be streamed indefinitely into a decode buffer by an
// unauthenticated caller.
func TestOversizedBodyIsRejected(t *testing.T) {
	s := testServer(t)
	huge := `{"prompt":"` + strings.Repeat("A", (2<<20)) + `"}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(
		"POST", "/v1/sessions", strings.NewReader(huge)))

	if rec.Code == http.StatusAccepted {
		t.Fatal("a 2 MiB body was accepted; the request cap is not applied")
	}
}

// A handler panic must not take the connection down silently: without recovery
// the request vanishes from the log, which is the worst outcome for something
// internet-facing.
func TestPanicIsRecoveredAndLogged(t *testing.T) {
	s := testServer(t)
	h := s.withMiddleware(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic("boom") }))

	rec := httptest.NewRecorder()
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("panic escaped the middleware: %v", v)
		}
	}()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panic produced status %d, want 500", rec.Code)
	}
	// A Go stack trace names packages, paths and versions.
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic detail leaked to the client")
	}
}

// The overview is public so the landing page can describe the deployment before
// sign-in, but the workspace is an absolute host path: it names the operator's
// account and directory layout to anyone who asks.
func TestAnonymousOverviewHidesWorkspacePath(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/overview", nil))

	var o overviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if o.Workspace != "" {
		t.Errorf("workspace path disclosed anonymously: %q", o.Workspace)
	}
	// The rest must still be there, or the landing page cannot stay honest.
	if o.Model == "" {
		t.Error("overview no longer names the model")
	}
}
