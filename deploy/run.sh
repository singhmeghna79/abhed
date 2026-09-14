#!/usr/bin/env bash
# Start Abhed in a container that cannot reach this Mac's filesystem.
#
# Every flag here is load-bearing. The requirement is not "run it in Docker for
# tidiness" — it is that an agent which executes shell commands, reachable from
# the public internet, must not be able to read or write a single file belonging
# to the person running it. That is achieved by giving it nothing to reach:
# no host path is mounted, so there is no host path to escape to.
#
# Read `deploy/README.md` for the threat model this implements.
set -euo pipefail

IMAGE="${ABHED_IMAGE:-abhed:local}"
NAME="${ABHED_CONTAINER:-abhed}"
VOLUME="${ABHED_VOLUME:-abhed-workspace}"
# Accounts live on their own volume rather than in the workspace.
#
# Two reasons, one practical and one structural. Mounting the config file into
# /workspace/.abhed makes the engine create that directory owned by root, so the
# unprivileged user cannot write users.json beside it. And the workspace is the
# tree the agent reads and writes: the password database does not belong in the
# one directory the model is pointed at.
STATE_VOLUME="${ABHED_STATE_VOLUME:-abhed-state}"
CONFIG="${ABHED_CONFIG:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/config.json}"
SKILLS="${ABHED_SKILLS:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.abhed/skills}"
# Bound to loopback deliberately: the only route in is the reverse proxy, which
# terminates TLS. Publishing on 0.0.0.0 would put the API on the LAN in the
# clear, behind nothing.
PORT="${ABHED_PORT:-127.0.0.1:8080}"

# Postgres, so sessions and accounts survive a restart.
NETWORK="${ABHED_NETWORK:-abhed-net}"
DB_NAME="${ABHED_DB_CONTAINER:-abhed-db}"
DB_VOLUME="${ABHED_DB_VOLUME:-abhed-db-data}"
DB_IMAGE="${ABHED_DB_IMAGE:-docker.io/library/postgres:16-alpine}"
# Generated once and kept in the state volume rather than written here. A
# password committed to a repo is a password published, and this one guards
# every transcript the deployment holds.
DB_PASSWORD="${ABHED_DB_PASSWORD:-}"
# Two roles. `abhed_admin` is the cluster superuser and is used only by this
# script to provision; `abhed` is the plain role the server connects as. The
# split is not ceremony: Postgres does not apply row-level security to a
# superuser, so a Abhed connected as one has every isolation policy in the
# schema and none of the isolation. The server refuses to start that way.
DB_ADMIN_PASSWORD="${ABHED_DB_ADMIN_PASSWORD:-}"
WITH_DB="${ABHED_WITH_DB:-1}"

# podman, not docker: this machine has both half-installed, DOCKER_HOST points
# at a socket the docker CLI cannot reach, and picking one explicitly avoids an
# hour of confusing failures. podman also runs containers inside a
# hardware-virtualised VM on macOS, which is the boundary this design rests on.
RUNTIME="${ABHED_RUNTIME:-podman}"

if ! command -v "$RUNTIME" >/dev/null 2>&1; then
  echo "error: $RUNTIME not found. Install it, or set ABHED_RUNTIME." >&2
  exit 1
fi

# The workspace is a named volume managed by the container engine. This is the
# crux of the whole design: NOT a bind mount of any host directory. A bind mount
# of $HOME or the repo would hand back exactly the access the container exists
# to remove.
# The product was renamed from Titan. A machine that ran the old deployment
# has its data in volumes with the old names; carry it over once, by copy, so
# the new volumes start with everything the old ones held and the old ones
# stay untouched until someone chooses to remove them. The old containers are
# stopped first: a database data directory is only safe to copy at rest.
"$RUNTIME" rm -f titan titan-db >/dev/null 2>&1 || true
for pair in "titan-workspace:$VOLUME" "titan-state:$STATE_VOLUME" "titan-db-data:$DB_VOLUME"; do
  old="${pair%%:*}"; new="${pair##*:}"
  if "$RUNTIME" volume inspect "$old" >/dev/null 2>&1 && ! "$RUNTIME" volume inspect "$new" >/dev/null 2>&1; then
    echo "migrating volume $old → $new"
    "$RUNTIME" volume create "$new" >/dev/null
    "$RUNTIME" run --rm -v "$old:/from:ro" -v "$new:/to" "$DB_IMAGE" sh -c 'cp -a /from/. /to/'
  fi
done

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
DB_ADMIN_SECRET="$(dirname "$CONFIG")/.db-admin-password"
if [ -z "$DB_ADMIN_PASSWORD" ]; then
  if [ -f "$DB_ADMIN_SECRET" ]; then
    DB_ADMIN_PASSWORD="$(cat "$DB_ADMIN_SECRET")"
  else
    DB_ADMIN_PASSWORD="$(openssl rand -base64 24 | tr -dc 'A-Za-z0-9')"
    (umask 077; printf '%s' "$DB_ADMIN_PASSWORD" > "$DB_ADMIN_SECRET")
    echo "generated database admin password → $DB_ADMIN_SECRET"
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
    # network, never from the LAN or the host. Abhed is its only client.
    "$RUNTIME" run -d \
      --name "$DB_NAME" \
      --network "$NETWORK" \
      --restart unless-stopped \
      --env POSTGRES_USER=abhed_admin \
      --env POSTGRES_PASSWORD="$DB_ADMIN_PASSWORD" \
      --env POSTGRES_DB=abhed \
      --volume "$DB_VOLUME":/var/lib/postgresql/data:rw \
      --health-cmd 'pg_isready -U abhed -d abhed' \
      --health-interval 5s \
      "$DB_IMAGE" >/dev/null
  fi

  # Abhed applies its schema on connect (internal/store/postgres.go), but only
  # once the server is actually accepting connections. Starting Abhed against a
  # database still initialising fails the first run for no good reason.
  printf 'waiting for %s' "$DB_NAME"
  for _ in $(seq 1 40); do
    if "$RUNTIME" exec "$DB_NAME" pg_isready -U abhed -d abhed >/dev/null 2>&1; then
      echo " ready"; break
    fi
    printf '.'; sleep 1
  done

  # Provision the plain role the server connects as: `abhed_app`, no
  # privileges beyond owning its tables. Idempotent, every start.
  #
  # A data directory initialised by an earlier version of this script has
  # `abhed` as its bootstrap superuser, and Postgres will not let the bootstrap
  # user be demoted. So that role keeps its superuser bit, becomes the admin
  # role for that deployment, and gets the admin password; a new plain role
  # takes over ownership of everything in the database and the server
  # connects as that. Existing sessions and accounts are untouched — only the
  # privilege that was quietly voiding row-level security is out of the DSN.
  db_sql() { "$RUNTIME" exec -i "$DB_NAME" psql -v ON_ERROR_STOP=1 -q -U "$1" -d abhed; }
  db_can() { "$RUNTIME" exec "$DB_NAME" psql -U "$1" -d abhed -c 'SELECT 1' >/dev/null 2>&1; }
  # A data directory carried over from the pre-rename deployment still holds
  # the old role and database names. Rename them once, as the bootstrap
  # superuser the old deployment created, from the maintenance database so
  # our own connection does not block the rename. SCRAM passwords survive a
  # role rename, so the app password file stays valid.
  db_can_pg() { "$RUNTIME" exec "$DB_NAME" psql -U "$1" -d postgres -c 'SELECT 1' >/dev/null 2>&1; }
  if ! db_can_pg abhed_admin && ! db_can_pg abhed && db_can_pg titan; then
    echo "migrating $DB_NAME: renaming the pre-rename roles and database"
    "$RUNTIME" exec -i "$DB_NAME" psql -v ON_ERROR_STOP=1 -q -U titan -d postgres <<SQL
DO \$\$ BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'titan_app') AND NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'abhed_app') THEN
    ALTER ROLE titan_app RENAME TO abhed_app;
  END IF;
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'titan_admin') AND NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'abhed_admin') THEN
    ALTER ROLE titan_admin RENAME TO abhed_admin;
  END IF;
END \$\$;
SQL
    if ! "$RUNTIME" exec "$DB_NAME" psql -U titan -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='abhed'" | grep -q 1; then
      "$RUNTIME" exec "$DB_NAME" psql -v ON_ERROR_STOP=1 -q -U titan -d postgres -c "ALTER DATABASE titan RENAME TO abhed"
    fi
  fi
  if db_can abhed_admin; then
    DB_ADMIN_ROLE=abhed_admin
  elif db_can abhed; then
    DB_ADMIN_ROLE=abhed
    echo "migrating $DB_NAME: 'abhed' stays the bootstrap superuser; Abhed will connect as 'abhed_app'"
    db_sql abhed <<SQL
ALTER ROLE abhed PASSWORD '${DB_ADMIN_PASSWORD}';
SQL
  else
    echo "error: cannot open $DB_NAME as abhed_admin or abhed" >&2
    exit 1
  fi
  db_sql "$DB_ADMIN_ROLE" <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'abhed_app') THEN CREATE ROLE abhed_app LOGIN; END IF;
END \$\$;
ALTER ROLE abhed_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB PASSWORD '${DB_PASSWORD}';
ALTER DATABASE abhed OWNER TO abhed_app;
DO \$\$ BEGIN
  IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'users') THEN
    UPDATE users SET groups = replace(groups, 'titan-admin', 'abhed-admin') WHERE groups LIKE '%titan-admin%';
  END IF;
END \$\$;
ALTER SCHEMA public OWNER TO abhed_app;
DO \$\$ DECLARE r record; BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER TABLE public.%I OWNER TO abhed_app', r.tablename);
  END LOOP;
  FOR r IN SELECT sequencename FROM pg_sequences WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER SEQUENCE public.%I OWNER TO abhed_app', r.sequencename);
  END LOOP;
  FOR r IN SELECT p.proname, pg_get_function_identity_arguments(p.oid) AS args
           FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' LOOP
    EXECUTE format('ALTER FUNCTION public.%I(%s) OWNER TO abhed_app', r.proname, r.args);
  END LOOP;
END \$\$;
SQL
  if [ "$("$RUNTIME" exec "$DB_NAME" psql -U "$DB_ADMIN_ROLE" -d abhed -tAc "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = 'abhed_app'" 2>/dev/null)" != "f" ]; then
    echo "error: the abhed_app database role is privileged; refusing to start Abhed against it" >&2
    exit 1
  fi
fi

"$RUNTIME" rm -f "$NAME" >/dev/null 2>&1 || true

DB_ARGS=()
if [ "$WITH_DB" = "1" ]; then
  # ABHED_DATABASE_URL is read by config.Load's applyEnv, so the credential
  # never has to appear in the config file that gets mounted read-only.
  DB_ARGS=(
    --network "$NETWORK"
    --env "ABHED_DATABASE_URL=postgres://abhed_app:${DB_PASSWORD}@${DB_NAME}:5432/abhed?sslmode=disable"
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
  --volume "$STATE_VOLUME":/home/abhed/.abhed:rw \
  `# The config is the one thing that comes from the host, and it is mounted` \
  `# read-only at the MANAGED path. That is deliberate: config.Load applies` \
  `# /etc/abhed/config.json last and sets Managed, which makes bypass mode` \
  `# refusable and the policy non-escalatable from inside the container.` \
  --volume "$CONFIG":/etc/abhed/config.json:ro \
  `# Skills are procedures the agent follows, which makes them instructions.` \
  `# Mounted READ-ONLY so the agent cannot rewrite its own operating rules —` \
  `# the same reason skills are never loaded from the workspace being edited.` \
  --volume "$SKILLS":/workspace/.abhed/skills:ro \
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
  --env ABHED_IN_CONTAINER=1 \
  "$IMAGE" "$@"
