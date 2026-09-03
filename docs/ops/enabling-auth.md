# Titan — Enabling Authentication

Four modes. Pick by how Titan is exposed, not by how much security sounds good.

| Mode | Who it is for | Identity comes from |
|---|---|---|
| `none` | Local development, single user | Nobody — everything is "anonymous/default" |
| `local` | A team with no identity provider | A username and password Titan holds |
| `proxy` | Behind an authenticating reverse proxy | `X-Titan-User` / `X-Titan-Tenant` headers |
| `oidc` | An organisation with an IdP | A verified token, or a browser sign-in |

`local` and `oidc` are **not exclusive**. Set `mode: "local"` and also give a
`provider` and `client_id`, and the sign-in page offers both: a password form
for people who are not in the corporate directory, and a "Continue with
Google/Microsoft" button for those who are.

**Headers are not trusted unless you ask for it.** In `none` mode a caller cannot
choose its own tenant by setting a header — there is a test asserting exactly that.
`proxy` mode is safe only when the proxy is the *sole* route to the port.

## Local accounts (no identity provider)

The mode for a pilot, an air-gapped enclave, or a team standing Titan up before
central IT is involved.

```json
{
  "auth": {
    "mode": "local",
    "session_hours": 12,
    "cookie_secure": true,
    "allow_signup": false
  },
  "storage": { "driver": "postgres", "dsn": "postgres://..." }
}
```

Create the first account from the CLI:

```bash
titan -C /srv/titan user add alice -email alice@corp.internal -name "Alice"
# generated password: 7Kq2mVx9pLd4  (change it after first sign-in)
```

Then open the server in a browser and sign in with it.

| Command | Does |
|---|---|
| `titan user add <name>` | Create an account. `-password` sets one; omitted, one is generated |
| `titan user list` | Show accounts, emails, tenants and groups |
| `titan user passwd <name>` | Reset a forgotten password to a new generated one |
| `titan user remove <name>` | Delete an account |

### Where accounts live

With `storage.driver: postgres`, accounts are a table in the same database as
the event store, which is what a multi-node deployment needs. Without it, they
go to `<workspace>/.titan/users.json`, mode `0600`, written atomically.

The file store exists because the alternative was silently broken: an in-memory
store meant `titan user add` created an account inside a CLI process that then
exited, reported success, and left the user unable to sign in.

### Self-registration

`allow_signup` is **off by default**. On an internal tool, open registration is
a way in for anyone who can reach the port, not a convenience. Turn it on and
the sign-in card grows a "Create one" link; leave it off and the card says
accounts are created by an administrator, which is true and actionable.

### Password handling

- bcrypt at the library default cost. Sign-in happens once per session, so a few
  hundred milliseconds is invisible to a person and expensive to an attacker
  holding the hash file.
- Minimum ten characters, enforced on the server. The browser checks too, but
  only to save a round trip.
- A wrong password and an unknown username return the *same* error and take the
  *same* time — a missing user is still run through bcrypt against a dummy hash.
  Without that, response timing enumerates valid usernames. There is a test.
- `User.Hash` is tagged `json:"-"`, so a hash cannot fall out of an HTTP
  response. The on-disk store uses its own type to persist it, rather than
  relaxing that tag.
- A password set by an administrator (`user add`, `user passwd`) is flagged
  `must_change_password`, and the console says so at sign-in.

### Signing in with Google or Microsoft as well

```json
{
  "auth": {
    "mode": "local",
    "provider": "google",
    "client_id": "...apps.googleusercontent.com",
    "client_secret_env": "TITAN_OIDC_SECRET",
    "redirect_url": "https://titan.internal/auth/callback"
  }
}
```

`provider` fills in the issuer, scopes and tenant claim, so `google`,
`microsoft` and `github` need only a client id and secret. See
[oidc-providers.md](oidc-providers.md) for registering the redirect URI.

## Browser sign-in (what most deployments want)

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "audience": "titan",
    "client_id": "titan-console",
    "client_secret_env": "TITAN_OIDC_SECRET",
    "redirect_url": "https://titan.internal/auth/callback",
    "tenant_claim": "org_id",
    "groups_claim": "groups",
    "require_group": "titan-users",
    "cookie_secure": true,
    "session_hours": 12
  }
}
```

Then register `https://titan.internal/auth/callback` as a redirect URI with your
provider, and:

```bash
export TITAN_OIDC_SECRET=...
titan serve -addr :8420
```

Opening the console now redirects to your IdP, and after sign-in the header shows
who you are and which tenant you are in.

### What the flow does

1. `GET /login` → redirects to the IdP with **PKCE** (S256) and a single-use `state`
2. The IdP authenticates the person and redirects back with a code
3. `GET /auth/callback` validates `state`, exchanges the code, and **verifies the
   returned `id_token`** — signature, issuer, audience, expiry — against the JWKS
4. A `HttpOnly`, `SameSite=Lax` cookie holds the session
5. `GET /logout` clears it and, where the provider supports it, ends the IdP session

PKCE is used even though Titan has a client secret: an authorization code in a
browser URL bar is precisely what PKCE exists to protect, and it costs one hash.

### Security properties, each with a test

| Property | Test |
|---|---|
| Signature, issuer, audience and expiry verified | `TestBrowserLoginFlow` |
| `state` is single-use — a replayed callback fails | `TestStateIsSingleUse` |
| Forged `state` rejected | `TestCallbackRejectsUnknownState` |
| PKCE actually enforced, not merely sent | `TestPKCEIsEnforced` |
| No open redirect via `?return=` | `TestOpenRedirectRejected` |
| Cookie is `HttpOnly` and `SameSite=Lax` | `TestBrowserLoginFlow` |
| Expired browser session refuses | `TestExpiredSessionRejected` |
| Logout clears server-side state | `TestLogoutClearsSession` |

## Signing in as a different user

Clearing Titan's session is not enough. The IdP keeps its own session, so
clicking "sign in" again silently returns the same person — which reads as
logout being broken.

Two controls, and they differ:

| Route | Effect |
|---|---|
| `/logout` | Ends Titan's session **and** the IdP's, then returns to `post_logout_redirect_url` |
| `/switch-user` | Sends `prompt=login`, forcing a credential prompt even with an active IdP session |

Both appear in the console header once someone is signed in.

**`post_logout_redirect_url` must be pre-registered with your provider.**
Keycloak, Entra and Auth0 all ignore an RP-initiated logout whose redirect they
do not recognise, leaving the user stranded at the IdP or still signed in.

```json
{
  "auth": {
    "mode": "oidc",
    "redirect_url": "https://titan.internal/auth/callback",
    "post_logout_redirect_url": "https://titan.internal/"
  }
}
```

Only `login`, `select_account`, `consent` and `none` are forwarded as `prompt`
values; anything else in the query string is dropped rather than passed to the
provider.

## When authentication is off

With `auth.mode: none` there are no user accounts, so `/login` and `/logout`
return a page explaining that and showing the config to enable sign-in — rather
than a 404, which reads as a fault. `/v1/whoami` answers
`{"authenticated": false, "reason": "..."}`, and the console removes the user
chip entirely rather than hiding it: a Sign out link that leads nowhere is worse
than no link.

## API clients

Bearer tokens work unchanged, and take precedence over a cookie:

```bash
curl -H "Authorization: Bearer $TOKEN" https://titan.internal/v1/sessions
```

A browser navigation with no session is redirected to sign in; an API call with
no token gets `401` with a reason. That distinction is `Accept: text/html`.

## Tenancy

`tenant_claim` names the claim carrying the tenant, and it flows all the way
down: the API scopes by it, and Postgres enforces it with row-level security.
**Point it at the wrong claim and every user lands in one tenant.**

Per-provider claim names — Keycloak, Okta, Entra ID, Auth0, Google — are in
[`oidc-providers.md`](oidc-providers.md), with the specific gotcha for each.

When `storage.driver` is `postgres`, `storage.tenant` must match the tenant your
tokens carry, or the first write fails RLS. Titan reports the mismatch by name
rather than passing Postgres's opaque error through.

## Air-gapped

Set `jwks_url` explicitly. Discovery fetches `/.well-known/openid-configuration`
from the issuer, which may be unreachable from an enclave even when a mirrored
JWKS is not:

```json
{ "auth": { "mode": "oidc", "issuer": "https://idp.internal/realms/eng",
            "jwks_url": "https://jwks-mirror.internal/keys", "audience": "titan" } }
```

A mirrored discovery document must keep the original `issuer` value — Titan
refuses one that disagrees, because that is either a misconfiguration or an
attack.

## Verifying

```bash
titan doctor      # reports the auth mode and whether the JWKS is reachable
```

It fails at startup rather than on a user's first request, so a bad issuer or an
unreachable IdP surfaces during deployment.

## What is not covered

The tests replay documented provider wire formats. They do not exercise a live
IdP's consent screen, refresh tokens, or revocation. Get a real token from your
provider and confirm `titan doctor` accepts it — a five-minute check that closes
the gap.
