#!/bin/bash
set -e

# Virtual CLU - Installation Script
# Run from extracted release archive or repo deploy-tmp/ directory.
# All paths resolved relative to this script's location.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

INSTALL_DIR="${1:-/opt/vclu}"
DATA_DIR="${2:-/opt/vclu/data}"
WEB_PORT="${3:-8080}"

echo "Installing Virtual CLU"
echo "======================"
echo ""
echo "Install directory: $INSTALL_DIR"
echo "Data directory:    $DATA_DIR"
echo "Web port:          $WEB_PORT"
echo ""

# Stop existing service if running
if systemctl is-active --quiet vclu 2>/dev/null; then
  echo "Stopping existing vclu service..."
  systemctl stop vclu
fi

# Create directories
mkdir -p "$INSTALL_DIR"
mkdir -p "$DATA_DIR"

# Binary
echo "Installing binary..."
cp "$SCRIPT_DIR/vclu" "$INSTALL_DIR/vclu"
chmod +x "$INSTALL_DIR/vclu"

# Web files
if [ -d "$SCRIPT_DIR/web" ]; then
  echo "Installing web files..."
  rm -rf "$INSTALL_DIR/web"
  cp -r "$SCRIPT_DIR/web" "$INSTALL_DIR/"
fi

# Lua runtime
if [ -d "$SCRIPT_DIR/runtime" ]; then
  echo "Installing Lua runtime..."
  rm -rf "$INSTALL_DIR/runtime"
  cp -r "$SCRIPT_DIR/runtime" "$INSTALL_DIR/"
fi

# AI docs (always overwrite — not user data)
if [ -d "$SCRIPT_DIR/docs/ai" ]; then
  echo "Installing AI documentation..."
  mkdir -p "$DATA_DIR/clu/docs/ai"
  cp "$SCRIPT_DIR/docs/ai/"*.md "$DATA_DIR/clu/docs/ai/"
fi

# Without systemd (container, WSL): installed, start by hand
if ! command -v systemctl >/dev/null 2>&1 || [ ! -d /run/systemd/system ]; then
  echo ""
  echo "Virtual CLU installed. systemd not found, start it by hand:"
  echo ""
  echo "  cd $DATA_DIR && $INSTALL_DIR/vclu --auto --data-dir . --web-port $WEB_PORT"
  exit 0
fi

# Systemd service
echo "Installing systemd service..."
sed "s|@INSTALL_DIR@|$INSTALL_DIR|g; s|@DATA_DIR@|$DATA_DIR|g; s|@WEB_PORT@|$WEB_PORT|g" \
  "$SCRIPT_DIR/vclu.service" > /etc/systemd/system/vclu.service
chmod 644 /etc/systemd/system/vclu.service

systemctl daemon-reload
systemctl enable vclu
systemctl start vclu

sleep 2

if systemctl is-active --quiet vclu; then
  LOCAL_IP=$(ip -4 route get 1 2>/dev/null | awk '{print $7; exit}')
  [ -n "$LOCAL_IP" ] || LOCAL_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  [ -n "$LOCAL_IP" ] || LOCAL_IP=localhost
  echo ""
  echo "Virtual CLU installed and running!"
  echo ""
  echo "  Web UI:   http://${LOCAL_IP}:${WEB_PORT}"
  echo ""
  echo "  Status:   sudo systemctl status vclu"
  echo "  Logs:     sudo journalctl -u vclu -f"
  echo "  Restart:  sudo systemctl restart vclu"
  echo "  Stop:     sudo systemctl stop vclu"
else
  echo ""
  echo "Service installed but not running. Check logs:"
  echo "  sudo journalctl -u vclu -n 50"
  exit 1
fi
