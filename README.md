# Titan

An on-prem, air-gap-capable deep agent platform. Model-agnostic by construction:
the better the reasoning model you point it at, the better it performs.

```
$ titan -p "fix the failing test" -mode auto -allow 'bash(go test*)'
● grep "func Add"      └ 2 line(s)
● read math.go         └ 5 line(s)
● edit math.go         └ Edited math.go.
● bash run tests       └ exit 0

Fixed the sign error in Add (math.go:4) and the tests now pass.
5 turns · 6000 in / 200 out tokens · 83% cached (5.9x prefill)
```

## The thesis

A controlled study found **harness-induced variance exceeds model-induced variance
by 7.80×** on SWE-bench Verified, with ranking reversals in 6 of 9 model-pair
comparisons. The scaffold around the model — context management, tool design,
subagents, permissions — is a first-class engineering variable, not glue code.

Titan is built on that: the harness is a separately engineered, separately
evaluated layer behind a provider abstraction. Better model, better agent.
Better harness, better agent. Both compound.

## Quick start

```bash
go build -o titan ./cmd/titan
./titan init            # write .titan/config.json
./titan doctor          # verify endpoint, tool-calling, sandbox, index, MCP
./titan                 # interactive
./titan serve -addr :8080   # web console + API
```

Point it at anything OpenAI-compatible — vLLM, SGLang, TensorRT-LLM, llama.cpp,
Ollama, or a hosted API:

```bash
export TITAN_BASE_URL=http://your-gpu-host:8000/v1
export TITAN_MODEL=Qwen/Qwen3-32B
./titan doctor
```

Measure whether your serving stack actually caches prefixes — the assumption the
whole capacity model rests on:

```bash
go build -o titan-bench ./cmd/titan-bench
./titan-bench -model Qwen/Qwen3-32B -turns 40
```

## What's implemented

| Area | Status |
|---|---|
| Event-sourced loop, 8 terminal reasons | ✅ tested |
| Tools: read, write, edit, glob, grep, bash, task | ✅ tested |
| Read-before-edit, exact-match, near-miss recovery | ✅ tested |
| Ordered policy engine, absolute deny, always-confirm destructive | ✅ tested |
| OpenAI-compatible adapter, streaming, reasoning-token stripping | ✅ tested |
| **Execution sandbox** (Seatbelt / bubblewrap / OCI / gVisor) | ✅ escape-tested |
| **Compaction** with PreCompact hook, tool-call integrity | ✅ tested |
| **Subagents** with hierarchical budgets, profile-scoped tools | ✅ tested |
| **MCP gateway** with tool-poisoning defense | ✅ tested |
| **Hybrid retrieval** — symbol, BM25, vector tiers | ✅ tested |
| **Server mode** — REST, SSE, remote approvals, tenancy | ✅ tested |
| **Web console** — self-contained, no CDN | ✅ tested |
| **Prefix-cache benchmark** | ✅ validated |
| Postgres store, OIDC, offline bundle, eval corpus | ⬜ next |

`go test ./...` — 9 packages, 140+ tests.

## Architecture

```
Access    CLI · Web console · REST/SSE API
Control   Orchestrator → Context → Policy → Tool router    ← the harness
Inference OpenAI-compatible gateway (any model)
Execution tiered sandbox, no egress by default
Data      event store · hybrid index · MCP registry
          ═══ air-gap boundary ═══
Egress    broker (optional, default OFF)
```

| Doc | Contents |
|---|---|
| [01 Principles](docs/architecture/01-principles.md) | 12 principles, tagged by evidence status |
| [02 System architecture](docs/architecture/02-system-architecture.md) | Planes, loop, subagents, retrieval |
| [03 Security](docs/architecture/03-security.md) | Isolation tiers, injection, validation status |
| [04 Sizing](docs/architecture/04-sizing.md) | VRAM math, tiers, prefill economics, cost |
| [05 Roadmap](docs/architecture/05-roadmap.md) | Build vs adopt, phasing, team, risks |
| [06 Tool contracts](docs/architecture/06-tool-contracts.md) | Exact schemas, semantics, error messages |
| [07 System prompt](docs/architecture/07-system-prompt.md) | Prompt layering, TITAN.md, anti-patterns |
| [08 Eval](docs/architecture/08-eval.md) | 4-layer harness incl. behavioral inspection |
| [09 UX](docs/architecture/09-ux.md) | CLI, approvals, modes, latency budget |
| [10 Data model](docs/architecture/10-data-model.md) | Events, schema, protocol, adapter interface |
| [Air-gap ops](docs/ops/air-gap.md) | Offline install, egress broker, compliance |

## Design decisions worth knowing

**Errors are written for the model, not the log.** A failed edit reports the
nearest matching line with context, so the model recovers in one turn instead of
guessing. This is the difference between a tool set that works and one that
frustrates the model into loops.

**Edits require a prior read**, match exactly, and never fuzzy-match. A near-miss
that "helpfully" applies produces a silent wrong edit — the worst outcome an
editing tool can have.

**The sandbox never silently downgrades.** If no backend meets the configured
minimum tier, Titan fails with what it tried and how to fix it. A sandbox that
quietly weakens itself is worse than none, because operators stop checking.

**Deny is absolute.** It blocks even in bypass mode. Destructive commands confirm
in every mode. Managed org policy cannot be escalated past locally.

**Everything untrusted is tagged at ingest** — file contents, tool output, MCP
responses — because a coding agent's whole job is reading untrusted text and
acting on it. MCP tool *descriptions* are sanitized too: they are third-party
text injected into the model's context, which makes them an attack surface.

**Nothing volatile in the prompt prefix.** The date is day-granular; a per-second
timestamp would invalidate the prefix cache on every request.

## Evidence discipline

Claims carry provenance: **[V]** verified by adversarial research, **[C]** computed
and reproducible, **[E]** engineering judgment.

The design pass ran 112 agents across 6 research angles with 3-vote adversarial
verification: 15 verified findings, **6 refuted claims**, and **5 of 9 areas with
zero surviving claims**. Those gaps are documented rather than papered over.

**Measurement has already corrected the design twice.** The sizing doc claimed
compaction "invalidates the prefix by construction" — benchmarking showed that is
false for Titan, because the system prompt and `TITAN.md` sit outside the
compacted history, so the cached prefix survives. And an end-to-end run exposed a
policy bug where `auto` mode rejected its own edits.

**Sandboxing evidence was refuted, not confirmed** — so isolation is now proven by
escape tests rather than assumption (see [validation status](docs/architecture/03-security.md)).
A red-team engagement is still outstanding before running genuinely hostile code;
for untrusted repositories set `sandbox.min_tier` to `container` or `vm`.

Raw findings: [`docs/research/deep-research-findings.json`](docs/research/deep-research-findings.json)
