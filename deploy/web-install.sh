#!/bin/bash
set -e

# Virtual CLU - Web Installer
# Usage: curl -sSL https://vclu.pl/install.sh | bash
#    or: curl -sSL https://vclu.pl/install.sh | bash -s -- --version v1.0.0

REPO="Domktorymysli/vclu"
INSTALL_DIR="/opt/vclu"
DATA_DIR="/opt/vclu/data"
VERSION=""

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --version) VERSION="$2"; shift 2 ;;
    --install-dir) INSTALL_DIR="$2"; shift 2 ;;
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --help)
      echo "Usage: curl -sSL https://vclu.pl/install.sh | bash -s -- [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --version VERSION    Install specific version (default: latest)"
      echo "  --install-dir DIR    Installation directory (default: /opt/vclu)"
      echo "  --data-dir DIR       Data directory (default: /opt/vclu/data)"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Check root
if [ "$(id -u)" -ne 0 ]; then
  echo "Error: This script must be run as root (use sudo)"
  exit 1
fi

# Check dependencies
for cmd in curl tar; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: $cmd is required but not installed"
    exit 1
  fi
done

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)   SUFFIX="linux-amd64" ;;
  aarch64|arm64)   SUFFIX="linux-arm64" ;;
  armv7l)          SUFFIX="linux-armv7" ;;
  armv6l)          SUFFIX="linux-armv6" ;;
  *)
    echo "Error: Unsupported architecture: $ARCH"
    echo "Supported: x86_64, aarch64, armv7l, armv6l"
    exit 1
    ;;
esac

# Determine version
if [ -z "$VERSION" ]; then
  echo "Fetching latest version..."
  VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
  if [ -z "$VERSION" ]; then
    echo "Error: Could not determine latest version"
    exit 1
  fi
fi

echo ""
echo "Virtual CLU Installer"
echo "====================="
echo "  Version:  $VERSION"
echo "  Arch:     $ARCH ($SUFFIX)"
echo "  Install:  $INSTALL_DIR"
echo "  Data:     $DATA_DIR"
echo ""

# Download
TARBALL="vclu-${SUFFIX}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading $URL ..."
if ! curl -sSL -o "$TMP_DIR/$TARBALL" "$URL"; then
  echo "Error: Download failed. Check version and network."
  exit 1
fi

# Verify download is not empty / HTML error page
FILE_SIZE=$(stat -c%s "$TMP_DIR/$TARBALL" 2>/dev/null || stat -f%z "$TMP_DIR/$TARBALL" 2>/dev/null || echo 0)
if [ "$FILE_SIZE" -lt 1000 ]; then
  echo "Error: Downloaded file too small ($FILE_SIZE bytes). Version $VERSION may not exist."
  exit 1
fi

# Extract
echo "Extracting..."
tar -xzf "$TMP_DIR/$TARBALL" -C "$TMP_DIR"

# Find extracted directory (goreleaser wraps in vclu-SUFFIX/)
EXTRACTED=$(find "$TMP_DIR" -maxdepth 1 -type d -name "vclu-*" | head -1)
if [ -z "$EXTRACTED" ]; then
  EXTRACTED="$TMP_DIR"
fi

# Run install.sh from archive
if [ -f "$EXTRACTED/install.sh" ]; then
  echo "Running installer..."
  bash "$EXTRACTED/install.sh" "$INSTALL_DIR" "$DATA_DIR"
else
  echo "Error: install.sh not found in archive"
  exit 1
fi
