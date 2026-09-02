package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Browser login: OIDC authorization code with PKCE.
//
// Verifier already validates a bearer token, which is what an API client or a
// reverse proxy provides. A person opening the console has no token, so this
// completes the picture: redirect to the IdP, exchange the code, and hold the
// resulting identity in a cookie-backed session.
//
// PKCE is used even though Titan has a client secret. A public redirect URL and
// an authorization code in a browser URL bar is exactly the shape PKCE exists
// to protect, and the cost is one hash.

// Endpoints are discovered from the issuer, or configured explicitly for an
// air-gapped deployment where the well-known document is unreachable.
type Endpoints struct {
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	EndSession    string `json:"end_session_endpoint"`
	JWKS          string `json:"jwks_uri"`
	Issuer        string `json:"issuer"`
}

type LoginConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// Endpoints overrides discovery.
	Endpoints *Endpoints
	// CookieName holds the browser session. Defaults to titan_session.
	CookieName string
	// SessionTTL bounds how long a browser session lives regardless of token
	// expiry, so a closed laptop does not stay signed in indefinitely.
	SessionTTL time.Duration
	// Secure marks the cookie Secure. Off only for local HTTP development.
	Secure     bool
	HTTPClient *http.Client
}

func (c *LoginConfig) applyDefaults() {
	if c.CookieName == "" {
		c.CookieName = "titan_session"
	}
	if c.SessionTTL == 0 {
		c.SessionTTL = 12 * time.Hour
	}
	if len(c.Scopes) == 0 {
		c.Scopes = []string{"openid", "profile", "email"}
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
}

// Login handles the browser side of authentication.
type Login struct {
	cfg      LoginConfig
	verifier *Verifier
	eps      Endpoints

	mu       sync.RWMutex
	pending  map[string]*pendingAuth // state -> PKCE verifier
	sessions map[string]*browserSession
}

type pendingAuth struct {
	Verifier string
	Return   string
	Created  time.Time
}

type browserSession struct {
	Identity *Identity
	Expires  time.Time
}

func NewLogin(cfg LoginConfig, v *Verifier) (*Login, error) {
	cfg.applyDefaults()
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("auth: client_id is required for browser login")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("auth: redirect_url is required for browser login")
	}

	l := &Login{
		cfg:      cfg,
		verifier: v,
		pending:  map[string]*pendingAuth{},
		sessions: map[string]*browserSession{},
	}
	if cfg.Endpoints != nil {
		l.eps = *cfg.Endpoints
	} else if err := l.discover(context.Background()); err != nil {
		return nil, err
	}
	go l.reap()
	return l, nil
}

func (l *Login) discover(ctx context.Context) error {
	u := strings.TrimSuffix(l.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := l.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("OIDC discovery at %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC discovery returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&l.eps); err != nil {
		return fmt.Errorf("decode discovery document: %w", err)
	}
	if l.eps.Issuer != l.cfg.Issuer {
		return fmt.Errorf("%w: discovery says %q, configured %q",
			ErrWrongIssuer, l.eps.Issuer, l.cfg.Issuer)
	}
	return nil
}

// reap drops expired pending authorizations and browser sessions. Without it a
// long-lived server accumulates state for every abandoned login attempt.
func (l *Login) reap() {
	for range time.Tick(5 * time.Minute) {
		now := time.Now()
		l.mu.Lock()
		for k, p := range l.pending {
			if now.Sub(p.Created) > 10*time.Minute {
				delete(l.pending, k)
			}
		}
		for k, s := range l.sessions {
			if now.After(s.Expires) {
				delete(l.sessions, k)
			}
		}
		l.mu.Unlock()
	}
}

// Start begins the login flow.
func (l *Login) Start(w http.ResponseWriter, r *http.Request) {
	state := randomToken()
	verifier := randomToken() + randomToken() // 64 bytes of entropy
	challenge := s256(verifier)

	returnTo := r.URL.Query().Get("return")
	if !safeReturn(returnTo) {
		returnTo = "/"
	}

	l.mu.Lock()
	l.pending[state] = &pendingAuth{Verifier: verifier, Return: returnTo, Created: time.Now()}
	l.mu.Unlock()

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {l.cfg.ClientID},
		"redirect_uri":          {l.cfg.RedirectURL},
		"scope":                 {strings.Join(l.cfg.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, l.eps.Authorization+"?"+q.Encode(), http.StatusFound)
}

// Callback completes the flow: validate state, exchange the code, verify the
// returned id_token, and issue a session cookie.
func (l *Login) Callback(w http.ResponseWriter, r *http.Request) {
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		loginError(w, http.StatusUnauthorized, errParam,
			orDefault(desc, "The identity provider rejected the sign-in."))
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		loginError(w, http.StatusBadRequest, "invalid_request",
			"The callback is missing its state or code. Start again from /login.")
		return
	}

	l.mu.Lock()
	pend, found := l.pending[state]
	delete(l.pending, state)
	l.mu.Unlock()
	if !found {
		// An unknown state is either a replay, a CSRF attempt, or a login that
		// sat idle past the ten-minute window.
		loginError(w, http.StatusBadRequest, "invalid_state",
			"This sign-in link is no longer valid. Start again from /login.")
		return
	}

	idToken, err := l.exchange(r.Context(), code, pend.Verifier)
	if err != nil {
		loginError(w, http.StatusUnauthorized, "exchange_failed", err.Error())
		return
	}

	identity, err := l.verifier.Verify(r.Context(), idToken)
	if err != nil {
		loginError(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}

	sid := randomToken()
	l.mu.Lock()
	l.sessions[sid] = &browserSession{
		Identity: identity,
		Expires:  time.Now().Add(l.cfg.SessionTTL),
	}
	l.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     l.cfg.CookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   l.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(l.cfg.SessionTTL),
	})
	http.Redirect(w, r, pend.Return, http.StatusFound)
}

type tokenResponse struct {
	IDToken          string `json:"id_token"`
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (l *Login) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {l.cfg.RedirectURL},
		"client_id":     {l.cfg.ClientID},
		"code_verifier": {verifier},
	}
	if l.cfg.ClientSecret != "" {
		form.Set("client_secret", l.cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.eps.Token,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := l.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("%s: %s", tr.Error, tr.ErrorDescription)
	}
	if tr.IDToken == "" {
		return "", fmt.Errorf("token response contained no id_token — check that the " +
			"'openid' scope is granted to this client")
	}
	return tr.IDToken, nil
}

// Logout clears the browser session and, where the provider supports it,
// redirects on to end the session at the IdP too.
func (l *Login) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(l.cfg.CookieName); err == nil {
		l.mu.Lock()
		delete(l.sessions, c.Value)
		l.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: l.cfg.CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: l.cfg.Secure, MaxAge: -1,
	})
	if l.eps.EndSession != "" {
		http.Redirect(w, r, l.eps.EndSession, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// FromCookie resolves a browser session to an identity.
func (l *Login) FromCookie(r *http.Request) (*Identity, bool) {
	c, err := r.Cookie(l.cfg.CookieName)
	if err != nil {
		return nil, false
	}
	l.mu.RLock()
	sess, found := l.sessions[c.Value]
	l.mu.RUnlock()
	if !found || time.Now().After(sess.Expires) {
		return nil, false
	}
	return sess.Identity, true
}

// Whoami reports the signed-in user, for the console header.
func (l *Login) Whoami(w http.ResponseWriter, r *http.Request) {
	id, ok := l.FromCookie(r)
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"subject":       id.Subject,
		"email":         id.Email,
		"name":          id.Name,
		"tenant":        id.Tenant,
		"groups":        id.Groups,
	})
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// A failing CSPRNG is not something to paper over with a weaker source.
		panic("auth: system random source unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func s256(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// safeReturn rejects an open redirect: only same-origin paths are allowed back.
func safeReturn(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func loginError(w http.ResponseWriter, code int, kind, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, loginErrorHTML, kind, detail)
}

const loginErrorHTML = `<!doctype html><meta charset="utf-8">
<title>Sign-in failed</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;
  background:#0B0E13;color:#E8EDF4;
  font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif}
@media (prefers-color-scheme:light){body{background:#F5F7FA;color:#0F141B}}
.card{max-width:460px;padding:28px 32px;border-radius:10px;
  background:#141922;border:1px solid #252D3A}
@media (prefers-color-scheme:light){.card{background:#fff;border-color:#DCE3EC}}
h1{margin:0 0 6px;font-size:16px}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;
  color:#E05A52}
p{margin:10px 0 0;color:#8A96A8}
a{color:#4C8FD6}
</style>
<div class="card">
  <h1>Sign-in failed</h1>
  <code>%s</code>
  <p>%s</p>
  <p><a href="/login">Try again</a></p>
</div>`
