// Package auth verifies OIDC identity tokens and signs people in through an
// identity provider.
//
// It verifies JWTs properly — signature, issuer, audience, expiry — against
// the provider's published JWKS, so Abhed can be exposed directly rather than
// only behind a proxy that has done the work. The Verifier is the Community
// middleware's TokenVerifier and Login is one of its Providers; nothing here
// is reached by the Community edition except through those two interfaces,
// which is what lets this package live in a different edition from the one
// that holds the accounts.
//
// Implemented against crypto/* rather than a JWT library: an air-gapped build
// benefits from fewer dependencies, and the verification path is small enough
// to read in full. Supported algorithms are RS256/384/512 and ES256/384/512 —
// the ones real IdPs issue. "none" and HMAC are rejected outright, since
// accepting HMAC against a public key is the classic JWT confusion attack.
package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	ceauth "github.com/zybuu-ai/abhed/auth"
)

var (
	ErrNoToken        = errors.New("no bearer token")
	ErrMalformed      = errors.New("malformed token")
	ErrBadSignature   = errors.New("signature verification failed")
	ErrExpired        = errors.New("token expired")
	ErrNotYetValid    = errors.New("token not yet valid")
	ErrWrongIssuer    = errors.New("unexpected issuer")
	ErrWrongAudience  = errors.New("unexpected audience")
	ErrUnknownKey     = errors.New("signing key not found in JWKS")
	ErrUnsupportedAlg = errors.New("unsupported signing algorithm")
)

// Identity is the Community edition's: a token verified here yields the same
// identity a local account does, so everything downstream — tenancy, groups,
// the admin gate — is one code path whatever signed the person in.
type Identity = ceauth.Identity

type Config struct {
	// Issuer must match the token's iss claim exactly.
	Issuer string
	// Audience must appear in the token's aud claim.
	Audience string
	// JWKSURL overrides discovery, which matters in an air-gapped enclave
	// where the well-known endpoint may not be reachable.
	JWKSURL string
	// TenantClaim names the claim carrying the tenant. Defaults to "tenant";
	// many IdPs use "org_id" or a namespaced claim.
	TenantClaim string
	// GroupsClaim names the claim carrying group membership.
	GroupsClaim string
	// Leeway absorbs clock skew between Abhed and the IdP.
	Leeway time.Duration
	// RefreshInterval bounds how long a rotated key takes to appear.
	RefreshInterval time.Duration
	HTTPClient      *http.Client
}

func (c *Config) applyDefaults() {
	if c.TenantClaim == "" {
		c.TenantClaim = "tenant"
	}
	if c.GroupsClaim == "" {
		c.GroupsClaim = "groups"
	}
	if c.Leeway == 0 {
		c.Leeway = 60 * time.Second
	}
	if c.RefreshInterval == 0 {
		c.RefreshInterval = 15 * time.Minute
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
}

// Verifier validates tokens against a cached JWKS.
type Verifier struct {
	cfg Config

	mu        sync.RWMutex
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
}

func NewVerifier(cfg Config) (*Verifier, error) {
	cfg.applyDefaults()
	if cfg.Issuer == "" {
		return nil, errors.New("auth: issuer is required")
	}
	return &Verifier{cfg: cfg, keys: make(map[string]crypto.PublicKey)}, nil
}

// discovery is the subset of the OIDC discovery document Abhed needs.
type discovery struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// Refresh fetches the JWKS. Called lazily on an unknown key id, which is what
// makes key rotation work without a restart.
func (v *Verifier) Refresh(ctx context.Context) error {
	jwksURL := v.cfg.JWKSURL
	if jwksURL == "" {
		wellKnown := strings.TrimSuffix(v.cfg.Issuer, "/") + "/.well-known/openid-configuration"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
		if err != nil {
			return err
		}
		resp, err := v.cfg.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("OIDC discovery at %s: %w", wellKnown, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("OIDC discovery returned %s", resp.Status)
		}
		var d discovery
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			return fmt.Errorf("decode discovery document: %w", err)
		}
		// A discovery document claiming a different issuer is a misconfiguration
		// or an attack; either way it must not be used.
		if d.Issuer != v.cfg.Issuer {
			return fmt.Errorf("%w: discovery says %q, configured %q",
				ErrWrongIssuer, d.Issuer, v.cfg.Issuer)
		}
		jwksURL = d.JWKSURI
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS at %s: %w", jwksURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %s", resp.Status)
	}

	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		pub, err := k.publicKey()
		if err != nil {
			continue // skip keys we cannot parse rather than failing the set
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("JWKS contained no usable keys")
	}

	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		var exp uint64
		for _, b := range e {
			exp = exp<<8 | uint64(b)
		}
		if exp == 0 || exp > 1<<31 {
			return nil, errors.New("invalid RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp)}, nil

	case "EC":
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		curve, err := curveFor(k.Crv)
		if err != nil {
			return nil, err
		}
		// Uncompressed SEC 1 point: 0x04 || X || Y, each coordinate left-padded
		// to the curve size. The parser validates the point is on the curve,
		// which the struct literal it replaces never did.
		size := (curve.Params().BitSize + 7) / 8
		if len(x) > size || len(y) > size {
			return nil, fmt.Errorf("jwk %q: coordinate longer than the curve", k.Kid)
		}
		point := make([]byte, 1+2*size)
		point[0] = 4
		copy(point[1+size-len(x):], x)
		copy(point[1+2*size-len(y):], y)
		pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return nil, fmt.Errorf("jwk %q: %w", k.Kid, err)
		}
		return pub, nil
	}
	return nil, fmt.Errorf("unsupported key type %q", k.Kty)
}

// Verify checks a bearer token and returns the caller's identity.
func (v *Verifier) Verify(ctx context.Context, token string) (*Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, ErrMalformed
	}

	// Reject "none" and HMAC before touching keys. Accepting HMAC verified
	// against a public key is the classic JWT algorithm-confusion attack.
	hash, isRSA, err := algorithm(header.Alg)
	if err != nil {
		return nil, err
	}

	key, err := v.keyFor(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := digestOf(hash, signed)

	if isRSA {
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, ErrBadSignature
		}
		if err := rsa.VerifyPKCS1v15(pub, hash, digest, sig); err != nil {
			return nil, ErrBadSignature
		}
	} else {
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return nil, ErrBadSignature
		}
		// JWS ECDSA signatures are fixed-width r||s, not ASN.1.
		half := len(sig) / 2
		if half == 0 || len(sig)%2 != 0 {
			return nil, ErrBadSignature
		}
		r := new(big.Int).SetBytes(sig[:half])
		s := new(big.Int).SetBytes(sig[half:])
		if !ecdsa.Verify(pub, digest, r, s) {
			return nil, ErrBadSignature
		}
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, ErrMalformed
	}
	return v.validateClaims(claims)
}

func (v *Verifier) validateClaims(claims map[string]any) (*Identity, error) {
	now := time.Now()

	if iss, _ := claims["iss"].(string); iss != v.cfg.Issuer {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrWrongIssuer, iss, v.cfg.Issuer)
	}
	if v.cfg.Audience != "" && !audienceContains(claims["aud"], v.cfg.Audience) {
		return nil, fmt.Errorf("%w: want %q", ErrWrongAudience, v.cfg.Audience)
	}

	exp, hasExp := numericClaim(claims["exp"])
	if !hasExp {
		// A token without an expiry never becomes invalid; refuse it.
		return nil, fmt.Errorf("%w: token has no exp claim", ErrMalformed)
	}
	if now.After(time.Unix(exp, 0).Add(v.cfg.Leeway)) {
		return nil, ErrExpired
	}
	if nbf, ok := numericClaim(claims["nbf"]); ok {
		if now.Before(time.Unix(nbf, 0).Add(-v.cfg.Leeway)) {
			return nil, ErrNotYetValid
		}
	}

	id := &Identity{Expires: exp}
	id.Subject, _ = claims["sub"].(string)
	if id.Subject == "" {
		return nil, fmt.Errorf("%w: token has no sub claim", ErrMalformed)
	}
	id.Email, _ = claims["email"].(string)
	id.Name, _ = claims["name"].(string)
	if iat, ok := numericClaim(claims["iat"]); ok {
		id.IssuedAt = iat
	}
	id.Tenant, _ = claims[v.cfg.TenantClaim].(string)
	if id.Tenant == "" {
		id.Tenant = "default"
	}
	if raw, ok := claims[v.cfg.GroupsClaim].([]any); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				id.Groups = append(id.Groups, s)
			}
		}
	}
	return id, nil
}

// keyFor returns the signing key, refreshing the JWKS once on a miss so key
// rotation does not require a restart.
func (v *Verifier) keyFor(ctx context.Context, kid string) (crypto.PublicKey, error) {
	v.mu.RLock()
	key, found := v.keys[kid]
	stale := time.Since(v.fetchedAt) > v.cfg.RefreshInterval
	empty := len(v.keys) == 0
	v.mu.RUnlock()

	if found && !stale {
		return key, nil
	}
	if !found || stale || empty {
		if err := v.Refresh(ctx); err != nil && !found {
			return nil, fmt.Errorf("refresh JWKS: %w", err)
		}
		v.mu.RLock()
		key, found = v.keys[kid]
		v.mu.RUnlock()
	}
	if !found {
		return nil, fmt.Errorf("%w: kid %q", ErrUnknownKey, kid)
	}
	return key, nil
}

func curveFor(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	}
	return nil, fmt.Errorf("unsupported curve %q", crv)
}

func algorithm(alg string) (crypto.Hash, bool, error) {
	switch alg {
	case "RS256":
		return crypto.SHA256, true, nil
	case "RS384":
		return crypto.SHA384, true, nil
	case "RS512":
		return crypto.SHA512, true, nil
	case "ES256":
		return crypto.SHA256, false, nil
	case "ES384":
		return crypto.SHA384, false, nil
	case "ES512":
		return crypto.SHA512, false, nil
	default:
		// Explicitly includes "none" and every HS* variant.
		return 0, false, fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
}

func digestOf(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}

func audienceContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func numericClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
