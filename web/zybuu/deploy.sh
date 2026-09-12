#!/usr/bin/env bash
# Not the publisher. This file exists only to overwrite what used to be here.
#
# The real publisher lives at deploy/publish-site.sh in the repository, outside
# the directory Cloudflare Pages uploads. It was briefly published from inside
# this directory, and Pages keeps an asset addressable once deployed — removing
# it from the payload did not retract it, and a _redirects rule does not win
# against a static file that exists. Overwriting the path is what works.
#
# Nothing secret was exposed: the script names RESEND_API_KEY but never holds a
# value. This was surface, not a leak.
echo "See deploy/publish-site.sh in the repository." >&2
exit 1
