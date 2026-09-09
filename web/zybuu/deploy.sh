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
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Publishing $DIR to Cloudflare Pages project '$PROJECT'"
echo

# npx rather than a global install: this runs a handful of times, and pinning
# the version here beats depending on whatever wrangler a machine happens to
# have. The first run opens a browser to authorise — that step cannot be
# automated, and should not be.
npx --yes wrangler@3 pages deploy "$DIR" \
    --project-name "$PROJECT" \
    --commit-dirty=true

cat <<'TEXT'

Done. Two things to check in the Cloudflare dashboard the first time:

  1. Workers & Pages -> zybuu -> Custom domains -> add  zybuu.com  and  www.zybuu.com
     Cloudflare adds the DNS records itself, since it already runs this zone.

  2. Confirm titan.zybuu.com still points at the tunnel and was not touched.

TEXT
