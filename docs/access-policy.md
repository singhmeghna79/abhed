# Titan console access policy

This governs the hosted console at `titan.zybuu.com`. It does not govern Titan
the software, which you run yourself under whatever rules you like.

Access is free and is a trial. There is no contract behind it, which cuts both
ways: nothing is owed to you, and nothing is extracted from you.

## What you get

A single-use invite creates one account. That account can use Titan Chat, run
the agent, and read its own sessions. It cannot administer the console, change
policy, or invite anyone else.

Invites expire — seven days by default. An account outlives its invite; the
code is only the door.

## What the console does with your data

- **Your sessions are yours to read.** Nobody else's account can see them.
- **Your actions are recorded.** Every tool call the agent makes is an event in
  an append-only log, and administrators can read that log. This is the
  product's central claim; it applies to you too.
- **Prompts go to the configured model.** On this deployment that is a model
  running on the same machine, so prompts do not leave it. That is a property
  of this deployment, not a promise about every deployment.
- **Nothing is sold, and there is no analytics or tracking.**

## What ends access

Any of these, at our discretion, with the clause named in the notice you
receive:

**P1 — Attacking the service.** Attempting to escape the sandbox, escalate
privileges, reach the host, exhaust resources deliberately, or access another
account's data. Security research is welcome; do it against your own install
and tell us what you find.

**P2 — Using it to attack others.** Scanning, credential stuffing, malware
development, or generating material intended to defraud or harass.

**P3 — Illegal content or purpose.** Including material that sexualises
children, incites violence, or violates sanctions.

**P4 — Automated or resource abuse.** Scripting the console for bulk work,
sharing one account across a team, or consuming capacity such that other
trials cannot run. Ask instead; the answer is usually yes.

**P5 — Misrepresentation.** Obtaining access under a false name, employer, or
purpose.

**P6 — Capacity.** Not your fault. This runs on one machine, and access may be
withdrawn to make room or because the trial is ending. You will be told this is
the reason.

**P7 — At request.** You asked us to close the account.

## How revocation works

Access ends immediately: the session stops working on its next request and the
account is disabled. It is disabled rather than deleted, so the record of what
happened survives — that is the point of an audit log.

You receive an email naming the clause. For P1 through P5 that notice is the
whole process; there is no appeal built into a free trial, though replying
reaches a person and we would rather be wrong briefly than unfair permanently.

Your transcripts stop being reachable by you when the account is disabled. They
are not deleted, because the audit trail they belong to is append-only. If you
want them, export before you lose access, or ask.

## What we owe you

Very little, stated plainly rather than buried:

- **No uptime commitment.** One machine. When it is down, it is down.
- **No durability commitment** beyond the Postgres store's own behaviour.
- **No certification.** No SOC 2, ISO 27001, HIPAA or FedRAMP.
- **No notice period** before the trial ends.

If any of that is a problem for what you want to do, the answer is to run Titan
on your own infrastructure, and we would rather have that conversation than
have you rely on this one.

## Changes

This policy changes without notice while Titan is in trial. The version that
applied when your access ended is the one quoted in your revocation notice.
