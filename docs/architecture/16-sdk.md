# Embedding Titan

Status: 2026-09-06

Everything Titan does lives under `internal/`, which Go refuses to let another
module import. That is right for a binary and a wall for anyone who wants the
agent inside their own service. The `sdk` package is the supported surface
across that wall, kept small so the internals stay free to change.

```go
import titan "github.com/yuvrajsingh/titan/sdk"

a, err := titan.New(ctx, titan.Options{
    Workspace: "/srv/work",
    Provider: &titan.Provider{
        Type: "anthropic", Model: "claude-opus-5",
        APIKey: os.Getenv("ANTHROPIC_API_KEY"),
    },
    Mode: "auto",
    Deny: []string{"bash(rm -rf *)"},
    Approve: func(ctx context.Context, tool string, args json.RawMessage, d titan.Decision) (bool, error) {
        return askTheUser(tool, args, d.Reason)
    },
    OnEvent: func(ev titan.Event) { log.Println(ev.Type) },
})
if err != nil {
    return err
}
defer a.Close()

answer, err := a.Run(ctx, "fix the failing tests")
```

## The surface

| Method | Purpose |
|---|---|
| `New` | build an agent from options, a config directory, or both |
| `Run`, `Continue` | send a prompt; `Continue` keeps the conversation |
| `Steer` | redirect a run already in progress, from another goroutine |
| `Fork` | rebuild the conversation up to a sequence number |
| `Events` | everything recorded; the stream is the session |
| `Usage` | tokens, turns, cache hit rate, compactions |
| `ExportHTML` | a self-contained transcript |
| `SetModel` | swap providers mid-conversation, keeping history |
| `Providers` | the provider types this build supports |

## What does not change when embedded

Policy still decides what runs. Every action is still recorded as an event, so
an embedded session replays exactly like one from the CLI. An extension still
may veto and never permit.

One difference is deliberate: **an agent with no `Approve` function refuses
anything needing approval** rather than assuming yes. A service with nobody to
ask should be stricter than a terminal with somebody watching, not looser —
defaulting to permissive would make the SDK quietly weaker than the same policy
on the command line.

## What is not here yet

**RPC over stdio.** Pi speaks JSONL on stdin and stdout so any language can
drive it. Titan offers this Go package and the HTTP server; a caller in Python
or TypeScript uses the server today. The subprocess protocol the extension host
already speaks is most of what an RPC mode needs, so this is a small gap rather
than a deep one.

**Subscription auth.** Pi can sign in with a Claude Pro, ChatGPT Plus or GitHub
Copilot subscription. Titan takes an API key. Closing this needs each vendor's
OAuth device flow and token refresh, one at a time — real work, and worth doing,
but not a change to the harness.
