#!/usr/bin/env bash
# Install the visual-companion binary from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/ystyle/visual-companion/main/scripts/install.sh | bash
#
# Options (environment variables):
#   VERSION=1.2.3        install a specific tag instead of the latest release
#   INSTALL_DIR=/path    where to put the binary (default: ~/.local/bin)
#   VERIFY=0             skip the checksum check (default is to verify)
#   VERIFY=1             make a missing SHA256SUMS a hard error too
#   REPO=owner/name      download from a different GitHub repository
#   BASE_URL=...         override the download root entirely (mirrors, GitHub
#                        Enterprise, and the test suite use this)
#
# This script downloads a prebuilt binary and puts it on your PATH. It does not
# need Go, Node, or any other runtime.
set -euo pipefail

REPO="${REPO:-ystyle/visual-companion}"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERIFY="${VERIFY:-auto}"
BASE_URL="${BASE_URL:-}"

die() {
  printf 'install: %s\n' "$1" >&2
  exit 1
}

# --- detect platform --------------------------------------------------------

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  MINGW*|MSYS*|CYGWIN*) os=windows ;;
  *) die "unsupported OS '$os'. Download a binary manually from https://github.com/$REPO/releases" ;;
esac

case "$arch" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64)  arch=amd64 ;;
  *) die "unsupported architecture '$arch'. Download a binary manually from https://github.com/$REPO/releases" ;;
esac

if [[ "$os" == "windows" && "$arch" != "amd64" ]]; then
  die "no Windows/$arch build is published; download manually from https://github.com/$REPO/releases"
fi

ext=""
[[ "$os" == "windows" ]] && ext=".exe"
artifact="visual-companion-$os-$arch$ext"

if [[ -n "$BASE_URL" ]]; then
  # Explicit override: use it verbatim. This is what makes the installer
  # testable against a local endpoint, and what lets a mirror work.
  base="$BASE_URL"
elif [[ "$VERSION" == "latest" ]]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/v${VERSION#v}"
fi

# --- download ---------------------------------------------------------------

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $artifact..."
curl -fsSL "$base/$artifact" -o "$tmp/$artifact" \
  || die "download failed. Check that a release exists at $base"

# Verification is on by default: the whole point of publishing SHA256SUMS is
# that a download gets checked. VERIFY=0 is an escape hatch for mirrors that do
# not carry the checksums file; it is never the recommended path.
if [[ "$VERIFY" == "0" ]]; then
  echo "Skipping checksum verification (VERIFY=0)."
else
  echo "Verifying checksum..."
  if ! curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"; then
    if [[ "$VERIFY" == "1" ]]; then
      die "could not download SHA256SUMS"
    fi
    echo "Warning: no SHA256SUMS at $base; installing unverified." >&2
    echo "         Re-run with VERIFY=1 to make this an error." >&2
  else
    expected="$(awk -v f="$artifact" '$2 == f || $2 == "./" f {print $1}' "$tmp/SHA256SUMS")"
    [[ -n "$expected" ]] || die "$artifact is not listed in SHA256SUMS"
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$tmp/$artifact" | cut -d' ' -f1)"
    else
      actual="$(shasum -a 256 "$tmp/$artifact" | cut -d' ' -f1)"
    fi
    [[ "$expected" == "$actual" ]] || die "checksum mismatch: expected $expected, got $actual"
    echo "Checksum OK."
  fi
fi

# --- install ----------------------------------------------------------------

mkdir -p "$INSTALL_DIR"
target="$INSTALL_DIR/visual-companion"
[[ "$os" == "windows" ]] && target="$target.exe"

# Install atomically so a running binary is never half-overwritten.
mv "$tmp/$artifact" "$target"
chmod +x "$target"

echo "Installed to $target"

if [[ "$os" == "darwin" ]]; then
  # A downloaded unsigned binary is quarantined by Gatekeeper.
  xattr -d com.apple.quarantine "$target" 2>/dev/null || true
fi

# --- report -----------------------------------------------------------------

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo
    echo "Note: $INSTALL_DIR is not on your PATH. Add it with:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

echo
echo "Register it with your agent:"
echo "  claude mcp add visual-companion -- $target"
echo
echo "No path argument is needed: the server discovers your workspace over MCP"
echo "roots when a companion starts. Add --project-dir only to pin a location."
