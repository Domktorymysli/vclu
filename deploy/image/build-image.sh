#!/bin/bash
set -euo pipefail

# Raspberry Pi image with vCLU installed on first boot.
# Takes the official Raspberry Pi OS Lite (arm64), puts the release archive and a one-shot
# installer into its root partition and recompresses it. No root, loop devices or emulation:
# the partition is edited as a file with debugfs.
#
# Usage: deploy/image/build-image.sh <tag> [vclu-linux-arm64.tar.gz]
# Needs: curl, xz, sfdisk, e2fsprogs (debugfs, e2fsck, resize2fs), sha256sum

TAG="${1:?usage: $0 <tag> [archive]}"
ARCHIVE="${2:-}"
REPO="Domktorymysli/vclu"
RPIOS_URL="${RPIOS_URL:-https://downloads.raspberrypi.com/raspios_lite_arm64_latest}"
GROW_MB="${GROW_MB:-256}"

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$PWD/dist}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"

if [ -z "$ARCHIVE" ]; then
  ARCHIVE="$WORK/vclu-linux-arm64.tar.gz"
  curl -fsSL -o "$ARCHIVE" "https://github.com/$REPO/releases/download/$TAG/vclu-linux-arm64.tar.gz"
fi
tar -tzf "$ARCHIVE" install.sh vclu >/dev/null

# Base image, verified against the checksum published next to it
url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$RPIOS_URL")"
echo "Base: $url"
curl -fsSL -o "$WORK/base.img.xz" "$url"
want="$(curl -fsSL "$url.sha256" | awk '{print $1}')"
echo "$want  $WORK/base.img.xz" | sha256sum -c -
xz -d "$WORK/base.img.xz"
IMG="$WORK/base.img"

# Room for the payload: grow the file and the root partition (2)
truncate -s "+${GROW_MB}M" "$IMG"
echo ", +" | sfdisk -q -N 2 --no-reread --no-tell-kernel "$IMG"
read -r start size < <(sfdisk -d "$IMG" | awk -F'[=,]' '/img2 /{gsub(/ /,""); print $2, $4}')

ROOT="$WORK/root.ext4"
dd if="$IMG" of="$ROOT" bs=512 skip="$start" count="$size" status=none
e2fsck -fy "$ROOT" >/dev/null || [ $? -le 1 ]
resize2fs -f "$ROOT" >/dev/null

UNIT=/etc/systemd/system/vclu-firstboot.service
debugfs -w "$ROOT" -f - >/dev/null <<CMDS
mkdir /opt/vclu-firstboot
write $ARCHIVE /opt/vclu-firstboot/vclu-linux-arm64.tar.gz
write $HERE/vclu-firstboot.sh /usr/local/sbin/vclu-firstboot
write $HERE/vclu-firstboot.service $UNIT
symlink /etc/systemd/system/multi-user.target.wants/vclu-firstboot.service $UNIT
sif /opt/vclu-firstboot mode 040700
sif /opt/vclu-firstboot/vclu-linux-arm64.tar.gz mode 0100600
sif /usr/local/sbin/vclu-firstboot mode 0100755
sif $UNIT mode 0100644
CMDS
for p in /opt/vclu-firstboot /opt/vclu-firstboot/vclu-linux-arm64.tar.gz /usr/local/sbin/vclu-firstboot "$UNIT"; do
  debugfs -w -R "sif $p uid 0" "$ROOT" 2>/dev/null
  debugfs -w -R "sif $p gid 0" "$ROOT" 2>/dev/null
done
# debugfs does not fail on a bad command: check what landed
if ! debugfs -R "stat /usr/local/sbin/vclu-firstboot" "$ROOT" 2>/dev/null | grep -q "Mode:  0755" ||
   ! debugfs -R "stat /opt/vclu-firstboot/vclu-linux-arm64.tar.gz" "$ROOT" 2>/dev/null | grep -q "User:     0" ||
   ! debugfs -R "stat /etc/systemd/system/multi-user.target.wants/vclu-firstboot.service" "$ROOT" 2>/dev/null | grep -q symlink; then
  echo "payload not written" >&2
  exit 1
fi
e2fsck -fy "$ROOT" >/dev/null || [ $? -le 1 ]

dd if="$ROOT" of="$IMG" bs=512 seek="$start" conv=notrunc status=none

NAME="vclu-${TAG#v}-rpi-arm64.img"
mv "$IMG" "$OUT/$NAME"
xz -T0 -6 -f "$OUT/$NAME"
(cd "$OUT" && sha256sum "$NAME.xz" > "$NAME.xz.sha256")
echo "Built $OUT/$NAME.xz"
