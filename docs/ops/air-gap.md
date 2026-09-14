# Abhed — Air-Gapped Deployment & Egress Brokering

Status: Draft · 2026-09-02
**Evidence status: [E] engineering judgment throughout.** The research pass produced
**zero verified claims** on air-gapped operations or compliance frameworks. Treat this as
a design proposal to review with your security organization, not as established practice.

## 1. Three postures, one build

Abhed ships one artifact set supporting three deployment postures. The posture is
configuration, never a different build — a separate air-gap build inevitably rots.

| Posture | Egress | Web search | Typical use |
|---|---|---|---|
| **A — Hard air-gap** | None, physically | Cached corpus only | Regulated / classified |
| **B — Brokered** | One-way through broker | Allowlisted, audited | Default enterprise |
| **C — Connected** | Proxy with policy | Live | Internal dev |

## 2. Offline install

Everything Abhed needs ships as one signed bundle, verified by digest at install:

```
abhed-release-<version>.tar          (detached signature + SBOM)
├── images/         OCI archives, digest-pinned (no :latest, ever)
├── charts/         Helm charts / Operator bundle
├── models/         weights + tokenizer + chat template + capability profile
├── deps/           language package mirrors (wheels, modules, crates)
├── mcp/            vetted MCP servers, pinned + signed
└── manifest.json   digests, SBOM, provenance attestation
```

Rules that matter in practice:

- **Digest-pinned images only.** A tag is mutable; an air-gap install must be reproducible.
- **Model weights are supply chain.** Verify signature *and* digest before load. A
  tampered weight file is an undetectable backdoor — it fails no test.
- **Chat template is part of the model artifact.** Template drift silently breaks prefix
  caching and tool parsing. Version it with the weights, not the code.
- **No build-time network access.** CI builds the bundle; the enclave only verifies.

## 3. The egress broker

The one component permitted to cross the boundary. Everything else is default-deny at the
network layer, so a compromised agent has no channel even if policy is bypassed.

```
   ENCLAVE (no route to internet)          │        DMZ            │  Internet
                                           │                       │
  agent ──▶ web_search tool                │                       │
              │ structured request         │                       │
              ▼                            │                       │
        Egress Broker client ──mTLS──────▶ Broker ──allowlist────▶ upstream
                                           │  │                    │
              ◀── sanitized, tagged ────────  ▼                    │
                  untrusted-content         audit log              │
                                            (full req + resp)      │
```

Non-negotiable properties:

1. **Structured requests only.** The agent submits a query object, never a URL it composed.
   No agent-controlled destinations.
2. **Domain allowlist**, reviewed like firewall policy.
3. **Responses are untrusted content** — tagged at ingest, never instruction (§ arch 2.6).
   This is the injection path that matters most: search results are attacker-influenceable.
4. **Full content audit** on both directions, retained for the compliance window.
5. **Rate limited and attributable** to tenant, session, and user.
6. **Default OFF.** Enabling is an explicit, logged administrative act.

For Posture A, replace the broker with a **periodically ingested cached corpus** —
documentation snapshots, package indexes, internal wikis — imported through the same signed
bundle path. The agent's `web_search` tool then resolves against local index only, and the
tool contract is unchanged, so no agent logic differs across postures.

## 4. Identity, tenancy, audit

- **SSO** via OIDC/SAML to the existing enterprise IdP. Abhed issues short-lived,
  tenant-scoped session tokens; it is never a credential store.
- **Tenancy** is enforced at the control plane and re-enforced at the data plane. A tenant
  boundary that exists only in the query layer is not a boundary.
- **Managed settings** (P7): org-level policy that a local user config cannot escalate
  past. This is the primitive that makes multi-tenant permission enforcement real.
- **Audit** derives from the event store (P6). Because state is event-sourced with
  deterministic replay, any session can be reconstructed exactly — which is what incident
  response and regulators both actually ask for. Ship the audit log to the enterprise SIEM;
  never let Abhed be the sole custodian of its own audit trail.

## 5. Observability

OpenTelemetry traces spanning the agent loop, with span attributes for model, tokens,
cache hit/miss, tool, policy decision, and terminal event. Metrics that matter operationally:

| Metric | Why | Alert |
|---|---|---|
| Prefix cache hit rate | 17× economics (P8) | < 70% |
| Compactions/hour | Each is a cold-prefill capacity event | Trend |
| KV utilization | OOM under load is a hard failure | > 85% |
| Turns to completion | Harness regression signal | Trend |
| Policy denials | Injection / misconfiguration signal | Spike |
| Terminal-event mix | Budget/turn exhaustion = harness bug | Shift |

Per P10, ship **automated log inspection** alongside metrics from day one. Agents with
identical success rates exhibit materially different behavior, and aggregate scores cannot
distinguish correct abstention from harmful action.

## 6. Compliance mapping [E — unverified; confirm with your compliance function]

| Framework | Where Abhed touches it |
|---|---|
| NIST AI RMF | Govern/Map/Measure/Manage → eval harness + audit + policy engine |
| SOC 2 | Access control, audit trail, change management on model registry |
| FedRAMP / IL4-IL5 | Posture A, signed bundles, no egress, FIPS crypto |
| EU AI Act | Logging, human oversight, transparency — event store supports all three |

The event store plus policy engine is the compliance substrate. Build them first; retrofitting
audit into an agent that already ships is far more expensive than it looks.
