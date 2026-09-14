#!/usr/bin/env bash
# The one-time cutover from titan.zybuu.com to abhed.zybuu.com.
#
#   ./deploy/cutover-abhed.sh
#
# Everything else about the rename is already in the repository and in the
# running deployment: the binary, image, containers, volumes and database
# roles carry the new name, the tunnel config lists both hostnames, and the
# docs worker answers on abhed.zybuu.com/docs. What remains needs the account
# that owns the zone, which is why it is a script a person runs rather than a
# step deploy/run.sh takes on its own:
#
#   1. a DNS record for abhed.zybuu.com pointing at the tunnel;
#   2. the old docs worker removed so the new one can own both /docs routes;
#   3. the server told its canonical host, so the old name redirects;
#   4. the site published with the new name.
#
# Each step is safe to repeat.
set -euo pipefail
cd "$(dirname "$0")/.."

step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

step "1/4 DNS: abhed.zybuu.com → the tunnel"
cloudflared tunnel route dns titan abhed.zybuu.com 2>&1 | tail -1 || echo "note: the record may already exist — continuing"
for i in $(seq 1 30); do
  dig +short abhed.zybuu.com | grep -q . && break
  sleep 2
done
dig +short abhed.zybuu.com | grep -q . || { echo "error: abhed.zybuu.com does not resolve yet; run this again in a minute" >&2; exit 1; }

step "2/4 Docs worker: one worker, both routes"
( cd deploy/docs-worker
  npx wrangler delete --name titan-docs --force 2>&1 | tail -1 || true
  npx wrangler deploy 2>&1 | tail -3 )

step "3/4 Server: titan.zybuu.com answers with a redirect"
python3 - <<'PY'
import json
p = "deploy/config.json"
c = json.load(open(p))
c.setdefault("server", {})["canonical_host"] = "abhed.zybuu.com"
json.dump(c, open(p, "w"), indent=2); open(p, "a").write("\n")
print("deploy/config.json: server.canonical_host = abhed.zybuu.com")
PY
./deploy/run.sh 2>&1 | tail -3

step "4/4 Site"
./deploy/publish-site.sh 2>&1 | tail -3

step "Check"
curl -sS -o /dev/null -w 'https://abhed.zybuu.com          %{http_code}\n' https://abhed.zybuu.com/
curl -sS -o /dev/null -w 'https://titan.zybuu.com/console  %{http_code} → %{redirect_url}\n' https://titan.zybuu.com/console
curl -sS -o /dev/null -w 'https://abhed.zybuu.com/docs/    %{http_code}\n' https://abhed.zybuu.com/docs/
curl -sS -o /dev/null -w 'https://titan.zybuu.com/docs/    %{http_code} → %{redirect_url}\n' https://titan.zybuu.com/docs/
echo
echo "Commit deploy/config.json when the checks read 200 / 301 / 200 / 301."
