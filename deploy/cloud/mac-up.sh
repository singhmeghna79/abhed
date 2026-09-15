#!/usr/bin/env bash
# Make sure the podman machine and the two containers are up on the Mac.
#
# Run by the com.zybuu.abhed.containers LaunchAgent (deploy/cloud/launchd) at
# login and every two minutes after that. It does nothing when everything is
# already running, which is the normal case, so the interval costs nothing.
# When the machine is stopped — after a reboot, or after Podman Desktop was
# quit — it starts it and then runs deploy/run.sh only if a container is
# missing, because run.sh always recreates the abhed container and a needless
# restart would drop every signed-in session.
#
# Safe to run by hand: ./deploy/cloud/mac-up.sh
set -uo pipefail
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

REPO="${ABHED_REPO_DIR:-$HOME/abhed}"
MACHINE="${ABHED_PODMAN_MACHINE:-podman-machine-default}"

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }

state="$(podman machine inspect --format '{{.State}}' "$MACHINE" 2>/dev/null || echo missing)"
case "$state" in
  running) ;;
  missing)
    log "podman machine '$MACHINE' does not exist; nothing to start"
    exit 1 ;;
  *)
    log "podman machine is '$state'; starting it"
    if ! podman machine start "$MACHINE" >/dev/null 2>&1; then
      log "podman machine start failed; will retry on the next run"
      exit 1
    fi ;;
esac

for _ in $(seq 1 60); do
  podman info >/dev/null 2>&1 && break
  sleep 2
done
podman info >/dev/null 2>&1 || { log "podman is not answering"; exit 1; }

up() { [ "$(podman inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }
if up abhed && up abhed-db; then
  exit 0
fi

log "abhed or abhed-db is not running; running deploy/run.sh"
"$REPO/deploy/run.sh" 2>&1 | sed 's/^/  /'

for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8080/v1/health >/dev/null 2>&1; then
    log "healthy"
    exit 0
  fi
  sleep 2
done
log "started, but /v1/health is not answering yet"
exit 1
