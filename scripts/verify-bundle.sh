#!/usr/bin/env bash
#
# Verify a Titan bundle before unpacking it on an air-gapped target.
#
# Run this on the ENCLAVE side, not the build side. It checks the archive
# digest, optionally the signature, and then every file's digest after
# extraction — because an archive that hashes correctly can still have been
# assembled from tampered inputs.
#
# Usage: scripts/verify-bundle.sh titan-<version>.tar.gz [public-key.pem]

set -euo pipefail

ARCHIVE="${1:?usage: verify-bundle.sh <bundle.tar.gz> [public-key.pem]}"
PUBKEY="${2:-}"

fail() { printf '\n  FAILED: %s\n' "$*" >&2; exit 1; }
ok()   { printf '  ok    %s\n' "$*"; }

echo "Verifying $(basename "$ARCHIVE")"

[ -f "$ARCHIVE" ] || fail "archive not found"

# 1. Archive digest.
if [ -f "${ARCHIVE}.sha256" ]; then
  expected="$(cat "${ARCHIVE}.sha256")"
  actual="$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')"
  [ "$expected" = "$actual" ] || fail "archive digest mismatch
    expected $expected
    actual   $actual"
  ok "archive digest"
else
  fail "no .sha256 file alongside the archive"
fi

# 2. Signature. Provenance, not just integrity.
if [ -n "$PUBKEY" ]; then
  [ -f "${ARCHIVE}.sig" ] || fail "public key given but no .sig file present"
  openssl dgst -sha256 -verify "$PUBKEY" -signature "${ARCHIVE}.sig" "$ARCHIVE" >/dev/null \
    || fail "SIGNATURE INVALID — do not install this bundle"
  ok "signature"
elif [ -f "${ARCHIVE}.sig" ]; then
  printf '  warn  signature present but no public key given; pass one to verify provenance\n'
else
  printf '  warn  bundle is UNSIGNED — integrity checked, provenance not\n'
fi

# 3. Per-file digests after extraction.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
tar -xzf "$ARCHIVE" -C "$TMP"
DIR="$(find "$TMP" -maxdepth 1 -mindepth 1 -type d | head -1)"

[ -f "$DIR/SHA256SUMS" ] || fail "bundle has no SHA256SUMS manifest"
( cd "$DIR" && shasum -a 256 -c SHA256SUMS --quiet ) \
  || fail "one or more files do not match the manifest"
ok "per-file digests ($(wc -l < "$DIR/SHA256SUMS" | tr -d ' ') files)"

# 4. The bundle must be self-contained.
[ -x "$DIR/install.sh" ] || fail "install.sh missing or not executable"
ls "$DIR"/bin/titan-* >/dev/null 2>&1 || fail "no binaries in bundle"
ok "install script and binaries present"

echo
echo "Build info:"
sed 's/^/  /' "$DIR/BUILDINFO"
echo
echo "Bundle verified. Install with:"
echo "  tar -xzf $(basename "$ARCHIVE") && cd $(basename "$DIR") && sudo ./install.sh"
