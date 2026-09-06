# Titan and Pi, component by component

Status: 2026-09-06

Pi (pi.dev, `earendil-works/pi`, MIT) is a minimal agent harness by Mario
Zechner. It is the most useful thing to compare Titan against, because the two
disagree about the same question and answer it in opposite directions: **what
belongs in the harness, and what belongs outside it.**

Pi's answer is *primitives, not features* — the harness stays small and an
extension API makes it anything you need. Titan's answer is that the properties
an enterprise deployment needs (audit, isolation, policy) cannot be extensions,
because a property that can be switched off is not a property.

Neither is wrong. They are built for different buyers.

## Sources

Pi's own documentation and source: [pi.dev](https://pi.dev/), the
[coding-agent README](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md),
and [docs/extensions.md](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/extensions.md).
Titan: this repository, read directly. Where a capability could not be verified
it is marked **?**, and a **?** is not evidence of absence.

## The core loop

| | Titan | Pi |
|---|---|---|
| Loop | turn-based, ~10 typed terminal reasons | turn-based, steerable mid-run |
| Mid-run steering | ❌ interrupt only | ✅ Enter steers, Alt+Enter queues a follow-up |
| Run modes | interactive CLI, headless `-p`, server + web console | interactive, print/JSON, RPC, SDK |
| Embeddable as a library | ❌ Go binary | ✅ SDK, RPC over stdin/stdout JSONL |
| Language | Go, single static binary | TypeScript/Node |

Pi's mid-run steering is a genuinely better interaction model and Titan has
nothing like it: a user who sees the agent going wrong must interrupt and start
again, rather than nudging it while it works. The same is true of switching
model mid-run — Titan requires a restart, and says so in the command's own
output, having traded that convenience for prefix-cache stability.

## Tools

| | Titan | Pi |
|---|---|---|
| File and shell | read, write, edit, glob, grep, bash | read, write, edit, bash, grep, find, ls, powershell |
| Web search | ✅ 5 providers (duckduckgo, brave, tavily, serper, searxng) | ❌ extension |
| Skills | ✅ `skill` tool, listing in prompt | ✅ Agent Skills standard |
| Remote execution | ✅ ssh, ssh_connect | ❌ extension (an example exists) |
| Kubernetes | ✅ k8s_get, k8s_apply, k8s_login | ❌ extension |
| MCP | ✅ client and gateway | ❌ deliberately excluded |
| Register a tool without recompiling | ⚠️ MCP server only | ✅ `pi.registerTool` in TypeScript |
| Override a built-in tool | ❌ | ✅ register the same name |

The asymmetry is the design. Titan ships the enterprise integrations (k8s, ssh,
five search providers, MCP) because an air-gapped customer cannot npm-install an
extension. Pi ships four tools and an API, because a developer on a laptop can.

## Extensibility — where Pi is far ahead

Pi's extension API is the most complete of any harness surveyed, including
Claude Code's. Extensions are TypeScript modules loaded through jiti, needing no
build step, and they can hook **more than thirty events**:

- lifecycle: `project_trust`, `session_start`, `session_shutdown`, `session_before_switch`, `session_before_fork`, `resources_discover`
- compaction: `session_before_compact` (can cancel, or supply its own summary), `session_compact`, `session_compact_failed`
- execution: `before_agent_start` (can rewrite the system prompt or inject a message), `agent_start`, `agent_end`, `agent_settled`, `turn_start`, `turn_end`, `message_start/update/end`, `input`
- tools: `tool_execution_start/update/end`, `tool_call` (**can block**), `tool_result` (**can modify the result**), `user_bash`
- provider: `context` (filter or rewrite the messages sent), `before_provider_headers`, `before_provider_request`, `after_provider_response`
- model: `model_select`, `thinking_level_select`

Plus `registerTool`, `registerCommand`, `registerShortcut`, `registerFlag`,
`registerProvider`, custom message and entry renderers, and durable custom
session entries via `appendEntry`.

Titan has one hook type, `policy.Hook`, which can veto a call — the hard part —
and no way to register one without editing Go and recompiling. Everything else
on that list has no Titan equivalent.

**This is the single largest capability gap in this document.** Pi's `context`
event alone — filter the message list before every provider call — is a
production-grade RAG and privacy mechanism that Titan cannot express at all.

## Sessions

| | Titan | Pi |
|---|---|---|
| Storage | Postgres, append-only, trigger-enforced | JSONL under `~/.pi/agent/sessions/`, by working directory |
| Model | flat event stream per session | tree: every entry has `id` and `parentId` |
| Branching | ❌ | ✅ in-place, no new file |
| Navigate history | ❌ | ✅ `/tree` |
| Fork from a past point | ❌ | ✅ `/fork`, `/clone` |
| Resume | ✅ `/resume` replays a transcript | ✅ `/resume` |
| Deterministic replay | ✅ the point of the design | ⚠️ full history is retained, replay is not a stated feature |
| Share | ⚠️ `/export` writes the raw event JSON | ✅ `/export` to HTML, `/share` to a gist |
| Multi-tenant isolation | ✅ Postgres RLS, enforced at two layers | ❌ single user, local files |

Both store everything and neither discards history on compaction. The difference
is what the store is *for*. Pi's tree exists so a developer can go back three
turns and try a different approach — an interaction feature. Titan's event log
exists so an auditor can reconstruct exactly what an agent did in a regulated
environment — a compliance feature. Pi's branching is the better daily
experience; Titan's replay and tenant isolation are the things a bank asks for.

## Context management

| | Titan | Pi |
|---|---|---|
| Auto-compaction | ✅ proactive, with headroom for the coming turn | ✅ proactive and reactive after overflow |
| Manual | ✅ `/compact` | ✅ `/compact` |
| Custom summary | ⚠️ `PreCompact` hook, Go only | ✅ `session_before_compact` can cancel or supply the summary |
| Full history preserved | ✅ event store | ✅ JSONL |
| Memory files | ✅ TITAN.md | ✅ AGENTS.md, SYSTEM.md |
| Per-result size cap | ✅ a quarter of the window | ? |
| Filter messages per request | ❌ | ✅ the `context` event |

Titan's compaction is measured: a hundred-turn session in a 32k window peaks at
17,981 tokens with about one compaction per twenty turns
([12](12-comparison.md), [13](13-zrag-benchmark.md)). Pi's is documented but no
published measurement was found — absence of a number, not absence of quality.

## Providers

| | Titan | Pi |
|---|---|---|
| Named providers | 20 | 25+, "hundreds of models" |
| Wire formats | 3 (OpenAI, Anthropic Messages, Gemini) | OpenAI- and Anthropic-compatible, plus per-provider |
| Switch model mid-session | ❌ `/model` records the choice and asks for a restart, deliberately: switching mid-session would invalidate the prefix cache | ✅ `/model`, or a shortcut, mid-run |
| Subscription auth (Claude Pro, ChatGPT Plus, Copilot) | ❌ | ✅ |
| Custom provider without recompiling | ❌ | ✅ `models.json`, or `registerProvider` |
| Sampling parameters | ✅ 13, refused at startup when unsupported | ⚠️ thinking level is first-class; full sampling surface not documented |

Comparable breadth, opposite mechanism. Pi adds a provider with a JSON file;
Titan needs a new file and a rebuild. Titan validates a parameter the provider
cannot honour and refuses to start, which no other harness surveyed does.

## Governance — where Titan is far ahead

| | Titan | Pi |
|---|---|---|
| Policy engine | ✅ 6-step ordered evaluation, deny outranks everything including bypass | ❌ "containerize, or write an extension" |
| Permission modes | ✅ default, plan, accept-edits, auto, bypass | ❌ |
| Approval UI | ✅ inline in the web console, with full arguments | ❌ deliberately excluded |
| Untrusted-content provenance | ✅ trust tag on every event | ❌ |
| Multi-tenancy | ✅ RLS, two enforcement layers | ❌ |
| OIDC / SSO | ✅ | ❌ |
| Air-gapped install | ✅ single static binary | ⚠️ Node plus npm |
| Evaluation harness | ✅ 169 tasks, objective assertions, behavioural flags that fail a passing task | ❌ none found |
| Cross-model conformance | ✅ `titan-modelcmp` | ❌ |

Pi's position here is explicit and coherent, not an oversight: it recommends
containerization or an extension rather than building a permission model, on the
same principle that keeps the core small. For a developer on their own machine
that is defensible. It is not available to a deployment that must prove to an
auditor what the agent was allowed to do, because an extension-supplied
permission gate is one an extension can also remove.

That is the sentence that separates the two products.

## What Titan should take from Pi

Ranked by value against effort, and none of these requires giving up the
governance model.

1. **Mid-run steering.** The clearest UX gap. Interrupting and restarting is
   strictly worse than nudging a running agent.
2. **A real extension API.** Not Pi's whole surface — but `tool_call` (block or
   rewrite), `tool_result` (rewrite), `context` (filter the messages sent), and
   `before_agent_start` (adjust the system prompt) would cover most of what
   operators currently cannot do without a fork. Titan's `policy.Hook` already
   proves the hard part works; it has no door to the outside.
3. **Session branching.** The event store already carries `parent_id` and
   sequence numbers, so the data model is most of the way there.
4. **Providers from configuration.** `models.json` rather than a rebuild.
5. **A readable export.** `/export` already writes the event stream, but as raw
   JSON — useful to a program, not to a colleague. Pi renders HTML and can push
   it to a gist, which is the difference between a transcript that exists and one
   that gets read.

## What Pi would take from Titan, if it wanted to

Mostly nothing, and deliberately — but the two capabilities a Pi user cannot
build as an extension are an event store with deterministic replay, and tenant
isolation enforced in the database. Both have to be in the core or they are not
guarantees.

## Summary

Pi is a better harness for one developer. It is smaller, more adaptable, better
to drive, and its extension API is the best of any harness surveyed here.

Titan is a better platform for a regulated deployment. Its policy engine, event
sourcing, tenant isolation and evaluation harness are the things that cannot be
added later by anyone who is allowed to remove them.

The most useful conclusion is not which is better. It is that Titan's
extensibility gap is real, large, and the one thing on this page it should fix
next — and that it can be fixed without touching a single guarantee, because a
hook that is allowed to veto is not the same as a hook that is allowed to permit.
