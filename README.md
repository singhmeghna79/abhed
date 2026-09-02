# Titan

An on-prem, air-gap-capable deep agent platform. Model-agnostic by construction:
the better the reasoning model you point it at, the better it performs.

**Status:** working agent loop. Phase 0 of [the roadmap](docs/architecture/05-roadmap.md).

```
$ titan -p "fix the failing test" -mode auto -allow 'bash(go test*)'
● grep "func Add"
  └ 2 line(s)
● read math.go
  └ 5 line(s)
● edit math.go
  └ Edited math.go.
● bash run tests
  └ exit 0

Fixed the sign error in Add (math.go:4) and the tests now pass.
5 turns · 6000 in / 200 out tokens · 83% cached
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
./titan init          # write .titan/config.json
./titan doctor        # verify the endpoint and tool-calling work
./titan               # interactive
```

Point it at anything OpenAI-compatible — vLLM, SGLang, TensorRT-LLM, llama.cpp,
Ollama, or a hosted API:

```bash
export TITAN_BASE_URL=http://your-gpu-host:8000/v1
export TITAN_MODEL=Qwen/Qwen3-32B
./titan doctor
```

`doctor` checks both that the endpoint responds *and* that the model emits tool
calls — the capability the agent actually depends on.

## What's implemented

| Area | Status |
|---|---|
| Event-sourced loop with 8 terminal reasons | ✅ tested |
| Tools: read, write, edit, glob, grep, bash | ✅ tested |
| Read-before-edit enforcement | ✅ tested |
| Exact-match editing with near-miss recovery | ✅ tested |
| Workspace escape prevention | ✅ tested |
| Ordered policy engine, absolute deny | ✅ tested |
| Destructive-command always-confirm | ✅ tested |
| OpenAI-compatible adapter, streaming | ✅ tested |
| Fragmented tool-call reassembly | ✅ tested |
| Reasoning-token stripping (in- and out-of-band) | ✅ tested |
| Prefix-cache accounting | ✅ tested |
| Layered system prompt + TITAN.md discovery | ✅ |
| CLI: interactive, headless, JSON output | ✅ |
| Compaction, subagents, MCP, server mode | ⬜ Phase 1–2 |

`go test ./...` — 80+ tests.

## Architecture

```
Access    CLI · Web console · API
Control   Orchestrator → Context → Policy → Tool router    ← the harness
Inference OpenAI-compatible gateway (any model)
Execution microVM per session, no egress
Data      Postgres (events/audit) · vector store · registry
          ═══ air-gap boundary ═══
Egress    broker (optional, default OFF)
```

The [design docs](docs/architecture/) carry the full picture:

| Doc | Contents |
|---|---|
| [01 Principles](docs/architecture/01-principles.md) | 12 principles, tagged by evidence status |
| [02 System architecture](docs/architecture/02-system-architecture.md) | Planes, loop, subagents, retrieval |
| [03 Security](docs/architecture/03-security.md) | Isolation tiers, injection, MCP supply chain |
| [04 Sizing](docs/architecture/04-sizing.md) | VRAM math, tiers, prefill economics, cost |
| [05 Roadmap](docs/architecture/05-roadmap.md) | Build vs adopt, phasing, team, risks |
| [06 Tool contracts](docs/architecture/06-tool-contracts.md) | Exact schemas, semantics, error messages |
| [07 System prompt](docs/architecture/07-system-prompt.md) | Prompt layering, TITAN.md, anti-patterns |
| [08 Eval](docs/architecture/08-eval.md) | 4-layer harness incl. behavioral inspection |
| [09 UX](docs/architecture/09-ux.md) | CLI, approvals, modes, latency budget |
| [10 Data model](docs/architecture/10-data-model.md) | Events, schema, protocol, adapter interface |
| [Air-gap ops](docs/ops/air-gap.md) | Offline install, egress broker, compliance |

Capacity model: `python3 docs/architecture/sizing.py`

## Design decisions worth knowing

**Errors are written for the model, not the log.** A failed edit reports the
nearest matching line with context, so the model recovers in one turn instead of
guessing. This is the difference between a tool set that works and one that
frustrates the model into loops.

**Edits require a prior read.** The model cannot replace content it has not seen.
External modification between read and edit is detected and refused.

**Exact-match only, never fuzzy.** A near-miss that "helpfully" applies produces
a silent wrong edit — the worst outcome an editing tool can have.

**Deny is absolute.** A deny rule blocks even in bypass mode. Destructive commands
confirm in every mode. Managed org policy cannot be escalated past locally.

**Tool output is untrusted.** Provenance is tagged at ingest and travels with the
event, because a coding agent's whole job is reading untrusted text and acting on it.

**Nothing volatile in the prompt prefix.** The date is rendered at day granularity;
a per-second timestamp would invalidate the prefix cache on every request, which
alone costs ~17× in prefill.

## Evidence discipline

Claims carry their provenance: **[V]** verified by adversarial research,
**[C]** computed and reproducible, **[E]** engineering judgment.

The design pass ran 112 agents across 6 research angles with 3-vote adversarial
verification. It produced 15 verified findings — and **6 refuted claims**, plus
**5 of 9 areas with zero surviving claims**. Those gaps are documented, not
papered over.

Most important: **sandboxing evidence was refuted, not confirmed.** Execution
isolation is the plan's most dangerous open area. Titan currently runs commands
directly and is therefore suitable for **trusted repositories only** until the
microVM execution plane lands and the [validation checklist](docs/architecture/03-security.md)
passes.

Raw findings: [`docs/research/deep-research-findings.json`](docs/research/deep-research-findings.json)
