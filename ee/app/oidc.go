// Package app bundles the enterprise edition's options for the Community
// command: the identity provider, schedules, telemetry and the access
// surface. An enterprise main is the Community main with these passed in.
package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zybuu-ai/abhed/ee/auth"
	"github.com/zybuu-ai/abhed/app"
	ceauth "github.com/zybuu-ai/abhed/auth"
	"github.com/zybuu-ai/abhed/config"
)

// Options is everything this edition adds, ready for app.Main.
func Options(version string) []app.Option {
	return []app.Option{
		app.WithVersion(version),
		app.WithEdition("Enterprise Edition"),
		app.WithDocsURL("https://abhed.zybuu.com/docs"),
		OIDC(),
		Schedules(),
		Telemetry(),
		Access(),
	}
}

// OIDC registers auth.mode "oidc": tokens verified against an identity
// provider's published keys, and browser sign-in through it when a client id
// and redirect URL are configured.
func OIDC() app.Option { return app.WithAuthMode("oidc", buildOIDC) }

func buildOIDC(ctx context.Context, cfg config.Config, _ string, _ ceauth.UserStore) (ceauth.Provider, ceauth.TokenVerifier, error) {
	// A named provider fills in the issuer, scopes and tenant claim, so
	// "sign in with Gmail" needs a client id and nothing else.
	if p, found := auth.Presets[strings.ToLower(cfg.Auth.Provider)]; found {
		if cfg.Auth.Issuer == "" {
			cfg.Auth.Issuer = p.Issuer
		}
		if cfg.Auth.TenantClaim == "" {
			cfg.Auth.TenantClaim = p.TenantClaim
		}
		if len(cfg.Auth.Scopes) == 0 {
			cfg.Auth.Scopes = p.Scopes
		}
	}
	v, err := auth.NewVerifier(auth.Config{
		Issuer:      cfg.Auth.Issuer,
		Audience:    cfg.Auth.Audience,
		JWKSURL:     cfg.Auth.JWKSURL,
		TenantClaim: cfg.Auth.TenantClaim,
		GroupsClaim: cfg.Auth.GroupsClaim,
	})
	if err != nil {
		return nil, nil, err
	}
	// Fetch the JWKS eagerly so a misconfigured issuer fails at startup
	// rather than on the first user request.
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := v.Refresh(fetchCtx); err != nil {
		return nil, nil, fmt.Errorf("OIDC setup failed: %w", err)
	}

	// Browser sign-in is optional: an API-only deployment behind a gateway
	// needs no redirect flow.
	if cfg.Auth.ClientID == "" || cfg.Auth.RedirectURL == "" {
		return nil, v, nil
	}
	secret := cfg.Auth.ClientSecret
	if secret == "" && cfg.Auth.ClientSecretEnv != "" {
		secret = os.Getenv(cfg.Auth.ClientSecretEnv)
	}
	lg, err := auth.NewLogin(auth.LoginConfig{
		Issuer:        cfg.Auth.Issuer,
		ClientID:      cfg.Auth.ClientID,
		ClientSecret:  secret,
		RedirectURL:   cfg.Auth.RedirectURL,
		PostLogoutURL: cfg.Auth.PostLogoutURL,
		Scopes:        cfg.Auth.Scopes,
		Secure:        cfg.Auth.CookieSecure,
		SessionTTL:    time.Duration(cfg.Auth.SessionHours) * time.Hour,
		Label:         auth.ProviderLabel(cfg.Auth.Provider, cfg.Auth.Issuer),
	}, v)
	if err != nil {
		return nil, nil, fmt.Errorf("browser sign-in setup failed: %w", err)
	}
	return lg, v, nil
}
