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

**Verified against the installed binary, 2026-09-06.** Every ✅ on Titan's side
was exercised through the binary on PATH — a real terminal for the CLI rows, a
separate Go module for the SDK, a Python subprocess for RPC — not read off the
source. That distinction earned itself: an earlier revision marked capabilities
closed that existed only on a branch, compiled to a scratch path and never
installed, so the table was true of the repository and false for anyone actually
running `titan`.

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
| Mid-run steering | ✅ type while the agent works, in the CLI and over HTTP; slash commands queue for after | ✅ Enter steers, Alt+Enter queues a follow-up |
| Line editing (arrows, history) | ✅ arrows, history, Home/End, Ctrl-A/E/U/K/W; falls back to plain reads off a terminal | ✅ full TUI editor |
| Run modes | interactive CLI, headless `-p`, JSON stream, RPC over stdio, server + web console, Go SDK | interactive, print/JSON, RPC, SDK |
| RPC over stdio | ✅ `titan rpc` — JSONL on stdin/stdout, events streamed live; verified from Python | ✅ |
| Embeddable as a library | ✅ `sdk` package — verified from a separate module | ✅ SDK, RPC over stdin/stdout JSONL |
| Language | Go, single static binary | TypeScript/Node |

Steering is where Pi set the bar and Titan followed. A user who sees the agent
going the wrong way should redirect it, not kill the run and pay again for every
file it had already read. Titan applies a typed line at the next turn boundary,
so a call in flight still completes and the transcript never shows one with no
result, and a slash command typed mid-run is queued rather than dropped.

The remaining difference is shape. Pi is a TUI with a full editor and a
distinction between steering now and queueing a follow-up; Titan reads a line
and treats anything sent during a turn as steering. Pi's is the nicer instrument;
they do the same job.

## Tools

| | Titan | Pi |
|---|---|---|
| File and shell | read, write, edit, glob, grep, bash | read, write, edit, bash, grep, find, ls, powershell |
| Web search | ✅ 5 providers (duckduckgo, brave, tavily, serper, searxng) | ❌ extension |
| Skills | ✅ `skill` tool, listing in prompt | ✅ Agent Skills standard |
| Remote execution | ✅ ssh, ssh_connect | ❌ extension (an example exists) |
| Kubernetes | ✅ k8s_get, k8s_apply, k8s_login | ❌ extension |
| MCP | ✅ client and gateway | ❌ deliberately excluded |
| Register a tool without recompiling | ✅ MCP server, or an extension answering `list_tools` | ✅ `pi.registerTool` in TypeScript |
| Override a built-in tool | ❌ a duplicate name is refused, so which tool ran cannot depend on load order | ✅ register the same name |

The asymmetry is the design. Titan ships the enterprise integrations (k8s, ssh,
five search providers, MCP) because an air-gapped customer cannot npm-install an
extension. Pi ships four tools and an API, because a developer on a laptop can.

## Extensibility

Pi's extension API is the broadest of any harness surveyed, Claude Code
included. Extensions are TypeScript modules loaded through jiti, needing no
build step, and they hook more than thirty events across lifecycle, compaction,
execution, tools, provider traffic and model selection, plus `registerTool`,
`registerCommand`, `registerShortcut`, `registerFlag`, `registerProvider`,
custom renderers, and durable session entries via `appendEntry`.

Titan now has an extension API too, deliberately narrower. Extensions are
separate processes speaking JSONL, in any language, and hook eight events:

| Event | The extension may |
|---|---|
| `tool_call` | block the call, force an approval prompt, rewrite the arguments |
| `tool_result` | rewrite what the model reads |
| `context` | drop messages before they are sent upstream |
| `before_agent_start` | append to the system prompt |
| `before_compact` | cancel the compaction, or supply the summary itself |
| `list_tools` | provide tools the harness never had |
| `invoke_tool` | run one of its own tools |
| `session_start`, `session_end` | set up and tear down |

That covers what an operator most often forks a harness to do — gate a
dangerous call, redact a result, filter context for privacy or RAG, add a
company-specific tool, keep something the summarizer would drop. It is not
Pi's surface. There is no equivalent of `before_provider_request`,
`registerShortcut`, custom renderers, session-fork hooks or the TUI, and
"an extension can do anything" remains true of Pi and not of Titan.

**One difference is a decision rather than a gap.** A Titan extension may veto
and never permit: it can block a call or force it to a prompt, and cannot turn a
denied action into an allowed one. Pi's answer to permissions is to containerise
or write an extension, which is coherent for one developer and unavailable to a
deployment that must prove to an auditor what the agent was permitted to do —
because a gate an extension supplies is one an extension can also remove. Two
tests hold that line, and they are the first thing to read if it ever changes.

## Sessions

| | Titan | Pi |
|---|---|---|
| Storage | Postgres, append-only, trigger-enforced | JSONL under `~/.pi/agent/sessions/`, by working directory |
| Model | flat event stream per session | tree: every entry has `id` and `parentId` |
| Branching | ✅ `Fork` rebuilds a conversation from any sequence number | ✅ in-place, no new file |
| Navigate history | ✅ `/tree` lists the steps and the numbers `/fork` takes | ✅ `/tree` |
| Fork from a past point | ✅ `/fork <step>`, and `Fork` in the SDK | ✅ `/fork`, `/clone` |
| Resume | ✅ `/resume` replays a transcript | ✅ `/resume` |
| Deterministic replay | ✅ the point of the design | ⚠️ full history is retained, replay is not a stated feature |
| Share | ✅ `/export` writes a self-contained HTML transcript; `.json` still writes events | ✅ `/export` to HTML, `/share` to a gist |
| Multi-tenant isolation | ✅ Postgres RLS, enforced at two layers | ❌ single user, local files |

Both store everything, neither discards history on compaction, and both can now
go back to an earlier point and take a different path. What differs is what the
store is *for*, and it shows in the shape rather than the feature list.

Pi's sessions are a tree: entries carry a `parentId`, branching happens in place,
and `/tree` is a navigation surface. Titan's is a flat event log per session, and
`Fork` rebuilds a conversation by replaying events up to a sequence number —
which works because the log was never a description of the session, it is the
session. Nothing extra had to be stored to make branching possible; the event
sourcing that exists for audit paid for it.

That is the honest summary of this section: Pi's model is the better one for
moving around a session, Titan's is the one that can prove afterwards what
happened, and each got the other's headline feature at a cost the other would
not pay.

## Context management

| | Titan | Pi |
|---|---|---|
| Auto-compaction | ✅ proactive, with headroom for the coming turn | ✅ proactive and reactive after overflow |
| Manual | ✅ `/compact` | ✅ `/compact` |
| Custom summary | ✅ the `before_compact` event can cancel or supply the summary | ✅ `session_before_compact` can cancel or supply the summary |
| Full history preserved | ✅ event store | ✅ JSONL |
| Memory files | ✅ TITAN.md | ✅ AGENTS.md, SYSTEM.md |
| Per-result size cap | ✅ a quarter of the window | ? |
| Filter messages per request | ✅ the `context` event, removal only | ✅ the `context` event |

Titan's compaction is measured: a hundred-turn session in a 32k window peaks at
17,981 tokens with about one compaction per twenty turns
([12](12-comparison.md), [13](13-zrag-benchmark.md)). Pi's is documented but no
published measurement was found — absence of a number, not absence of quality.

## Providers

| | Titan | Pi |
|---|---|---|
| Named providers | 20 | 25+, "hundreds of models" |
| Wire formats | 3 (OpenAI, Anthropic Messages, Gemini) | OpenAI- and Anthropic-compatible, plus per-provider |
| Switch model mid-session | ✅ `/model` swaps the adapter and keeps the conversation; the next turn re-prefills | ✅ `/model`, or a shortcut, mid-run |
| Subscription auth (Claude Pro, ChatGPT Plus) | ⚠️ a subscription token authenticates — `CLAUDE_CODE_OAUTH_TOKEN` or `oauth_token` in config — but Titan does not run the browser flow that mints one | ✅ built-in login |
| Custom provider without recompiling | ✅ `custom_providers` in config | ✅ `models.json`, or `registerProvider` |
| Sampling parameters | ✅ 13, refused at startup when unsupported | ⚠️ thinking level is first-class; full sampling surface not documented |

Comparable breadth, and both now add a provider from configuration rather than a
rebuild. Two differences remain.

Titan refuses to start when a configured sampling parameter is one the provider
cannot honour, which no other harness surveyed does. `min_p` sent to a hosted API
is ignored silently, and the evidence is nowhere in the output — the answers are
simply drawn from a distribution nobody chose. Failing the config is the smaller
harm.

Pi signs in to a subscription; Titan reads a token someone else minted. That is
deliberate — the browser flow belongs to the vendor and changes without notice,
and a broken copy of someone else's login locks users out of their own account —
but it does mean a Pi user runs one command and a Titan user runs two.

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

## Where Titan still trails, as of this revision

| Capability | Why it is still open |
|---|---|
| Minting a subscription token | A token authenticates once you have one, but Titan does not run the browser flow that produces it: `claude setup-token` prints one for Claude, and an OpenAI subscription needs its own. Reimplementing someone else's login is a standing liability — it changes without notice and a broken copy locks users out — so Titan reads the token and does not mint it. GitHub Copilot is not supported at all. |
| The breadth of Pi's extension surface | Titan hooks eight events; Pi hooks more than thirty, including provider request and response, session fork, keyboard shortcuts and the whole TUI. The eight cover blocking, rewriting, context filtering, compaction and tool provision — most of what an operator forks a harness to do — but "an extension can do anything" remains Pi's, not Titan's. |
| Themes, prompt templates, packaged distribution | Pi ships themes, `{{variable}}` prompt templates, and npm/git distribution for extension bundles. Titan has none of it. Cosmetic next to the rest, and genuinely missing. |

## What Titan took from Pi

Everything on this list was a gap when the comparison was first written, and all
of it is now built. It is kept because the order turned out to matter: the
extension API came second and made several of the others cheap, and skipping
straight to the features would have meant building each of them into the core.

| Taken | Where it landed |
|---|---|
| Mid-run steering | type while the agent works; a slash command queues for after |
| An extension API | eight events, veto-only — [15-extensions.md](15-extensions.md) |
| Session branching | `/tree` and `/fork`, rebuilt from the event log |
| Providers from configuration | `custom_providers`, no rebuild |
| A readable export | `/export` writes a self-contained HTML transcript |
| Driving it from another language | `titan rpc`, JSONL over stdio |
| Embedding it in a program | the `sdk` package |
| Line editing at the prompt | arrows, history, the usual control keys |

One correction is worth recording, because it cost two rounds of this document
being wrong. Building a mechanism is not closing a gap. Steering existed only
over HTTP while the CLI still blocked on a read; `Fork` was written and called by
nothing; and the whole thing sat on a branch, compiled to a scratch path, while
the binary on the user's PATH was from before any of it. The tables above say
"verified against the installed binary" for that reason.

## What Pi would take from Titan, if it wanted to

Mostly nothing, and deliberately — but the two capabilities a Pi user cannot
build as an extension are an event store with deterministic replay, and tenant
isolation enforced in the database. Both have to be in the core or they are not
guarantees.

## Summary

Pi is still the better harness for one developer. It is smaller, its extension
API is the broadest of any surveyed, and its TUI is a nicer instrument than
Titan's prompt. A developer who wants to reshape the harness itself should use
Pi, and that is what it is for.

Titan is the better platform for a regulated deployment, and the reason is
narrow: its policy engine, event sourcing, tenant isolation and evaluation
harness cannot be added later by anyone who is allowed to remove them. An
extension-supplied permission gate is one an extension can also remove; a deny
rule that an extension cannot override is a different kind of object.

The useful conclusion has changed since this document was first written. The
extensibility gap that was its headline is closed — not by copying Pi's surface,
but by taking the parts an operator actually forks a harness for and keeping the
one rule Pi gives up. What remains is real and small: no provider-request hook,
no shortcuts or custom renderers, no minting of subscription tokens, no Copilot,
no themes or prompt templates.

The two harnesses now differ mostly where they meant to.
