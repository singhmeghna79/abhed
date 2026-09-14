#!/usr/bin/env bash
# Verify the live access form end to end.
#
# The local suite proves the Function's logic; only this proves the deployed
# one has a working key and that mail actually leaves. It submits a real
# request, so it lands in the destination mailbox — that arrival IS the test.
set -euo pipefail

SITE="${1:-https://zybuu.com}"
echo "==> Checking the access form at $SITE"

frag() {
    curl -s -o /dev/null -w '%{redirect_url}' -X POST "$SITE/api/access" "$@" \
        | sed 's/.*#//'
}

fail=0
check() {
    if [ "$2" = "$3" ]; then
        printf '  ok    %-34s %s\n' "$1" "$2"
    else
        printf '  FAIL  %-34s got %s, want %s\n' "$1" "$2" "$3"; fail=1
    fi
}

check "missing fields refused"  "$(frag -d 'name=T')"                        access-missing
check "bad address refused"     "$(frag -d 'name=T&email=nope')"             access-email
check "bot field absorbed"      "$(frag -d 'name=T&email=t@e.co&website=x')" access-ok

# The real one. A configured form answers ok; an unconfigured one says so.
got=$(frag -d "name=Deployment check&email=noreply@zybuu.com&company=Zybuu&use=Automated check from check-access-live.sh")
case "$got" in
  access-ok)
    printf '  ok    %-34s %s\n' "delivered" "$got"
    echo
    echo "  Resend accepted and queued the message. That is a real signal, not"
    echo "  a guess: a sandbox restriction or an unverified sending domain"
    echo "  comes back non-2xx and this would have said access-failed."
    echo
    echo "  What it still does not prove is arrival. Open the destination"
    echo "  mailbox and look for:"
    echo
    echo "      Abhed access request - Deployment check (Zybuu)"
    echo
    echo "  If it is not there within a minute or two, check the Resend"
    echo "  dashboard's Emails tab — it shows delivered, bounced or blocked"
    echo "  per message, which is the only place the truth is recorded."
    ;;
  access-unconfigured)
    printf '  FAIL  %-34s %s\n' "delivered" "$got"
    echo
    echo "  RESEND_API_KEY is not set on the deployment. Run:"
    echo "      ./deploy/set-access-email.sh && ./deploy/publish-site.sh"
    fail=1
    ;;
  access-failed)
    printf '  FAIL  %-34s %s\n' "delivered" "$got"
    echo
    echo "  The key is set but Resend refused. Usual causes: the key was"
    echo "  revoked, or ACCESS_FROM uses a domain not verified with Resend."
    fail=1
    ;;
  *)
    printf '  FAIL  %-34s %s\n' "delivered" "${got:-<no redirect>}"; fail=1 ;;
esac

exit $fail
