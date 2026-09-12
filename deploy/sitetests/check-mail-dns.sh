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

echo "==> Mail DNS for $DOMAIN"

# 1. Receiving must keep working. This is the one that breaks things: the
#    GoDaddy MX is what delivers mail TO the mailbox the form writes to.
mx=$(dig +short MX "$DOMAIN" | tr '\n' ' ')
case "$mx" in
    *secureserver.net*) say pass "inbound MX intact" "$mx" ;;
    "")                 say fail "inbound MX intact" "NO MX — mail to @$DOMAIN is undeliverable" ;;
    *)                  say fail "inbound MX intact" "unexpected: $mx" ;;
esac

# 2. DKIM: Resend's signing key.
dkim=$(dig +short TXT "resend._domainkey.$DOMAIN" | head -1)
if [ -n "$dkim" ]; then say pass "Resend DKIM" "present"
else say fail "Resend DKIM" "missing — add the TXT record Resend shows"; fi

# 3. Exactly one SPF record. Two is the classic and silent failure: RFC 7208
#    says a domain publishing multiple v=spf1 records is a permerror, which
#    means SPF fails for ALL mail from the domain, not just the new sender.
spfcount=$(dig +short TXT "$DOMAIN" | grep -ci 'v=spf1')
if [ "$spfcount" -gt 1 ]; then
    say fail "one SPF record" "$spfcount found — two v=spf1 records fail SPF entirely; merge them into one"
fi

# SPF must list Resend, and must still list the existing provider.
spf=$(dig +short TXT "$DOMAIN" | grep -i 'v=spf1' | head -1)
if [ -z "$spf" ]; then
    say fail "SPF" "no SPF record at all"
else
    case "$spf" in
        *resend*)
            case "$spf" in
                *secureserver*) say pass "SPF lists both" "$spf" ;;
                *) say fail "SPF" "lists Resend but DROPPED secureserver — inbound provider's sending will now fail" ;;
            esac ;;
        *) say fail "SPF lists Resend" "not listed: $spf" ;;
    esac
fi

# 4. Bounce handling. Resend puts an MX on a subdomain; absence is not fatal
#    but bounces are then invisible, which is how a dead address goes unnoticed.
bounce=$(dig +short MX "send.$DOMAIN" | head -1)
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
