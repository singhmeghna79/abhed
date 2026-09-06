# Titan — Runbook

Everything verified working on this machine (M3 Pro, 36 GB) as of 2026-09-02.

---

## Quick start

One command, from the repo root:

```bash
./scripts/start-local.sh          # start Postgres, Ollama, and the Titan server
./scripts/start-local.sh --stop   # stop Titan and Ollama
```

It is idempotent — it skips whatever is already running, so re-running it is
always safe. Then:

```bash
cd .titan-workspace && titan doctor   # verify (run this before trusting a session)
open http://localhost:8420            # web UI
titan                                 # interactive CLI
```

What it does, in order, and what each step is for:

| Step | Why it can fail |
|---|---|
| 1. Postgres | A killed postmaster leaves a stale `postmaster.pid` that blocks startup. The script removes it **only** when no postgres process is running. |
| 2. Ollama | Started with `OLLAMA_FLASH_ATTENTION=1` and `OLLAMA_KV_CACHE_TYPE=q8_0` — without them a 26B model will not hold a long context in 36 GB. |
| 3. Workspace | Checks `.titan-workspace/.titan/config.json` exists; scaffolds one with `titan init` if not. |
| 4. Titan server | Refuses to start if :8420 is held by something that is not Titan. |

### Doing it by hand

If you would rather run the steps yourself:

```bash
# 1. Postgres
pg_isready                                    # already up? then skip
brew services start postgresql@16

# 2. Ollama
(OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0 \
   nohup ollama serve > .titan-workspace/logs/ollama.log 2>&1 &)
curl -s http://127.0.0.1:11434/api/version    # {"version":"0.33.2"}

# 3. Titan server
cd .titan-workspace
(nohup titan serve -addr :8420 > logs/titan-serve.log 2>&1 &)
curl -s http://127.0.0.1:8420/v1/health       # {"status":"ok",...}
```

Stopping by hand:

```bash
pkill -f "titan serve"
pkill -f "ollama serve"
brew services stop postgresql@16    # usually worth leaving up
```

> **The workspace lives in the repo, not `/tmp`.** It is
> `.titan-workspace/` (gitignored, with its own `go.mod` so it stays out of
> the parent Go module). It used to be `/tmp/titan-test`, and macOS purging
> `/tmp` silently deleted the workspace and its config — that is what took the
> stack down on 2026-09-05. Never put the workspace or its logs back in `/tmp`.

> **Port note:** something else on this machine already uses **:8080**, so
> Titan is set up on **:8420**. Check any port with
> `lsof -nP -iTCP:8420 -sTCP:LISTEN` before using it.

---

## Checking what is running

```bash
pg_isready                                        # Postgres
curl -s http://127.0.0.1:11434/api/version        # Ollama
curl -s http://127.0.0.1:8420/v1/health           # Titan
ollama ps                                         # is the model loaded in memory?
pgrep -fl "titan serve"; pgrep -fl "ollama serve"
```

`ollama ps` printing an empty table means the model is on disk but not in
memory — the next request pays an 18 GB load (`OLLAMA_KEEP_ALIVE=5m` unloads
it after five idle minutes). `titan doctor` warms it as a side effect.

---

## 1. Ollama

### Start / stop

```bash
# Start in the foreground (Ctrl-C to stop) — best while testing
OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0 ollama serve

# Start in the background
(OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0 ollama serve > .titan-workspace/logs/ollama.log 2>&1 &)

# Or as a managed service that restarts at login
brew services start ollama
brew services stop ollama
brew services restart ollama
brew services list | grep ollama
```

The two env vars matter: flash attention cuts memory use, and a q8 KV cache
roughly halves KV memory — which is what lets a 30B model hold a long context
in 36 GB.

### Check it is up

```bash
curl -s http://127.0.0.1:11434/api/version     # {"version":"0.33.2"}
tail -20 .titan-workspace/logs/ollama.log                       # startup log, GPU discovery
```

### Stop it

```bash
pkill -f "ollama serve"          # if started manually
brew services stop ollama        # if started as a service
```

There is no pause. To free memory without stopping the server, unload the model:

```bash
ollama stop gemma4:26b           # unloads from memory, keeps it on disk
```

### Models

```bash
ollama list                      # what is downloaded
ollama ps                        # what is loaded in memory right now
ollama pull gemma4:26b           # download (18 GB)
ollama rm qwen2.5-coder:7b       # delete from disk
ollama show gemma4:26b           # architecture, context length, licence
```

**Currently installed here:**

| Model | Size | Decode | Verdict |
|---|---:|---:|---|
| `gemma4:26b` | 18 GB | 35 tok/s | ✅ **The default.** MoE, 8 of 128 experts active |
| `qwen3-coder:30b` | 18 GB | 47 tok/s | ⚠️ Faster, but misreports whether it verified its work |
| `qwen3.8:27b` | 17 GB | 3 tok/s | ❌ Dense — every parameter activates. Correct but unusably slow |
| `qwen2.5-coder:7b` | 4.7 GB | — | ❌ Fails `titan doctor` — emits tool calls as text |

`gemma4:26b` is the default because it found *both* planted bugs in a review
task (a data race and an authorization hole), ran the build itself, and
reported the real output. `qwen3-coder:30b` is ~26% faster and found one — then
added an unrelated check, called it the security fix, and twice claimed
environment restrictions that did not exist.

Full measurements and method: [docs/ops/model-selection.md](docs/ops/model-selection.md).

**Check before trusting a size.** Decode speed tracks *active* parameters, not
total, so a 27B dense model is far slower than a 30B MoE:

```bash
ollama show gemma4:26b | grep -i expert   # expert_used_count is what matters
```

**Will not fit in 36 GB:** `gpt-oss:120b` needs ~58 GB. That is what the
cluster is for.

### Configuration

Ollama is configured by environment variables, not a config file:

```bash
OLLAMA_FLASH_ATTENTION=1        # reduce attention memory
OLLAMA_KV_CACHE_TYPE=q8_0       # quantize the KV cache (q8_0 | q4_0 | f16)
OLLAMA_HOST=127.0.0.1:11434     # bind address
OLLAMA_MODELS=~/.ollama/models  # where weights live
OLLAMA_KEEP_ALIVE=5m            # how long a model stays loaded
OLLAMA_NUM_PARALLEL=1           # concurrent requests per model
```

Inspect what is in effect:

```bash
ollama ps                                    # loaded models, context, expiry
grep "inference compute" .titan-workspace/logs/ollama.log     # GPU detected and memory available
du -sh ~/.ollama/models                      # disk used by weights
```

### Test the model directly

```bash
# Plain completion
curl -s http://127.0.0.1:11434/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemma4:26b","messages":[{"role":"user","content":"say ok"}]}' \
  | python3 -m json.tool

# Tool calling — the capability Titan actually depends on
curl -s http://127.0.0.1:11434/v1/chat/completions \
  -H 'Content-Type: application/json' -d '{
   "model":"gemma4:26b",
   "messages":[{"role":"user","content":"List go files with the glob tool."}],
   "tools":[{"type":"function","function":{"name":"glob",
     "parameters":{"type":"object","properties":{"pattern":{"type":"string"}}}}}]}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['choices'][0]['message'].get('tool_calls'))"
```

If that last command prints `None`, the model cannot drive Titan. Use a
different one — this is exactly the check `titan doctor` automates.

---

## 2. Titan CLI

### Interactive

```bash
cd .titan-workspace    # or any repo
titan
```

Then type a task. Try:

```
The tests in pkg/auth are failing. Read the code, find the bug, fix it, then run the tests.
```

### Slash commands

| Command | Effect |
|---|---|
| `/help` | List commands |
| `/mode <name>` | `default`, `accept-edits`, `plan`, `auto` |
| `/undo` | Revert the last turn's file changes |
| `/diff` | Files changed this session, with line counts |
| `/cost` | Tokens, cache hit rate, compactions |
| `/compact` | Compact the context now |
| `/clear` | Clear context, keep the workspace |
| `/memory` | Show the TITAN.md files in effect |
| `/model` | Show or switch provider |
| `/sessions` | Recent sessions (needs Postgres) |
| `/resume <id>` | Replay a past session |
| `/export [path]` | Write the transcript — HTML by default, `.json` for raw events |
| `/cwd` | Workspace root |
| `/fork [step]` | Rebuild the conversation up to a step and continue from it |
| `/quit` | Exit |

The prompt supports the editing a terminal user expects: Left and Right to move,
Up and Down for history, Home, End, Ctrl-A, Ctrl-E, Ctrl-U, Ctrl-K, Ctrl-W.
Piped input skips raw mode, so scripts and here-docs behave unchanged.

**Type while the agent is working.** A line sent mid-run steers it at the next
step rather than interrupting: the files it has read and the results it has
gathered are kept. A slash command typed mid-run is queued and runs when the
turn finishes.

### Headless

```bash
titan -p "fix the failing tests" -mode auto -allow 'bash(go test*)'
titan -p "explain what pkg/auth does" -mode plan
titan -p "add a test for Valid" -mode auto -output-format json > events.jsonl
```

Exit codes: `0` completed · `2` turn limit · `3` budget · `4` policy denied ·
`130` interrupted.

### Permission modes

| Mode | Behaviour |
|---|---|
| `default` | Ask before every mutation (interactive only) |
| `accept-edits` | Auto-approve edits, ask for shell |
| `plan` | **Read-only** — safe for exploring |
| `auto` | Approve by rule; deny rules and destructive commands still confirm |

In headless mode there is no TTY, so anything needing approval is refused —
use `-mode auto` with explicit `-allow` rules.

### Other commands

```bash
titan doctor                    # verify endpoint, tools, sandbox, storage, index
titan providers                 # model providers this build supports
titan rpc                       # drive Titan from another language over stdio
titan init                      # write a starter .titan/config.json
titan index                     # build the retrieval index
titan eval                      # run the 144-task corpus
titan -version
```

**Run `titan doctor` first, always.** It catches a model that cannot emit tool
calls before you waste a run on it.

---

## 3. Titan web UI

```bash
cd .titan-workspace
titan serve -addr :8420
```

Open **http://localhost:8420**

You land on an overview page describing this deployment — model, sandbox tier,
storage, whether the agent can reach the internet, and how many sessions have
run. With sign-in configured it asks you to authenticate first; without it,
"Start working" goes straight through.

| Route | What it is |
|---|---|
| `/` | Overview and sign-in |
| `/console` | The chat workspace |
| `/v1/overview` | The JSON the overview page renders |

In the console:

- **Left** — type a task, pick a mode, browse recent sessions
- **Main** — live event stream: tool calls, output, agent replies
- **Approvals** — appear inline with full arguments; approve or reject
- **Top** — model and active session count

Background it and watch the log:

```bash
(titan serve -addr :8420 > .titan-workspace/logs/titan-serve.log 2>&1 &)
tail -f .titan-workspace/logs/titan-serve.log
pkill -f "titan serve"
```

### API

```bash
B=http://127.0.0.1:8420

curl -s $B/v1/health

SID=$(curl -s -X POST $B/v1/sessions -H 'Content-Type: application/json' \
  -d '{"prompt":"read pkg/auth/token.go and explain it","mode":"plan"}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['session_id'])")

curl -sN $B/v1/sessions/$SID/events        # live stream
curl -s $B/v1/sessions/$SID/replay | jq    # full audit trail
curl -s -X POST $B/v1/sessions/$SID/approve -d '{"approved":true}'
curl -s -X POST $B/v1/sessions/$SID/interrupt
```

---

## 4. Postgres

Sessions survive restarts only with Postgres. Configured in
`.titan-workspace/.titan/config.json`, which carries `storage.dsn` directly —
so `titan doctor` and `titan serve` work with no environment variable set.

If `storage.dsn` is ever missing while `storage.driver` is `postgres`, every
command fails with *"storage.driver is postgres but no DSN is set"*. Either put
the DSN back in the config or export it for the session:

```bash
export TITAN_DATABASE_URL='postgres://titan_app:<password>@localhost:5432/titan_local'
```

The real password is in `.titan-workspace/.titan/config.json` under
`storage.dsn` (gitignored — deliberately not committed here).

```bash
pg_isready
brew services start postgresql@16
brew services stop postgresql@16

psql -d titan_local -c "SELECT count(*) FROM sessions;"
psql -d titan_local -c "SELECT id, terminal_reason, turns, tokens_in
                        FROM sessions ORDER BY started_at DESC LIMIT 5;"
psql -d titan_local -c "SELECT type, count(*) FROM events GROUP BY type;"
```

### Permission rules for skills

Auto mode approves file edits but always asks for `bash`, because its blast
radius is unbounded. A skill that shells out — zrag runs its retrieval through
`run.sh` — therefore prompts for approval on every call, which in the web UI
looks like auto mode not working at all.

The fix is a narrow allow rule in `.titan-workspace/.titan/config.json`, not a
broader mode:

```json
"permissions": {
  "mode": "auto",
  "allow": [
    "bash(*/.titan/skills-active/zrag/scripts/run.sh*)",
    "web_search"
  ]
}
```

Match the script, not a prefix of one invocation form: the model calls it both
as `bash /path/run.sh …` and as `/path/run.sh …`, and a rule written for the
first silently denies the second. Verify a rule allows what you meant and still
refuses what you did not before trusting it.

**Do not connect Titan as a superuser** — row-level security is what isolates
tenants, and superusers bypass it.

> **Tenant note:** `storage.tenant` in config must match the tenant your
> requests carry. With `auth.mode: none` Titan reconciles this for you; with
> OIDC the token's tenant is authoritative.

---

## 5. Benchmarking

```bash
titan-bench -model gemma4:26b -turns 20
```

Reports cache hit rate, prefill savings, cold vs warm TTFT.

**Expect 0% cache on Ollama** — it does not report `cached_tokens`. That is a
serving-stack limitation, not a Titan one, and it is exactly why the same
benchmark against vLLM on your cluster is worth running.

---

## 6. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `doctor`: no tool call | Model cannot tool-call | Use `gemma4:26b` |
| `doctor`: empty response | Reasoning consumed the token budget | Raise `context.max_tokens` |
| `connection refused` :11434 | Ollama down | `./scripts/start-local.sh` |
| Postgres will not start, log says `lock file "postmaster.pid" already exists` | Stale lock from an interrupted shutdown; the PID it names has been recycled | Confirm no postmaster runs (`pgrep -fl postgres`), check the PID is not postgres (`ps -p <pid>`), then `rm /opt/homebrew/var/postgresql@16/postmaster.pid` and restart. **Never remove it while a postmaster is live.** |
| Workspace and config vanished | It was in `/tmp`, which macOS purges | Keep it at `.titan-workspace/` in the repo |
| `storage.driver is postgres but no DSN is set` | `storage.dsn` missing from config | Add it to `.titan/config.json`, or export `TITAN_DATABASE_URL` |
| Wrong app answers the port | Port already taken | `lsof -nP -iTCP:8420 -sTCP:LISTEN` |
| `row-level security policy` | Tenant mismatch | Match `storage.tenant` to the request tenant |
| `operation not permitted` on build | Sandbox scoping | Expected outside the workspace |
| Sessions vanish | Memory store | Set `storage.driver: postgres` |
| Every request anonymous | `auth.mode: none` | Set `proxy` or `oidc` |
| Model very slow | Cold load | First call loads 18 GB; `ollama ps` to confirm |
| Ollama and Titan both die mid-run, macOS reports low memory | `context_window` exceeds what the GPU can hold | Ollama logs its own sizing at startup: `grep "vram-based default context" .titan-workspace/logs/ollama.log`. Set `context_window` to that number or below — asking for more does not fail loudly, it just stops fitting once a long session fills it. |
| A retrieval session uses far more context than expected | a retrieval call returning 20 documents is ~7,700 tokens | Two hops plus the 5,000-token skill body is ~22,000 tokens before the answer. Size `context_window` for the worst case, or lower `k` in the retrieval profile. |
| `go build` version mismatch | Toolchain confusion | Use `/usr/local/go/bin/go` |

Diagnostics:

```bash
titan doctor
tail -50 .titan-workspace/logs/ollama.log
tail -50 .titan-workspace/logs/titan-serve.log
titan -p "..." -output-format json | jq 'select(.type=="observation")'
```

---

## 7. Full reset

```bash
./scripts/start-local.sh --stop
./scripts/start-local.sh
cd .titan-workspace && titan doctor
```

Reset the demo bug so you have something to fix again:

```bash
cd .titan-workspace && cat > pkg/auth/token.go <<'GO'
package auth

import (
	"errors"
	"time"
)

var ErrExpired = errors.New("token expired")

type Token struct {
	Value   string
	Expires time.Time
}

// Valid reports whether the token is still usable.
func Valid(t *Token) bool {
	return t.Expires.After(time.Now()) || t.Expires.Equal(time.Now())
}
GO
go test ./pkg/auth/    # fails with a nil-pointer panic
```
