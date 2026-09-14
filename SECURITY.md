# Security Policy

Titan is pre-release software (0.1.x) built and maintained by one person at
Zybuu. This document says what we support, how to report a problem, and what
you can expect back.

## Supported versions

| Version | Supported |
|---|---|
| 0.1.x (pre-release) | Yes — this is the only line that exists |

There is no stable release yet and no long-term-support branch. Every fix
lands on `main`. If you are running an older commit, the first thing we will
ask is whether the issue reproduces at `HEAD`.

## Reporting a vulnerability

Email **security@zybuu.com**.

If you do not get a response and need a fallback, use **support@zybuu.com** —
that is the address currently monitored day to day, and it reaches the same
person.

Please include:

- What you found and why it matters (impact, not just mechanism).
- Steps to reproduce, or a proof of concept.
- The commit or version you tested against.
- Whether the finding is against the harness itself, the hosted console at
  `titan.zybuu.com`, or the `zybuu.com` site.

Do not open a public GitHub issue for a security report. Use email so the
report isn't public before a fix ships.

## What to expect

- **Acknowledgement within 3 business days.**
- **A fix or a mitigation plan within 30 days for high-severity reports.**
  Lower-severity reports may take longer; we will tell you the plan, not
  leave you guessing.
- Credit in the fix's changelog entry or commit message, if you want it.
- One person is doing this work. Response times are honest estimates, not
  contractual commitments — see `docs/access-policy.md` for the same
  disclosure applied to the hosted console.

## Safe harbor

We consider good-faith security research conducted under this policy to be
authorized:

- We will not pursue legal action against you for research that stays within
  this scope, avoids privacy violations and service disruption, and is
  reported to us before any public disclosure.
- If a third party (for example, a cloud or hosting provider) initiates
  action against you for research conducted in accordance with this policy,
  we will make it known that your actions were authorized.
- This safe harbor does not extend to attacks on other users, attempts to
  access another account's data, or anything covered by "out of scope"
  below. `docs/access-policy.md` §P1 already invites exactly this kind of
  research against the hosted console and asks that it be done against your
  own account, not someone else's.

We ask that you:

- Give us a reasonable time to investigate and remediate before any public
  disclosure.
- Make a good-faith effort to avoid privacy violations, data destruction, and
  interruption of the service for other people.
- Only interact with accounts you own or have explicit permission to test.

## Scope

In scope:

- **The harness** — everything under `cmd/`, `internal/`, and `sdk/`: the
  agent loop, the policy engine (`internal/policy`), the sandbox
  (`internal/sandbox`), the MCP gateway, the auth layer (`internal/auth`),
  the Postgres store (`internal/store`), and the server (`internal/server`).
- **The console** — the web UI served by `titan serve` and the hosted
  instance at `titan.zybuu.com`.
- **Deploy scripts** — everything under `deploy/`, including
  `deploy/run.sh`, the container hardening it applies, and the access-grant
  and revocation scripts.
- **The zybuu.com Functions** — the Cloudflare Pages Functions under
  `web/zybuu/functions/`, including the access-request endpoint.

## Out of scope

- **The model's own behaviour.** Titan is model-agnostic (`docs/vision.md`);
  what a given model chooses to say or do, hallucination, or bias in its
  output is not a Titan vulnerability. If Titan's policy engine or sandbox
  fails to *contain* a model's action, that is in scope — the containment
  failure, not the model's decision.
- **Third-party providers.** The behavior, availability, or security of
  model endpoints, identity providers, Cloudflare, Resend, or any other
  service an operator configures Titan to talk to. Report those to the
  provider; report to us only where Titan's own handling of their response
  is the problem (for example, treating their output as untrusted per
  `docs/architecture/03-security.md`).
- Findings that require an operator to have already misconfigured Titan in a
  way the documentation explicitly warns against (for example, running with
  `sandbox.min_tier: none` in `docs/access-policy.md`'s or
  `deploy/GO-LIVE.md`'s deployment context, which the deployed
  `deploy/config.json` does not do).
