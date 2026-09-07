#!/usr/bin/env bash
# Start Titan in a container that cannot reach this Mac's filesystem.
#
# Every flag here is load-bearing. The requirement is not "run it in Docker for
# tidiness" — it is that an agent which executes shell commands, reachable from
# the public internet, must not be able to read or write a single file belonging
# to the person running it. That is achieved by giving it nothing to reach:
# no host path is mounted, so there is no host path to escape to.
#
# Read `deploy/README.md` for the threat model this implements.
set -euo pipefail

IMAGE="${TITAN_IMAGE:-titan:local}"
NAME="${TITAN_CONTAINER:-titan}"
VOLUME="${TITAN_VOLUME:-titan-workspace}"
# Accounts live on their own volume rather than in the workspace.
#
# Two reasons, one practical and one structural. Mounting the config file into
# /workspace/.titan makes the engine create that directory owned by root, so the
# unprivileged user cannot write users.json beside it. And the workspace is the
# tree the agent reads and writes: the password database does not belong in the
# one directory the model is pointed at.
STATE_VOLUME="${TITAN_STATE_VOLUME:-titan-state}"
CONFIG="${TITAN_CONFIG:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/config.json}"
# Bound to loopback deliberately: the only route in is the reverse proxy, which
# terminates TLS. Publishing on 0.0.0.0 would put the API on the LAN in the
# clear, behind nothing.
PORT="${TITAN_PORT:-127.0.0.1:8080}"

# podman, not docker: this machine has both half-installed, DOCKER_HOST points
# at a socket the docker CLI cannot reach, and picking one explicitly avoids an
# hour of confusing failures. podman also runs containers inside a
# hardware-virtualised VM on macOS, which is the boundary this design rests on.
RUNTIME="${TITAN_RUNTIME:-podman}"

if ! command -v "$RUNTIME" >/dev/null 2>&1; then
  echo "error: $RUNTIME not found. Install it, or set TITAN_RUNTIME." >&2
  exit 1
fi

# The workspace is a named volume managed by the container engine. This is the
# crux of the whole design: NOT a bind mount of any host directory. A bind mount
# of $HOME or the repo would hand back exactly the access the container exists
# to remove.
for v in "$VOLUME" "$STATE_VOLUME"; do
  if ! "$RUNTIME" volume inspect "$v" >/dev/null 2>&1; then
    echo "creating volume $v"
    "$RUNTIME" volume create "$v" >/dev/null
  fi
done

if [ ! -f "$CONFIG" ]; then
  echo "error: no config at $CONFIG" >&2
  exit 1
fi

"$RUNTIME" rm -f "$NAME" >/dev/null 2>&1 || true

exec "$RUNTIME" run \
  --name "$NAME" \
  --detach \
  --restart unless-stopped \
  \
  `# --- what the process may do -------------------------------------------` \
  --user 10001:10001 \
  `# Drop every capability and add none back. An agent's shell has no business` \
  `# with CAP_NET_ADMIN or CAP_SYS_ADMIN, and dropping them costs nothing.` \
  --cap-drop ALL \
  `# Blocks setuid binaries from raising privilege — the standard escalation` \
  `# path once someone has code execution as an unprivileged user.` \
  --security-opt no-new-privileges \
  \
  `# --- what the process may write ----------------------------------------` \
  `# The image itself is immutable at run time, so a compromise cannot install` \
  `# a backdoor into the binary, drop a startup script, or persist at all` \
  `# outside the two writable mounts below.` \
  --read-only \
  `# Everything genuinely ephemeral is a tmpfs: gone on restart, never on disk,` \
  `# and noexec so a payload written there cannot be run.` \
  --tmpfs /tmp:rw,noexec,nosuid,size=512m \
  `# The two persistent writable surfaces, both volumes the engine owns. No` \
  `# host directory is mounted anywhere: that is the whole point.` \
  --volume "$VOLUME":/workspace:rw \
  `# Accounts and sessions, kept out of the tree the agent reads.` \
  --volume "$STATE_VOLUME":/home/titan/.titan:rw \
  `# The config is the one thing that comes from the host, and it is mounted` \
  `# read-only at the MANAGED path. That is deliberate: config.Load applies` \
  `# /etc/titan/config.json last and sets Managed, which makes bypass mode` \
  `# refusable and the policy non-escalatable from inside the container.` \
  --volume "$CONFIG":/etc/titan/config.json:ro \
  \
  `# --- what the process may consume --------------------------------------` \
  `# A runaway or hostile agent should exhaust its own limits, not the host's.` \
  --memory 2g \
  --memory-swap 2g \
  --pids-limit 512 \
  --cpus 2 \
  \
  `# --- what the process may reach ----------------------------------------` \
  `# Loopback only. The reverse proxy is the sole route in.` \
  --publish "$PORT":8080 \
  \
  --env TITAN_IN_CONTAINER=1 \
  "$IMAGE" "$@"
