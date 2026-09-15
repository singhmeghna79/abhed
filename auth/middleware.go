package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type ctxKey string

const identityKey ctxKey = "abhed.identity"

// FromContext returns the verified identity, if any.
func FromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityKey).(*Identity)
	return id, ok
}

// WithIdentity is exported for tests and for transports that authenticate
// outside HTTP.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// Middleware establishes the caller's identity on every request.
//
// Three modes, because deployments genuinely differ:
//
//   - providers or a verifier → a session or a token is required
//   - neither, TrustHeaders    → identity comes from proxy headers
//   - neither                  → anonymous single-tenant, for local dev
//
// The proxy-header path is only safe when a trusted proxy is the sole route to
// the port; it is not the default, and `abhed doctor` says which mode is live.
type Middleware struct {
	// Providers hold browser sessions, in the order they are asked. The first
	// with an entry point is where a browser without a session is sent.
	Providers []Provider
	// Verifier validates bearer tokens for API clients. A session still takes
	// precedence, so the console works without every request carrying a token.
	Verifier     TokenVerifier
	TrustHeaders bool
	// PublicPaths bypass authentication (health checks, the console shell).
	PublicPaths []string
}

func (m Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public paths skip verification but still receive an anonymous
		// identity: a handler must never have to nil-check what the
		// middleware is responsible for providing.
		for _, p := range m.PublicPaths {
			if r.URL.Path == p {
				next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(),
					&Identity{Subject: "anonymous", Tenant: "default"})))
				return
			}
		}

		// An identity already in the context was established by whoever
		// dispatched here — an outer router that authenticated once and then
		// chose this server. Asking again would either fail, because that
		// router holds the session and this middleware does not, or succeed
		// and disagree. Either way the outer answer stands.
		if _, ok := FromContext(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}

		for _, p := range m.Providers {
			if id, ok := p.Identify(r); ok {
				next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
				return
			}
		}

		if m.Verifier != nil {
			token := bearerToken(r)
			if token == "" {
				m.challenge(w, r, "missing bearer token")
				return
			}
			id, err := m.Verifier.Verify(r.Context(), token)
			if err != nil {
				// The reason is safe to return: it helps a legitimate client
				// fix its configuration and tells an attacker nothing they
				// could not determine by trying.
				unauthorized(w, err.Error())
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
			return
		}

		if len(m.Providers) > 0 {
			// Sessions are the whole authentication mechanism here, not a
			// fallback: no session means not signed in.
			m.challenge(w, r, "sign in required")
			return
		}

		if m.TrustHeaders {
			id := &Identity{
				Subject: headerOr(r, "X-Abhed-User", "anonymous"),
				Email:   r.Header.Get("X-Abhed-Email"),
				Tenant:  headerOr(r, "X-Abhed-Tenant", "default"),
			}
			if groups := r.Header.Get("X-Abhed-Groups"); groups != "" {
				id.Groups = strings.Split(groups, ",")
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
			return
		}

		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(),
			&Identity{Subject: "anonymous", Tenant: "default"})))
	})
}

// challenge answers a request with no credentials. A browser is sent to sign
// in, keeping where it was going; an API client gets 401 and a reason.
//
// The destination is the first provider with an entry point of its own. With
// none — local accounts only — it is the front door, where the server renders
// the sign-in form. Either way the page receives ?return= so it can send the
// person back afterwards.
func (m Middleware) challenge(w http.ResponseWriter, r *http.Request, reason string) {
	if !wantsHTML(r) || len(m.Providers) == 0 {
		unauthorized(w, reason)
		return
	}
	target := "/"
	for _, p := range m.Providers {
		if u, _ := p.SignIn(); u != "" {
			target = u
			break
		}
	}
	http.Redirect(w, r, target+"?return="+url.QueryEscape(r.URL.RequestURI()),
		http.StatusFound)
}

// wantsHTML distinguishes a browser navigation from an API call, so only the
// former is redirected to a sign-in page.
func wantsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func headerOr(r *http.Request, key, fallback string) string {
	if v := r.Header.Get(key); v != "" {
		return v
	}
	return fallback
}

func unauthorized(w http.ResponseWriter, reason string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="abhed"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized", "reason": reason})
}

// RequireGroup gates a handler on group membership, for RBAC above tenancy.
func RequireGroup(group string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			unauthorized(w, "no identity")
			return
		}
		for _, g := range id.Groups {
			if g == group {
				next.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "forbidden", "reason": "requires group " + group})
	})
}
