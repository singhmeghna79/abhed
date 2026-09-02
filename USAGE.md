# Titan — Usage Guide

Everything you need to run Titan from the CLI and the web UI, including a
zero-dependency path that needs no GPU.

---

## 0. Five-minute start with no model endpoint

You do not need a GPU to see Titan work. This mock endpoint replays a scripted
agent so you can exercise the CLI, the console, undo, and the audit trail.

```bash
cd ~/titan
go build -o titan ./cmd/titan

# A demo workspace with a deliberately broken test
mkdir -p /tmp/titan-demo && cd /tmp/titan-demo
cat > go.mod <<'GOMOD'
module example.com/demo

go 1.24
GOMOD
cat > math.go <<'GO'
package demo

func Add(a, b int) int {
	return a - b
}
GO
cat > math_test.go <<'GO'
package demo

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2,3) = %d, want 5", got)
	}
}
GO
go test ./...   # fails, as intended
```

Save this as `mock.py` in the same directory:

```python
import json, http.server, socketserver, re
state = {}
def sse(o): return f"data: {json.dumps(o)}\n\n".encode()
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        req = json.loads(self.rfile.read(n) or b"{}")
        text = " ".join(str(m.get("content") or "") for m in req.get("messages", []))
        ws = (re.search(r'Working directory: (\S+)', text) or [None, "/tmp"])[1]
        key = text[:150]; step = state.get(key, 0); state[key] = step + 1
        self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
        calls = []
        if step == 0:   calls = [("grep", {"pattern": "func Add", "output_mode": "content"})]
        elif step == 1: calls = [("read", {"path": ws + "/math.go"})]
        elif step == 2: calls = [("edit", {"path": ws + "/math.go", "old_string": "a - b", "new_string": "a + b"})]
        elif step == 3: calls = [("bash", {"command": "go test ./...", "description": "run tests"})]
        else: self.wfile.write(sse({"choices": [{"delta": {"content": "Fixed the sign error in Add. Tests pass."}}]}))
        for i, (nm, a) in enumerate(calls):
            self.wfile.write(sse({"choices": [{"delta": {"tool_calls": [
                {"index": i, "id": f"c{step}", "function": {"name": nm, "arguments": json.dumps(a)}}]}}]}))
        self.wfile.write(sse({"choices": [{"delta": {}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 1200, "completion_tokens": 30,
                      "prompt_tokens_details": {"cached_tokens": 1000}}}))
        self.wfile.write(b"data: [DONE]\n\n")
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", 8099), H).serve_forever()
```

Run it:

```bash
python3 mock.py &
export TITAN_BASE_URL=http://127.0.0.1:8099/v1
export TITAN_MODEL=mock

~/titan/titan doctor
~/titan/titan -p "fix the failing test" -mode auto -allow 'bash(go test*)'
go test ./...    # now passes
```

You should see the agent grep, read, edit, run the tests, and report. Then try
the interactive mode and the console below.

---

## 1. Pointing at a real model

Titan speaks the OpenAI chat-completions API, so anything that serves it works.

```bash
export TITAN_BASE_URL=http://your-gpu-host:8000/v1
export TITAN_MODEL=Qwen/Qwen3-32B
export TITAN_API_KEY=...        # only if your endpoint requires one
titan doctor
```

Or write it into config:

```bash
titan init          # creates .titan/config.json
$EDITOR .titan/config.json
```

**`titan doctor` is the command to run first.** It checks the endpoint responds,
that the model actually emits tool calls (the capability Titan depends on), and
reports the sandbox tier, auth mode, storage backend, index size and MCP servers.

Serving stacks known to work: vLLM, SGLang, TensorRT-LLM's OpenAI server,
llama.cpp, Ollama, and hosted APIs. For vLLM, **enable `--enable-prefix-caching`** —
see §7.

---

## 2. CLI

### Interactive

```bash
cd /path/to/your/repo
titan
```

```
titan 0.1.0-dev  Qwen/Qwen3-32B · /home/you/repo
Type a task, or /help for commands. Ctrl-C interrupts, Ctrl-D exits.

› fix the failing auth tests
● grep "auth.*test"      └ 3 files
● read auth_test.go      └ 84 line(s)
● bash run tests         └ exit 1
  │ --- FAIL: TestLogin_Expired
● edit auth.go
  ╭ 87  - return nil, err
  ╰ 87  + return nil, ErrTokenMissing
  [a]ccept  [r]eject  [A]lways allow edit(auth.go)
```

### Headless

```bash
titan -p "fix the failing tests"
titan -p "review this diff" -mode plan
titan -p "add tests for the parser" -mode auto -allow 'bash(go test*)'
titan -p "summarize this repo" -output-format json > events.jsonl
```

Exit codes let CI distinguish outcomes:

| Code | Meaning |
|---:|---|
| 0 | Completed |
| 1 | Error |
| 2 | Turn limit reached |
| 3 | Budget exhausted |
| 4 | Policy denied |
| 5 | Retries exhausted |
| 130 | Interrupted |

### Flags

| Flag | Purpose |
|---|---|
| `-p "<prompt>"` | Headless; exit code reflects the outcome |
| `-mode <name>` | `default`, `accept-edits`, `plan`, `auto`, `bypass` |
| `-model <name>` | Provider from config |
| `-C <dir>` | Workspace directory |
| `-max-turns N` | Override the turn cap |
| `-allow '<rule>'` | Extra allow rules, comma-separated |
| `-deny '<rule>'` | Extra deny rules |
| `-output-format json` | Emit the event stream |

### Slash commands

| Command | Effect |
|---|---|
| `/help` | List commands |
| `/mode <name>` | Switch permission mode |
| `/undo` | Revert the last turn's file changes |
| `/diff` | Files changed this session, with line counts |
| `/cost` | Tokens, cache hit rate, prefill saving, compactions |
| `/compact` | Compact the context now |
| `/clear` | Clear context, keep the workspace |
| `/memory` | Show the TITAN.md files in effect |
| `/model [name]` | Show or switch the configured provider |
| `/sessions` | Recent sessions (needs Postgres) |
| `/resume <id>` | Replay a past session's transcript |
| `/export [path]` | Write the transcript to JSON |
| `/cwd` | Workspace root |
| `/quit` | Exit |

`/undo` reverts **a whole turn**: a model that edits four files to make one change
undoes as one change. Undoing a file the agent *created* deletes it.

### Permission modes

| Mode | Behaviour |
|---|---|
| `default` | Ask before any mutation |
| `accept-edits` | Auto-approve file edits, ask for shell |
| `plan` | Read-only — produces a plan, changes nothing |
| `auto` | Approve by rule; deny rules and destructive commands still confirm |
| `bypass` | Dangerous; refusable by org policy, blocked as root |

Deny rules are **absolute** — they hold even in `bypass`. Destructive commands
(`rm -rf`, force push, `find -delete`, `mkfs`…) confirm in *every* mode.

---

## 3. Web UI

```bash
titan serve -addr :8080
```

Open `http://localhost:8080`.

- **Left panel** — start a session, pick a mode, browse recent sessions
- **Main panel** — live event stream: tool calls, output, agent messages
- **Approvals** — appear inline with the full arguments; approve or reject
- **Status bar** — model and active session count

The console is a single self-contained HTML document with no CDN and no build
step, because an air-gapped enclave has no CDN and the binary alone must be a
complete install.

### API

```bash
# Create a session
curl -X POST localhost:8080/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"fix the tests","mode":"auto"}'

# Stream events (resumable via Last-Event-ID)
curl -N localhost:8080/v1/sessions/<id>/events

# Full audit replay
curl localhost:8080/v1/sessions/<id>/replay | jq

# Approve a pending action
curl -X POST localhost:8080/v1/sessions/<id>/approve -d '{"approved":true}'

# Interrupt
curl -X POST localhost:8080/v1/sessions/<id>/interrupt
```

---

## 4. Configuration

`.titan/config.json` in the repo, `~/.titan/config.json` for user defaults, and
`/etc/titan/config.json` for org policy. **Managed config always wins** — a local
file cannot escalate past it.

```json
{
  "model": {
    "default": "onprem",
    "providers": {
      "onprem": {
        "type": "openai-compatible",
        "base_url": "http://vllm.internal:8000/v1",
        "model": "Qwen/Qwen3-32B",
        "api_key_env": "TITAN_API_KEY",
        "context_window": 131072
      }
    }
  },
  "permissions": {
    "mode": "default",
    "deny": ["bash(rm -rf /*)", "write(/etc/**)"],
    "allow": ["bash(git status*)", "bash(go test*)"]
  },
  "sandbox": { "min_tier": "process", "allow_network": false },
  "storage": { "driver": "memory" },
  "auth": { "mode": "none" },
  "retrieval": { "enabled": false },
  "limits": { "max_turns": 100, "max_subagents": 20 }
}
```

Environment overrides: `TITAN_BASE_URL`, `TITAN_MODEL`, `TITAN_API_KEY`,
`TITAN_DATABASE_URL`.

### TITAN.md

Project conventions the agent should always know. Re-injected on every request,
so keep it short — every line is paid on every turn.

```markdown
# Project

## Commands
build: make build     test: make test     lint: golangci-lint run

## Conventions
- Errors wrapped with fmt.Errorf("%w"), never bare
- Table-driven tests, no testify

## Gotchas
- Integration tests need TEST_DB set, otherwise skipped
```

---

## 5. Durable sessions

Without Postgres, sessions vanish on exit. With it, they persist and can be
replayed — which is what audit requires.

```bash
createdb titan
psql -d titan -c "CREATE ROLE titan_app LOGIN PASSWORD 'changeme';"
psql -d titan -c "GRANT ALL ON SCHEMA public TO titan_app;"

export TITAN_DATABASE_URL="postgres://titan_app:changeme@localhost:5432/titan"
titan doctor     # confirms "postgres (durable...)"
```

The schema applies automatically on first start. Then:

```
› /sessions
  s-1788362231965  completed  2026-09-02 20:47  yuvraj
› /resume s-1788362231965
```

**Do not connect as a superuser.** Row-level security is what isolates tenants,
and superusers bypass it.

---

## 6. Security

### Sandbox tiers

| Tier | Mechanism | Use |
|---|---|---|
| `none` | Direct execution | Never for untrusted code |
| `process` | Seatbelt (macOS) / bubblewrap (Linux) | Default; trusted repos |
| `container` | OCI, all capabilities dropped | Untrusted repos |
| `vm` | gVisor / microVM | Hostile code |

```json
{ "sandbox": { "min_tier": "container", "allow_network": false } }
```

Titan **fails to start** rather than silently downgrading below your configured
tier. `titan doctor` always reports the tier actually in force.

### Authentication

```json
{
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "audience": "titan",
    "tenant_claim": "org_id",
    "groups_claim": "groups",
    "require_group": "titan-users"
  }
}
```

See [`docs/ops/oidc-providers.md`](docs/ops/oidc-providers.md) for copy-paste
blocks for Keycloak, Okta, Entra ID, Auth0 and Google, each with its specific
gotcha.

`mode: "proxy"` trusts `X-Titan-User` / `X-Titan-Tenant` headers and is only safe
when a trusted proxy is the sole route to the port. `mode: "none"` is
single-tenant local development.

---

## 7. Measuring your serving stack

Titan's context design assumes prefix caching works. Verify it:

```bash
go build -o titan-bench ./cmd/titan-bench
titan-bench -model Qwen/Qwen3-32B -turns 40
```

```
prompt tokens     49652
cached            43120
cache hit rate    86.8%
prefill savings   7.6x
cold TTFT         360 ms
warm TTFT          26 ms
TTFT speedup      13.7x

VERDICT: prefix caching is working.
```

If it reports **no cached tokens**, every turn pays full prefill and the capacity
model in `docs/architecture/04-sizing.md` does not hold for your stack. For vLLM,
add `--enable-prefix-caching`.

---

## 8. Evaluation

```bash
titan eval                                    # 144-task corpus
titan eval -json report.json                  # machine-readable
titan eval -corpus /path/to/your/tasks        # your own
```

The report carries behavioural flags alongside the score, because identical pass
rates hide different behaviour. **A task that passes every assertion still fails
if a blocking flag fires** — credential access, injection compliance, benchmark
gaming. Exit code is non-zero on failure so CI can gate on it.

---

## 9. Retrieval

```json
{ "retrieval": { "enabled": true, "embed": false } }
```

```bash
titan index      # build the index
```

Symbol and BM25 tiers need no embedding model. For natural-language search over
docs, add the vector tier:

```json
{
  "retrieval": {
    "enabled": true, "embed": true,
    "embed_base_url": "http://vllm.internal:8000/v1",
    "embed_model": "BAAI/bge-large-en-v1.5",
    "embed_dims": 1024
  }
}
```

Titan is **agentic-first**: grep and glob are always available, and the index
accelerates rather than replaces them. The evidence favouring one over the other
is contested, so the search tool counts its own calls and you can measure which
your codebase actually needs.

---

## 10. MCP servers

```json
{
  "mcp": {
    "servers": [{
      "name": "tracker",
      "command": "python3",
      "args": ["/opt/mcp/tracker.py"],
      "enabled": true,
      "allow_tools": ["lookup_incident"]
    }]
  }
}
```

Tools appear as `mcp__tracker__lookup_incident`. Every server is treated as
hostile: namespaced names prevent shadowing native tools, descriptions are
sanitized before reaching the model, and every call routes through the policy
engine.

`enabled` defaults to false — listing a server is not authorizing it.

---

## 11. Air-gapped install

```bash
# Connected build machine
scripts/build-bundle.sh -v 1.0.0 -k signing-key.pem

# Enclave
scripts/verify-bundle.sh titan-1.0.0.tar.gz public-key.pem
tar -xzf titan-1.0.0.tar.gz && cd titan-1.0.0 && sudo ./install.sh
```

Verification checks the archive digest, the signature, and every file's digest
after extraction. A swapped binary with a recomputed digest is caught by the
signature — which is why signing matters and hashing alone does not.

---

## 12. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `doctor`: model did not emit a tool call | No tool-call parser | Enable one for your model in the serving stack |
| `connection refused` | Endpoint unreachable | Check `TITAN_BASE_URL` |
| `no sandbox backend meets...` | Configured tier unavailable | Install runsc/Docker, or lower `min_tier` |
| Sessions vanish on exit | Memory store | Set `TITAN_DATABASE_URL` |
| `/sessions` says needs postgres | Same | Same |
| Cache hit rate 0% | Prefix caching off | `--enable-prefix-caching` on vLLM |
| Every request anonymous | `auth.mode: none` | Set `proxy` or `oidc` |
| `unexpected issuer` | Trailing-slash mismatch | Match `iss` byte-for-byte |
| Agent won't edit | `plan` mode | `/mode default` |
| Too many prompts | Narrow rules | `-mode accept-edits`, or `A` to always-allow |

Full diagnostics:

```bash
titan doctor
titan -p "..." -output-format json | jq 'select(.type=="observation")'
```

---

## 13. Where to read next

| Doc | Contents |
|---|---|
| [01 Principles](docs/architecture/01-principles.md) | The 12 design principles and their evidence status |
| [03 Security](docs/architecture/03-security.md) | Threat model, isolation tiers, validation status |
| [04 Sizing](docs/architecture/04-sizing.md) | GPU sizing math and prefill economics |
| [06 Tool contracts](docs/architecture/06-tool-contracts.md) | Exact tool schemas and error semantics |
| [08 Eval](docs/architecture/08-eval.md) | The four evaluation layers |
| [Air-gap ops](docs/ops/air-gap.md) | Offline install and egress brokering |
| [OIDC providers](docs/ops/oidc-providers.md) | Per-provider auth configuration |
| [Red-team scope](docs/ops/red-team-scope.md) | What to commission, and why it is still open |
