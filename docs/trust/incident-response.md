# Incident response

A short, honest runbook. Zybuu is a one-person company — this is what
happens when there is one person, not a security operations center, and it
is written that way on purpose rather than dressed up as something bigger.

## Detection sources

There is no dedicated monitoring team. Detection depends on:

- **Application logs** — `abhed serve` logging, and the append-only event
  store itself (`internal/store/schema.sql`), which is queryable for
  anomalous action sequences after the fact even without live alerting.
- **The health endpoint** — `GET /v1/health`, checked manually
  (`deploy/GO-LIVE.md`'s verify step: `curl -s https://abhed.zybuu.com/v1/health`).
  There is no automated uptime monitor wired to it today; the operator
  checks it and would notice a service-down condition on next use, not
  necessarily immediately.
- **Cloudflare** — DNS, the Pages deployment, and (for mail) DNS-based
  delivery signals sit in front of `zybuu.com`; Cloudflare's own dashboard
  is a source of anomaly signal for the site surface (traffic spikes,
  blocked requests) even though Abhed itself is not proxied through it
  (`deploy/GO-LIVE.md`: the console is reached through a tunnel/port-forward
  to the operator's own machine, separate from the Cloudflare Pages site).
- **Policy denials** as a signal. `docs/ops/air-gap.md`'s observability
  section names "Policy denials" as an injection/misconfiguration signal
  worth watching, even though the automated alerting pipeline described
  there is aspirational (`[E]` engineering judgment) rather than running
  today.

There is no automated log inspection or anomaly-detection pipeline in
production yet. Detection today is substantially manual: something looks
wrong, someone checks the logs and the event store.

## Severity levels

| Level | Definition | Example |
|---|---|---|
| **Sev 1 — Critical** | Active exploitation, data breach, or an account able to reach data or systems outside its authorization | A sandbox escape in use against the hosted console; another tenant's session data readable |
| **Sev 2 — High** | A confirmed vulnerability with real exploit potential, not yet known to be exploited | A policy bypass that would let an approved-but-scoped bash rule reach an unintended file, found by internal testing or a report |
| **Sev 3 — Moderate** | A weakness with limited blast radius, or requiring an already-privileged position | An admin-only endpoint with a minor authorization gap |
| **Sev 4 — Low** | Hardening opportunity, no meaningful exploit path today | A dependency with an advisory that does not reach exploitable code |

## First-hour steps

For anything Sev 1 or Sev 2 involving the hosted console:

1. **Revoke access via the admin dashboard.** `internal/server/admin.go`'s
   `revokeAccess` cuts the session immediately — "Cut the session before
   anything else can fail. Revocation that leaves a live session working is
   not revocation" — and disables the account. If the incident is one
   account misbehaving, this is the first action, before investigation is
   even finished, because a live session is the actively bad state.
2. **Rotate the database password** via `deploy/run.sh`. The password lives
   at `deploy/.db-password`; deleting it and re-running `deploy/run.sh`
   generates and applies a fresh one (the script's own comment: it is
   "generated once and kept beside the deploy config" — replacing the file
   forces regeneration on next run). This is the response to any suspicion
   the database credential itself is compromised.
3. **Rotate Cloudflare Pages secrets** via `deploy/set-access-email.sh`. It
   resets `RESEND_API_KEY` and `ACCESS_TO` without ever writing the key to a
   file or the repository (the script pipes the key directly into
   `wrangler pages secret put` on stdin). Run this if the Resend API key or
   the access-request routing address is suspected compromised.
4. **If the container image or host itself is suspected compromised**, stop
   the container (`podman stop abhed` / `docker stop abhed`) rather than
   restart it — the workspace and state volumes persist independently of the
   container, so stopping does not lose data, and a compromised running
   process should not be trusted to shut down cleanly.

## Notification commitment

**Affected users are notified within 72 hours** of confirming an incident
that affects their data or access, consistent with the reporting timelines
in `SECURITY.md`. For the hosted console, notification uses the same channel
`internal/server/admin.go`'s `mailRevocation` already uses for access changes
— email via Resend, from `support@zybuu.com` — because that path exists,
is tested, and reaches the same inbox described in
`docs/access-policy.md`.

This is a commitment on timeline, not on completeness of information at
that point: the 72-hour notice says what is known and what is being done,
and a fuller account follows once the investigation is further along.

## Post-incident write-up

Every Sev 1 or Sev 2 incident gets a written post-incident report, added to
this folder (`docs/trust/incidents/<date>-<short-name>.md`, created when the
first such incident occurs — there is no incident to report as of this
writing). At minimum: timeline, root cause, what was exposed and to whom,
remediation taken, and what changed in the code, config, or process as a
result. This follows the same evidence-discipline convention documented in
`README.md`'s "Evidence discipline" section: incidents are written up
honestly rather than papered over, the same as the design bugs the project
already documents finding (row-level security silently inert, the auth
ordering bug, the bundle manifest that listed its own digest).

## What this runbook does not cover

There is no on-call rotation, no secondary responder, and no guaranteed
response time outside business hours — one person is the entire incident
response capacity of this company. `docs/trust/README.md` and
`docs/access-policy.md` say the same thing in their own contexts; it is
repeated here because it directly bounds what "first-hour steps" can mean in
practice at 3 a.m.
