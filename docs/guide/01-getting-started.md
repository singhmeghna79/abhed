# Getting started

## Install

Abhed is a single static binary with no runtime to install.

```bash
go build -o ~/.local/bin/abhed ./cmd/abhed
abhed -version
```

## Point it at a model

Abhed does not ship a model. It needs an endpoint, and the fastest one to get
running is a local server:

```bash
ollama serve
ollama pull qwen3-coder:30b
```

Then, in the directory you want the agent to work in:

```bash
abhed init      # writes .abhed/config.json
abhed doctor    # check the endpoint before you rely on it
```

`abhed doctor` is worth the ten seconds. It checks that the endpoint answers,
**and that the model can emit a structured tool call** — a model that describes a
tool call in prose instead of emitting one cannot drive an agent at all, however
well it writes, and finding that out during a real task wastes the run.

```
checking endpoint... ok
checking tool calling... ok
  called glob with {"pattern":"*.go"}

Ready.
```

## First run

```bash
abhed
```

Type a task. Abhed reads code, runs commands, edits files, and asks before
anything it is not permitted to do unattended.

```
⬢ the tests in pkg/auth are failing — find out why and fix it
```

While it works, **you can keep typing.** A line sent mid-run steers it at the
next step rather than interrupting, so the files it has already read and the
results it has already gathered are kept:

```
⬢ actually, only look at token.go
  steering — applied at the next step
```

A slash command typed mid-run is queued and runs when the turn finishes.

## The prompt

Arrow keys move and recall history; Home, End, Ctrl-A, Ctrl-E, Ctrl-U, Ctrl-K
and Ctrl-W do what they do in a shell. Ctrl-C interrupts the task without ending
the session; Ctrl-D exits.

| Command | |
|---|---|
| `/help` | list commands |
| `/mode <name>` | `default`, `accept-edits`, `plan`, `auto` |
| `/diff` | what changed this session |
| `/undo` | revert the last turn's file changes |
| `/cost` | tokens, cache hit rate, compactions |
| `/compact` | compact the context now |
| `/tree` | the session's steps |
| `/fork <step>` | rebuild the conversation up to a step and continue from there |
| `/export [path]` | write the transcript, HTML by default |
| `/model [name]` | show or switch the model, keeping the conversation |

## Without a terminal

```bash
abhed -p "explain what pkg/auth does" -mode plan
abhed -p "fix the failing tests" -mode auto -allow 'bash(go test*)'
abhed -p "add a test for Valid" -output-format json > events.jsonl
```

Exit codes: `0` completed · `2` turn limit · `3` budget · `4` policy denied ·
`5` retries exhausted · `130` interrupted.

## Next

- [Configuration](02-configuration.md) — the settings that matter
- [Permissions](04-permissions.md) — before you run `-mode auto` on anything real
