# Trust documentation

This folder exists to answer the questions a security or procurement
questionnaire actually asks, in one place, with claims traceable to the
files that make them true. It is written for a reviewer evaluating Titan or
the hosted console at `titan.zybuu.com`, not for a Titan operator configuring
their own deployment — that audience is `docs/access-policy.md` and
`docs/ops/`.

Every claim in these documents points at a file in this repository. Where we
don't have something (a certification, a third-party audit, a redundant
deployment), the documents say so plainly rather than staying silent about
it — silence on a questionnaire reads as "we didn't think about it," and the
honest answer is more useful than that.

## In this folder

| Document | Answers |
|---|---|
| [security-posture.md](security-posture.md) | Architecture summary for a security reviewer: trust boundaries, data flow, storage, authentication, container hardening, telemetry, and what is not yet in place |
| [data-handling.md](data-handling.md) | What data Titan stores, where, for how long, what an operator can export or delete, and subprocessors for the hosted console |
| [vulnerability-disclosure.md](vulnerability-disclosure.md) | The disclosure policy from `SECURITY.md`, in questionnaire form |
| [incident-response.md](incident-response.md) | Detection, severity, first-hour response, and notification commitments for a one-person company |
| [backup-restore.md](backup-restore.md) | How the Postgres store and its volumes are backed up and restored, and the recovery point that gives you today |

## Related, outside this folder

- [`docs/access-policy.md`](../access-policy.md) — the terms that govern the
  hosted console at `titan.zybuu.com`: what an invited account can do, what
  ends access, and what Zybuu does and does not owe a trial user.
- [`docs/ops/enabling-auth.md`](../ops/enabling-auth.md) — authentication
  modes, session handling, and OIDC verification for a self-hosted
  deployment.
- [`docs/ops/air-gap.md`](../ops/air-gap.md) — the offline deployment
  posture, including the sections marked as engineering judgment rather than
  verified practice.
- [`docs/architecture/03-security.md`](../architecture/03-security.md) — the
  detailed isolation and prompt-injection design notes, including their
  verification status.
- [`SECURITY.md`](../../SECURITY.md) — how to report a vulnerability.

## Who wrote this

Zybuu is a one-person company. That fact is relevant to a questionnaire in
more than one place in this folder, and it is stated once, here, so it does
not need restating: there is one maintainer, one on-call, and no redundancy
in the team behind this software. What that does and does not mean for risk
is spelled out document by document rather than summarized away.
