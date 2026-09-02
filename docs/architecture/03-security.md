# Titan — Execution Isolation & Prompt-Injection Defense

Status: Draft · 2026-09-02

> ## ⚠ Read this first
>
> The research pass produced **zero verified claims** on sandboxing, and **two candidate
> sandboxing claims were refuted 0-3** by adversarial verification. The isolation posture
> of every comparable agent is therefore **unverified** in this evidence set. One notable
> data point that did survive: a major open-source agent SDK made sandboxing **opt-in
> rather than mandatory** in its V1 rewrite.
>
> **This is the single most dangerous gap in the Titan plan.** For an enclave that ingests
> untrusted repo content and brokered search results, shipping on assumed-safe isolation is
> disqualifying. Everything below is [E] engineering judgment and must be independently
> validated — ideally by a red-team engagement — before Titan executes untrusted code.

## 1. Threat model

| # | Threat | Vector | Consequence |
|---|---|---|---|
| T1 | Agent-generated code is malicious or wrong | Model output | Host compromise, data loss |
| T2 | **Prompt injection via repo content** | README, comments, test fixtures | Agent acts against operator intent |
| T3 | Prompt injection via tool output | MCP response, search result | Same, with attacker-chosen payload |
| T4 | MCP server supply chain | Third-party server | Full tool-surface compromise |
| T5 | Exfiltration | Any egress path | Source code / secret loss |
| T6 | Cross-tenant leakage | Shared cache, shared FS | Confidentiality breach |
| T7 | Resource exhaustion | Runaway loop, fork bomb | Denial of service |

**T2/T3 are the defining hazard of an agentic system.** A coding agent's entire job is to
read untrusted text and act on it. There is no known complete defense — which is exactly why
the architecture assumes injection *sometimes succeeds* and constrains blast radius instead.

## 2. Defense in depth

```
 L1  Provenance      every observation tagged trusted | untrusted at ingest
 L2  Policy          evaluated on the ACTION, never on the text that motivated it
 L3  Isolation       microVM per session; assume code inside is hostile
 L4  Egress          default-deny network; broker is the only path out
 L5  Detection       log inspection + anomaly detection on action streams
 L6  Recovery        event-sourced replay; deterministic incident reconstruction
```

The key design decision is **L2**: policy never asks "does this look like a legitimate
request?" It asks "is this action permitted for this session, regardless of why the model
wants it?" That distinction is what makes injection survivable — a successfully injected
agent still cannot exceed its granted authority.

## 3. Isolation tiers

| Tier | Mechanism | Boundary | Overhead | Use |
|---|---|---|---|---|
| I0 | Process + seccomp/Landlock | Weak | ~0 | Never for untrusted code |
| I1 | Container (OCI) | Namespace | Low | Trusted internal only |
| I2 | **gVisor** | Userspace kernel | ~10-20% | Default for tool execution |
| I3 | **Firecracker / Kata microVM** | Hardware virt | ~50-150 ms boot | **Default for sessions** |
| I4 | Dedicated node | Physical | High | Classified / cross-tenant-sensitive |

**Titan default: I3 per session, I2 per tool invocation within it.** A microVM per session
gives a hardware-enforced boundary at a boot cost small relative to agent turn latency.
Container-only isolation (I1) is *not* sufficient for agent-generated code — a container
shares the host kernel, and kernel escape is a realistic threat from arbitrary code.

Each session VM gets: scoped filesystem (workspace only, no host mounts), no network by
default, CPU/memory/PID/disk quotas, wall-clock lifetime cap, and destruction on session end.
**VMs are never reused across tenants** — reuse is how T6 happens.

## 4. Prompt-injection controls

Layered, because no single control is sufficient:

1. **Provenance tagging.** Repo content, tool output, MCP responses, and search results
   enter as `untrusted` and stay tagged through the event store. Never concatenate untrusted
   text into a system prompt.
2. **Structural separation.** Untrusted content is delivered in a distinct message role or
   delimited block, never spliced into instructions.
3. **Action-level policy (L2).** Deny rules are absolute and survive every permission mode.
4. **Sensitive-action confirmation.** Destructive filesystem ops, credential access, egress,
   and privilege changes require explicit approval regardless of mode — no mode auto-approves
   them.
5. **Egress default-deny.** Even a fully injected agent has nowhere to send data.
6. **Anomaly detection.** Alert on action-sequence patterns inconsistent with the stated task
   (mass file reads, unexpected network attempts, credential-path access).

**What Titan explicitly does not claim:** that it detects prompt injection reliably.
Detection is a mitigation layer, not the boundary. The boundary is L3 and L4.

## 5. MCP supply chain (T4)

MCP research was also unverified, so treat every third-party server as hostile until reviewed:

- **Registry with review gate.** No server runs that isn't in the signed internal registry.
- **Pin by digest**, never by tag or `latest`.
- **Least privilege per server** — its own credentials, its own network policy, its own
  isolation tier. A wiki-reader server has no reason to reach a database.
- **Tool-definition review.** Tool descriptions enter the model's context and are therefore
  an injection surface in themselves ("tool poisoning"). Review descriptions like code.
- **Runtime containment.** MCP servers run in I2 minimum, with declared egress only.

## 6. Tool surface discipline

The harness research surfaced a relevant number: cutting a tool set from 15 tools to 2 moved
task success from 80% to 100% [medium confidence — vendor-reported]. Fewer, better-scoped
tools improve both reliability *and* security: every tool is an attack surface and a
decision the model can get wrong.

Titan ships a deliberately small native tool set — read, write, edit, glob, grep, bash,
task/subagent, plan — and everything else arrives through the reviewed MCP gateway.

## 7. Validation plan (required before production)

Since nothing here is research-backed, isolation must be *demonstrated*:

- [ ] Escape testing against I2/I3 with known CVE classes and a red-team engagement
- [ ] Injection corpus: adversarial READMEs, comments, fixtures, tool outputs
- [ ] Egress verification: attempt exfiltration from inside a session VM, expect zero paths
- [ ] Cross-tenant leakage: shared cache and FS probing
- [ ] Resource exhaustion: fork bombs, disk fill, memory pressure
- [ ] Policy bypass: attempt escalation past managed settings from a local config

**Do not enable arbitrary code execution for untrusted repositories until this checklist
passes.** The pilot (Phase 1) should run against trusted internal repos only.
