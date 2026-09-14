# MCP

Abhed is an MCP client and gateway. A tool exposed by a Model Context Protocol
server appears in the model's tool list like any built-in one.

```json
"mcp": {
  "servers": [
    { "name": "github", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" } },
    { "name": "corpus", "url": "https://retrieval.internal/mcp",
      "headers_env": { "Authorization": "CORPUS_TOKEN" } }
  ]
}
```

Both transports are supported: **stdio** for a local process, **HTTP** for a
remote service.

## Namespacing and policy

Remote tools are namespaced by server, so two servers offering `search` do not
collide, and a policy rule can name one precisely:

```json
"deny": ["github__create_issue"]
```

Every MCP tool routes through the policy engine, because Abhed cannot know what
someone else's tool does. A remote tool is subject to approval exactly as `bash`
is, and its result is recorded and tagged untrusted.

## Failure

A server that will not start is reported and skipped; the agent runs without it
rather than refusing to start. Tool lists are fetched lazily on first use, so an
unreachable server costs nothing until something needs it.
