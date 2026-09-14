# Why Zybuu, and why Abhed

The answer to the first question every buyer and every investor asks.

## The thesis

Every enterprise is going to run AI agents: software that reads, decides,
acts and reports across the code, tickets, records and systems the business
runs on. The market is selling that capability one way — as a vendor's cloud,
running the vendor's model, on the vendor's terms, with the organisation's
data leaving the building on every turn.

A large part of the economy cannot buy it that way. Banks and insurers,
hospitals, defence and government, utilities, and every company with a
data-residency clause in a customer contract are offered a choice between
waiting, accepting a weaker product, or building the whole thing themselves.
Most are building. Most of what they build is the same thing, badly: a loop, a
sandbox that is not quite one, a log that is not quite complete, an approval
prompt that only works when a person happens to be watching.

**Zybuu builds the infrastructure that lets an organisation run AI workloads
under its own control** — SaaS, privately hosted, or fully air-gapped — with
the same guarantees at every tier. The vision is not a better chatbot. It is
the layer underneath: the part that decides what a model may do, does it
safely, records it, and can prove afterwards what happened. That layer is what
an enterprise actually has to trust, and today almost nobody sells it.

## Abhed is the first product

Abhed is that layer for agents: an agent harness. The model is a replaceable
part. The harness is the product.

"Under your control" means five specific things, and each one is a property
this repository tests rather than a line of copy:

- **It runs where the data is.** One static binary, from a laptop to an
  air-gapped rack, with a signed offline bundle for the rack.
- **It runs any model.** Twenty providers over three wire formats, including
  the one on your own GPUs. Changing vendors is a line of config, not a
  migration — which is also what keeps the vendors honest.
- **The agent is sandboxed by default**, inside a boundary the operator sets
  and the agent cannot lift, with an approval policy the operator owns.
- **Every action is on the record.** An append-only event log, replayable
  step by step, with every approval and refusal and who made it, exportable as
  traces to the tools the organisation already runs.
- **It embeds without weakening.** The SDK gives a program the same loop with
  the same guarantees. The console is a reference application built on it,
  not the product.

## How Abhed competes with products people already trust

We do not compete on the model. We run theirs, or yours. We compete on where
the agent runs, what it can prove, and what it costs to operate.

**Against Claude Code and OpenAI's Codex CLI.** Excellent tools, built around
their makers' own models and clouds. For a developer who can send their code
to that cloud they are hard to beat, and we do not try to. For an organisation
that cannot, they are not on the table, and that organisation is our customer.

**Against pi.dev.** A superb harness for one developer in a terminal, with
many models. For that developer, Pi is the better choice than Abhed, and we
say so. Abhed is built for a platform team serving an organisation: many
users, tenants, policy, audit, a server, an SDK.

**Against CrewAI and frameworks like it.** A toolkit, and a good one, for a
team assembling agents in Python. The sandbox, the approvals, the audit log
and the deployment are left for that team to build. Abhed is the assembled,
hardened thing, with a framework underneath it — extensions, skills, MCP,
custom providers — for the parts that should be yours.

**Against open-source harnesses such as OpenHands and Goose.** The closest
competitors, and the ones to respect: any model, sandboxed, with real
communities and published benchmarks. Abhed's difference is narrower and
specific — the audit record and the policy engine are in the core and
enforced by the database, the deployment is one signed binary with an
offline bundle, and the SDK carries the same guarantees into your own code.

The pattern is the same in each case: the incumbents own the developer who
can accept their terms, and most regulated firms can — a cloud region and a
no-retention agreement satisfy them. Abhed is for the ones that cannot:
defence and intelligence contractors, sovereign and public-sector
deployments, operational networks with no route out, and the banks whose
policy is no cloud at all. That is a smaller market than "the regulated
economy", and it is the honest one.

## What we are not claiming

Abhed is not the best model; it does not have one. It is not a multi-agent
framework with roles and message passing. It is not the right tool for a solo
developer who is happy in the cloud. It is pre-release and invite-only, and
its properties are proven at the scale of its test suite and its own
deployment, not yet at a customer's.

## The business, as proposed

**Land** with platform and security teams in regulated industries, usually
through the SDK: they have an agent they cannot ship because of where it would
run, and Abhed is the shortest path to shipping it.

**Charge** per deployment, annually, by tier — hosted, private, air-gapped —
with support. The tiers are the same product; what differs is what the
customer needs proven and who is on the hook for it. Prices are not announced.

**Expand** with further products on the same infrastructure layer, each
scoped the way Abhed is (`abhed.zybuu.com`), sharing the store, the identity,
the policy, and the record.
