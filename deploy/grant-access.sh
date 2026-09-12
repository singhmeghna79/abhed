#!/usr/bin/env bash
# Grant someone access to the Titan console, and tell them so.
#
#   ./deploy/grant-access.sh priya@siemens.com "Priya Raman"
#   ./deploy/grant-access.sh priya@siemens.com "Priya Raman" 168   # 7 days
#
# This is the step between a request arriving and a person being able to sign
# in. It is deliberately manual: an invite is a shell on a machine, and the
# decision to hand one out is yours. What it automates is the tedium — minting
# the code, composing the mail, and saying the same true things every time.
#
# It needs an admin session on the console. Sign in once at
# https://titan.zybuu.com and export the cookie, or set TITAN_ADMIN_USER and
# TITAN_ADMIN_PASS and this will sign in for you.
set -euo pipefail

EMAIL="${1:-}"
NAME="${2:-}"
HOURS="${3:-168}"          # 7 days, matching what the site implies
BASE="${TITAN_BASE:-https://titan.zybuu.com}"
CACHE="${TMPDIR:-/tmp}/zybuu-npm"
FROM="${ACCESS_FROM:-Zybuu <support@zybuu.com>}"
REPLY="${ACCESS_TO:-support@zybuu.com}"

if [ -z "$EMAIL" ] || [ -z "$NAME" ]; then
    cat >&2 <<USAGE
usage: $0 <email> <name> [hours]

  hours defaults to 168 (7 days). The invite is single use and the account it
  creates lands with no groups, so the person can use the console and cannot
  administer it or invite anyone else.
USAGE
    exit 2
fi

# --- the admin session -----------------------------------------------------
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"' EXIT

if [ -n "${TITAN_SESSION:-}" ]; then
    printf '%s\tTRUE\t/\tTRUE\t0\ttitan_session\t%s\n' \
        "${BASE#https://}" "$TITAN_SESSION" > "$COOKIE_JAR"
elif [ -n "${TITAN_ADMIN_USER:-}" ] && [ -n "${TITAN_ADMIN_PASS:-}" ]; then
    echo "==> Signing in as $TITAN_ADMIN_USER"
    curl -sS -c "$COOKIE_JAR" -X POST "$BASE/v1/signin" \
        -H 'Content-Type: application/json' \
        -d "{\"username\":\"$TITAN_ADMIN_USER\",\"password\":\"$TITAN_ADMIN_PASS\"}" \
        -o /dev/null
else
    cat >&2 <<'AUTH'
No admin credentials. Either:

    export TITAN_ADMIN_USER=yuvraj TITAN_ADMIN_PASS=...
or  export TITAN_SESSION=<the titan_session cookie from a signed-in browser>

The password is read from the environment rather than prompted for here so it
never lands in shell history.
AUTH
    exit 1
fi

# --- mint ------------------------------------------------------------------
echo "==> Minting an invite valid for ${HOURS}h"
RESP="$(curl -sS -b "$COOKIE_JAR" -X POST "$BASE/v1/admin/invites" \
    -H 'Content-Type: application/json' -d "{\"hours\":$HOURS}")"

CODE="$(printf '%s' "$RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("code",""))' 2>/dev/null || true)"
EXPIRES="$(printf '%s' "$RESP" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("expires_at",""))' 2>/dev/null || true)"

if [ -z "$CODE" ]; then
    echo "!!  No invite code came back. The console said:" >&2
    echo "    $RESP" >&2
    echo "    Usual cause: the session is not an admin, or the console is down." >&2
    exit 1
fi

HUMAN_EXPIRY="$(python3 - "$EXPIRES" <<'PY'
import sys, datetime
try:
    t = datetime.datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00"))
    print(t.strftime("%d %B %Y at %H:%M UTC"))
except Exception:
    print(sys.argv[1])
PY
)"

echo "    code    $CODE"
echo "    expires $HUMAN_EXPIRY"

# --- send ------------------------------------------------------------------
if [ -z "${RESEND_API_KEY:-}" ]; then
    cat <<MANUAL

RESEND_API_KEY is not set here, so nothing was emailed. The invite is real and
usable; send it yourself:

  To:      $EMAIL
  Code:    $CODE
  Expires: $HUMAN_EXPIRY
  Sign in: $BASE

MANUAL
    exit 0
fi

FIRST="${NAME%% *}"
BODY="$(cat <<MAIL
Hi $FIRST,

Here is your access to the Titan console.

  Sign in at:  $BASE
  Invite code: $CODE
  Valid until: $HUMAN_EXPIRY

The code works once. Use it to create your account, then sign in with the
password you choose — the code is not the password and is not needed again.

What the account can do: use Titan Chat, run the agent, read your own
sessions. It cannot administer the console, change policy, or invite anyone
else. Every action the agent takes is recorded and replayable.

What to expect, stated plainly:

  - The console runs on a single machine. It is a trial environment and
    carries no uptime commitment. If it is down, it is down.
  - Your sessions are visible to you. The audit log is visible to us.
  - The agent runs shell commands in a container with no network and no
    access to the host. That is deliberate: it means a mistake is contained,
    and it also means the agent cannot fetch things from the internet.
  - Zybuu holds no SOC 2, ISO 27001 or HIPAA certification.

Documentation: https://titan.zybuu.com/docs

When the code expires, reply to this email and we will issue another. If you
would rather run Titan on your own infrastructure than use the hosted console,
reply and say so — that is a different and better conversation.

— Zybuu
MAIL
)"

echo "==> Emailing $EMAIL"
PAYLOAD="$(python3 - "$FROM" "$EMAIL" "$REPLY" "$NAME" "$BODY" <<'PY'
import json, sys
frm, to, reply, name, body = sys.argv[1:6]
print(json.dumps({
    "from": frm, "to": [to], "reply_to": reply,
    "subject": "Your Titan access",
    "text": body,
}))
PY
)"

HTTP="$(curl -sS -o /tmp/grant-resp.json -w '%{http_code}' \
    -X POST https://api.resend.com/emails \
    -H "Authorization: Bearer $RESEND_API_KEY" \
    -H 'Content-Type: application/json' \
    -d "$PAYLOAD")"

if [ "$HTTP" = "200" ]; then
    echo "    sent."
    echo
    echo "    Record it: $EMAIL granted ${HOURS}h, expires $HUMAN_EXPIRY"
else
    echo "!!  Resend returned $HTTP:" >&2
    cat /tmp/grant-resp.json >&2; echo >&2
    echo "    The invite is still valid — send it by hand:" >&2
    echo "    code $CODE, expires $HUMAN_EXPIRY" >&2
    exit 1
fi
