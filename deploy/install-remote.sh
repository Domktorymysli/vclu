#!/bin/bash
set -e

# Virtual CLU - Remote Install Script
# Usage: curl -sSL https://vclu.pl/install.sh | bash
#
# Detects platform, downloads the latest release from GitHub,
# extracts, and runs the bundled installer.

REPO="Domktorymysli/vclu"

echo "Virtual CLU - Remote Installer"
echo "=============================="
echo ""

# --- Detect platform ---
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
  linux)  ;;
  darwin) ;;
  *)
    echo "Error: Unsupported OS: $OS"
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Error: Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

SUFFIX="${OS}-${ARCH}"
echo "Detected platform: $SUFFIX"

# --- Get latest version ---
echo "Fetching latest release..."
VERSION=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)

if [ -z "$VERSION" ]; then
  echo "Error: Could not determine latest version. Check your internet connection."
  exit 1
fi

echo "Latest version: $VERSION"

# --- Download ---
URL="https://github.com/$REPO/releases/download/$VERSION/vclu-${SUFFIX}.tar.gz"
TMPDIR=$(mktemp -d)
ARCHIVE="$TMPDIR/vclu.tar.gz"

echo "Downloading $URL ..."
if ! curl -sSL --fail -o "$ARCHIVE" "$URL"; then
  echo "Error: Download failed. Release vclu-${SUFFIX}.tar.gz may not exist for $VERSION."
  rm -rf "$TMPDIR"
  exit 1
fi

# --- Extract ---
echo "Extracting..."
tar -xzf "$ARCHIVE" -C "$TMPDIR"

# --- Run bundled installer ---
EXTRACT_DIR="$TMPDIR/vclu-${SUFFIX}"
if [ ! -f "$EXTRACT_DIR/install.sh" ]; then
  echo "Error: install.sh not found in archive."
  rm -rf "$TMPDIR"
  exit 1
fi

echo "Running installer..."
echo ""
cd "$EXTRACT_DIR"
sudo bash install.sh

# --- Cleanup ---
rm -rf "$TMPDIR"
