#!/bin/sh
# fabrika installer — downloads the right prebuilt binary from GitHub releases
# and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/berkaycubuk/fabrika/main/install.sh | sh
#
# Override with environment variables:
#   FABRIKA_VERSION=v0.1.0   pin a specific release tag (default: latest)
#   FABRIKA_INSTALL_DIR=~/.local/bin   install location (default: /usr/local/bin)
#
# Installs via curl, which does NOT set the macOS quarantine flag — so the
# unsigned binary runs without a Gatekeeper prompt.
set -eu

REPO_URL="https://github.com/berkaycubuk/fabrika"
VERSION="${FABRIKA_VERSION:-latest}"
INSTALL_DIR="${FABRIKA_INSTALL_DIR:-/usr/local/bin}"

os=$(uname -s)
arch=$(uname -m)

case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "fabrika: unsupported OS: $os" >&2; exit 1 ;;
esac

case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "fabrika: unsupported architecture: $arch" >&2; exit 1 ;;
esac

asset="fabrika_${os}_${arch}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  url="${REPO_URL}/releases/latest/download/${asset}"
else
  # Accept both v0.1.0 and 0.1.0 — release tags carry the v prefix.
  case "$VERSION" in
    v*) tag="$VERSION" ;;
    *) tag="v$VERSION" ;;
  esac
  url="${REPO_URL}/releases/download/${tag}/${asset}"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $url"
if ! curl -fSL "$url" -o "$tmp/$asset"; then
  echo "fabrika: download failed ($url)" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"

# Use sudo only when the install dir isn't writable by the current user.
if [ -w "$INSTALL_DIR" ] || [ "$(id -u)" = "0" ]; then
  install -d "$INSTALL_DIR"
  install -m 0755 "$tmp/fabrika" "$INSTALL_DIR/fabrika"
else
  echo "Installing to $INSTALL_DIR (needs sudo)"
  sudo install -d "$INSTALL_DIR"
  sudo install -m 0755 "$tmp/fabrika" "$INSTALL_DIR/fabrika"
fi

echo "Installed $("$INSTALL_DIR/fabrika" version 2>/dev/null || echo "fabrika") to $INSTALL_DIR"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) echo "Run 'fabrika --help' to get started." ;;
  *) echo "Note: $INSTALL_DIR is not on your PATH — add it, then run 'fabrika --help'." ;;
esac
