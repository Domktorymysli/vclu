#!/bin/sh
set -eu

# First boot of the vCLU image: install the bundled release, then remove the payload.
# A failure leaves the payload in place and the next boot tries again.

PAYLOAD=/opt/vclu-firstboot

# Keep a hostname set in Raspberry Pi Imager; rename only the stock one
if [ "$(hostname)" = raspberrypi ]; then
  hostnamectl set-hostname vclu
  sed -i 's/\braspberrypi\b/vclu/g' /etc/hosts
  systemctl try-restart avahi-daemon || true
fi

tmp="$(mktemp -d)"
tar -xzf "$PAYLOAD/vclu-linux-arm64.tar.gz" -C "$tmp"
"$tmp/install.sh"
rm -rf "$tmp" "$PAYLOAD"
systemctl disable vclu-firstboot.service
