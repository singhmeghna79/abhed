# Automation

Three ways to run Abhed without a person at the prompt.

## Headless

```bash
abhed -p "fix the failing tests" -mode auto -allow 'bash(go test*)'
abhed -p "explain what pkg/auth does" -mode plan
abhed -p "add a test for Valid" -output-format json > events.jsonl
```

Exit codes: `0` completed · `2` turn limit · `3` budget · `4` policy denied ·
`5` retries exhausted · `130` interrupted. A CI job can branch on those.

There is no one to approve, so anything needing approval is refused. Name what
may run with `-allow`, and keep the list narrow.

## RPC

For a caller that is not Go, `abhed rpc` speaks line-delimited JSON on stdin and
stdout — no server, no port, no auth for what is one process talking to its own
child.

```python
p = subprocess.Popen(["abhed", "rpc"], stdin=PIPE, stdout=PIPE, text=True, bufsize=1)

def send(**kw):
    p.stdin.write(json.dumps(kw) + "\n"); p.stdin.flush()

send(method="start", mode="auto")
send(method="prompt", prompt="fix the failing tests")
# every event arrives as {"type":"event", ...} while it works
```

| Method | |
|---|---|
| `start` | open a session — `workspace`, `mode`, `allow`, `deny` |
| `prompt` | send a prompt, get the final answer |
| `steer` | redirect a run in progress |
| `usage` | tokens, turns, compactions |
| `export` | the HTML transcript |
| `providers` | what this build supports |
| `quit` | close |

Events stream as they happen rather than only at the end, so a caller can render
progress. `steer` is why this is a persistent process rather than one request
per run.

## Server

```bash
abhed serve -addr :8420
```

A web console with an event stream, inline approvals and session history, plus a
JSON API. This is the multi-user path: it supports OIDC, and with Postgres
storage it enforces tenant isolation in the database.

```bash
B=http://127.0.0.1:8420
curl -s $B/v1/health
SID=$(curl -s -X POST $B/v1/sessions -d '{"prompt":"...","mode":"plan"}' | jq -r .session_id)
curl -sN $B/v1/sessions/$SID/events     # live
curl -s  $B/v1/sessions/$SID/replay     # the full audit trail
```

Sending a message to a session that is already working **steers** it rather than
being refused.
