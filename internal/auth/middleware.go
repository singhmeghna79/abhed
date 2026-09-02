package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey string

const identityKey ctxKey = "titan.identity"

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

// Middleware verifies the bearer token on every request.
//
// Three modes, because deployments genuinely differ:
//
//   - verifier != nil          → tokens are required and verified
//   - verifier == nil, trusted → identity comes from proxy headers
//   - neither                  → anonymous single-tenant, for local dev
//
// The proxy-header path is only safe when a trusted proxy is the sole route to
// the port; it is not the default, and `titan doctor` says which mode is live.
type Middleware struct {
	Verifier     *Verifier
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

		if m.Verifier != nil {
			token := bearerToken(r)
			if token == "" {
				unauthorized(w, "missing bearer token")
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

		if m.TrustHeaders {
			id := &Identity{
				Subject: headerOr(r, "X-Titan-User", "anonymous"),
				Email:   r.Header.Get("X-Titan-Email"),
				Tenant:  headerOr(r, "X-Titan-Tenant", "default"),
			}
			if groups := r.Header.Get("X-Titan-Groups"); groups != "" {
				id.Groups = strings.Split(groups, ",")
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
			return
		}

		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(),
			&Identity{Subject: "anonymous", Tenant: "default"})))
	})
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
	w.Header().Set("WWW-Authenticate", `Bearer realm="titan"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized", "reason": reason})
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
		json.NewEncoder(w).Encode(map[string]string{
			"error": "forbidden", "reason": "requires group " + group})
	})
}
