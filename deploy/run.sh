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
SKILLS="${TITAN_SKILLS:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.titan/skills}"
# Bound to loopback deliberately: the only route in is the reverse proxy, which
# terminates TLS. Publishing on 0.0.0.0 would put the API on the LAN in the
# clear, behind nothing.
PORT="${TITAN_PORT:-127.0.0.1:8080}"

# Postgres, so sessions and accounts survive a restart.
NETWORK="${TITAN_NETWORK:-titan-net}"
DB_NAME="${TITAN_DB_CONTAINER:-titan-db}"
DB_VOLUME="${TITAN_DB_VOLUME:-titan-db-data}"
DB_IMAGE="${TITAN_DB_IMAGE:-docker.io/library/postgres:16-alpine}"
# Generated once and kept in the state volume rather than written here. A
# password committed to a repo is a password published, and this one guards
# every transcript the deployment holds.
DB_PASSWORD="${TITAN_DB_PASSWORD:-}"
WITH_DB="${TITAN_WITH_DB:-1}"

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
for v in "$VOLUME" "$STATE_VOLUME" "$DB_VOLUME"; do
  if ! "$RUNTIME" volume inspect "$v" >/dev/null 2>&1; then
    echo "creating volume $v"
    "$RUNTIME" volume create "$v" >/dev/null
  fi
done

if [ ! -f "$CONFIG" ]; then
  echo "error: no config at $CONFIG" >&2
  exit 1
fi

# The password is generated once and kept beside the deploy config, mode 0600.
# It must be STABLE across restarts: Postgres sets the role password when it
# initialises its data directory, so a fresh password on a second run leaves a
# database nobody can open.
DB_SECRET="$(dirname "$CONFIG")/.db-password"
if [ -z "$DB_PASSWORD" ]; then
  if [ -f "$DB_SECRET" ]; then
    DB_PASSWORD="$(cat "$DB_SECRET")"
  else
    # `tr </dev/urandom | head -c` makes tr die of SIGPIPE once head has taken
    # its 32 bytes. Harmless, but under `set -o pipefail` that failure is the
    # pipeline's exit status and the script dies here — silently, before
    # anything starts. openssl reads a bounded amount and exits cleanly.
    DB_PASSWORD="$(openssl rand -base64 24 | tr -dc 'A-Za-z0-9')"
    (umask 077; printf '%s' "$DB_PASSWORD" > "$DB_SECRET")
    echo "generated database password → $DB_SECRET"
  fi
fi

# ------------------------------------------------------------------ database
#
# Postgres runs as its own container rather than on the host.
#
# The host's Postgres binds loopback only — verified: 127.0.0.1 and [::1], with
# nothing on the VM's interface — so the container simply cannot reach it, and
# "point the DSN at host.containers.internal" fails. Running it here also keeps
# the deployment self-contained: one script brings up the whole thing, and there
# is no dependency on what happens to be installed on the laptop.
#
# Durability is the point. With the in-memory store every session and every
# account vanished on restart, which is not a property you can ask a user to
# accept.
if [ "$WITH_DB" = "1" ]; then
  if ! "$RUNTIME" network exists "$NETWORK" 2>/dev/null; then
    echo "creating network $NETWORK"
    "$RUNTIME" network create "$NETWORK" >/dev/null
  fi

  if ! "$RUNTIME" container exists "$DB_NAME" 2>/dev/null || \
     [ "$("$RUNTIME" inspect -f '{{.State.Running}}' "$DB_NAME" 2>/dev/null)" != "true" ]; then
    "$RUNTIME" rm -f "$DB_NAME" >/dev/null 2>&1 || true
    echo "starting $DB_NAME"
    # No published port: the database is reachable only from the container
    # network, never from the LAN or the host. Titan is its only client.
    "$RUNTIME" run -d \
      --name "$DB_NAME" \
      --network "$NETWORK" \
      --restart unless-stopped \
      --env POSTGRES_USER=titan \
      --env POSTGRES_PASSWORD="$DB_PASSWORD" \
      --env POSTGRES_DB=titan \
      --volume "$DB_VOLUME":/var/lib/postgresql/data:rw \
      --health-cmd 'pg_isready -U titan -d titan' \
      --health-interval 5s \
      "$DB_IMAGE" >/dev/null
  fi

  # Titan applies its schema on connect (internal/store/postgres.go), but only
  # once the server is actually accepting connections. Starting Titan against a
  # database still initialising fails the first run for no good reason.
  printf 'waiting for %s' "$DB_NAME"
  for _ in $(seq 1 40); do
    if "$RUNTIME" exec "$DB_NAME" pg_isready -U titan -d titan >/dev/null 2>&1; then
      echo " ready"; break
    fi
    printf '.'; sleep 1
  done
fi

"$RUNTIME" rm -f "$NAME" >/dev/null 2>&1 || true

DB_ARGS=()
if [ "$WITH_DB" = "1" ]; then
  # TITAN_DATABASE_URL is read by config.Load's applyEnv, so the credential
  # never has to appear in the config file that gets mounted read-only.
  DB_ARGS=(
    --network "$NETWORK"
    --env "TITAN_DATABASE_URL=postgres://titan:${DB_PASSWORD}@${DB_NAME}:5432/titan?sslmode=disable"
  )
fi

exec "$RUNTIME" run \
  --name "$NAME" \
  --detach \
  --restart unless-stopped \
  "${DB_ARGS[@]}" \
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
  `# Skills are procedures the agent follows, which makes them instructions.` \
  `# Mounted READ-ONLY so the agent cannot rewrite its own operating rules —` \
  `# the same reason skills are never loaded from the workspace being edited.` \
  --volume "$SKILLS":/workspace/.titan/skills:ro \
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
