# Titan compared: Claude Code, LangChain Deep Agents, IBM Bob, Paver

Status: 2026-09-06

Two objections come up about Titan: that IBM already has Paver, and that anyone
can build a deep agent now. Both deserve a factual answer rather than an
argument, so this is a capability comparison with its sources named and its
gaps marked as gaps.

**How to read the tables.** ✅ means present and verified from the source named;
⚠️ means partial or qualified; ❌ means absent; **?** means not established —
the source does not say, and it has not been tested. A **?** is not a claim of
absence. Anything marked ? must not be used in an argument either way.

## Sources

| System | What was consulted |
|---|---|
| Titan | this repository, read directly |
| Claude Code | official docs, v2.1.261 |
| Deep Agents | LangChain's own documentation |
| IBM Bob | IBM's public announcement of the shared agent and harness |
| Paver / Paver Pulse | two documents in `z-domain-skills`: the ground-truth collector reference and a provider fix plan; plus `benchmarks/zrag.yaml` and `tests/zrag/0001.yaml` |

**The Paver column is the weakest and should be treated as such.** The CLI is
not installed on the development machine, no source was available, and the
evidence is three documents plus a benchmark definition. Everything below about
Paver is either quoted from those or marked ?. Nobody should carry a claim about
Paver out of this document without checking it.

## Harness capabilities

| Capability | Titan | Claude Code | Deep Agents | IBM Bob | Paver |
|---|---|---|---|---|---|
| Agent loop with typed terminal states | ✅ ~10 distinct reasons | ✅ | ✅ | ✅ | ? |
| Subagents with isolated context | ⚠️ present, unmeasured | ✅ worktree isolation, depth limits, resumable | ✅ `task` tool | ✅ | ? |
| Planning / todo state | ❌ event types declared, never emitted, no tool | ✅ Task* tools, `/goal` with model-evaluated completion | ✅ `write_todos` | ✅ workflow engine | ? |
| Hooks | ⚠️ a policy hook can veto a call, but only from Go — nothing external can register one | ✅ 11 events, can block a call | ⚠️ middleware | ? | ✅ extension hooks in benchmarks |
| Skills (SKILL.md, progressive disclosure) | ✅ names+descriptions in prompt, body on demand | ✅ | ✅ | ? | ✅ `--skillset` |
| MCP | ✅ | ✅ 4 transports | ✅ | ? | ? |
| Context compaction | ✅ with prompt and memory re-injection | ✅ auto + `/compact` | ✅ summarization + offloading | ? | ? |
| Memory files | ✅ TITAN.md | ✅ CLAUDE.md + auto-memory | ✅ AGENTS.md | ? | ✅ `paver memory list` |
| Web search | ✅ 5 providers | ✅ | ⚠️ via tools | ? | ? |
| Parallel tool execution | ⚠️ model may batch; not enforced | ✅ | ✅ | ✅ | ? |

## Governance, deployment, auditability

This is where the systems diverge most, and where Titan's differentiation
actually sits.

| Capability | Titan | Claude Code | Deep Agents | IBM Bob | Paver |
|---|---|---|---|---|---|
| Policy engine, ordered evaluation, absolute deny | ✅ 6-step, deny outranks allow | ✅ allow/deny rules, 6 modes | ✅ filesystem rules, first-match-wins | ⚠️ "governance workflows" | ? |
| Event-sourced state | ✅ every action and observation | ⚠️ JSONL transcript, format explicitly internal and version-unstable | ⚠️ LangGraph checkpoints | ? | ❌ behaviour recovered by regex over logs |
| Deterministic replay for audit | ✅ replay reconstructs a session | ❌ not a documented feature | ? | ? | ? |
| Multi-tenant isolation | ✅ Postgres RLS, enforced at two layers | ❌ single-user CLI | ? | ✅ enterprise admin | ? |
| Provenance on untrusted content | ✅ trust tag travels with each event | ⚠️ documented as a practice | ? | ? | ? |
| Self-hosted / air-gapped | ✅ single Go binary, no cloud SDK | ❌ needs Anthropic or a gateway | ⚠️ self-hosted, Python + LangChain | ? announcement does not say | ⚠️ runs on internal infra |
| Runs against any OpenAI-compatible endpoint | ✅ | ❌ Claude models only | ✅ | ? | ? |
| Distinct model providers | ✅ 20 | ⚠️ 5 routes, Claude models only | ⚠️ 7 named | ? | ? |
| Per-provider sampling controls | ✅ 13 params, refused at startup if unsupported | ❌ not exposed | ⚠️ via the model object | ? | ? |

The row that matters most is **event sourcing versus log scraping**. Paver's
own zrag benchmark recovers what the agent did with regular expressions over log
text — `"Returning pending_tool_call:\s*\{[\s\S]*?tool:\s*'([^']+)'"` — and does
the same for skill loads, exceptions and tool errors (`benchmarks/zrag.yaml`).
Titan records those as typed events with a sequence number, an actor and a trust
tag. One system reconstructs behaviour after the fact and breaks when a log line
changes; the other has it by construction and can replay it.

Claude Code is candid about the same limit: the transcript is JSONL whose
"format is internal to Claude Code and changes between versions", and replay is
not offered. That is the right trade for a single-developer CLI. It is the wrong
trade for a system that has to answer what an agent did in a regulated
environment six months ago, which is the question Titan is built for.

## Evaluation

| Capability | Titan | Claude Code | Deep Agents | Paver Pulse |
|---|---|---|---|---|
| Task corpus | ✅ 144, plus 25 ported from Paver (scored: [13-zrag-benchmark.md](13-zrag-benchmark.md)) | ⚠️ `claude plugin eval`, early access | ? | ✅ 18 benchmark suites |
| Objective assertions | ✅ file and response checks, never model-judged | ✅ regex, tool_used, file_exists | ? | ✅ JS graders |
| LLM-as-judge | ❌ deliberately | ✅ `llm` grader | ? | ✅ LLMaJ grader |
| Behavioural inspection as a gate | ✅ injection compliance and destructive side effects fail a task that passed every assertion | ? | ? | ⚠️ adherence grader |
| Ground truth from live systems | ❌ | ❌ | ❌ | ✅ 14 collectors: IBM Cloud APIs, IAM, COS, Kubernetes, agent memory, endpoint probes |
| Cross-model conformance | ✅ `titan-modelcmp` | ❌ | ❌ | ? |

**Paver Pulse is better at evaluation than Titan is, in one specific and
important way.** Its collectors fetch ground truth from live IBM Cloud APIs,
Kubernetes, IAM, COS and agent memory at evaluation time, so a test can check
what was actually true when the agent answered. Titan's assertions are static.
For infrastructure questions — did the agent read the real resource state — that
is a genuine capability Titan does not have, and it should be copied rather than
dismissed.

Titan is better in a different specific way: its evaluation refuses to let a
model grade a model, and it fails a task that passes every assertion while
showing injection compliance. Those are different bets about what evaluation is
for, and both are defensible.

## What this means for the two objections

**Measured, on Paver's own benchmark.** Titan scores 20/25 on the zRAG suite —
every failure a missing citation or a leaked command, none a crash. The Paver
side is unmeasured because the CLI could not be obtained, so this is one column,
not a table. Details and caveats: [13-zrag-benchmark.md](13-zrag-benchmark.md).

**"We already have Paver."** Paver Pulse and Titan overlap least where each is
strongest. Paver's live ground-truth collectors are an evaluation capability
Titan lacks. Titan's event sourcing, deterministic replay, tenant isolation,
provider breadth and air-gap story are harness capabilities the Paver material
does not evidence — and its own benchmark shows it recovering agent behaviour by
regex over logs, which is what a system without event sourcing has to do.

**"Anyone can build a deep agent."** True of the loop, and Deep Agents proves it
— an open-source harness scoring on par with Claude Code at the same model tier.
Not true of the parts that took a day of real debugging to find in this
repository alone: a retrieval client discarding three quarters of its documents
after the reranker had already ranked them, a permission rule that silently
denied half of every retrieval, a UI printing every answer twice, sampling
parameters that were parsed and validated and never sent. None of those are
visible in an architecture diagram. All of them change what the agent does.

The honest summary is that Titan is not differentiated by having an agent loop.
It is differentiated by being auditable, multi-tenant, provider-agnostic and
installable where a cloud CLI cannot go — and by measuring itself, which is the
part that turns the argument into a number.

## Gaps this comparison exposes in Titan

Marked here rather than buried, because a comparison that only flatters its
subject is not worth writing.

1. **No planning or todo state.** `EvPlanUpdated` and `EvTodoUpdated` are
   declared in the event schema and never emitted, and no tool writes them — the
   design anticipated the feature and it was not built. Every other harness has
   it. This is the clearest missing feature.
2. **Hooks exist but are not reachable.** `policy.Hook` is evaluated first and
   can veto any call, which is the hard part; there is no way to register one
   without editing Go, so an operator cannot use it. Claude Code exposes 11 hook
   events externally.
3. **Subagents exist but are unmeasured.** No eval task exercises them.
4. **No live ground truth in evaluation.** Paver Pulse's collector model is
   better here and worth adopting.
5. **Parallel tool execution is not enforced**, only hoped for in the prompt.
