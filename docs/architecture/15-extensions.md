# Titan extensions

Status: 2026-09-06

An extension changes how the agent behaves without forking it. Extensions are
separate processes speaking JSONL over stdin and stdout, so they can be written
in any language and need no build step in Titan.

## The one rule

**An extension may veto, never permit.**

It can block a call, force it to an approval prompt, rewrite arguments before a
tool runs, rewrite a result before the model reads it, drop messages before they
are sent upstream, and add to the system prompt. It cannot turn a denied action
into an allowed one.

This is the single line Titan draws differently from Pi, whose extension API is
otherwise broader and better. Pi's answer to permissions is to containerise or
write an extension, which is coherent for a developer on a laptop. It is not
available to a deployment that has to prove to an auditor what the agent was
permitted to do, because a gate an extension supplies is one an extension can
also remove.

Concretely: the policy engine evaluates hooks first so they can veto, which
means a hook returning `Allow` would short-circuit the deny rules beneath it.
Acceptable for a hook an operator compiled in; not for one an operator dropped
into a directory. `Host.PolicyHook` is structurally incapable of constructing an
`Allow`, and two tests hold that line — read those first if this ever changes.

## Protocol

One JSON object per line, in each direction. Titan writes a request; the
extension writes exactly one reply.

```jsonc
// Titan → extension
{"event":"tool_call","session_id":"s-1","tool":"bash","args":{"command":"rm -rf /tmp/x"}}

// extension → Titan
{"block":true,"reason":"rm -rf is not permitted here","log":"blocked a destructive command"}
```

### Events

| Event | When | The reply may |
|---|---|---|
| `tool_call` | before a tool runs | `block`, `ask`, rewrite `args` |
| `tool_result` | before the model sees output | rewrite `content`, set `is_error` |
| `context` | before each model call | `keep` a subset of messages, by index |
| `before_agent_start` | once per run | append to `system` |
| `session_start`, `session_end` | run boundaries | nothing; for setup and teardown |

Declaring no `events` subscribes to all of them.

### Reply fields

| Field | Effect |
|---|---|
| `block` | refuse the call; `reason` is fed back so the model can adapt |
| `ask` | force an approval prompt even where policy would have allowed it |
| `args` | replace the call's arguments |
| `content`, `is_error` | replace a tool result |
| `keep` | indices of the messages to send; `[]` sends none |
| `system` | text appended to the system prompt |
| `log` | written to Titan's log |

An empty reply `{}` means no opinion, which is also how a crashed, hung or
nonsensical extension is treated.

## Configuration

```json
{
  "extensions": [
    {
      "name": "guard",
      "command": "bash",
      "args": ["/opt/titan/guard.sh"],
      "events": ["tool_call"],
      "timeout_ms": 5000
    }
  ]
}
```

## Behaviour that is deliberate

**Extensions combine toward the stricter answer.** Where two disagree, the
blocking one wins; where one asks for approval and another is silent, the call
is asked. Load order therefore cannot change a verdict, which is what keeps the
audit trail reproducible.

**A failing extension is skipped, not fatal.** One that crashes, hangs past its
timeout, or replies with something unparseable is marked dead and skipped from
then on; the agent continues under policy alone. Failing the session instead
would trade a working agent for a broken one and protect nothing, since an
extension could only ever have made a decision stricter.

**A hung extension is not retried.** The read is abandoned but the stream is
not, so a later reply would be matched to the wrong request.

**The extension sees the whole call.** Including fields the model wrote itself,
such as a bash `description`. That is deliberate — an extension judging a
command should see the intent stated alongside it — but it means a naive
whole-line match can fire on a description rather than the command. Match the
field you mean.

## Worked example

Block reads of anything matching a pattern:

```bash
#!/bin/bash
while IFS= read -r line; do
  tool=$(jq -r '.tool // ""' <<<"$line")
  path=$(jq -r '.args.path // ""' <<<"$line")
  if [[ "$tool" == "read" && "$path" == *secret* ]]; then
    echo '{"block":true,"reason":"secrets are off limits"}'
  else
    echo '{}'
  fi
done
```

Redact credentials from every tool result:

```bash
#!/bin/bash
while IFS= read -r line; do
  if [[ $(jq -r '.event' <<<"$line") == "tool_result" ]]; then
    jq -c '{content: (.content | gsub("AKIA[A-Z0-9]{16}"; "[redacted]"))}' <<<"$line"
  else
    echo '{}'
  fi
done
```

## Custom model providers

A provider no longer needs a rebuild either:

```json
{
  "custom_providers": [
    {
      "name": "internal-vllm",
      "api": "openai",
      "base_url": "https://llm.internal.example/v1",
      "sampling": ["temperature", "top_p", "top_k", "seed"]
    }
  ]
}
```

`api` is named rather than guessed from the URL: a wrong guess produces
rejected requests whose errors point nowhere near the cause. The `sampling`
list is what the endpoint honours, and a parameter outside it is refused at
startup rather than silently ignored.
