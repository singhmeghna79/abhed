// Package auth establishes who is calling.
//
// Trusting a proxy-set header is fine when a trusted proxy is the only path
// in, but it fails open the moment anything else can reach the port. This
// package holds the identity a request carries, the middleware that
// establishes it, and the one mechanism every deployment has: accounts Abhed
// keeps itself. Verifying tokens against an identity provider is a Provider
// like any other, implemented elsewhere and handed to the middleware.
package auth

// Identity is the verified caller.
type Identity struct {
	Subject  string   `json:"sub"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Tenant   string   `json:"tenant"`
	Groups   []string `json:"groups"`
	IssuedAt int64    `json:"iat"`
	Expires  int64    `json:"exp"`
}
