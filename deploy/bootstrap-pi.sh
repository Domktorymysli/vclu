#!/bin/bash
set -e

# Virtual CLU - Raspberry Pi Bootstrap Script
# Run this on fresh Raspberry Pi OS to set everything up automatically

echo "🍓 Virtual CLU - Raspberry Pi Setup"
echo "===================================="
echo ""

# Check if running as root
if [ "$EUID" -ne 0 ]; then
  echo "❌ Please run as root: sudo bash bootstrap-pi.sh"
  exit 1
fi

# Configuration
VCLU_VERSION="${VCLU_VERSION:-latest}"
STATIC_IP="${STATIC_IP:-}"  # Leave empty for DHCP
GATEWAY="${GATEWAY:-192.168.1.1}"
DNS="${DNS:-8.8.8.8}"
INTERFACE="${INTERFACE:-eth0}"  # or wlan0 for WiFi

echo "📋 Configuration:"
echo "   Version: $VCLU_VERSION"
echo "   Interface: $INTERFACE"
if [ -n "$STATIC_IP" ]; then
  echo "   IP: $STATIC_IP (static)"
else
  echo "   IP: DHCP"
fi
echo ""

# Update system
echo "🔄 Updating system packages..."
apt-get update -qq
apt-get upgrade -y -qq

# Install required packages
echo "📦 Installing required packages..."
apt-get install -y -qq \
  curl \
  git \
  ca-certificates \
  sudo

# Configure static IP if requested
if [ -n "$STATIC_IP" ]; then
  echo "🌐 Configuring static IP: $STATIC_IP..."

  cat >> /etc/dhcpcd.conf <<EOF

# Virtual CLU static IP configuration
interface $INTERFACE
static ip_address=$STATIC_IP/24
static routers=$GATEWAY
static domain_name_servers=$DNS
EOF

  echo "✅ Static IP configured (will apply after reboot)"
fi

# Set hostname
echo "🏷️  Setting hostname to 'vclu'..."
hostnamectl set-hostname vclu
sed -i 's/raspberrypi/vclu/g' /etc/hosts

# Enable SSH (if not already)
echo "🔐 Enabling SSH..."
systemctl enable ssh
systemctl start ssh

# Configure firewall (optional)
echo "🔥 Configuring firewall..."
apt-get install -y -qq ufw
ufw --force enable
ufw allow ssh
ufw allow 1234/udp  # CLU communication
ufw allow 69/udp    # TFTP

# Optimize for headless operation
echo "⚡ Optimizing for headless operation..."
systemctl disable bluetooth
systemctl disable hciuart
systemctl disable avahi-daemon  # We don't need mDNS

# Create vclu user (optional, for non-root operation)
if ! id -u vclu >/dev/null 2>&1; then
  echo "👤 Creating vclu user..."
  useradd -r -s /bin/false vclu
fi

# Create directories
echo "📁 Creating directories..."
mkdir -p /opt/vclu
mkdir -p /var/lib/vclu
chown vclu:vclu /var/lib/vclu

# Download and install latest vclu binary
echo "📥 Downloading Virtual CLU..."
# This will be replaced by your actual binary during deployment
echo "   (Binary will be deployed via build-and-deploy.sh)"

# Create a placeholder service
cat > /etc/systemd/system/vclu.service <<'EOF'
[Unit]
Description=Virtual CLU - Grenton CLU Emulator
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/var/lib/vclu
ExecStart=/opt/vclu/vclu --auto
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=vclu
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_BROADCAST

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

# Configure logs rotation
echo "📝 Configuring log rotation..."
cat > /etc/logrotate.d/vclu <<EOF
/var/log/vclu/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0640 vclu vclu
}
EOF

# Set timezone
echo "🕐 Setting timezone to UTC..."
timedatectl set-timezone UTC

# Reduce GPU memory (headless)
echo "🎮 Reducing GPU memory..."
if ! grep -q "gpu_mem=16" /boot/config.txt; then
  echo "gpu_mem=16" >> /boot/config.txt
fi

# Disable unnecessary services
echo "🔇 Disabling unnecessary services..."
systemctl disable triggerhappy || true
systemctl disable bluetooth || true

# Print summary
echo ""
echo "✅ Raspberry Pi setup complete!"
echo ""
echo "📋 Summary:"
echo "   Hostname: vclu"
echo "   Interface: $INTERFACE"
if [ -n "$STATIC_IP" ]; then
  echo "   IP: $STATIC_IP (static, after reboot)"
else
  echo "   IP: $(hostname -I | awk '{print $1}') (DHCP)"
fi
echo "   SSH: Enabled"
echo "   Firewall: Enabled (ports 22, 1234/udp, 69/udp)"
echo ""
echo "📦 Next steps:"
echo "   1. Reboot Pi: sudo reboot"
echo "   2. From your Mac, run:"
echo "      ./build-and-deploy.sh --host vclu.local"
echo "      (or use IP address if mDNS doesn't work)"
echo ""
echo "🎉 Done! Rebooting in 10 seconds..."
sleep 10
reboot