#!/usr/bin/env bash
# Publish the zybuu.com homepage to Cloudflare Pages.
#
# The homepage is hosted separately from Abhed on purpose. Abhed runs on a
# laptop behind a tunnel, so it is up only while that machine is awake; the
# homepage is the thing an investor opens at an arbitrary hour, and it must not
# depend on a lid being open. Pages serves it from Cloudflare's edge for free,
# with no origin to be down.
#
#   zybuu.com        -> Cloudflare Pages   (always up, static)
#   abhed.zybuu.com  -> tunnel -> this Mac (the live product)
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

# The shared stylesheet is inlined into both marketing pages; a page whose
# copy has drifted from deploy/sitegen/site.css fails here, not in a browser.
echo "==> Inlining the shared stylesheet"
python3 "$(dirname "${BASH_SOURCE[0]}")/sitegen/inline-css.py"
python3 "$(dirname "${BASH_SOURCE[0]}")/sitegen/inline-css.py" --check
echo

# The demo is published with the site, behind the signed-link gate. Warn when
# the artefacts are missing rather than fail: the site is publishable without
# them, and /demo/ answers 404 behind the gate until they exist.
MEDIA_DIR="$(dirname "${BASH_SOURCE[0]}")/../web/zybuu/media"
if [ ! -f "$MEDIA_DIR/zybuu-announcement.mp4" ]; then
    echo "!!  web/zybuu/media/zybuu-announcement.mp4 missing (the homepage film; copy it from web/zybuu/demo/series/01-zybuu.mp4)"
    echo
fi
DEMO_DIR="$(dirname "${BASH_SOURCE[0]}")/../web/zybuu/demo"
if [ ! -f "$DEMO_DIR/abhed-demo.mp4" ] || [ ! -f "$DEMO_DIR/abhed-deck.pptx" ]; then
    echo "!!  demo artefacts missing in web/zybuu/demo (run deploy/demo/build.sh); /demo/ will 404"
    echo
fi

if command -v node >/dev/null 2>&1; then
    echo "==> Checking the access Function"
    node "$(dirname "${BASH_SOURCE[0]}")/sitetests/access.test.mjs"
    echo
    echo "==> Checking the demo gate"
    node "$(dirname "${BASH_SOURCE[0]}")/sitetests/demo.test.mjs"
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
# Pages routes a deploy to Production only when the branch name given (or
# inferred from git) matches the project's production branch, which was set
# when the project was created from the public-deploy branch. Every other
# name lands in Preview, silently, with a *.pages.dev URL and no change to
# zybuu.com — which is what happened after the trunk was renamed to main.
# So the branch is passed explicitly, and production is checked afterwards.
out=$(npm_config_cache="${TMPDIR:-/tmp}/zybuu-npm" npx --yes wrangler@3 pages deploy . \
    --project-name "$PROJECT" \
    --branch "${ZYBUU_PRODUCTION_BRANCH:-public-deploy}" \
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

# Verify what was published, against the live site, by exercising the path a
# visitor actually takes. Every regression on this form shipped because it was
# checked some other way: the CSS by reading it, the status handler by typing
# the fragment into the URL bar. Neither submits the form, and the form was
# what was broken.
if command -v curl >/dev/null 2>&1; then
    echo
    echo "==> Verifying the published form"
    sleep 6
    LOC="$(curl -sD- -o /dev/null --max-time 25 -X POST "https://zybuu.com/api/access" \
        -H 'Referer: https://zybuu.com/abhed/' \
        -d 'name=Publish check&email=noreply@zybuu.com' 2>/dev/null \
        | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')"
    case "$LOC" in
        https://zybuu.com/abhed/#access-ok)
            echo "    ok    submits and returns to /abhed/#access-ok" ;;
        https://zybuu.com/#access*)
            echo "    FAIL  returns to the homepage, which has no form and no"
            echo "          status handler - the visitor would be told nothing."
            exit 1 ;;
        "")
            echo "    FAIL  no redirect at all; the Function may not be deployed"
            exit 1 ;;
        *)
            echo "    FAIL  unexpected landing: $LOC"
            exit 1 ;;
    esac
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

  2. Confirm abhed.zybuu.com still points at the tunnel and was not touched.

TEXT

# Verified, not assumed, part two: production must now serve what was just
# uploaded. robots.txt is small, static and changes rarely, so it is compared
# byte for byte; a mismatch means the deploy went to Preview or is cached.
ok=0
for _ in $(seq 1 12); do
    if diff -q <(curl -sS --max-time 20 "https://zybuu.com/robots.txt") "$DIR/robots.txt" >/dev/null; then ok=1; break; fi
    sleep 5
done
if [ "$ok" != 1 ]; then
    echo
    echo "!!  zybuu.com does not serve the robots.txt just deployed. The deploy"
    echo "!!  most likely landed in the Preview environment: check that"
    echo "!!  ZYBUU_PRODUCTION_BRANCH matches the project's production branch."
    exit 1
fi
echo "production verified: zybuu.com serves the deployed files"
