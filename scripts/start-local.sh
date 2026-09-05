#!/usr/bin/env bash
# Start the local Titan stack: Postgres -> Ollama -> Titan server.
# Safe to re-run: every step is idempotent and skips what is already up.
#
#   ./scripts/start-local.sh          # start everything
#   ./scripts/start-local.sh --stop   # stop Titan and Ollama (leaves Postgres)

set -uo pipefail

# Everything lives inside this repo: /tmp is purged by macOS, which is what
# silently destroyed the old /tmp/titan-test workspace.
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="$REPO/.titan-workspace"
LOGDIR="$REPO/.titan-workspace/logs"
ADDR=:8420
PORT=8420
PGDATA=/opt/homebrew/var/postgresql@16

mkdir -p "$LOGDIR"

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$*"; }
warn() { printf '  \033[33mwarn\033[0m %s\n' "$*"; }
die()  { printf '  \033[31mfail\033[0m %s\n' "$*"; exit 1; }

if [[ "${1:-}" == "--stop" ]]; then
  say "Stopping Titan stack"
  pkill -f "titan serve" && ok "titan serve stopped" || warn "titan serve was not running"
  pkill -f "ollama serve" && ok "ollama stopped"      || warn "ollama was not running"
  echo "  (Postgres left running: brew services stop postgresql@16)"
  exit 0
fi

# 1. Postgres ---------------------------------------------------------------
say "1/4  Postgres"
if pg_isready -q; then
  ok "already accepting connections"
else
  # A crash or a killed postmaster can leave a stale lock that blocks startup.
  # Only remove it when no postmaster is actually running.
  if [[ -f "$PGDATA/postmaster.pid" ]] && ! pgrep -qf "[p]ostgres -D"; then
    warn "stale postmaster.pid found (no postgres running) - removing"
    rm -f "$PGDATA/postmaster.pid"
  fi
  brew services restart postgresql@16 >/dev/null 2>&1
  for _ in $(seq 1 15); do pg_isready -q && break; sleep 1; done
  pg_isready -q && ok "started" || die "did not come up - tail /opt/homebrew/var/log/postgresql@16.log"
fi

# 2. Ollama -----------------------------------------------------------------
say "2/4  Ollama"
if curl -sf --max-time 3 http://127.0.0.1:11434/api/version >/dev/null; then
  ok "already listening on :11434"
else
  # Flash attention + a q8 KV cache are what let a 26B model hold a long
  # context in 36 GB. Do not drop them.
  (OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0 \
     nohup ollama serve > "$LOGDIR/ollama.log" 2>&1 &)
  for _ in $(seq 1 20); do
    curl -sf --max-time 2 http://127.0.0.1:11434/api/version >/dev/null && break
    sleep 1
  done
  curl -sf --max-time 3 http://127.0.0.1:11434/api/version >/dev/null \
    && ok "started (log: $LOGDIR/ollama.log)" || die "did not come up - tail $LOGDIR/ollama.log"
fi

# 3. Workspace --------------------------------------------------------------
# The workspace is committed-adjacent (gitignored) inside the repo, so it
# survives reboots. A missing config here means it was deleted by hand.
say "3/4  Workspace $WORKSPACE"
if [[ -f "$WORKSPACE/.titan/config.json" ]]; then
  ok "config present"
else
  warn "no .titan/config.json - scaffolding a new one"
  mkdir -p "$WORKSPACE"
  ( cd "$WORKSPACE" && titan init >/dev/null 2>&1 )
  [[ -f "$WORKSPACE/.titan/config.json" ]] \
    && ok "scaffold written - re-add storage.dsn, model and skills settings" \
    || die "titan init did not produce a config"
fi

# 4. Titan server -----------------------------------------------------------
say "4/4  Titan server"
if lsof -nP -iTCP:$PORT -sTCP:LISTEN >/dev/null 2>&1; then
  if pgrep -qf "titan serve"; then
    ok "already listening on $ADDR"
  else
    die "port $PORT is taken by something that is not titan - lsof -nP -iTCP:$PORT -sTCP:LISTEN"
  fi
else
  ( cd "$WORKSPACE" && nohup titan serve -addr "$ADDR" > "$LOGDIR/titan-serve.log" 2>&1 & )
  for _ in $(seq 1 20); do
    curl -sf --max-time 2 "http://127.0.0.1:$PORT/v1/health" >/dev/null && break
    sleep 1
  done
  curl -sf --max-time 3 "http://127.0.0.1:$PORT/v1/health" >/dev/null \
    && ok "started (log: $LOGDIR/titan-serve.log)" || die "did not come up - tail $LOGDIR/titan-serve.log"
fi

say "Health"
curl -s "http://127.0.0.1:$PORT/v1/health"; echo
echo
echo "  Web UI   http://localhost:$PORT"
echo "  Verify   cd $WORKSPACE && titan doctor"
echo "  Stop     $0 --stop"
