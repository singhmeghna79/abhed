package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests replay the ACTUAL token and JWKS shapes that Keycloak, Okta,
// Microsoft Entra ID, Auth0 and Google issue.
//
// The generic tests in oidc_test.go use my own minimal provider, which proves
// the crypto is right but not that real IdPs are accommodated. Providers differ
// in ways that break naive verifiers: Entra puts the tenant in `tid` and uses
// a v2.0 issuer suffix, Okta nests groups differently, Keycloak wraps roles in
// `realm_access`, and Google omits `groups` entirely.
//
// A live-IdP test would need a running Keycloak; these replay the documented
// wire formats instead, which is the part that actually varies.

type providerFixture struct {
	name        string
	issuer      string
	audience    string
	tenantClaim string
	groupsClaim string
	claims      map[string]any
	wantTenant  string
	wantGroups  []string
	wantSubject string
}

func fixtures() []providerFixture {
	now := time.Now()
	return []providerFixture{
		{
			// Keycloak: realm-scoped issuer, roles under realm_access.
			name:        "keycloak",
			issuer:      "https://idp.internal/realms/engineering",
			audience:    "abhed",
			tenantClaim: "org_id",
			groupsClaim: "groups",
			claims: map[string]any{
				"iss":                "https://idp.internal/realms/engineering",
				"aud":                "abhed",
				"sub":                "f:8a1c:yuvraj",
				"typ":                "Bearer",
				"azp":                "abhed",
				"preferred_username": "yuvraj",
				"email":              "yuvraj@example.com",
				"name":               "Yuvraj Singh",
				"org_id":             "acme",
				"groups":             []string{"/engineering", "/abhed-admins"},
				"realm_access":       map[string]any{"roles": []string{"default-roles-engineering"}},
				"iat":                now.Unix(),
				"exp":                now.Add(time.Hour).Unix(),
			},
			wantTenant:  "acme",
			wantGroups:  []string{"/engineering", "/abhed-admins"},
			wantSubject: "f:8a1c:yuvraj",
		},
		{
			// Okta: issuer includes /oauth2/<authServerId>, aud is the API name.
			name:        "okta",
			issuer:      "https://example.okta.com/oauth2/aus1a2b3c",
			audience:    "api://abhed",
			tenantClaim: "orgId",
			groupsClaim: "groups",
			claims: map[string]any{
				"iss":    "https://example.okta.com/oauth2/aus1a2b3c",
				"aud":    "api://abhed",
				"sub":    "00u1a2b3c4d5e6f7g8h9",
				"scp":    []string{"openid", "profile", "email"},
				"email":  "yuvraj@example.com",
				"orgId":  "acme-corp",
				"groups": []string{"Everyone", "Engineering"},
				"iat":    now.Unix(),
				"exp":    now.Add(time.Hour).Unix(),
			},
			wantTenant:  "acme-corp",
			wantGroups:  []string{"Everyone", "Engineering"},
			wantSubject: "00u1a2b3c4d5e6f7g8h9",
		},
		{
			// Entra ID v2.0: tenant in `tid`, groups as object ids, aud is a GUID.
			name:        "entra",
			issuer:      "https://login.microsoftonline.com/72f988bf-1234/v2.0",
			audience:    "api://8a1b2c3d-abhed",
			tenantClaim: "tid",
			groupsClaim: "groups",
			claims: map[string]any{
				"iss":   "https://login.microsoftonline.com/72f988bf-1234/v2.0",
				"aud":   "api://8a1b2c3d-abhed",
				"sub":   "AAAAAAAAAAAAAAAAAAAAAG5kZXg",
				"oid":   "9f4880d8-80ba-4c40-97bc-f7a23c703084",
				"tid":   "72f988bf-1234",
				"email": "yuvraj@example.com",
				"name":  "Yuvraj Singh",
				"groups": []string{
					"a1b2c3d4-0000-1111-2222-333344445555",
					"b2c3d4e5-0000-1111-2222-333344445556",
				},
				"ver": "2.0",
				"iat": now.Unix(),
				"nbf": now.Add(-time.Minute).Unix(),
				"exp": now.Add(time.Hour).Unix(),
			},
			wantTenant: "72f988bf-1234",
			wantGroups: []string{
				"a1b2c3d4-0000-1111-2222-333344445555",
				"b2c3d4e5-0000-1111-2222-333344445556",
			},
			wantSubject: "AAAAAAAAAAAAAAAAAAAAAG5kZXg",
		},
		{
			// Auth0: namespaced custom claims, aud as an array.
			name:        "auth0",
			issuer:      "https://example.eu.auth0.com/",
			audience:    "https://abhed.internal/api",
			tenantClaim: "https://abhed.internal/tenant",
			groupsClaim: "https://abhed.internal/groups",
			claims: map[string]any{
				"iss":                           "https://example.eu.auth0.com/",
				"aud":                           []string{"https://abhed.internal/api", "https://example.eu.auth0.com/userinfo"},
				"sub":                           "auth0|65f1a2b3c4d5e6f7",
				"azp":                           "abhedClientId",
				"scope":                         "openid profile email",
				"https://abhed.internal/tenant": "acme",
				"https://abhed.internal/groups": []string{"engineering"},
				"iat":                           now.Unix(),
				"exp":                           now.Add(time.Hour).Unix(),
			},
			wantTenant:  "acme",
			wantGroups:  []string{"engineering"},
			wantSubject: "auth0|65f1a2b3c4d5e6f7",
		},
		{
			// Google Workspace: `hd` carries the domain, no groups claim at all.
			name:        "google",
			issuer:      "https://accounts.google.com",
			audience:    "1234567890-abhed.apps.googleusercontent.com",
			tenantClaim: "hd",
			groupsClaim: "groups",
			claims: map[string]any{
				"iss":            "https://accounts.google.com",
				"aud":            "1234567890-abhed.apps.googleusercontent.com",
				"sub":            "110169484474386276334",
				"email":          "yuvraj@example.com",
				"email_verified": true,
				"hd":             "example.com",
				"name":           "Yuvraj Singh",
				"iat":            now.Unix(),
				"exp":            now.Add(time.Hour).Unix(),
			},
			wantTenant:  "example.com",
			wantGroups:  nil, // Google issues no groups claim
			wantSubject: "110169484474386276334",
		},
	}
}

// realIDP mints tokens with the fixture's exact issuer while serving JWKS from
// a local test server, which is how a real deployment behaves when the issuer
// URL and the JWKS host differ.
type realIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
	issuer string
}

func newRealIDP(t *testing.T, issuer string) *realIDP {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p := &realIDP{key: key, ecKey: ecKey, issuer: issuer}

	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{
			map[string]string{
				"kty": "RSA", "kid": "sig-1", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			},
			map[string]string{
				"kty": "EC", "kid": "sig-ec", "use": "sig", "alg": "ES256", "crv": "P-256",
				"x": base64.RawURLEncoding.EncodeToString(ecKey.X.Bytes()),
				"y": base64.RawURLEncoding.EncodeToString(ecKey.Y.Bytes()),
			},
		}})
	})
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *realIDP) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "sig-1"})
	c, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(h) + "." +
		base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, 5, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestRealProviderTokenShapes(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			idp := newRealIDP(t, fx.issuer)

			v, err := NewVerifier(Config{
				Issuer:      fx.issuer,
				Audience:    fx.audience,
				JWKSURL:     idp.server.URL + "/jwks", // explicit: no discovery round trip
				TenantClaim: fx.tenantClaim,
				GroupsClaim: fx.groupsClaim,
			})
			if err != nil {
				t.Fatal(err)
			}

			id, err := v.Verify(context.Background(), idp.sign(t, fx.claims))
			if err != nil {
				t.Fatalf("%s token rejected: %v", fx.name, err)
			}
			if id.Subject != fx.wantSubject {
				t.Errorf("subject: got %q, want %q", id.Subject, fx.wantSubject)
			}
			if id.Tenant != fx.wantTenant {
				t.Errorf("tenant: got %q, want %q (claim %q)", id.Tenant, fx.wantTenant, fx.tenantClaim)
			}
			if len(id.Groups) != len(fx.wantGroups) {
				t.Errorf("groups: got %v, want %v", id.Groups, fx.wantGroups)
			}
			t.Logf("%s → sub=%s tenant=%s groups=%v", fx.name, id.Subject, id.Tenant, id.Groups)
		})
	}
}

// Google issues no groups claim. A verifier that assumes one is present would
// panic or silently produce an empty tenant.
func TestMissingGroupsClaimIsNotFatal(t *testing.T) {
	fx := fixtures()[4] // google
	idp := newRealIDP(t, fx.issuer)
	v, _ := NewVerifier(Config{
		Issuer: fx.issuer, Audience: fx.audience,
		JWKSURL:     idp.server.URL + "/jwks",
		TenantClaim: fx.tenantClaim, GroupsClaim: "groups",
	})
	id, err := v.Verify(context.Background(), idp.sign(t, fx.claims))
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Groups) != 0 {
		t.Fatalf("expected no groups, got %v", id.Groups)
	}
}

// A missing tenant claim must fall back to "default" rather than empty string,
// which would break the storage layer's tenant scoping.
func TestMissingTenantClaimFallsBack(t *testing.T) {
	issuer := "https://idp.internal/realms/x"
	idp := newRealIDP(t, issuer)
	v, _ := NewVerifier(Config{
		Issuer: issuer, Audience: "abhed",
		JWKSURL:     idp.server.URL + "/jwks",
		TenantClaim: "nonexistent_claim",
	})
	id, err := v.Verify(context.Background(), idp.sign(t, map[string]any{
		"iss": issuer, "aud": "abhed", "sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if id.Tenant != "default" {
		t.Fatalf("missing tenant claim should fall back to default, got %q", id.Tenant)
	}
}

// Entra sends nbf; a verifier that ignores clock skew rejects valid tokens.
func TestClockSkewLeeway(t *testing.T) {
	issuer := "https://login.microsoftonline.com/t/v2.0"
	idp := newRealIDP(t, issuer)
	v, _ := NewVerifier(Config{
		Issuer: issuer, Audience: "abhed",
		JWKSURL: idp.server.URL + "/jwks",
		Leeway:  60 * time.Second,
	})

	// Issued 30s in the future: within the 60s leeway a real deployment needs.
	id, err := v.Verify(context.Background(), idp.sign(t, map[string]any{
		"iss": issuer, "aud": "abhed", "sub": "u1",
		"nbf": time.Now().Add(30 * time.Second).Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}))
	if err != nil {
		t.Fatalf("token within clock-skew leeway rejected: %v", err)
	}
	if id.Subject != "u1" {
		t.Fatal("wrong subject")
	}

	// Well outside the leeway: must be rejected.
	_, err = v.Verify(context.Background(), idp.sign(t, map[string]any{
		"iss": issuer, "aud": "abhed", "sub": "u1",
		"nbf": time.Now().Add(10 * time.Minute).Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}))
	if err != ErrNotYetValid {
		t.Fatalf("token far in the future should be rejected, got %v", err)
	}
}

// Auth0 sends aud as an array containing several values.
func TestAudienceArrayFromRealProvider(t *testing.T) {
	fx := fixtures()[3] // auth0
	idp := newRealIDP(t, fx.issuer)
	v, _ := NewVerifier(Config{
		Issuer: fx.issuer, Audience: fx.audience,
		JWKSURL:     idp.server.URL + "/jwks",
		TenantClaim: fx.tenantClaim, GroupsClaim: fx.groupsClaim,
	})
	if _, err := v.Verify(context.Background(), idp.sign(t, fx.claims)); err != nil {
		t.Fatalf("multi-value aud rejected: %v", err)
	}
}

// A trailing slash in the issuer is a classic mismatch: Auth0 includes one,
// most others do not, and a config copied by hand often differs.
func TestIssuerTrailingSlashMismatchIsRejected(t *testing.T) {
	idp := newRealIDP(t, "https://example.eu.auth0.com/")
	v, _ := NewVerifier(Config{
		Issuer:   "https://example.eu.auth0.com", // no trailing slash
		Audience: "abhed",
		JWKSURL:  idp.server.URL + "/jwks",
	})
	_, err := v.Verify(context.Background(), idp.sign(t, map[string]any{
		"iss": "https://example.eu.auth0.com/", // with slash
		"aud": "abhed", "sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(),
	}))
	if err == nil {
		t.Fatal("issuer mismatch must be rejected — exact match is the contract")
	}
	if !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error should name the issuer mismatch so the operator can fix config: %v", err)
	}
}

// Discovery must work when the well-known document is served by the issuer.
func TestDiscoveryFlow(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"jwks_uri":                              srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	v, err := NewVerifier(Config{Issuer: srv.URL, Audience: "abhed"})
	if err != nil {
		t.Fatal(err)
	}

	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	c, _ := json.Marshal(map[string]any{
		"iss": srv.URL, "aud": "abhed", "sub": "u1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signing := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, 5, sum[:])
	token := signing + "." + base64.RawURLEncoding.EncodeToString(sig)

	// No JWKSURL configured: the verifier must discover it.
	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("discovery flow failed: %v", err)
	}
	if id.Subject != "u1" {
		t.Fatal("wrong subject")
	}
}
