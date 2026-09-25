#!/usr/bin/env bash
# Cross-compile the release artifacts into dist/.
#
# Usage:
#   scripts/build-release.sh            # build every platform, version "0.0.0"
#   VERSION=1.2.3 scripts/build-release.sh
#
# Outputs one static binary per platform plus SHA256SUMS:
#   dist/visual-companion-<os>-<arch>[.exe]
#   dist/SHA256SUMS
#
# The script is the single source of truth for artifact names. The README's
# install one-liner and the release workflow both depend on these exact names.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.0.0}"
VERSION="${VERSION#v}" # accept v1.2.3 or 1.2.3
DIST="${DIST:-dist}"

# os/arch pairs. One entry per published artifact.
TARGETS=(
  darwin/arm64
  darwin/amd64
  linux/arm64
  linux/amd64
  windows/amd64
)

rm -rf "$DIST"
mkdir -p "$DIST"

echo "Building visual-companion v$VERSION"
for target in "${TARGETS[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  ext=""
  [[ "$os" == "windows" ]] && ext=".exe"
  out="$DIST/visual-companion-$os-$arch$ext"

  # CGO_ENABLED=0 is what makes the artifact "copy and run": no libc, no
  # dynamic loader, nothing the target machine has to provide.
  # -trimpath keeps build paths out of the binary for reproducible output.
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$out" .

  size="$(du -h "$out" | cut -f1)"
  printf '  %-16s %s\n' "$os/$arch" "$size"
done

# Checksums so users can verify a download. Bare filenames (not dist/... paths)
# so `sha256sum -c SHA256SUMS` works from inside the download directory.
# ./visual-companion-* deliberately excludes SHA256SUMS itself.
(
  cd "$DIST"
  # shellcheck disable=SC2035
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./visual-companion-* >SHA256SUMS
  else
    shasum -a 256 ./visual-companion-* >SHA256SUMS
  fi
)

echo
echo "Artifacts in $DIST/:"
ls -1 "$DIST"
