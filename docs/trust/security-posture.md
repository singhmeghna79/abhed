# Security posture

A summary for a security reviewer. Every claim below names the file that
makes it true — read the file if you need more than the summary.

Titan is pre-release (0.1.x), built by a one-person company. No claim here
should be read as a certification; see "What is not in place" at the end.

## Trust boundaries

**Sandbox tiers.** Titan executes agent-issued commands inside a sandbox
whose strength is explicit and self-reporting (`internal/sandbox/sandbox.go`):

| Tier | Mechanism | Notes |
|---|---|---|
| `none` | Runs directly on the host | Suitable only for a trusted single-user local run |
| `process` | Process-level confinement (macOS `sandbox-exec`, Linux namespaces/seccomp) | The deployed default — see `deploy/config.json`'s `sandbox.min_tier: "process"` |
| `container` | OCI container: namespace isolation, shared kernel | |
| `vm` | microVM or gVisor userspace kernel | Strongest tier implemented |

`Select` (`internal/sandbox/sandbox.go`) picks the strongest backend
available that meets the configured `MinTier`, and **refuses to start** if
none qualifies, naming what it tried and how to fix it — it never silently
downgrades (see the comment above `Select` and `README.md`'s "The sandbox
never silently downgrades"). This is enforced for the hosted console by
`internal/deploycheck/config_test.go`, which fails the build if
`deploy/config.json` ships `sandbox.min_tier: "none"` or empty.

Note: `docs/architecture/03-security.md` describes a more elaborate tier
design (gVisor by default, a microVM per session) as the target architecture.
That document is explicit that this is **engineering judgment, not verified
practice**, and the tiers actually implemented in
`internal/sandbox/sandbox.go` today are `none` / `process` / `container` /
`vm`, verified by the escape tests in that package. Treat the design doc as
direction, and this document and the code as what runs today.

**Policy engine order.** Every tool call is evaluated in a fixed order,
documented at the top of `internal/policy/policy.go`:

```
Hooks → Deny rules → Ask rules → Permission mode → Allow rules → Callback
```

**Deny rules are absolute.** A matching deny rule blocks the call even in
`bypass` mode — the most permissive mode Titan has (`internal/policy/policy.go`,
`ModeBypass` comment: "dangerous; refusable by org policy"). Rules are
scoped per-command, not per-tool: allowing `bash(npm test)` never allows
`bash(rm -rf /)`. `internal/deploycheck/config_test.go` asserts the deployed
deny list actually blocks SSH keys, cloud credentials, `.env` files, and the
Titan config itself, and that no allow rule pre-approves an interpreter or
file-reading command that could be used to exfiltrate one of those files
under the cover of an approved rule.

**Extensions may only veto, never permit.** `internal/extension/extension.go`
states the rule directly: "An extension may VETO, never PERMIT." Hooks run
first in the policy order specifically so they can veto before anything
else executes (`internal/extension/host.go`), and an extension's refusal is
enforced the same way a deny rule is — it can turn an `allow` into a `deny`,
never the reverse. `internal/extension/extension_test.go` asserts this
directly (an extension can force `Decision: Deny`).

**Tool output is tagged untrusted at ingest.** `internal/store/schema.sql`'s
`events` table carries a `trust` column (`CHECK (trust IN ('trusted',
'untrusted'))`) on every event, so provenance travels with the data rather
than being inferred later. File contents, tool output, and MCP responses are
tagged at the point they enter the system (`README.md`: "Everything untrusted
is tagged at ingest"). Policy decisions are made on the *action* requested,
never on the untrusted text that motivated it (`docs/architecture/03-security.md`
§2, "L2").

## Data flow — what leaves the deployment

**Nothing, by default.** The container Titan runs in has no route to the
host filesystem and, per `deploy/run.sh`'s sandbox flags, the shell tool
itself gets no network access (`sandbox.allow_network: false` in
`deploy/config.json`; verified by
`TestProcessSandboxBlocksNetworkByDefault` per
`docs/architecture/03-security.md` §7).

**`web_search`, when enabled**, is the one narrow, structured exception: a Go
tool in the server process makes the request, not the sandboxed shell, so a
compromised session cannot turn it into an arbitrary outbound connection
(`internal/deploycheck/websearch_test.go`'s comment: "Web search reaches the
internet through a Go tool in the server process instead... with no way to
turn it into an arbitrary request"). It is **enabled on the deployed hosted
console** (`deploy/config.json`: `"web_search": {"enabled": true, "provider":
"duckduckgo"}`) and is a separate capability from shell networking — enabling
one does not enable the other, and `TestWebSearchSurvivesSandboxHardening`
(`internal/deploycheck/websearch_test.go`) fails the build if that separation
breaks.

**The model endpoint the operator configured.** Prompts and context go to
whatever model endpoint is set in `model.providers` — for the hosted console
today that is a model running on the same machine (`docs/access-policy.md`:
"On this deployment that is a model running on the same machine, so prompts
do not leave it. That is a property of this deployment, not a promise about
every deployment."). Titan is model-agnostic by construction
(`docs/vision.md`); a self-hosted operator who points it at a third-party API
is sending data to that API, and that is their configuration choice, not
Titan's.

## Storage

**Postgres**, with a schema in `internal/store/schema.sql`. Two properties
the schema comment states directly: events are append-only (no `UPDATE`, no
`DELETE`), and tenant isolation is enforced by row-level security, not only
by query construction.

**Append-only by trigger, not convention.** `titan_events_immutable()` and
`access_events_immutable()` are triggers that raise an exception on any
`UPDATE` or `DELETE` against `events` and `access_events` — the database
itself refuses the operation, regardless of what the application code does
or how it is compromised.

**`FORCE ROW LEVEL SECURITY`**, not just `ENABLE`. The schema comment explains
why this specific word matters: a table's owner bypasses ordinary RLS, and
the application role almost always owns the tables it created, so `ENABLE`
alone leaves the isolation policy silently inert. This was found by
`TestRowLevelSecurityIsolatesTenants`, which could read another tenant's rows
until `FORCE` was added (`README.md`'s evidence-discipline section tells the
same story). `sessions`, `events`, `checkpoints`, `access_grants`, and
`access_events` all carry `ENABLE` and `FORCE` together, with a
`current_setting('app.tenant_id', true)` policy on each.

**Two database roles, and the app refuses to run as the wrong one.**
`deploy/run.sh` provisions `titan_admin` (cluster superuser, used only by the
script to provision) and `titan_app` (`NOSUPERUSER NOBYPASSRLS NOCREATEROLE
NOCREATEDB`, owns the application's tables). The reason is in the script's
comment: "Postgres does not apply row-level security to a superuser, so a
Titan connected as one has every isolation policy in the schema and none of
the isolation." After provisioning, `run.sh` checks
`rolsuper OR rolbypassrls` on `titan_app` and **exits with an error rather
than starting Titan** if that role is privileged.

## Authentication

Four modes (`docs/ops/enabling-auth.md`): `none` (local dev only), `local`
(username/password Titan holds), `proxy` (identity from a trusted reverse
proxy's headers), and `oidc` (verified token or browser sign-in against an
IdP). `local` and `oidc` can run side by side.

- **Local accounts use bcrypt** at the library default cost
  (`internal/auth/local.go`, `internal/auth/filestore.go`). A wrong password
  and an unknown username return the same error in the same time — a missing
  user is still run through bcrypt against a dummy hash — specifically to
  prevent timing-based username enumeration, with a test asserting it.
- **OIDC verification is full**, not trust-on-receipt: signature, issuer,
  audience, and expiry are checked against the JWKS
  (`docs/ops/enabling-auth.md`'s flow steps 1-3), with PKCE (S256) even
  though a client secret is also configured, single-use `state`, and a
  documented test for each property (signature/issuer/audience/expiry, replay,
  forged state, PKCE enforcement, open-redirect rejection, cookie flags,
  session expiry, logout).
- **Invite-only.** Self-registration (`allow_signup`) defaults off; the
  hosted console has no public signup at all
  (`deploy/GO-LIVE.md`: "Create your account — there is no public signup").
  Access is granted by a single-use, expiring invite
  (`internal/server/admin.go`'s `inviteStore`), and a new account lands with
  no groups — it can use the console and read its own sessions, and cannot
  administer the console, change policy, or invite anyone else
  (`docs/access-policy.md`, `internal/server/admin.go` comment on the group
  plumbing).

## Container hardening (`deploy/run.sh`)

Every flag is commented in the script with the specific threat it closes.
Summary:

| Control | Flag | Why |
|---|---|---|
| No host filesystem access | no bind mount; workspace is a named volume | "no host path is mounted, so there is no host path to escape to" |
| Unprivileged user | `--user 10001:10001` | |
| No Linux capabilities | `--cap-drop ALL` | "An agent's shell has no business with CAP_NET_ADMIN or CAP_SYS_ADMIN" |
| No privilege escalation | `--security-opt no-new-privileges` | Blocks setuid-binary escalation |
| Immutable image | `--read-only` | A compromise can't install a backdoor into the binary or persist outside the two writable volumes |
| Ephemeral scratch space | `--tmpfs /tmp:rw,noexec,nosuid,size=512m` | Gone on restart, `noexec` so a dropped payload can't run |
| Resource caps | `--memory 2g --memory-swap 2g --pids-limit 512 --cpus 2` | "A runaway or hostile agent should exhaust its own limits, not the host's" |
| Loopback-only bind | `--publish 127.0.0.1:8080:8080` | The reverse proxy is the sole route in |
| Config mounted read-only at the managed path | `--volume $CONFIG:/etc/titan/config.json:ro` | Loading config at the managed path sets `Managed`, making `bypass` mode refusable and policy non-escalatable from inside the container |
| Skills mounted read-only | `--volume $SKILLS:/workspace/.titan/skills:ro` | Skills are instructions; the agent must not be able to rewrite its own operating rules |

`deploy/GO-LIVE.md`'s "What is protecting you" table restates this same set
for the live `titan.zybuu.com` deployment, plus the transport and browser
layers (TLS/HSTS, CSP, `frame-ancestors 'none'`) and auth/authorization
(`bypass` refused even when signed in, rate limits on sign-in).

## Telemetry

**Off by default.** `internal/config/config.go`'s `TelemetryConfig.Enabled`
is a bare `bool` with no default set, so it is `false` unless explicitly
turned on — and `deploy/config.json`, the config actually deployed to
`titan.zybuu.com`, does not set a `telemetry` block at all, so the hosted
console runs with it off.

**When enabled, it goes to the operator's own collector**, not to Zybuu.
`internal/telemetry/otlp.go`'s package comment: it "ships them over OTLP/HTTP
to whatever collector the operator runs," posting to `<Endpoint>/v1/traces`
where `Endpoint` is a value the operator sets — there is no built-in Zybuu
endpoint. The event log stays the source of truth regardless; telemetry is
described as "a tap on it," so a collector being down cannot slow or fail a
session.

## What is NOT in place

Stated plainly, per `docs/access-policy.md`'s own convention ("What we owe
you... stated plainly rather than buried"):

- **No SOC 2, ISO 27001, HIPAA, or FedRAMP certification.**
  `docs/access-policy.md` says this outright: "No certification. No SOC 2,
  ISO 27001, HIPAA or FedRAMP." `docs/ops/air-gap.md`'s compliance mapping is
  itself marked `[E — unverified; confirm with your compliance function]`.
- **No third-party penetration test yet.** `README.md`'s evidence-discipline
  section: "A human red-team engagement remains outstanding and is not
  substitutable" for the internal adversarial suite. The suite that exists
  (`internal/redteam`) proves the implemented controls resist the attacks its
  author thought of; it is not independent verification.
- **Single node.** The hosted console runs on one machine
  (`deploy/GO-LIVE.md`: "The site is up only while this Mac is awake and
  online"). There is no horizontal scaling, no failover, and no uptime
  commitment (`docs/access-policy.md`: "No uptime commitment. One machine.
  When it is down, it is down.").
- **One maintainer.** Zybuu is a one-person company (`docs/vision.md`). There
  is no second reviewer, no on-call rotation, and no bus-factor mitigation
  beyond what is written down in this repository. See
  `docs/trust/incident-response.md` for what that means in practice.
