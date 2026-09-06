# Titan — Clusters, Machines, and External Retrieval

Four capabilities that let Titan work on infrastructure rather than only on
files. All are **off by default**: reaching a cluster, a VM, or a corpus is an
authorization decision, and credentials already sitting on the machine are not
a reason to hand them to an agent unasked.

## Kubernetes

```json
{
  "k8s": {
    "enabled": true,
    "context": "prod-eu",
    "namespace": "payments",
    "allow_writes": false
  }
}
```

Two tools, deliberately not one:

| Tool | Approval | What it does |
|---|---|---|
| `k8s_get` | never prompts | list, describe, pod logs |
| `k8s_apply` | **always prompts** | apply, delete, scale, restart |

Splitting them is what makes the safety real. Titan's permission engine decides
by tool name; a single tool with a `verb` argument would force it to parse an
opaque string to tell `list pods` from `delete namespace`. As separate tools the
read path is genuinely non-mutating, and every write goes through approval by
construction rather than by correctly interpreting text.

`allow_writes` decides whether `k8s_apply` exists at all. Even with it on, there
is no permission mode that auto-approves a cluster write: a mistaken delete in
production is not recoverable the way a file edit is.

### Logging in during a conversation

A cluster token expires, and a stale one in a kubeconfig produces a 401 that
reads like a permissions problem. When that happens, paste the login:

```
oc login --token=sha256~... --server=https://api.cluster.example.com:6443
```

The agent calls `k8s_login`, which asks for approval once and then holds the
credential **in memory for that Titan process only**. It is never written to
your kubeconfig, the event store, or a log — a token pasted into a chat should
not become a durable artifact of that chat.

**Do not expect `oc login` through bash to work.** Three separate things stop
it, and the combination produced a confusing failure in practice:

1. The sandbox denies reads of `~/.kube`, so `oc` cannot read or write the
   kubeconfig at all.
2. Each bash call is a fresh sandboxed process, so a login inside one would not
   survive to the next.
3. Approving the command approves *running* it — the sandbox denial is a
   separate layer that approval does not lift.

`bash` now says so when a command fails on a denied credential path, and names
the tool to use instead, rather than leaving the agent to conclude the file is
simply unreadable.

For a non-interactive deployment, `TITAN_K8S_TOKEN` overrides the kubeconfig
credential at startup.

**Credentials come from your kubeconfig, never from the model.** The agent picks
a cluster only by naming a context you already have, so the worst it can reach
is what your own `kubectl` can. Token, tokenFile, client certificates and `exec`
credential helpers (the cloud CLIs) all work; exec tokens are refreshed before
they expire, because an expired token returns a 401 that reads like a
permissions problem.

Titan talks to the API directly rather than importing `client-go`, which would
add roughly a hundred transitive dependencies to a bundle where each one is
something an operator has to accept.

## SSH

```json
{
  "ssh": {
    "enabled": true,
    "hosts": [
      { "name": "build1", "addr": "10.0.0.5", "user": "ci",
        "identity_file": "~/.ssh/id_ed25519" },
      { "name": "db1", "addr": "db.internal:2222", "user": "ops",
        "password_env": "DB1_PASSWORD" }
    ]
  }
}
```

### Connecting to a machine during a conversation

Hosts do not have to be in config. When the user gives an address and a key:

```
connect to 52.116.120.159, key is at ~/Downloads/id_rsa
```

the agent calls `ssh_connect`, which asks for approval once, verifies the
connection works, and registers the host for the life of the process. Nothing
is written to `~/.ssh/config`.

`ssh.enabled` is all that is required — the `hosts` list is optional. Requiring
a pre-declared host to reach the tool that declares hosts was a real bug: a user
with a VM and a key had no way in, and the agent fell back to `ssh` through
bash, where the sandbox denies the key read.

The key path is resolved from what the user typed. `~Dowloads/key (1).prv` —
missing slash, misspelled directory, space in the name — resolves correctly,
because a path pasted into a chat is approximate and sending the agent hunting
with `glob` through denied directories is worse than trying the obvious places.

A host whose key is not in `known_hosts` is refused, and the refusal says to
retry with `accept_host_key: true` **only if the user has said the host is new
or ephemeral**. That is a deliberate line: the agent should not decide on its
own to stop verifying the identity of a machine it is about to run commands on.

**Every remote command requires approval — there is no read-only classification.**
A local `bash` call can be judged by its text because it runs inside a sandbox
with a workspace boundary and a checkpoint behind it. None of that holds over
SSH: the command runs with the remote account's full authority, outside any
scoping, with no undo. Calling `cat` safe there would be judging the string
rather than the consequence.

The agent can only name a host from this list. It cannot introduce one, so the
blast radius is the operator's decision.

Credentials, in order of preference: the **SSH agent** (the key never leaves
it), then `identity_file`, then `password_env` — which names an environment
variable, never the password itself. A passphrase-protected key says so and
tells you to `ssh-add` it, rather than failing with a parse error.

Host keys are verified against `known_hosts`. `insecure_skip_host_key_check`
exists because ephemeral lab VMs have no stable key and refusing would push
people to run `ssh` through bash where Titan sees nothing — but it is off by
default, and `titan doctor` marks any host using it.

## External retrieval (RAG)

```json
{
  "rag": { "corpora": [
    { "name": "runbooks", "enabled": true,
      "description": "Operational runbooks and incident history.",
      "url": "https://rag.internal/v1/search",
      "headers_env": { "Authorization": "RAG_TOKEN" },
      "results_path": "results" }
  ]}
}
```

Each corpus becomes a `rag_<name>` tool: namespaced so none can shadow a native
tool, read-only so it never prompts.

There are no per-vendor clients. Every retrieval API is the same shape
underneath and differs only in field names, so dotted paths map any of them:

```json
{ "results_path": "hits.hits", "text_field": "_source.body",
  "title_field": "_source.title", "score_field": "_score" }
```

That is Elasticsearch. With **no** mapping at all, common field names
(`text`, `content`, `passage`, `source`, `title`, `score`) are inferred, so a
simple endpoint needs no configuration.

For a GET-style endpoint use `query_param` instead of `query_field`. For fixed
parameters — an index name, a collection — use `body`.

**Retrieved passages are untrusted.** An internal wiki page is not more
trustworthy than the internet just because it sits behind a firewall: whoever
wrote the indexed document chose its words. Passages are tagged untrusted like
any other tool output and must never be followed as instructions.

## Remote MCP servers

```json
{
  "mcp": { "servers": [
    { "name": "corpus", "enabled": true,
      "url": "https://retrieval.internal/mcp",
      "headers_env": { "Authorization": "CORPUS_TOKEN" } }
  ]}
}
```

`url` reaches a server that already runs; `command` spawns one locally. Exactly
one of the two — a server is either spawned or reached, and leaving which one
wins to chance is a configuration trap.

Both wire shapes are supported, because a deployment does not get to choose
which one its vendor implemented: **Streamable HTTP** (one endpoint, POST, reply
as JSON or SSE by content type) and the older **HTTP+SSE** (a long-lived GET
carrying replies, with a separate POST endpoint announced by an `endpoint`
event). Titan probes for the legacy shape and falls back, rather than making you
declare it.

## Verifying

`titan doctor` reports every one of these, resolves the kubeconfig context, and
names any SSH host with host key checking disabled:

```
kubernetes  reads + writes (every write needs approval)
            context prod-eu
            namespace payments · server https://api.prod.example:6443
ssh host    build1 → ci@10.0.0.5
rag corpus  runbooks → https://rag.internal/v1/search
```
