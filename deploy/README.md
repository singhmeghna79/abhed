# Deploying Titan on a public domain

This directory runs Titan at `https://zybuu.com` from a Mac on a home
connection, under one hard requirement: **the agent must never be able to read
or write a file belonging to the person running it.**

Everything here follows from that.

## Why a container, and not just a config setting

Titan has a sandbox. It applies to `bash` and nothing else.

`internal/tools/bash.go:28` carries a `Sandbox` hook. The file tools do not:

| Tool | What it actually calls |
|---|---|
| `read` | `os.ReadFile` — `internal/tools/file.go:71` |
| `write` | `os.CreateTemp` + `os.Rename` — `file.go:246,267` |
| `edit` | `os.ReadFile` — `edit.go:89` |
| `grep` | `os.ReadFile` — `search.go:317` |

So `sandbox.min_tier: container` sandboxes the shell and leaves every file tool
running as the operator, in the operator's process, with the operator's access.
The only thing constraining them is `tools.Session.Resolve`
(`internal/tools/session.go:157`) — a path check in Go, in the same process as
the agent. That is a good check. It is not a boundary: it holds only while the
process behaves as written.

Putting the **whole process** in a container moves the boundary into the kernel.
`os.ReadFile` cannot open a path that does not exist in the namespace. `Resolve`
still runs, and is now the second line rather than the only one.

## What the isolation is actually worth

On macOS, podman runs containers inside a hardware-virtualised Linux VM. The
host filesystem is not permission-denied, it is **unaddressable** — there is no
path that names it. Verified against the built image:

```
$ ls /Users                          → No such file or directory
$ cat /Users/.../.ssh/id_rsa         → No such file or directory
$ echo x > /Users/.../pwned.txt      → Directory nonexistent
$ echo x > /usr/local/bin/evil       → Read-only file system
$ id                                 → uid=10001(titan)  # not root
$ mount | grep workspace             → /dev/vda4  # a disk in the VM
```

The only writable surfaces are `/workspace` (a volume the container engine owns)
and two tmpfs mounts that vanish on restart.

## `sandbox.min_tier: none` — why that is correct here

It reads like the security setting has been switched off. It has not; the
boundary moved outward.

Inside the container there is no nested container runtime and no `bubblewrap`,
so any tier above `none` makes `sandbox.Select` fail and **Titan refuses to
start** — it never silently downgrades. Verified:

```
titan: no sandbox backend meets the required minimum tier "process".
```

The alternative is nested containers, which needs the podman socket mounted into
the container. That hands the agent control of the container engine, which is
root on the VM — strictly worse than what it replaces. **Rejected deliberately.**

The honest statement of the trade-off: bash inside the container is unsandboxed
*relative to that container*. The container is disposable, holds nothing of
yours, and cannot see macOS.

## Layers

```
internet
  │
  ├─ router :80 :443 ──────────► 192.168.1.7
  │                                │
  │  Caddy — TLS, HSTS, headers    │  port 80 exists only for the
  │  Let's Encrypt via HTTP-01     │  ACME challenge
  │                                ▼
  │                          127.0.0.1:8080   ← loopback only, never the LAN
  │                                │
  └─ container ────────────────────┘
       uid 10001 · cap-drop ALL · no-new-privileges
       read-only rootfs · 2g memory · 512 pids
       /workspace = a volume.  No host path is mounted.
```

## Running it

```bash
podman build -t titan:local -f Dockerfile .   # from the repo root
./deploy/run.sh                                # starts the container
caddy run --config deploy/Caddyfile            # TLS edge (needs :80 and :443)
```

Create the first account — there is no signup on a public deployment:

```bash
podman exec -it titan titan user add <name>
```

## What you must do by hand

These cannot be automated from this machine.

1. **Router** at `http://192.168.1.1`: forward TCP **80** and **443** to
   `192.168.1.7`. Port 80 is required — HTTP-01 is the only challenge available
   (see below) and it will not work without it.
2. **Reserve** `192.168.1.7` as a static DHCP lease, or the forward breaks when
   the address moves.
3. **GoDaddy DNS**: `A @ → 171.76.80.224` and `A www → 171.76.80.224`, TTL 600.
4. **Turn the VPN off.** An active tunnel (`utun4`) routes around the forward.

### Why HTTP-01 and not DNS-01

GoDaddy revoked DNS API access in May 2024 for accounts below roughly 10-50
domains. With one domain the `caddy-dns/godaddy` plugin cannot authenticate, so
the DNS challenge is unavailable regardless of configuration. HTTP-01 needs
inbound port 80 instead.

If your ISP blocks inbound 80/443 — some residential Indian ISPs do — HTTP-01
cannot complete and neither can any other local-only certificate path. That is a
hard stop, not a tuning problem.

## Residual risks

Stated plainly, because a deployment guide that only lists strengths is not
useful.

- **Dynamic IP.** Residential addresses rotate. When yours does, `zybuu.com`
  points at someone else's connection until the A record is updated.
- **Uptime.** The site is up only while this Mac is awake, unlocked and online.
- **Container escape.** The residual risk. A VM boundary is strong, not
  infinite. Nothing of yours is mounted, which is what bounds the damage.
- **Prompt injection is contained, not prevented.** The agent still runs a model
  that executes commands. `allow_network: false` denies it an exfiltration
  channel; enabling egress for a hosted model gives one back.
- **`storage.driver: memory`.** Sessions do not survive a restart. Postgres
  means reaching the host at `host.containers.internal`, which re-adds a host
  dependency — a deliberate trade, and the reason it is not the default here.
- Per the project README, **a human red-team engagement remains outstanding and
  is not substitutable** by the adversarial suite.
