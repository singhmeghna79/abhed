#!/usr/bin/env bash
#
# Build a signed, self-contained Abhed release bundle for air-gapped install.
#
# The enclave never builds and never fetches: it verifies and unpacks. Anything
# that would need the network at install time is a defect in this script, not
# something to work around on the target (docs/ops/air-gap.md §2).
#
# Usage:
#   scripts/build-bundle.sh [-v VERSION] [-o OUTDIR] [-k SIGNING_KEY]
#
# Produces:
#   abhed-<version>.tar.gz          the bundle
#   abhed-<version>.tar.gz.sha256   digest
#   abhed-<version>.tar.gz.sig      detached signature (when a key is given)

set -euo pipefail

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OUTDIR="dist"
SIGNING_KEY=""
PLATFORMS="linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"

while getopts "v:o:k:p:h" opt; do
  case $opt in
    v) VERSION="$OPTARG" ;;
    o) OUTDIR="$OPTARG" ;;
    k) SIGNING_KEY="$OPTARG" ;;
    p) PLATFORMS="$OPTARG" ;;
    h) sed -n '2,20p' "$0"; exit 0 ;;
    *) exit 2 ;;
  esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="$(mktemp -d)"
BUNDLE="abhed-${VERSION}"
trap 'rm -rf "$STAGE"' EXIT

say() { printf '  %s\n' "$*"; }

echo "Building Abhed bundle ${VERSION}"

# ---------------------------------------------------------------- binaries
say "compiling static binaries"
mkdir -p "$STAGE/$BUNDLE/bin"
for platform in $PLATFORMS; do
  os="${platform%/*}"; arch="${platform#*/}"
  out="$STAGE/$BUNDLE/bin/abhed-${os}-${arch}"
  # CGO off is what makes these genuinely static, so the enclave needs no
  # matching libc.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "$out" "$ROOT/cmd/abhed"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -trimpath -ldflags "-s -w" \
    -o "$STAGE/$BUNDLE/bin/abhed-bench-${os}-${arch}" "$ROOT/cmd/abhed-bench"
  say "  $(basename "$out") ($(du -h "$out" | cut -f1))"
done

# ---------------------------------------------------------------- source deps
# Vendoring means the enclave can rebuild from source without a module proxy,
# which matters when a security patch has to be applied offline.
say "vendoring Go dependencies"
( cd "$ROOT" && go mod vendor -o "$STAGE/$BUNDLE/vendor" 2>/dev/null ) || \
  say "  (skipped: no external dependencies to vendor)"

# ---------------------------------------------------------------- docs & config
say "staging documentation and templates"
mkdir -p "$STAGE/$BUNDLE/docs" "$STAGE/$BUNDLE/config" "$STAGE/$BUNDLE/schema"
cp -R "$ROOT/docs/." "$STAGE/$BUNDLE/docs/"
cp "$ROOT/README.md" "$STAGE/$BUNDLE/"
cp "$ROOT/internal/store/schema.sql" "$STAGE/$BUNDLE/schema/"

cat > "$STAGE/$BUNDLE/config/config.example.json" <<'JSON'
{
  "model": {
    "default": "onprem",
    "providers": {
      "onprem": {
        "type": "openai-compatible",
        "base_url": "http://vllm.internal:8000/v1",
        "model": "Qwen/Qwen3-32B",
        "api_key_env": "ABHED_API_KEY",
        "context_window": 131072
      }
    }
  },
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "audience": "abhed",
    "tenant_claim": "org_id",
    "groups_claim": "groups"
  },
  "storage": {
    "driver": "postgres",
    "tenant": "default",
    "max_conns": 20
  },
  "sandbox": {
    "min_tier": "container",
    "allow_network": false,
    "max_memory_mb": 4096,
    "max_procs": 512
  },
  "permissions": {
    "mode": "default",
    "deny": ["bash(rm -rf /*)", "bash(*mkfs*)", "write(/etc/**)"],
    "allow": ["bash(git status*)", "bash(git diff*)", "read(**)"]
  },
  "retrieval": { "enabled": true, "embed": false },
  "limits": { "max_turns": 100, "max_subagents": 20, "nested_subagents": false }
}
JSON

# ---------------------------------------------------------------- installer
cat > "$STAGE/$BUNDLE/install.sh" <<'INSTALL'
#!/usr/bin/env bash
# Install Abhed from this bundle. Requires no network access.
set -euo pipefail

PREFIX="${PREFIX:-/opt/abhed}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

binary="bin/abhed-${os}-${arch}"
if [ ! -f "$binary" ]; then
  echo "no binary for ${os}/${arch} in this bundle" >&2
  echo "available:" >&2; ls bin/ >&2
  exit 1
fi

echo "Installing Abhed to ${PREFIX}"
mkdir -p "$PREFIX/bin" "$PREFIX/docs" "$PREFIX/schema" "$PREFIX/config"
install -m 0755 "$binary" "$PREFIX/bin/abhed"
install -m 0755 "bin/abhed-bench-${os}-${arch}" "$PREFIX/bin/abhed-bench" 2>/dev/null || true
cp -R docs/. "$PREFIX/docs/"
cp schema/schema.sql "$PREFIX/schema/"
cp config/config.example.json "$PREFIX/config/"

echo
echo "Installed. Next steps:"
echo "  1. cp ${PREFIX}/config/config.example.json /etc/abhed/config.json"
echo "  2. edit it to point at your model endpoint and database"
echo "  3. ${PREFIX}/bin/abhed doctor"
echo
echo "The schema in ${PREFIX}/schema/schema.sql is applied automatically on"
echo "first start; apply it manually if your database role cannot create tables."
INSTALL
chmod +x "$STAGE/$BUNDLE/install.sh"

# ---------------------------------------------------------------- manifest
say "generating manifest and SBOM"
go version > "$STAGE/$BUNDLE/BUILDINFO"
{
  echo "version=${VERSION}"
  echo "built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "git_commit=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "platforms=${PLATFORMS}"
} >> "$STAGE/$BUNDLE/BUILDINFO"

# A minimal SPDX-style SBOM. Abhed's dependency surface is deliberately small,
# which is itself an air-gap property worth recording.
{
  echo "{"
  echo "  \"bundle\": \"${BUNDLE}\","
  echo "  \"version\": \"${VERSION}\","
  echo "  \"generated\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
  echo "  \"go_version\": \"$(go version | awk '{print $3}')\","
  echo "  \"dependencies\": ["
  ( cd "$ROOT" && go list -m -f '    {"module": "{{.Path}}", "version": "{{.Version}}"}' all 2>/dev/null |
      grep -v '^    {"module": "github.com/yuvrajsingh/abhed"' | paste -sd, - )
  echo "  ]"
  echo "}"
} > "$STAGE/$BUNDLE/sbom.json"

# Digest every file so the enclave verifies contents, not just the archive.
# Generated LAST, and excluding itself: a manifest that lists its own digest can
# never verify.
(
  cd "$STAGE/$BUNDLE"
  find . -type f ! -name SHA256SUMS -exec shasum -a 256 {} \; | sort -k2 > SHA256SUMS
)

# ---------------------------------------------------------------- archive
# Absolute OUTDIR must not be re-rooted under the repo.
case "$OUTDIR" in
  /*) OUTPATH="$OUTDIR" ;;
   *) OUTPATH="$ROOT/$OUTDIR" ;;
esac
mkdir -p "$OUTPATH"
ARCHIVE="$OUTPATH/${BUNDLE}.tar.gz"
say "creating ${BUNDLE}.tar.gz"
tar -czf "$ARCHIVE" -C "$STAGE" "$BUNDLE"

shasum -a 256 "$ARCHIVE" | awk '{print $1}' > "${ARCHIVE}.sha256"
say "sha256 $(cat "${ARCHIVE}.sha256")"

if [ -n "$SIGNING_KEY" ]; then
  say "signing with ${SIGNING_KEY}"
  openssl dgst -sha256 -sign "$SIGNING_KEY" -out "${ARCHIVE}.sig" "$ARCHIVE"
  say "signature written to $(basename "${ARCHIVE}.sig")"
else
  cat <<'WARN'

  WARNING: no signing key given, so this bundle is UNSIGNED.
  An air-gapped install should verify a signature before unpacking; a bundle
  that is merely hashed proves integrity, not provenance. Re-run with -k.

WARN
fi

echo
echo "Bundle: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"
echo "Verify on the target with: scripts/verify-bundle.sh $(basename "$ARCHIVE")"
