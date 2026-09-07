#!/usr/bin/env bash
# Check whether zybuu.com is ready to serve, before trying to get a certificate.
#
# Let's Encrypt rate-limits failed authorisations. Discovering that port 80 is
# unreachable by watching Caddy fail is slower and costs an attempt each time,
# so every precondition is checked here first and reported together.
set -uo pipefail

DOMAIN="${TITAN_DOMAIN:-titan.zybuu.com}"
LAN_IP="$(ipconfig getifaddr en0 2>/dev/null || echo unknown)"

ok=0; warn=0; fail=0
say()  { printf '  %s %s\n' "$1" "$2"; }
pass() { say '✓' "$1"; ok=$((ok+1)); }
flag() { say '!' "$1"; warn=$((warn+1)); }
bad()  { say '✗' "$1"; fail=$((fail+1)); }

echo
echo "preflight · $DOMAIN"
echo

# ---------------------------------------------------------------- addressing
PUBLIC_IP="$(curl -fsS --max-time 10 https://api.ipify.org 2>/dev/null || echo '')"
if [ -z "$PUBLIC_IP" ]; then
  bad "cannot determine this connection's public address"
else
  pass "public address $PUBLIC_IP"
fi

DNS_IP="$(dig +short "$DOMAIN" A 2>/dev/null | head -1)"
if [ -z "$DNS_IP" ]; then
  bad "$DOMAIN has no A record"
elif [ "$DNS_IP" = "$PUBLIC_IP" ]; then
  pass "$DOMAIN resolves here ($DNS_IP)"
else
  bad "$DOMAIN resolves to $DNS_IP, but this connection is $PUBLIC_IP"
  say ' ' "  set the A record to $PUBLIC_IP in GoDaddy DNS"
fi

# A VPN reroutes outbound traffic and will not carry inbound port forwards, so
# the certificate challenge fails while everything else looks fine.
if ifconfig 2>/dev/null | grep -q '^utun[0-9]*:.*<UP'; then
  if route get 8.8.8.8 2>/dev/null | grep -q 'interface: utun'; then
    flag "a VPN is carrying your default route — turn it off before deploying"
  fi
fi

[ "$LAN_IP" != unknown ] && pass "LAN address $LAN_IP (forward ports to this)"

# ------------------------------------------------------------------- inbound
# HTTP-01 needs port 80 reachable from the internet, which cannot be proven from
# inside the LAN: connecting to your own public address only shows whether the
# router hairpins, which is a different question.
#
# Deliberately not resolved by asking a third-party port-scanning site — that
# would hand an outside service your address and open ports. The honest answer
# is that Caddy's first certificate attempt is the real test, so this reports
# what it can and says which part it cannot.
if command -v nc >/dev/null 2>&1 && [ -n "$PUBLIC_IP" ]; then
  for port in 80 443; do
    if nc -z -G 5 -w 5 "$PUBLIC_IP" "$port" 2>/dev/null; then
      pass "port $port answers at $PUBLIC_IP (hairpin — inbound still unproven)"
    else
      flag "port $port did not answer at $PUBLIC_IP"
      say ' ' "  forward TCP $port to $LAN_IP on the router at 192.168.1.1"
    fi
  done
  say ' ' "note: only a request from outside your network proves inbound works;"
  say ' ' "      Caddy's first certificate attempt is that test."
fi

# ------------------------------------------------------------------ services
if command -v podman >/dev/null 2>&1; then
  if podman info >/dev/null 2>&1; then
    pass "podman is running"
  else
    bad "podman is installed but its machine is not running (podman machine start)"
  fi
else
  bad "podman not installed"
fi

command -v caddy >/dev/null 2>&1 && pass "caddy installed" || bad "caddy not installed"

if podman image exists titan:local 2>/dev/null; then
  pass "titan:local image built"
else
  flag "titan:local not built yet (podman build -t titan:local -f Dockerfile .)"
fi

# --------------------------------------------------------------------- verdict
echo
if [ "$fail" -gt 0 ]; then
  echo "  $fail blocking · $warn to check · not ready"
  exit 1
fi
if [ "$warn" -gt 0 ]; then
  echo "  $warn to check before requesting a certificate"
  exit 0
fi
echo "  ready"
