# Titan — Build Plan, Team & Sequencing

Status: Draft · 2026-09-02
**Evidence status: [E].** Research produced no verified claims on build cost, team, or
timeline. Estimates below are engineering judgment, calibrated to comparable platform work.

## 1. Build vs adopt

The principle: **build the harness, adopt everything else.** The harness is where the 7.80×
variance lives (P1) — it is the only layer where custom engineering returns more than it
costs. Everything below it is undifferentiated infrastructure.

| Layer | Decision | Choice | License |
|---|---|---|---|
| Agent harness / control plane | **BUILD** | Titan core | — |
| Event store & replay | **BUILD** | On Postgres | — |
| Policy engine | **BUILD** | 6-step ordered eval (P7) | — |
| Context manager / compaction | **BUILD** | P3/P4 mechanisms | — |
| Model adapters | **BUILD** thin | Over OpenAI-compatible API | — |
| Cross-model conformance suite | **BUILD** | Titan's differentiator (P12) | — |
| Inference serving | ADOPT | vLLM (primary), SGLang (alt) | Apache-2.0 |
| Sandboxing | ADOPT | Firecracker + gVisor | Apache-2.0 |
| Vector/hybrid search | ADOPT | OpenSearch or Qdrant | Apache-2.0 |
| Code structure | ADOPT | tree-sitter, LSP servers | MIT/Apache |
| MCP protocol | ADOPT | Spec + SDKs | MIT |
| Observability | ADOPT | OpenTelemetry, Prometheus, Grafana | Apache-2.0 |
| Identity | ADOPT | Enterprise OIDC/SAML IdP | — |

**Reference implementation worth studying:** BeeAI Framework is Apache-2.0, Linux
Foundation-hosted (AI & Data, *incubation* status), actively maintained, with a
provider-agnostic `provider:model` backend abstraction and 13 documented providers. Apache-2.0
means forking for air-gapped use is legally clean.

Two caveats, both verified: its own README states the code is provided with **no support
commitment** and that it "will not be maintaining this code going forward"; and its Backend
abstracts mostly *hosted* provider SDKs, so on-prem paths are the minority. **Study the
abstraction, don't inherit the dependency.**

**License discipline:** prefer Apache-2.0/MIT throughout. Avoid AGPL in anything linked into
the product, and avoid source-available licenses with field-of-use restrictions entirely —
they are incompatible with shipping Titan to customers.

## 2. Phasing

### Phase 0 — Foundations (weeks 1–6)
Prove the loop and the economics before building a platform around them.

- Event store + `step(state)` loop with all terminal events (P6, arch §2)
- Model adapter + capability probe against one OpenAI-compatible endpoint
- Native tool set: read, write, edit, glob, grep, bash
- Policy engine with ordered evaluation and absolute deny (P7)
- CLI (Go static binary — matters for air-gap install)
- **Prefix-cache measurement harness** — validate the 17× assumption on real hardware

*Exit criteria:* agent completes a multi-file change on a trusted repo; every terminal path
exercised; prefix-cache hit rate measured and > 70%.

### Phase 1 — Harness depth (weeks 7–16)
This is where agent quality is actually won.

- Compaction with PreCompact hook + `TITAN.md` re-injection (P4)
- Subagent supervisor with hierarchical budget enforcement (P3)
- JIT retrieval tier 0/1 (grep/glob + tree-sitter repo map)
- Eval harness: task suite **plus automated log inspection** (P10)
- Composable loop strategies: ReAct spine + generate-test-repair (P5)

*Exit criteria:* measurable improvement on your own eval suite across ≥2 model families;
log inspection catches at least one class of misbehavior that scores miss.

### Phase 2 — Platform (weeks 17–28)
- Server mode: multi-user, tenancy, OIDC SSO, managed settings
- Web console (session view, approvals, audit, replay)
- Firecracker execution pool with per-session isolation
- MCP gateway + internal registry with review gate
- OpenTelemetry tracing end to end

*Exit criteria:* two tenants isolated and verified; security checklist (§03-security §7)
passing; audit replay reconstructs a session exactly.

### Phase 3 — Air-gap & scale (weeks 29–40)
- Signed offline bundle + installer; digest-pinned everything
- Egress broker with allowlist and full audit
- Retrieval tier 2 (hybrid + rerank) for monorepo scale
- Multi-node inference; capacity model validated against real load
- Compliance evidence package

*Exit criteria:* clean install into a network-isolated enclave from bundle only; zero
egress verified by test.

### Phase 4 — Differentiation (ongoing)
- **Cross-model consistency benchmark** (P12) — the asset no one else has
- Declarative requirement/rule constraints, measured not assumed
- Per-model, per-task-class effort policy (P11)
- Continuous harness A/B against the eval suite

## 3. Team

| Role | Count | Focus |
|---|---:|---|
| Agent/harness engineers | 3–4 | Loop, context, subagents, tools — the core |
| Platform/backend | 2–3 | Server, tenancy, event store, API |
| Inference/ML systems | 1–2 | Serving, quantization, capacity, adapters |
| Security engineer | 1 | Isolation, injection, MCP supply chain, red team |
| Frontend | 1–2 | Console, approvals, replay UI |
| SRE/release | 1 | Air-gap bundles, install, observability |
| Eval/quality | 1 | Benchmarks, log inspection, regression gating |
| **Total** | **10–14** | |

**The eval engineer is not optional.** Given P1, a team that cannot measure harness changes
is flying blind on the single variable that matters most. Hire that role in Phase 0, not
Phase 3.

## 4. Effort estimate [E]

| Phase | Duration | Eng-months |
|---|---|---:|
| 0 — Foundations | 6 wks | ~12 |
| 1 — Harness depth | 10 wks | ~28 |
| 2 — Platform | 12 wks | ~40 |
| 3 — Air-gap & scale | 12 wks | ~36 |
| **To production-capable** | **~40 wks** | **~116** |

At fully-loaded cost this is roughly **$1.5–2.5M of engineering** — likely more than the T2
hardware it runs on. That ratio is normal and worth stating plainly to sponsors: the GPUs
are the visible cost, the harness is the real one.

A credible **demo** is reachable in 6 weeks (Phase 0). A credible **product** is not.

## 5. Risk register

| Risk | Severity | Mitigation |
|---|---|---|
| Sandboxing assumptions wrong | **Critical** | Red team before untrusted execution (§03 §7) |
| Prefix caching underperforms | High | Measured in Phase 0, before architecture depends on it |
| Harness variance doesn't transfer to open-weight models | High | Open question from research; test in Phase 1 |
| Compaction quality regression | Medium | Log inspection + eval gating |
| MCP supply chain compromise | High | Registry review gate, digest pinning, per-server isolation |
| Model churn invalidates tuning | Medium | Capability probe + conformance suite per model |
| Scope creep into a full IDE | Medium | Harness is the product; IDE is a client |

## 6. Open questions to resolve with your own data

Carried directly from the research pass — these are unresolved in the literature, and
answering them on your own hardware is genuine contribution:

1. **Does the 7.80× harness-variance finding hold for self-hosted open-weight models?**
   The factorial used frontier models. If harness variance is *larger* for weaker on-prem
   models, harness investment pays off more on-prem. If it's driven by frontier models'
   ability to exploit rich scaffolds, the conclusion inverts and thin-harness/strong-model
   becomes correct. **This determines Titan's entire engineering budget split.** Testable.
2. **What are the GPU-second economics of subagent fan-out?** Nobody has published
   tokens-per-solved-task mapped onto GPU-seconds, nor the crossover where a subagent's
   1–2k summary stops repaying its own prefill.
3. **How does prefix caching behave under compaction and subagent fan-out** in a real
   serving engine? Phase 0 answers this.
4. **Does declarative rule-constraining actually reduce cross-model variance, and by how
   much?** Zero independent validation exists. Measuring it is Titan's differentiator.
5. **Is there any sandboxing/injection-defense evidence that survives scrutiny?** Next
   research target; currently a blind spot.
