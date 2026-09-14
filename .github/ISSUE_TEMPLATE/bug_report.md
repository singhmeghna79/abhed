---
name: Bug report
about: Report something in Titan that doesn't work as documented
title: ""
labels: bug
assignees: ""
---

**Do not use this template for a security vulnerability.** See
[SECURITY.md](../../SECURITY.md) instead — report to security@zybuu.com
(fallback: support@zybuu.com), not as a public issue.

## What happened

A clear description of the bug.

## What you expected

What the documentation, a comment, or reasonable behavior led you to expect
instead.

## How to reproduce

Steps, ideally as commands:

```bash
go build -o titan ./cmd/titan
./titan ...
```

Include your `.titan/config.json` (with secrets redacted) if the bug is
config-dependent.

## Environment

- Titan commit or version: `git rev-parse HEAD`
- Go version: `go version`
- OS/architecture:
- Storage driver (`memory` or `postgres`):
- Auth mode (`none`, `local`, `proxy`, `oidc`):

## Relevant output

```
paste logs, error messages, or `titan doctor` output here
```

## Additional context

Anything else that would help — a failing test you wrote, a linked PR, a
related issue.
