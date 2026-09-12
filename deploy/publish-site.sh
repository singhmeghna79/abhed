#!/usr/bin/env bash
# Publish the zybuu.com homepage to Cloudflare Pages.
#
# The homepage is hosted separately from Titan on purpose. Titan runs on a
# laptop behind a tunnel, so it is up only while that machine is awake; the
# homepage is the thing an investor opens at an arbitrary hour, and it must not
# depend on a lid being open. Pages serves it from Cloudflare's edge for free,
# with no origin to be down.
#
#   zybuu.com        -> Cloudflare Pages   (always up, static)
#   titan.zybuu.com  -> tunnel -> this Mac (the live product)
set -euo pipefail

PROJECT="${ZYBUU_PAGES_PROJECT:-zybuu}"
# The site lives in web/zybuu; this script lives in deploy/. Keeping the
# publisher OUT of the published directory is deliberate: Pages serves every
# file it is handed, and zybuu.com/deploy.sh returned 200 when it sat inside.
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../web/zybuu" && pwd)"

# The access Function is the page's only conversion and its two most important
# behaviours are invisible in a browser: refusing honestly when RESEND_API_KEY
# is unset, and reporting an upstream failure instead of claiming success.
# Checked here so a broken form cannot be published.
# Docs are generated from docs/ at publish time, so the markdown in the repo
# stays the single source and the site cannot drift from it.
# The access Function imports the screening rules, and Pages Functions cannot
# import from outside the functions directory. Copy rather than symlink, so
# what deploys is what was tested.
echo "==> Syncing the screening rules into the Function"
cp "$(dirname "${BASH_SOURCE[0]}")/sitegen/screen.js" \
   "$(dirname "${BASH_SOURCE[0]}")/../web/zybuu/functions/api/_screen.js"
echo

echo "==> Rendering documentation"
python3 "$(dirname "${BASH_SOURCE[0]}")/sitegen/build-docs.py"
echo

if command -v node >/dev/null 2>&1; then
    echo "==> Checking the access Function"
    node "$(dirname "${BASH_SOURCE[0]}")/sitetests/access.test.mjs"
    echo
else
    echo "!!  node not found — publishing without checking the access Function"
    echo
fi

echo "==> Publishing $DIR to Cloudflare Pages project '$PROJECT'"
echo

# npx rather than a global install: this runs a handful of times, and pinning
# the version here beats depending on whatever wrangler a machine happens to
# have. The first run opens a browser to authorise — that step cannot be
# automated, and should not be.
# cd into the site directory first. wrangler resolves functions/ relative to
# the CURRENT WORKING DIRECTORY, not the directory being deployed, so running
# this from the repo root uploaded the page and silently skipped the Function:
# the deploy reported success, and POST /api/access returned 405 in production
# with the form pointing straight at it. From inside the directory the output
# reads "Compiled Worker successfully" and "Uploading Functions bundle" — if
# those two lines are missing, the form is not deployed.
cd "$DIR"
out=$(npm_config_cache="${TMPDIR:-/tmp}/zybuu-npm" npx --yes wrangler@3 pages deploy . \
    --project-name "$PROJECT" \
    --commit-dirty=true 2>&1 | tee /dev/stderr)

# Verified, not assumed. A deploy that skips the Function still reports
# "Deployment complete", so success here is not the same as the form working.
if ! grep -q "Functions bundle" <<<"$out"; then
    echo
    echo "!!  The Functions bundle was NOT uploaded — /api/access will 405 and"
    echo "!!  the access form is broken in production. Nothing else is wrong;"
    echo "!!  re-run this script rather than deploying by hand."
    exit 1
fi

cat <<'TEXT'

Done. Three things to set in the Cloudflare dashboard the first time:

  0. Workers & Pages -> zybuu -> Settings -> Environment variables
     Add RESEND_API_KEY (an API key from resend.com; the free tier is enough).
     Optionally ACCESS_TO to send requests somewhere other than
     support@zybuu.com.

     WITHOUT IT the access form still answers, honestly: every submission
     returns "The form is not connected yet. Email support@zybuu.com." No
     request is lost, but none is emailed either.


  1. Workers & Pages -> zybuu -> Custom domains -> add  zybuu.com  and  www.zybuu.com
     Cloudflare adds the DNS records itself, since it already runs this zone.

  2. Confirm titan.zybuu.com still points at the tunnel and was not touched.

TEXT
