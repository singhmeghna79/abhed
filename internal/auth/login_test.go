package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockIDP implements enough of an OIDC provider to drive the browser flow:
// discovery, JWKS, authorization and token endpoints — with real signatures.
type mockIDP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	lastPKCE  string
	issuedFor string
}

func newMockIDP(t *testing.T) *mockIDP {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	m := &mockIDP{key: key}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Endpoints{
			Issuer:        m.srv.URL,
			Authorization: m.srv.URL + "/authorize",
			Token:         m.srv.URL + "/token",
			JWKS:          m.srv.URL + "/jwks",
			EndSession:    m.srv.URL + "/logout",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		m.lastPKCE = r.URL.Query().Get("code_challenge")
		// Bounce straight back with a code, as a real IdP would after consent.
		back := r.URL.Query().Get("redirect_uri") +
			"?code=test-code&state=" + r.URL.Query().Get("state")
		http.Redirect(w, r, back, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		// Verify PKCE the way a real provider does.
		if s256(r.Form.Get("code_verifier")) != m.lastPKCE {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant",
				"error_description": "PKCE verification failed"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": m.mint(t)})
	})

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockIDP) mint(t *testing.T) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	c, _ := json.Marshal(map[string]any{
		"iss": m.srv.URL, "aud": "titan", "sub": "user-7",
		"email": "yuvraj@example.com", "name": "Yuvraj Singh",
		"tenant": "acme", "groups": []string{"engineering"},
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	signing := base64.RawURLEncoding.EncodeToString(h) + "." +
		base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, m.key, 5, sum[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newLogin(t *testing.T, idp *mockIDP) *Login {
	t.Helper()
	v, err := NewVerifier(Config{Issuer: idp.srv.URL, Audience: "titan"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLogin(LoginConfig{
		Issuer: idp.srv.URL, ClientID: "titan", ClientSecret: "s3cret",
		RedirectURL: "http://localhost:8420/auth/callback",
	}, v)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The full flow: /login → IdP → /auth/callback → session cookie.
func TestBrowserLoginFlow(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	// Step 1: /login redirects to the IdP with PKCE.
	rec := httptest.NewRecorder()
	l.Start(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE challenge missing from the authorization request")
	}
	if q.Get("state") == "" {
		t.Fatal("state missing — CSRF protection absent")
	}

	// Step 2: the IdP redirects back with a code.
	resp, err := http.Get(loc.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Step 3: the callback exchanges it and sets a cookie.
	rec = httptest.NewRecorder()
	cb := httptest.NewRequest("GET", "/auth/callback?code=test-code&state="+q.Get("state"), nil)
	l.Callback(rec, cb)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback failed: %d %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie issued")
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly — JavaScript must not read it")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie should be SameSite=Lax")
	}

	// Step 4: the cookie resolves to the verified identity.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	id, ok := l.FromCookie(req)
	if !ok {
		t.Fatal("cookie did not resolve to a session")
	}
	if id.Subject != "user-7" || id.Tenant != "acme" || id.Email != "yuvraj@example.com" {
		t.Fatalf("identity wrong: %+v", id)
	}
	t.Logf("signed in as %s (%s) tenant=%s groups=%v", id.Name, id.Email, id.Tenant, id.Groups)
}

// An unknown state is a replay, a CSRF attempt, or an abandoned login.
func TestCallbackRejectsUnknownState(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	rec := httptest.NewRecorder()
	l.Callback(rec, httptest.NewRequest("GET", "/auth/callback?code=x&state=forged", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged state must be rejected, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no longer valid") {
		t.Fatalf("error should be actionable: %s", rec.Body.String())
	}
}

// State is single-use: replaying a captured callback must fail.
func TestStateIsSingleUse(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	rec := httptest.NewRecorder()
	l.Start(rec, httptest.NewRequest("GET", "/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	http.Get(loc.String())

	first := httptest.NewRecorder()
	l.Callback(first, httptest.NewRequest("GET", "/auth/callback?code=c&state="+state, nil))
	if first.Code != http.StatusFound {
		t.Fatalf("first callback should succeed: %d", first.Code)
	}
	second := httptest.NewRecorder()
	l.Callback(second, httptest.NewRequest("GET", "/auth/callback?code=c&state="+state, nil))
	if second.Code == http.StatusFound {
		t.Fatal("REPLAY: the same state was accepted twice")
	}
}

// A return path must not be usable to bounce the user off-site.
func TestOpenRedirectRejected(t *testing.T) {
	for _, bad := range []string{
		"https://evil.example/steal", "//evil.example", "http://evil.example",
	} {
		if safeReturn(bad) {
			t.Errorf("OPEN REDIRECT: %q accepted as a return path", bad)
		}
	}
	for _, good := range []string{"/", "/console", "/v1/sessions"} {
		if !safeReturn(good) {
			t.Errorf("same-origin path %q wrongly rejected", good)
		}
	}
}

func TestLogoutClearsSession(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	rec := httptest.NewRecorder()
	l.Start(rec, httptest.NewRequest("GET", "/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	http.Get(loc.String())
	cb := httptest.NewRecorder()
	l.Callback(cb, httptest.NewRequest("GET", "/auth/callback?code=c&state="+loc.Query().Get("state"), nil))
	cookie := cb.Result().Cookies()[0]

	out := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/logout", nil)
	req.AddCookie(cookie)
	l.Logout(out, req)

	check := httptest.NewRequest("GET", "/", nil)
	check.AddCookie(cookie)
	if _, ok := l.FromCookie(check); ok {
		t.Fatal("session survived logout")
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	idp := newMockIDP(t)
	v, _ := NewVerifier(Config{Issuer: idp.srv.URL, Audience: "titan"})
	l, err := NewLogin(LoginConfig{
		Issuer: idp.srv.URL, ClientID: "titan",
		RedirectURL: "http://localhost:8420/auth/callback",
		SessionTTL:  time.Millisecond,
	}, v)
	if err != nil {
		t.Fatal(err)
	}

	l.mu.Lock()
	l.sessions["sid"] = &browserSession{
		Identity: &Identity{Subject: "u"}, Expires: time.Now().Add(-time.Minute)}
	l.mu.Unlock()

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "titan_session", Value: "sid"})
	if _, ok := l.FromCookie(req); ok {
		t.Fatal("an expired browser session must not authenticate")
	}
}

// PKCE must actually be verified end to end, not merely sent.
func TestPKCEIsEnforced(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	rec := httptest.NewRecorder()
	l.Start(rec, httptest.NewRequest("GET", "/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	state := loc.Query().Get("state")
	http.Get(loc.String())

	// Corrupt the stored verifier: the token endpoint must now refuse.
	l.mu.Lock()
	l.pending[state].Verifier = "wrong-verifier"
	l.mu.Unlock()

	out := httptest.NewRecorder()
	l.Callback(out, httptest.NewRequest("GET", "/auth/callback?code=c&state="+state, nil))
	if out.Code == http.StatusFound {
		t.Fatal("a mismatched PKCE verifier was accepted")
	}
}

func TestWhoamiReportsSignedOut(t *testing.T) {
	idp := newMockIDP(t)
	l := newLogin(t, idp)

	rec := httptest.NewRecorder()
	l.Whoami(rec, httptest.NewRequest("GET", "/v1/whoami", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when signed out, got %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["authenticated"] != false {
		t.Fatalf("body should say authenticated:false, got %v", body)
	}
}
