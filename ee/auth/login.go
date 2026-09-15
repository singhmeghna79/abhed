package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
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
// PKCE is used even though Abhed has a client secret. A public redirect URL and
// an authorization code in a browser URL bar is exactly the shape PKCE exists
// to protect, and the cost is one hash.

// Presets for the providers people actually sign in with.
//
// Google and Microsoft are ordinary OIDC providers, so "sign in with Gmail" or
// "sign in with Outlook" needs no special code — only the right issuer and the
// claim that carries the tenant. Naming them here saves an operator looking up
// three URLs and getting the tenant claim wrong.
var Presets = map[string]struct {
	Issuer      string
	TenantClaim string
	Scopes      []string
	Label       string
}{
	"google": {
		Issuer:      "https://accounts.google.com",
		TenantClaim: "hd", // hosted domain; absent for personal accounts
		Scopes:      []string{"openid", "profile", "email"},
		Label:       "Google",
	},
	"microsoft": {
		// The "common" tenant accepts both work/school and personal accounts,
		// which is what people mean by "sign in with Outlook".
		Issuer:      "https://login.microsoftonline.com/common/v2.0",
		TenantClaim: "tid",
		Scopes:      []string{"openid", "profile", "email"},
		Label:       "Microsoft",
	},
	"github": {
		Issuer:      "https://token.actions.githubusercontent.com",
		TenantClaim: "",
		Scopes:      []string{"openid"},
		Label:       "GitHub",
	},
}

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
	// PostLogoutURL is where the IdP returns after ending its session.
	// Providers require it to be pre-registered.
	PostLogoutURL string
	Scopes        []string
	// Endpoints overrides discovery.
	Endpoints *Endpoints
	// CookieName holds the browser session. Defaults to abhed_session.
	CookieName string
	// SessionTTL bounds how long a browser session lives regardless of token
	// expiry, so a closed laptop does not stay signed in indefinitely.
	SessionTTL time.Duration
	// Secure marks the cookie Secure. Off only for local HTTP development.
	Secure     bool
	HTTPClient *http.Client
	// Label names the identity provider on the sign-in button. Defaults to
	// the issuer's host, which is still more useful to a person than "SSO".
	Label string
}

func (c *LoginConfig) applyDefaults() {
	if c.CookieName == "" {
		c.CookieName = "abhed_session"
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
	if c.Label == "" {
		c.Label = ProviderLabel("", c.Issuer)
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
	// prompt=login forces a credential prompt even when the IdP has an active
	// session, which is the only way to switch users. prompt=select_account
	// offers a chooser where the provider supports one.
	if p := r.URL.Query().Get("prompt"); p == "login" || p == "select_account" ||
		p == "consent" || p == "none" {
		q.Set("prompt", p)
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
	// Ending the LOCAL session is not enough to switch users. The IdP still
	// holds its own session, so clicking "sign in" again returns the same
	// person without a prompt — which reads as logout being broken.
	//
	// RP-initiated logout needs post_logout_redirect_uri, and providers ignore
	// it unless client_id is present too. Without both, Keycloak and Entra
	// return the user straight back still authenticated.
	if l.eps.EndSession != "" {
		q := url.Values{"client_id": {l.cfg.ClientID}}
		if l.cfg.PostLogoutURL != "" {
			q.Set("post_logout_redirect_uri", l.cfg.PostLogoutURL)
		}
		sep := "?"
		if strings.Contains(l.eps.EndSession, "?") {
			sep = "&"
		}
		http.Redirect(w, r, l.eps.EndSession+sep+q.Encode(), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// ForceReauth sends the user to the IdP with prompt=login, so they are asked
// for credentials even if the provider still has them signed in.
//
// This is what "sign in as someone else" actually needs: logout clears Abhed's
// session, but only prompt=login makes the IdP stop auto-approving.
func (l *Login) ForceReauth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("prompt", "login")
	r.URL.RawQuery = q.Encode()
	l.Start(w, r)
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

// loginError renders a sign-in failure.
//
// Both arguments can be attacker-controlled: Callback passes the `error` and
// `error_description` query parameters straight through, and this page is
// served from the same origin as the session cookie. Interpolating them into
// HTML unescaped is reflected XSS on the one origin where it matters most, so
// they are escaped here rather than at each call site — the call sites are
// where someone will forget.
func loginError(w http.ResponseWriter, code int, kind, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The same policy the console and landing page carry: even with the
	// escaping above, this page should not be able to load or execute anything
	// external if a future edit reintroduces an injection.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(code)
	fmt.Fprintf(w, loginErrorHTML,
		template.HTMLEscapeString(kind), template.HTMLEscapeString(detail))
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

// ProviderLabel names the identity provider for a sign-in button. A known
// preset gets its brand name; anything else gets its issuer host, which is
// still more useful to a person than a generic "Sign in with SSO".
func ProviderLabel(provider, issuer string) string {
	if p, found := Presets[strings.ToLower(provider)]; found {
		return p.Label
	}
	if issuer != "" {
		if u, err := url.Parse(issuer); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return "SSO"
}

// Login satisfies Provider, so the middleware and the server can hold it
// without naming it. Everything below delegates to the handlers above.

func (l *Login) Name() string { return "oidc" }

func (l *Login) Identify(r *http.Request) (*Identity, bool) { return l.FromCookie(r) }

func (l *Login) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", l.Start)
	mux.HandleFunc("GET /auth/callback", l.Callback)
	mux.HandleFunc("GET /switch-user", l.ForceReauth)
}

// PublicPaths: the redirect out and the callback back are, by definition,
// reached by someone who is not yet signed in. /switch-user is not listed —
// it is for a person who already has a session and wants a different one.
func (l *Login) PublicPaths() []string { return []string{"/login", "/auth/callback"} }

func (l *Login) SignIn() (url, label string) { return "/login", l.cfg.Label }

func (l *Login) SignOut(w http.ResponseWriter, r *http.Request) { l.Logout(w, r) }

// Check fetches the signing keys, which is the one thing that has to work
// before the first person can sign in. It is what `abhed doctor` reports.
func (l *Login) Check(ctx context.Context) error { return l.verifier.Refresh(ctx) }
