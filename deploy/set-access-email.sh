#!/usr/bin/env bash
# Connect the homepage access form to a mailbox.
#
# Run this yourself: it prompts for the API key and hands it straight to
# Cloudflare. The key is never written to a file, never echoed, and never
# passes through the repository.
#
#   ./deploy/set-access-email.sh
#
# Get a key at https://resend.com — the free tier (3,000 emails/month) is more
# than an access form will ever use. Until this is set the form refuses
# honestly: "The form is not connected yet. Email support@zybuu.com."
set -euo pipefail

PROJECT="${ZYBUU_PAGES_PROJECT:-zybuu}"
CACHE="${TMPDIR:-/tmp}/zybuu-npm"

echo "==> Connecting the access form for Pages project '$PROJECT'"
echo
echo "    1. Sign in at https://resend.com"
echo "    2. Verify the domain zybuu.com (Resend shows the DNS records to add"
echo "       at GoDaddy — this is what lets mail be sent AS @zybuu.com)"
echo "    3. Create an API key, then paste it below."
echo
echo "    Skipping step 2 still works: mail is then sent from Resend's shared"
echo "    onboarding address instead, which is fine for a handful of requests"
echo "    but is likelier to be filtered."
echo

# --- the key ---------------------------------------------------------------
# Piped into wrangler on stdin rather than passed as an argument: an argument
# is visible in `ps` to every other process on the machine while it runs.
printf 'Resend API key (input hidden): '
read -rs KEY
echo
[ -n "$KEY" ] || { echo "No key entered; nothing was changed." >&2; exit 1; }

printf '%s' "$KEY" | npm_config_cache="$CACHE" npx --yes wrangler@3 \
    pages secret put RESEND_API_KEY --project-name "$PROJECT"
unset KEY

# --- where requests land ---------------------------------------------------
printf 'Deliver requests to [support@zybuu.com]: '
read -r TO
TO="${TO:-support@zybuu.com}"
printf '%s' "$TO" | npm_config_cache="$CACHE" npx --yes wrangler@3 \
    pages secret put ACCESS_TO --project-name "$PROJECT"

# --- the From: address -----------------------------------------------------
# Checked rather than asked blindly. Sending as the domain before Resend's
# records resolve is worse than not doing it: the SPF record ends in -all and
# DMARC is p=quarantine, so an unverified send gets quarantined instead of
# merely looking generic.
echo
DOM="${TO#*@}"
if dig +short TXT "resend._domainkey.$DOM" | grep -q .; then
    SUGGEST="Zybuu <support@$DOM>"
    echo "$DOM is verified with Resend, so mail can be sent as your own domain."
else
    SUGGEST=""
    echo "$DOM is NOT yet verified with Resend (no resend._domainkey record)."
    echo "Leave this blank for now — mail keeps going out from Resend's shared"
    echo "sender, which is generic but delivers. To send as support@$DOM, add"
    echo "the records Resend shows, then:"
    echo
    echo "    ./deploy/sitetests/check-mail-dns.sh   # confirm they resolve"
    echo "    ./deploy/set-access-email.sh           # and answer this prompt"
fi
echo
printf 'Send mail from [%s]: ' "${SUGGEST:-onboarding@resend.dev}"
read -r FROM
FROM="${FROM:-$SUGGEST}"
if [ -n "$FROM" ]; then
    printf '%s' "$FROM" | npm_config_cache="$CACHE" npx --yes wrangler@3 \
        pages secret put ACCESS_FROM --project-name "$PROJECT"
fi

cat <<TEXT

Done. Secrets take effect on the next deployment, so publish once:

    ./deploy/publish-site.sh

Then verify end to end — this sends a real email to $TO:

    ./deploy/sitetests/check-access-live.sh

TEXT
