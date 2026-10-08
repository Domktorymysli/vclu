#!/bin/bash
set -e

# Setup script to run bootstrap on a fresh Raspberry Pi
# This runs FROM YOUR MAC and sets up a new Pi remotely

echo "🍓 Virtual CLU - New Raspberry Pi Setup"
echo "========================================"
echo ""

# Configuration
PI_HOST="${1:-raspberrypi.local}"
PI_USER="${2:-pi}"
STATIC_IP="${3:-}"  # Optional static IP

if [ -z "$PI_HOST" ]; then
  echo "Usage: $0 <pi-hostname-or-ip> [username] [static-ip]"
  echo ""
  echo "Examples:"
  echo "  $0 raspberrypi.local"
  echo "  $0 192.168.1.100"
  echo "  $0 192.168.1.100 pi 192.168.1.100"
  exit 1
fi

echo "Target Pi: $PI_USER@$PI_HOST"
if [ -n "$STATIC_IP" ]; then
  echo "Static IP: $STATIC_IP"
fi
echo ""

# Test connection
echo "🔍 Testing connection to Pi..."
if ! ssh -o ConnectTimeout=5 "$PI_USER@$PI_HOST" "echo Connected" 2>/dev/null; then
  echo "❌ Cannot connect to $PI_USER@$PI_HOST"
  echo ""
  echo "Please ensure:"
  echo "  1. Pi is connected to network"
  echo "  2. SSH is enabled (use raspi-config)"
  echo "  3. Default password is 'raspberry'"
  exit 1
fi

echo "✅ Connected to Pi"
echo ""

# Copy bootstrap script
echo "📤 Copying bootstrap script to Pi..."
scp deploy/bootstrap-pi.sh "$PI_USER@$PI_HOST:/tmp/"

# Run bootstrap
echo "🚀 Running bootstrap on Pi..."
if [ -n "$STATIC_IP" ]; then
  ssh "$PI_USER@$PI_HOST" "sudo STATIC_IP=$STATIC_IP bash /tmp/bootstrap-pi.sh"
else
  ssh "$PI_USER@$PI_HOST" "sudo bash /tmp/bootstrap-pi.sh"
fi

echo ""
echo "⏳ Waiting for Pi to reboot..."
sleep 15

# Wait for Pi to come back online
echo "🔄 Waiting for Pi to come back online..."
MAX_WAIT=60
WAITED=0
NEW_HOST="${STATIC_IP:-vclu.local}"

while [ $WAITED -lt $MAX_WAIT ]; do
  if ssh -o ConnectTimeout=2 "$PI_USER@$NEW_HOST" "echo Ready" 2>/dev/null; then
    echo "✅ Pi is back online!"
    break
  fi
  sleep 2
  WAITED=$((WAITED + 2))
  echo -n "."
done
echo ""

if [ $WAITED -ge $MAX_WAIT ]; then
  echo "⚠️  Pi didn't come back online in time"
  echo "   It might still be booting. Wait a minute and try:"
  if [ -n "$STATIC_IP" ]; then
    echo "   ./build-and-deploy.sh --host $STATIC_IP"
  else
    echo "   ./build-and-deploy.sh --host vclu.local"
  fi
  exit 0
fi

# Deploy vclu
echo ""
echo "📦 Deploying Virtual CLU..."
if [ -n "$STATIC_IP" ]; then
  ./build-and-deploy.sh --host "$STATIC_IP" --user "$PI_USER"
else
  ./build-and-deploy.sh --host vclu.local --user "$PI_USER"
fi

echo ""
echo "🎉 Setup complete!"
echo ""
echo "📋 Your Virtual CLU is now running at:"
if [ -n "$STATIC_IP" ]; then
  echo "   IP: $STATIC_IP"
else
  echo "   Hostname: vclu.local"
  echo "   IP: $(ssh "$PI_USER@$NEW_HOST" "hostname -I | awk '{print \$1}'")"
fi
echo ""
echo "📊 Useful commands:"
echo "   Check status: ssh $PI_USER@${NEW_HOST} 'sudo systemctl status vclu'"
echo "   View logs:    ssh $PI_USER@${NEW_HOST} 'sudo journalctl -u vclu -f'"