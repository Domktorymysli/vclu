#!/bin/sh
set -eu

# First boot of the vCLU image: install the bundled package, then remove the payload.
# A failure leaves the payload in place; the unit retries.

PAYLOAD=/opt/vclu-firstboot

# Keep a hostname set in Raspberry Pi Imager; rename only the stock one (not worth failing for)
if [ "$(hostname)" = raspberrypi ]; then
  hostnamectl set-hostname vclu || true
  sed -i 's/\braspberrypi\b/vclu/g' /etc/hosts || true
  systemctl try-restart avahi-daemon || true
fi

if [ -f "$PAYLOAD/vclu.deb" ]; then
  # Enables and starts vCLU on a first install; apt upgrades it from then on
  dpkg -i "$PAYLOAD/vclu.deb"
else
  tmp="$(mktemp -d)"
  tar -xzf "$PAYLOAD/vclu-linux-arm64.tar.gz" -C "$tmp"
  "$tmp/install.sh"
  rm -rf "$tmp"
fi

# The package's postinst does not fail on a service that will not start: check here
i=0
until systemctl is-active --quiet vclu; do
  i=$((i + 1))
  [ "$i" -le 30 ] || { echo "vclu did not start" >&2; exit 1; }
  sleep 2
done

rm -rf "$PAYLOAD"
systemctl disable vclu-firstboot.service
