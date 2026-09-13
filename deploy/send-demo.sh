#!/usr/bin/env bash
# Send someone the recorded demo and the deck.
#
#   DEMO_SECRET=… RESEND_API_KEY=… ./deploy/send-demo.sh <email> "<name>" [days]
#
# The demo lives at https://zybuu.com/demo/, behind a Pages Function that only
# answers to a signed, time-limited link (web/zybuu/functions/demo/[[path]].js).
# This script mints that link with the same secret and emails it from
# support@zybuu.com. Nothing is stored here: Resend keeps the record of what
# was sent to whom, and the person asked through the access form or by mail,
# which is the record of who asked.
#
# DEMO_SECRET is read from the environment, or from deploy/.demo-secret, which
# deploy/set-access-email.sh writes when it uploads the same value to Pages.
set -euo pipefail

TO="${1:-}"
NAME="${2:-}"
DAYS="${3:-7}"
FROM="${ACCESS_FROM:-Zybuu <support@zybuu.com>}"
BASE="${DEMO_BASE:-https://zybuu.com/demo/}"

if [ -z "$TO" ] || [ -z "$NAME" ]; then
  echo "usage: $0 <email> \"<name>\" [days]" >&2
  exit 2
fi
# The same check the access form applies: enough to catch a typo before it
# becomes a link mailed to nobody.
if ! printf '%s' "$TO" | grep -Eq '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'; then
  echo "error: '$TO' does not look like an email address" >&2
  exit 2
fi
case "$DAYS" in ''|*[!0-9]*) echo "error: days must be a number" >&2; exit 2;; esac

DIR="$(cd "$(dirname "$0")" && pwd)"
if [ -z "${DEMO_SECRET:-}" ] && [ -f "$DIR/.demo-secret" ]; then
  DEMO_SECRET="$(cat "$DIR/.demo-secret")"
fi
if [ -z "${DEMO_SECRET:-}" ]; then
  echo "error: DEMO_SECRET is not set and $DIR/.demo-secret does not exist (run deploy/set-access-email.sh)" >&2
  exit 1
fi
if [ -z "${RESEND_API_KEY:-}" ]; then
  echo "error: RESEND_API_KEY is not set; nothing was sent" >&2
  exit 1
fi

EXP=$(( $(date +%s) + DAYS * 86400 ))
SIG="$(printf 'demo|%s' "$EXP" | openssl dgst -sha256 -hmac "$DEMO_SECRET" -hex | awk '{print $NF}')"
LINK="${BASE}?t=${EXP}.${SIG}"
UNTIL="$(date -u -r "$EXP" '+%d %b %Y' 2>/dev/null || date -u -d "@$EXP" '+%d %b %Y')"
FIRST="${NAME%% *}"

read -r -d '' TEXT <<EOF || true
Hi ${FIRST},

Here is the recorded Titan demo and the deck you asked for:

  ${LINK}

The link is personal to you and works until ${UNTIL}. It opens a short
narrated walk through zybuu.com, the Titan harness, the console and the
documentation; the deck is a download on the same page.

If anything in it raises a question, reply to this email — it reaches a
person, not a queue.

Yuvraj
Zybuu · zybuu.com
EOF

PAYLOAD="$(python3 - "$FROM" "$TO" "$TEXT" <<'PY'
import json, sys
frm, to, text = sys.argv[1], sys.argv[2], sys.argv[3]
print(json.dumps({"from": frm, "to": [to], "reply_to": "support@zybuu.com",
                  "subject": "Titan — the recorded demo and deck", "text": text}))
PY
)"

HTTP="$(curl -sS -o /tmp/send-demo-resp.json -w '%{http_code}' \
    -X POST https://api.resend.com/emails \
    -H "Authorization: Bearer $RESEND_API_KEY" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD")"
if [ "$HTTP" != "200" ] && [ "$HTTP" != "201" ]; then
  echo "error: Resend answered $HTTP: $(cat /tmp/send-demo-resp.json)" >&2
  exit 1
fi
echo "sent to $TO (expires $UNTIL)"
echo "link: $LINK"
