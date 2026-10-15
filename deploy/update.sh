#!/bin/bash
set -e

# Virtual CLU - Updater
# Updates an installation made by install.sh (systemd) to the latest release, keeping its
# directories and web port. Data (config, Lua, history) is not touched; the bundled AI docs
# in <data>/clu/docs/ai are replaced, as by install.sh.
#
# Usage: curl -sSL https://raw.githubusercontent.com/Domktorymysli/vclu/main/deploy/update.sh | sudo bash
#    or: ... | sudo bash -s -- --version v1.3.0

main() {
REPO="Domktorymysli/vclu"
SERVICE_FILE="/etc/systemd/system/vclu.service"
VERSION=""
FORCE=0

while [[ $# -gt 0 ]]; do
  case $1 in
    --version) VERSION="v${2#v}"; shift 2 ;;
    --force) FORCE=1; shift ;;
    --help)
      echo "Usage: curl -sSL https://raw.githubusercontent.com/${REPO}/main/deploy/update.sh | sudo bash -s -- [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --version VERSION    Update to a specific version (default: latest)"
      echo "  --force              Reinstall even if this version is installed, and even if"
      echo "                       the service file was edited (install.sh rewrites it)"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "Error: This script must be run as root (use sudo)"
  exit 1
fi

for cmd in curl tar systemctl; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: $cmd is required but not installed"
    exit 1
  fi
done

# The installation as install.sh left it: directories and port from the service file
if [ ! -f "$SERVICE_FILE" ]; then
  echo "Error: $SERVICE_FILE not found. Install first:"
  echo "  curl -sSL https://vclu.pl/install.sh | sudo bash"
  exit 1
fi

EXEC_START=$(grep -m1 '^ExecStart=' "$SERVICE_FILE" | cut -d= -f2-)
BINARY=$(echo "$EXEC_START" | awk '{print $1}')
INSTALL_DIR=$(dirname "$BINARY")
DATA_DIR=$(grep -m1 '^WorkingDirectory=' "$SERVICE_FILE" | cut -d= -f2-)
WEB_PORT=$(echo "$EXEC_START" | sed -n 's/.*--web-port[ =]\([0-9]*\).*/\1/p')

if [ ! -x "$BINARY" ] || [ -z "$DATA_DIR" ] || [ -z "$WEB_PORT" ]; then
  echo "Error: could not read the installation from $SERVICE_FILE"
  echo "  ExecStart: $EXEC_START"
  echo "  WorkingDirectory: $DATA_DIR"
  exit 1
fi

CURRENT=$("$BINARY" -version 2>/dev/null | sed -n 's/.*version \([^ ]*\).*/\1/p')

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)   SUFFIX="linux-amd64" ;;
  aarch64|arm64)  SUFFIX="linux-arm64" ;;
  armv7l)         SUFFIX="linux-armv7" ;;
  armv6l)         SUFFIX="linux-armv6" ;;
  *)
    echo "Error: Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

if [ -z "$VERSION" ]; then
  echo "Fetching latest version..."
  VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
  if [ -z "$VERSION" ]; then
    echo "Error: Could not determine latest version"
    exit 1
  fi
fi

echo ""
echo "Virtual CLU Updater"
echo "==================="
echo "  Installed: ${CURRENT:-unknown}"
echo "  New:       $VERSION"
echo "  Install:   $INSTALL_DIR"
echo "  Data:      $DATA_DIR"
echo "  Web port:  $WEB_PORT"
echo ""

if [ "$FORCE" -eq 0 ] && [ -n "$CURRENT" ] && [ "${VERSION#v}" = "${CURRENT#v}" ]; then
  echo "Already up to date."
  exit 0
fi

TARBALL="vclu-${SUFFIX}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading $URL ..."
if ! curl -fsSL -o "$TMP_DIR/$TARBALL" "$URL"; then
  echo "Error: Download failed. Check version and network."
  exit 1
fi

tar -xzf "$TMP_DIR/$TARBALL" -C "$TMP_DIR"
EXTRACTED=$(find "$TMP_DIR" -maxdepth 1 -type d -name "vclu-*" | head -1)
[ -n "$EXTRACTED" ] || EXTRACTED="$TMP_DIR"
if [ ! -f "$EXTRACTED/install.sh" ] || [ ! -f "$EXTRACTED/vclu" ]; then
  echo "Error: the archive has no install.sh or vclu"
  exit 1
fi

# install.sh writes the service file from its template: an edited one would be lost
TEMPLATE_EXEC="$INSTALL_DIR/vclu --auto --data-dir . --web-port $WEB_PORT"
if [ "$EXEC_START" != "$TEMPLATE_EXEC" ] || [ -d "${SERVICE_FILE}.d" ]; then
  echo "The service was changed after installation:"
  [ "$EXEC_START" != "$TEMPLATE_EXEC" ] && echo "  ExecStart: $EXEC_START"
  [ -d "${SERVICE_FILE}.d" ] && echo "  drop-ins in ${SERVICE_FILE}.d (their settings are not read here)"
  if [ "$FORCE" -eq 0 ]; then
    echo "install.sh writes ExecStart back to: $TEMPLATE_EXEC"
    echo "Put your changes in a drop-in (sudo systemctl edit vclu), which install.sh leaves alone,"
    echo "or run again with --force to update anyway (a copy of the service file is kept)."
    exit 1
  fi
fi

# Backup of what install.sh replaces: program, web files, Lua runtime, service file
BACKUP="$INSTALL_DIR/backup-${CURRENT:-unknown}-$(date +%Y%m%d-%H%M%S)"
echo "Backing up to $BACKUP ..."
mkdir -p "$BACKUP"
cp -a "$BINARY" "$BACKUP/"
SAVED_DIRS=""
for d in web runtime; do
  if [ -d "$INSTALL_DIR/$d" ]; then
    cp -a "$INSTALL_DIR/$d" "$BACKUP/"
    SAVED_DIRS="$SAVED_DIRS $d"
  fi
done
cp -a "$SERVICE_FILE" "$BACKUP/"

# Keep the three newest backups
ls -1dt "$INSTALL_DIR"/backup-* 2>/dev/null | tail -n +4 | while read -r old; do
  rm -rf "$old"
done

if ! bash "$EXTRACTED/install.sh" "$INSTALL_DIR" "$DATA_DIR" "$WEB_PORT"; then
  echo ""
  echo "Update failed. The previous version is in $BACKUP; to restore it:"
  echo "  sudo systemctl stop vclu"
  echo "  sudo cp -a '$BACKUP/vclu' '$INSTALL_DIR/'"
  for d in $SAVED_DIRS; do
    echo "  sudo rm -rf '$INSTALL_DIR/$d' && sudo cp -a '$BACKUP/$d' '$INSTALL_DIR/'"
  done
  echo "  sudo cp '$BACKUP/vclu.service' '$SERVICE_FILE' && sudo systemctl daemon-reload"
  echo "  sudo systemctl start vclu"
  exit 1
fi

echo ""
echo "Updated ${CURRENT:-unknown} -> $VERSION. What changed:"
echo "  https://github.com/${REPO}/releases/tag/${VERSION}"
}

# Wrapped in main so a script cut off while downloading does not run half
main "$@"
