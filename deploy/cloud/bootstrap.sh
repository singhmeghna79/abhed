#!/usr/bin/env bash
# Bring a fresh Ubuntu 24.04 host (arm64 or amd64) up as the Abhed console.
#
#   sudo ABHED_REF=main ./bootstrap.sh
#   curl -fsSL https://raw.githubusercontent.com/zybuu-ai/abhed/main/deploy/cloud/bootstrap.sh | sudo bash
#
# Idempotent: every step checks before it acts, so running it again after a
# failure, a reboot, or a new release (ABHED_REF=v0.2.0) is the upgrade path.
# It installs podman, cloudflared and git; clones the repository; builds the
# image natively for whichever architecture this is; starts the deployment
# through deploy/run.sh — the same script the Mac uses, with the same
# container flags, volumes and database roles — installs cloudflared as a
# system service on the existing tunnel; and schedules the nightly backup
# from docs/trust/backup-restore.md. Then it prints what to check.
#
# What it deliberately does not do: touch DNS or the Cloudflare account
# (the tunnel already exists and its hostnames already point at it; only the
# credentials file moves, and the operator copies that by hand), open any
# inbound port (cloudflared dials out; nothing listens beyond loopback and
# ssh), or write a secret it did not generate.
#
# podman rather than docker, and rootless. deploy/run.sh uses podman-only
# subcommands (`network exists`, `container exists`) and deploy/config.json
# names `host.containers.internal`, so docker would mean patching both for
# no gain; Ubuntu 24.04's podman 4.9 is a stable LTS package. Rootless
# because a cloud host has no VM around the container the way the Mac does:
# the engine running as an ordinary user is the boundary that replaces it.
# Restart-on-boot, the usual weak spot of rootless podman, is handled below
# with lingering and podman-restart.service.
#
# Settings, all optional:
#   ABHED_REF                git ref to deploy (default: main)
#   ABHED_REPO               clone URL (default: https://github.com/zybuu-ai/abhed.git)
#   ABHED_DEPLOY_KEY         path to an ssh private key for a private repo; switches the clone to ssh
#   ABHED_USER               service account that owns the containers (default: abhed)
#   ABHED_TUNNEL_ID          Cloudflare tunnel id (default: the zybuu tunnel)
#   ABHED_TUNNEL_CREDENTIALS path to the tunnel's <id>.json copied from the Mac
#                            (default: /etc/cloudflared/<id>.json, if already there)
#   ABHED_HOSTNAMES          hostnames the tunnel answers (default: "abhed.zybuu.com titan.zybuu.com")
#   ABHED_RESTART=1          re-run deploy/run.sh even when nothing changed
set -euo pipefail

REF="${ABHED_REF:-main}"
REPO="${ABHED_REPO:-https://github.com/zybuu-ai/abhed.git}"
DEPLOY_KEY="${ABHED_DEPLOY_KEY:-}"
SVC_USER="${ABHED_USER:-abhed}"
TUNNEL_ID="${ABHED_TUNNEL_ID:-6eae1767-91e8-453f-8766-a329c64a63a1}"
HOSTNAMES="${ABHED_HOSTNAMES:-abhed.zybuu.com titan.zybuu.com}"
CF_DIR=/etc/cloudflared
TUNNEL_CREDENTIALS="${ABHED_TUNNEL_CREDENTIALS:-$CF_DIR/$TUNNEL_ID.json}"
RESTART="${ABHED_RESTART:-0}"

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
note() { printf '    %s\n' "$1"; }

[ "$(id -u)" -eq 0 ] || { echo "error: run as root (sudo)" >&2; exit 1; }

# ------------------------------------------------------------------ 0. host
step "Host"
. /etc/os-release
if [ "${ID:-}" != ubuntu ] || [ "${VERSION_ID:-}" != 24.04 ]; then
  note "warning: written for Ubuntu 24.04; this is ${PRETTY_NAME:-unknown}. Continuing."
fi
ARCH="$(dpkg --print-architecture)"
note "architecture $ARCH — the image is built here, for this architecture"

MEM_MB="$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)"
note "memory ${MEM_MB} MB"
# The image build (a Go compile plus a pip install of matplotlib) peaks well
# above what a 4 GB host has free. Swap is only a safety net for the build;
# the running deployment stays under the container's own 2 GB limit.
if [ "$MEM_MB" -lt 6000 ] && [ "$(swapon --show --noheadings | wc -l)" -eq 0 ]; then
  note "under 6 GB and no swap: adding a 2 GB swapfile for the image build"
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

# -------------------------------------------------------------- 1. packages
step "Packages"
export DEBIAN_FRONTEND=noninteractive
need=()
for p in podman uidmap slirp4netns passt git curl openssl ca-certificates gnupg; do
  dpkg -s "$p" >/dev/null 2>&1 || need+=("$p")
done
if [ "${#need[@]}" -gt 0 ]; then
  note "installing: ${need[*]}"
  apt-get update -qq
  apt-get install -y -qq "${need[@]}"
else
  note "podman, git and friends already installed"
fi
note "podman $(podman --version | awk '{print $3}')"

if ! command -v cloudflared >/dev/null 2>&1; then
  note "installing cloudflared from pkg.cloudflare.com"
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg -o /usr/share/keyrings/cloudflare-main.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main" \
    > /etc/apt/sources.list.d/cloudflared.list
  apt-get update -qq
  apt-get install -y -qq cloudflared
fi
note "cloudflared $(cloudflared --version 2>/dev/null | awk '{print $3}')"

# ------------------------------------------------------------ 2. the account
step "Service account $SVC_USER"
if ! id "$SVC_USER" >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash "$SVC_USER"
  note "created"
fi
SVC_UID="$(id -u "$SVC_USER")"
SVC_HOME="$(getent passwd "$SVC_USER" | cut -d: -f6)"
# Rootless podman maps container uids onto a range of subordinate ids. Ubuntu
# allocates one at useradd; an account created some other way may lack it.
grep -q "^$SVC_USER:" /etc/subuid || usermod --add-subuids 100000-165535 "$SVC_USER"
grep -q "^$SVC_USER:" /etc/subgid || usermod --add-subgids 100000-165535 "$SVC_USER"

# run.sh limits the container's memory, cpu and pids. Rootless podman can
# only apply those if systemd delegates the controllers to the user's cgroup
# subtree; by default it delegates pids and memory but not cpu.
DELEGATE=/etc/systemd/system/user@.service.d/abhed-delegate.conf
if [ ! -f "$DELEGATE" ]; then
  install -d -m 0755 "$(dirname "$DELEGATE")"
  printf '[Service]\nDelegate=cpu cpuset io memory pids\n' > "$DELEGATE"
  systemctl daemon-reload
  note "cgroup delegation enabled for user services"
fi

# Lingering starts the account's systemd user manager at boot without a
# login, which is what lets its containers and timers come back on their own.
loginctl enable-linger "$SVC_USER"
for _ in $(seq 1 30); do
  [ -S "/run/user/$SVC_UID/bus" ] && break
  sleep 1
done
[ -S "/run/user/$SVC_UID/bus" ] || { echo "error: user manager for $SVC_USER did not start" >&2; exit 1; }

as_user() {
  runuser -u "$SVC_USER" -- env \
    HOME="$SVC_HOME" \
    XDG_RUNTIME_DIR="/run/user/$SVC_UID" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$SVC_UID/bus" \
    "$@"
}
# The same thing as a command, for the operator: `sudo -u abhed podman ps`
# shows nothing, because rootless podman and `systemctl --user` need the
# account's runtime directory and bus, which sudo does not set.
cat > /usr/local/bin/abhed-as <<HELPER
#!/bin/sh
# Run a command as the $SVC_USER account with its user manager reachable.
#   abhed-as podman ps
#   abhed-as systemctl --user list-timers
[ "\$(id -u)" -eq 0 ] && exec runuser -u $SVC_USER -- env HOME=$SVC_HOME XDG_RUNTIME_DIR=/run/user/$SVC_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$SVC_UID/bus "\$@"
exec sudo runuser -u $SVC_USER -- env HOME=$SVC_HOME XDG_RUNTIME_DIR=/run/user/$SVC_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$SVC_UID/bus "\$@"
HELPER
chmod 755 /usr/local/bin/abhed-as

# Containers with --restart unless-stopped only restart on boot if something
# asks podman to start them; this user unit is that something.
if as_user systemctl --user list-unit-files podman-restart.service 2>/dev/null | grep -q podman-restart; then
  as_user systemctl --user enable --now podman-restart.service >/dev/null 2>&1 || true
  note "podman-restart.service enabled (containers return after a reboot)"
else
  note "warning: podman-restart.service not shipped by this podman; containers will not return after a reboot"
fi

# ------------------------------------------------------------ 3. repository
step "Repository at $REF"
SRC="$SVC_HOME/abhed"
if [ -n "$DEPLOY_KEY" ]; then
  # A private repository: the operator copied a read-only deploy key here.
  install -d -m 0700 -o "$SVC_USER" -g "$SVC_USER" "$SVC_HOME/.ssh"
  install -m 0600 -o "$SVC_USER" -g "$SVC_USER" "$DEPLOY_KEY" "$SVC_HOME/.ssh/abhed-deploy"
  if ! grep -q 'abhed-deploy' "$SVC_HOME/.ssh/config" 2>/dev/null; then
    printf 'Host github.com\n  IdentityFile ~/.ssh/abhed-deploy\n  IdentitiesOnly yes\n' >> "$SVC_HOME/.ssh/config"
    chown "$SVC_USER:$SVC_USER" "$SVC_HOME/.ssh/config"; chmod 600 "$SVC_HOME/.ssh/config"
  fi
  as_user ssh-keyscan -t ed25519 github.com 2>/dev/null > "$SVC_HOME/.ssh/known_hosts.tmp" \
    && as_user sh -c "cat ~/.ssh/known_hosts.tmp >> ~/.ssh/known_hosts && sort -u -o ~/.ssh/known_hosts ~/.ssh/known_hosts && rm ~/.ssh/known_hosts.tmp"
  case "$REPO" in
    https://github.com/*) REPO="git@github.com:${REPO#https://github.com/}" ;;
  esac
fi
if [ ! -d "$SRC/.git" ]; then
  as_user git clone --quiet "$REPO" "$SRC"
  note "cloned $REPO"
else
  as_user git -C "$SRC" fetch --quiet --tags origin
fi
as_user git -C "$SRC" checkout --quiet --detach "$(as_user git -C "$SRC" rev-parse --verify "origin/$REF^{commit}" 2>/dev/null || as_user git -C "$SRC" rev-parse --verify "$REF^{commit}")"
COMMIT="$(as_user git -C "$SRC" rev-parse HEAD)"
note "at ${COMMIT:0:12}"

# ---------------------------------------------------------------- 4. config
# The running config lives outside the clone so `git fetch` never has to
# merge over it, and so the database passwords run.sh generates beside it
# stay out of the working tree. The repository's deploy/config.json is the
# reference; this copy is what runs. Seeded once, never overwritten.
step "Config"
CONF_DIR="$SVC_HOME/abhed-config"
install -d -m 0700 -o "$SVC_USER" -g "$SVC_USER" "$CONF_DIR"
if [ ! -f "$CONF_DIR/config.json" ]; then
  install -m 0600 -o "$SVC_USER" -g "$SVC_USER" "$SRC/deploy/config.json" "$CONF_DIR/config.json"
  note "seeded $CONF_DIR/config.json from deploy/config.json"
  note "this host has no local model: set model.default to a hosted provider"
  note "and put its key in $CONF_DIR/.env (mode 600) — see deploy/cloud/migrate-from-mac.md"
elif ! diff -q "$SRC/deploy/config.json" "$CONF_DIR/config.json" >/dev/null; then
  note "$CONF_DIR/config.json differs from the repository's deploy/config.json:"
  diff "$SRC/deploy/config.json" "$CONF_DIR/config.json" | sed 's/^/      /' || true
  note "(expected for model settings; review anything else)"
fi

# ----------------------------------------------------------------- 5. image
step "Image abhed:local"
BUILT="$(as_user podman image inspect -f '{{index .Config.Labels "abhed.commit"}}' abhed:local 2>/dev/null || true)"
REBUILT=0
if [ "$BUILT" != "$COMMIT" ]; then
  note "building for $ARCH at ${COMMIT:0:12} (the first build takes several minutes)"
  as_user podman build --quiet --label "abhed.commit=$COMMIT" -t abhed:local -f "$SRC/Dockerfile" "$SRC" >/dev/null
  REBUILT=1
else
  note "already built from ${COMMIT:0:12}"
fi

# ------------------------------------------------------------------ 6. run
step "Deployment"
RUNNING="$(as_user podman inspect -f '{{.State.Running}}' abhed 2>/dev/null || echo false)"
if [ "$REBUILT" = 1 ] || [ "$RUNNING" != true ] || [ "$RESTART" = 1 ]; then
  as_user env ABHED_CONFIG="$CONF_DIR/config.json" "$SRC/deploy/run.sh" | sed 's/^/    /'
else
  note "abhed is running from the current image; nothing to do (ABHED_RESTART=1 forces it)"
fi

# ------------------------------------------------------------ 7. cloudflared
# The same tunnel the Mac uses, so DNS does not change: its CNAME already
# points at this tunnel id. Only the credentials file travels, by hand. The
# ingress rules mirror deploy/tunnel.sh step 3 exactly; tunnel.sh itself is
# not reused here because it needs the account certificate (cert.pem) from a
# browser login, and a server should hold the tunnel's credentials only.
step "cloudflared"
install -d -m 0700 "$CF_DIR"
if [ -f "$TUNNEL_CREDENTIALS" ] && [ "$TUNNEL_CREDENTIALS" != "$CF_DIR/$TUNNEL_ID.json" ]; then
  install -m 0600 -o root -g root "$TUNNEL_CREDENTIALS" "$CF_DIR/$TUNNEL_ID.json"
fi
{
  echo "# Generated by deploy/cloud/bootstrap.sh — the ingress mirrors deploy/tunnel.sh"
  echo "tunnel: $TUNNEL_ID"
  echo "credentials-file: $CF_DIR/$TUNNEL_ID.json"
  echo "ingress:"
  for h in $HOSTNAMES; do
    cat <<RULE
  - hostname: $h
    service: http://127.0.0.1:8080
    originRequest:
      # Server-sent events are the console's transport; an agent run is long.
      disableChunkedEncoding: false
      connectTimeout: 30s
      tcpKeepAlive: 30s
RULE
  done
  echo "  - service: http_status:404"
} > "$CF_DIR/config.yml.new"
if ! diff -q "$CF_DIR/config.yml.new" "$CF_DIR/config.yml" >/dev/null 2>&1; then
  mv "$CF_DIR/config.yml.new" "$CF_DIR/config.yml"; CF_CHANGED=1
else
  rm "$CF_DIR/config.yml.new"; CF_CHANGED=0
fi
chmod 600 "$CF_DIR/config.yml"

if [ -f "$CF_DIR/$TUNNEL_ID.json" ]; then
  chmod 600 "$CF_DIR/$TUNNEL_ID.json"
  if [ ! -f /etc/systemd/system/cloudflared.service ]; then
    cloudflared --config "$CF_DIR/config.yml" service install >/dev/null 2>&1
    note "installed cloudflared.service"
  fi
  systemctl enable cloudflared >/dev/null 2>&1 || true
  if [ "$CF_CHANGED" = 1 ] || ! systemctl is-active --quiet cloudflared; then
    systemctl restart cloudflared
  fi
  note "cloudflared: $(systemctl is-active cloudflared) on tunnel $TUNNEL_ID"
  note "if the Mac's connector is still running, both serve this hostname — stop"
  note "the Mac's before relying on this one (deploy/cloud/migrate-from-mac.md)"
else
  note "no credentials at $CF_DIR/$TUNNEL_ID.json — the tunnel is NOT started."
  note "copy it from the Mac and run this script again:"
  note "  scp ~/.cloudflared/$TUNNEL_ID.json <host>:/tmp/ && sudo install -m 600 /tmp/$TUNNEL_ID.json $CF_DIR/"
fi

# ---------------------------------------------------------------- 8. backup
step "Nightly backup"
UNIT_DIR="$SVC_HOME/.config/systemd/user"
install -d -m 0755 -o "$SVC_USER" -g "$SVC_USER" "$UNIT_DIR"
install -m 0644 -o "$SVC_USER" -g "$SVC_USER" "$SRC/deploy/cloud/systemd/abhed-backup.service" "$UNIT_DIR/"
install -m 0644 -o "$SVC_USER" -g "$SVC_USER" "$SRC/deploy/cloud/systemd/abhed-backup.timer" "$UNIT_DIR/"
as_user systemctl --user daemon-reload
as_user systemctl --user enable --now abhed-backup.timer >/dev/null 2>&1
note "$(as_user systemctl --user list-timers abhed-backup.timer --no-legend | awk '{print "next run", $1, $2, $3}')"
note "backups land in $SVC_HOME/backups; set ABHED_BACKUP_RCLONE_REMOTE in the unit for an off-host copy"

# ---------------------------------------------------------------- 9. checks
step "Checks to run"
cat <<TEXT
    On this host (abhed-as runs a command as $SVC_USER with its podman visible):
      abhed-as podman ps                                # abhed and abhed-db, both Up
      curl -s http://127.0.0.1:8080/v1/health           # {"status":"ok"}
      abhed-as podman logs --tail 20 abhed
      systemctl status cloudflared --no-pager           # active (running), 4 connections registered
      journalctl -u cloudflared -n 20 --no-pager
      abhed-as systemctl --user list-timers             # abhed-backup.timer scheduled
      abhed-as $SRC/deploy/cloud/backup.sh              # one backup now, to prove it works
      sudo reboot                                       # once: everything above must be true again afterwards

    From anywhere else (the model is deploy/cutover-abhed.sh):
      curl -sS -o /dev/null -w '%{http_code}\n' https://abhed.zybuu.com/            # 200
      curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' https://titan.zybuu.com/console   # 301 → abhed
      curl -sS -o /dev/null -w '%{http_code}\n' https://abhed.zybuu.com/docs/       # 200 (the docs worker, not this host)
      curl -s https://abhed.zybuu.com/v1/health                                     # {"status":"ok"}
    Then sign in with an invited account and run one turn.
TEXT
