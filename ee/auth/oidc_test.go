package auth

import (
	"context"
	"crypto"
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

// idp is a miniature OIDC provider: real keys, real signatures, real JWKS.
// Verification is only meaningfully tested against genuine crypto.
type idp struct {
	server *httptest.Server
	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
	issuer string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{rsaKey: rsaKey, ecKey: ecKey}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": p.issuer, "jwks_uri": p.issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{
			map[string]string{
				"kty": "RSA", "kid": "rsa-1", "use": "sig", "alg": "RS256",
				"n": b64(rsaKey.N.Bytes()),
				"e": b64(big.NewInt(int64(rsaKey.E)).Bytes()),
			},
			map[string]string{
				"kty": "EC", "kid": "ec-1", "use": "sig", "alg": "ES256", "crv": "P-256",
				"x": b64(ecKey.X.Bytes()), "y": b64(ecKey.Y.Bytes()),
			},
		}})
	})
	p.server = httptest.NewServer(mux)
	p.issuer = p.server.URL
	t.Cleanup(p.server.Close)
	return p
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *idp) signRS256(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := b64(mustJSON(t, map[string]string{"alg": "RS256", "typ": "JWT", "kid": "rsa-1"}))
	payload := b64(mustJSON(t, claims))
	signing := header + "." + payload
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.rsaKey, 5, sum[:]) // 5 = crypto.SHA256
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

func (p *idp) signES256(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := b64(mustJSON(t, map[string]string{"alg": "ES256", "typ": "JWT", "kid": "ec-1"}))
	payload := b64(mustJSON(t, claims))
	signing := header + "." + payload
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, p.ecKey, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	// JWS wants fixed-width r||s, zero-padded to the curve size.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + b64(sig)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func validClaims(issuer string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": "abhed", "sub": "user-42",
		"email": "yuvraj@example.com", "name": "Yuvraj",
		"tenant": "acme", "groups": []string{"engineering", "abhed-admins"},
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
}

func verifier(t *testing.T, p *idp) *Verifier {
	t.Helper()
	v, err := NewVerifier(Config{Issuer: p.issuer, Audience: "abhed"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyRS256(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)

	id, err := v.Verify(context.Background(), p.signRS256(t, validClaims(p.issuer)))
	if err != nil {
		t.Fatalf("valid RS256 token rejected: %v", err)
	}
	if id.Subject != "user-42" || id.Tenant != "acme" || id.Email != "yuvraj@example.com" {
		t.Fatalf("claims not extracted: %+v", id)
	}
	if len(id.Groups) != 2 {
		t.Fatalf("groups not extracted: %v", id.Groups)
	}
}

func TestVerifyES256(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	id, err := v.Verify(context.Background(), p.signES256(t, validClaims(p.issuer)))
	if err != nil {
		t.Fatalf("valid ES256 token rejected: %v", err)
	}
	if id.Subject != "user-42" {
		t.Fatalf("got %+v", id)
	}
}

// A tampered payload must fail: this is the whole point of verification.
func TestTamperedPayloadRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	token := p.signRS256(t, validClaims(p.issuer))

	parts := strings.Split(token, ".")
	evil := validClaims(p.issuer)
	evil["tenant"] = "victim-corp" // escalate to another tenant
	parts[1] = b64(mustJSON(t, evil))

	if _, err := v.Verify(context.Background(), strings.Join(parts, ".")); err == nil {
		t.Fatal("TAMPERED TOKEN ACCEPTED - tenant escalation possible")
	}
}

// alg:none is the classic JWT bypass.
func TestAlgNoneRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)

	header := b64(mustJSON(t, map[string]string{"alg": "none", "typ": "JWT", "kid": "rsa-1"}))
	payload := b64(mustJSON(t, validClaims(p.issuer)))
	token := header + "." + payload + "."

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("alg:none ACCEPTED - authentication bypass")
	}
	if !strings.Contains(err.Error(), "unsupported signing algorithm") {
		t.Fatalf("wrong rejection reason: %v", err)
	}
}

// Algorithm confusion: HMAC signed with the RSA public key as the secret.
func TestHMACConfusionRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)

	header := b64(mustJSON(t, map[string]string{"alg": "HS256", "typ": "JWT", "kid": "rsa-1"}))
	payload := b64(mustJSON(t, validClaims(p.issuer)))
	token := header + "." + payload + "." + b64([]byte("whatever"))

	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("HS256 ACCEPTED against an RSA key - algorithm confusion attack works")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	claims := validClaims(p.issuer)
	claims["exp"] = time.Now().Add(-2 * time.Hour).Unix()

	_, err := v.Verify(context.Background(), p.signRS256(t, claims))
	if err != ErrExpired {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestTokenWithoutExpiryRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	claims := validClaims(p.issuer)
	delete(claims, "exp")

	if _, err := v.Verify(context.Background(), p.signRS256(t, claims)); err == nil {
		t.Fatal("a token with no expiry never becomes invalid and must be rejected")
	}
}

func TestWrongIssuerRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	claims := validClaims("https://evil.example.com")

	_, err := v.Verify(context.Background(), p.signRS256(t, claims))
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("wrong issuer must be rejected, got %v", err)
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	claims := validClaims(p.issuer)
	claims["aud"] = "some-other-service"

	_, err := v.Verify(context.Background(), p.signRS256(t, claims))
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("wrong audience must be rejected, got %v", err)
	}
}

func TestAudienceArrayAccepted(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	claims := validClaims(p.issuer)
	claims["aud"] = []string{"other", "abhed"}

	if _, err := v.Verify(context.Background(), p.signRS256(t, claims)); err != nil {
		t.Fatalf("aud array containing the audience should pass: %v", err)
	}
}

func TestUnknownKeyIDRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)

	header := b64(mustJSON(t, map[string]string{"alg": "RS256", "typ": "JWT", "kid": "attacker-key"}))
	payload := b64(mustJSON(t, validClaims(p.issuer)))
	token := header + "." + payload + "." + b64([]byte("sig"))

	_, err := v.Verify(context.Background(), token)
	if err == nil || !strings.Contains(err.Error(), "kid") {
		t.Fatalf("unknown kid must be rejected, got %v", err)
	}
}

func TestMalformedTokensRejected(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)
	for _, bad := range []string{"", "abc", "a.b", "a.b.c.d", "...", "!!!.???.***"} {
		if _, err := v.Verify(context.Background(), bad); err == nil {
			t.Errorf("malformed token %q accepted", bad)
		}
	}
}

func TestDiscoveryIssuerMismatchRejected(t *testing.T) {
	// A discovery document claiming a different issuer is a misconfiguration
	// or an attack; either way it must not be used.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": "https://attacker.example", "jwks_uri": "https://attacker.example/jwks",
		})
	}))
	defer srv.Close()

	v, _ := NewVerifier(Config{Issuer: srv.URL, Audience: "abhed"})
	err := v.Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("issuer mismatch in discovery must be rejected, got %v", err)
	}
}

func TestKeyRotationRefetchesJWKS(t *testing.T) {
	p := newIDP(t)
	v := verifier(t, p)

	// Prime the cache.
	if _, err := v.Verify(context.Background(), p.signRS256(t, validClaims(p.issuer))); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	v.keys = map[string]crypto.PublicKey{} // simulate a rotation invalidating the cache
	v.mu.Unlock()

	// A fresh token should still verify, via lazy refresh.
	if _, err := v.Verify(context.Background(), p.signRS256(t, validClaims(p.issuer))); err != nil {
		t.Fatalf("verifier did not refresh JWKS after rotation: %v", err)
	}
}
