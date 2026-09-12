#!/usr/bin/env bash
# Is zybuu.com ready to send as support@zybuu.com?
#
# Run this after adding Resend's DNS records at GoDaddy and before switching
# ACCESS_FROM. Sending as the domain before these resolve is worse than not
# doing it: the SPF record ends in -all and DMARC is p=quarantine, so an
# unverified send is actively quarantined rather than merely looking generic.
set -uo pipefail

DOMAIN="${1:-zybuu.com}"
ok=0; bad=0

say() {
    if [ "$1" = pass ]; then printf '  ok    %-26s %s\n' "$2" "$3"; ok=$((ok+1))
    else printf '  FAIL  %-26s %s\n' "$2" "$3"; bad=$((bad+1)); fi
}

# Ask a public resolver, and retry once against a second one before believing a
# negative. A local resolver under a burst of queries returns an empty answer
# that is indistinguishable from "the record does not exist" — which reported a
# correctly configured domain as broken, the worst way for a check like this to
# be wrong.
q() {
    local type="$1" name="$2" out
    for server in 1.1.1.1 8.8.8.8; do
        out=$(dig @"$server" +short "$type" "$name" 2>/dev/null)
        [ -n "$out" ] && { printf '%s' "$out"; return 0; }
    done
    return 1
}

echo "==> Mail DNS for $DOMAIN"

# 1. Receiving must keep working. This is the one that breaks things: the
#    GoDaddy MX is what delivers mail TO the mailbox the form writes to.
mx=$(q MX "$DOMAIN" | tr '\n' ' ')
case "$mx" in
    *secureserver.net*) say pass "inbound MX intact" "$mx" ;;
    "")                 say fail "inbound MX intact" "NO MX — mail to @$DOMAIN is undeliverable" ;;
    *)                  say fail "inbound MX intact" "unexpected: $mx" ;;
esac

# 2. DKIM: Resend's signing key.
dkim=$(q TXT "resend._domainkey.$DOMAIN" | head -1)
if [ -n "$dkim" ]; then say pass "Resend DKIM" "present"
else say fail "Resend DKIM" "missing — add the TXT record Resend shows"; fi

# 3. Exactly one SPF record. Two is the classic and silent failure: RFC 7208
#    says a domain publishing multiple v=spf1 records is a permerror, which
#    means SPF fails for ALL mail from the domain, not just the new sender.
spfcount=$(q TXT "$DOMAIN" | grep -ci 'v=spf1')
if [ "$spfcount" -gt 1 ]; then
    say fail "one SPF record" "$spfcount found — two v=spf1 records fail SPF entirely; merge them into one"
fi

# The root SPF must survive untouched. Resend's Domain Connect flow puts its
# SPF on the send subdomain, a separate scope, so the root should still read
# exactly as it did before — anything else means something edited it.
spf=$(q TXT "$DOMAIN" | grep -i 'v=spf1' | head -1)
if [ -z "$spf" ]; then
    say fail "root SPF" "no SPF record at all"
elif ! printf '%s' "$spf" | grep -qi secureserver; then
    say fail "root SPF" "no longer lists secureserver — the inbound provider's own sending will now fail: $spf"
else
    say pass "root SPF intact" "$spf"
fi

# The sending scope: Resend signs as send.<domain>, so this is the SPF that
# has to name Amazon SES (Resend sends through it).
sendspf=$(q TXT "send.$DOMAIN" | grep -i 'v=spf1' | head -1)
if printf '%s' "$sendspf" | grep -qi 'amazonses\|resend'; then
    say pass "send SPF" "$sendspf"
else
    say fail "send SPF" "missing at send.$DOMAIN — authorize the records in Resend"
fi

# 4. Bounce handling. Resend puts an MX on a subdomain; absence is not fatal
#    but bounces are then invisible, which is how a dead address goes unnoticed.
bounce=$(q MX "send.$DOMAIN" | head -1)
if [ -n "$bounce" ]; then say pass "bounce MX" "$bounce"
else printf '  note  %-26s %s\n' "bounce MX" "absent — bounces will not be reported"; fi

echo
if [ "$bad" -eq 0 ]; then
    cat <<TEXT
Ready. Point the form at the domain:

    ./deploy/set-access-email.sh     # third prompt: Zybuu <support@$DOMAIN>
    ./deploy/publish-site.sh
    ./deploy/sitetests/check-access-live.sh

TEXT
else
    cat <<TEXT
Not ready — $bad check(s) failed. Add the missing records at GoDaddy first.
DNS can take a few minutes to propagate; re-run this until it is clean.

Leave ACCESS_FROM unset until then. Mail keeps going out from Resend's
shared sender, which is generic but delivers.

TEXT
fi
exit $bad
