# Titan

An on-prem, air-gap-capable deep agent platform. Model-agnostic by construction: the
better the reasoning model you point it at, the better it performs.

**Status:** design phase. No implementation yet — `docs/` is the plan.

## The thesis

A controlled study found **harness-induced variance exceeds model-induced variance by
7.80×** on SWE-bench Verified, with ranking reversals in 6 of 9 model-pair comparisons.
The scaffold around the model — context management, tool design, subagents, permissions —
is a first-class engineering variable, not glue code.

Titan is built on that: the harness is a separately engineered, separately evaluated layer
behind a provider abstraction. Swap in a better model, get a better agent. Improve the
harness, get a better agent. Both paths compound.

## Documents

| Doc | Contents |
|---|---|
| [Principles](docs/architecture/01-principles.md) | 12 design principles, each tagged by evidence status |
| [System architecture](docs/architecture/02-system-architecture.md) | Planes, agent loop, subagents, retrieval, model abstraction |
| [Security](docs/architecture/03-security.md) | Isolation tiers, prompt injection, MCP supply chain |
| [Sizing](docs/architecture/04-sizing.md) | VRAM math, deployment tiers, prefill economics, cost |
| [Roadmap](docs/architecture/05-roadmap.md) | Build vs adopt, phasing, team, effort, risks |
| [Air-gap ops](docs/ops/air-gap.md) | Offline install, egress broker, audit, compliance |

Reproduce the capacity model: `python3 docs/architecture/sizing.py`

## Evidence discipline

Every claim carries its provenance:

- **[V]** Verified — survived 3-vote adversarial verification against primary sources
- **[C]** Computed — derived arithmetically here; reproducible via `sizing.py`
- **[E]** Engineering judgment — not established by research; validate before betting on it

The design pass ran 112 agents over 6 research angles with adversarial verification
(≥2/3 refutations kill a claim). It produced 15 verified findings — and **six refuted
claims**, plus **five of nine research areas with zero surviving claims**.

Those gaps are documented rather than papered over. Most important: **sandboxing evidence
was refuted, not confirmed.** Execution isolation is the plan's most dangerous open area
and must be independently validated before Titan runs untrusted code.

Raw findings: [`docs/research/deep-research-findings.json`](docs/research/deep-research-findings.json)
