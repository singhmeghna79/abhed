# Titan — OIDC Provider Configuration

Verified against the real token and JWKS shapes each provider issues
(`internal/auth/providers_test.go`). The differences below are the ones that
actually break a naive verifier, so copy the matching block rather than
guessing at claim names.

## The two settings that cause most failures

**`issuer` must match the token's `iss` claim byte-for-byte.** Auth0 includes a
trailing slash; most others do not. Entra appends `/v2.0`. A mismatch produces a
confusing "unexpected issuer" rejection that looks like a key problem but is a
one-character config error. There is a test for exactly this.

**`tenant_claim` is where multi-tenancy comes from.** Titan scopes storage and
row-level security by it, so pointing it at the wrong claim silently puts every
user in one tenant. If no claim carries a tenant, leave it unset — Titan falls
back to `default` rather than an empty string.

## Keycloak

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "audience": "titan",
    "tenant_claim": "org_id",
    "groups_claim": "groups"
  }
}
```

Groups arrive slash-prefixed (`/engineering`), so `require_group` must include
the slash. Realm roles live under `realm_access.roles`, which Titan does not read
— map the roles you need into a top-level `groups` claim with a client mapper.

## Okta

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://example.okta.com/oauth2/aus1a2b3c",
    "audience": "api://titan",
    "tenant_claim": "orgId",
    "groups_claim": "groups"
  }
}
```

The issuer includes the authorization server id. Using the org URL alone
(`https://example.okta.com`) is the most common misconfiguration. Groups need a
claim added to the authorization server; they are not present by default.

## Microsoft Entra ID (Azure AD)

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://login.microsoftonline.com/<tenant-guid>/v2.0",
    "audience": "api://<app-id>",
    "tenant_claim": "tid",
    "groups_claim": "groups"
  }
}
```

`tid` is the directory tenant, which is usually what you want for `tenant_claim`.
Groups are **object GUIDs**, not names, so `require_group` takes a GUID. Above
roughly 200 group memberships Entra omits the claim and sends an overage
indicator instead; use app roles rather than groups at that scale.

Entra also sends `nbf`, so keep the default clock-skew leeway.

## Auth0

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://example.eu.auth0.com/",
    "audience": "https://titan.internal/api",
    "tenant_claim": "https://titan.internal/tenant",
    "groups_claim": "https://titan.internal/groups"
  }
}
```

Note the **trailing slash** on the issuer — Auth0 includes it. Custom claims must
be namespaced with a URI or Auth0 strips them, which is why the claim names look
like URLs. `aud` is an array; Titan accepts any element matching.

## Google Workspace

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://accounts.google.com",
    "audience": "<client-id>.apps.googleusercontent.com",
    "tenant_claim": "hd"
  }
}
```

`hd` is the hosted domain, which is the closest thing Google offers to a tenant.
**Google issues no groups claim**, so omit `groups_claim` and do not use
`require_group` — use Workspace-level access control instead.

## Air-gapped deployments

Set `jwks_url` explicitly. Discovery fetches `/.well-known/openid-configuration`
from the issuer, which may not be reachable from an enclave even when the JWKS
host is:

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "jwks_url": "https://jwks-mirror.internal/keys",
    "audience": "titan",
    "tenant_claim": "org_id"
  }
}
```

Titan refuses a discovery document whose `issuer` disagrees with the configured
one, so a mirrored document must preserve the original issuer value.

## Verifying

```bash
titan doctor
```

reports the auth mode and, for `oidc`, whether the JWKS is reachable and tokens
will be verified. It fails at startup rather than on the first user request, so a
misconfiguration surfaces during deployment.

To check a specific token:

```bash
curl -H "Authorization: Bearer $TOKEN" http://titan.internal:8080/v1/sessions
```

A 401 body names the reason — expired, wrong audience, unknown key id — which is
safe to return because it helps a legitimate client fix its configuration and
tells an attacker nothing they could not learn by trying.

## What is not covered

These tests replay documented wire formats. They do not exercise a live IdP's
token *issuance*, consent screens, refresh flows, or revocation. Before going to
production, obtain a real token from your provider and confirm `titan doctor`
accepts it — that is a five-minute check that closes the remaining gap.
