# Sessions and audit

Titan's state is a chronological stream of events. The agent is a function from
that history to an action, and the runtime is a function from an action to an
observation. Everything else — replay, forking, export, audit — falls out of
that rather than being built separately.

## What is recorded

Every user message, model reply, reasoning block, tool call, approval, denial,
observation, compaction and terminal reason. Each event carries a sequence
number, an actor, and a **trust tag**: content read from files, tool output and
search results is data, never instruction, and the tag travels with it.

## Storage

In memory by default, which loses everything when the process exits. Postgres
makes sessions durable, and is what `/sessions`, `/resume`, replay and audit
need:

```json
"storage": { "driver": "postgres", "dsn": "postgres://...", "tenant": "default" }
```

The event table is append-only, enforced by a database trigger rather than by
convention. Tenants are isolated by row-level security, enforced at the data
layer as well as in the application — a boundary that exists in only one place
is not a boundary.

## Going back

```
/tree            the session's steps
/fork 12         rebuild the conversation up to step 12 and continue from there
```

A wrong turn three steps back should cost three steps, not the session.
Everything before it was still right, and re-establishing it means paying for
the same reading twice.

Forking needs no extra storage: the messages are derivable from events that were
already being recorded for audit. The log was never a description of the
session — it is the session.

## Getting it out

```
/export                     a self-contained HTML transcript
/export session.json        the raw events, for a program
```

The HTML embeds everything and fetches nothing, so it works from a filesystem,
an email attachment, or an air-gapped machine. Tool output is escaped and never
rendered as markup: a transcript is a record of untrusted content, and a session
that read a hostile file must not produce a page that runs it.

## Replay

```bash
curl -s $B/v1/sessions/$SID/replay
```

Deterministic replay is the point of the design. It answers what an agent
actually did, not what it reported doing — which is the question that matters
after an incident, and the one a summary cannot answer.

## Context over a long session

History is summarized as it approaches the window, keeping the recent exchanges
verbatim and re-injecting the system prompt and memory file whole. A single tool
result is capped at a quarter of the window, since no amount of summarizing
earlier turns rescues one message too large to send.

Compaction is visible: `/cost` reports how many have happened, and each one is
an event with its token accounting. It is a capacity number, not a curiosity —
every compaction invalidates the prefix cache and pays cold prefill again.
