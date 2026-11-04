#!/bin/bash
set -euo pipefail

# Publishes a release's .deb packages to the vCLU APT repository.
# Run by the maintainer on their own machine: the signing key never leaves it.
#
# Usage: deploy/apt/publish.sh <tag>
#   APT_KEY   GPG key id or fingerprint that signs the repository (required)
#   APT_DEST  rsync destination, e.g. user@host:/srv/apt.vclu.pl (required unless --no-upload)
#   APT_DIR   local copy of the repository (default: ~/vclu-apt)
#   SUITE     stable (default) or beta
#   KEEP      versions kept per architecture (default: 5)
#   DEBS_FROM local directory with the .deb files instead of the GitHub release (tests)
# Needs: gh, gpg, docker (apt-ftparchive runs in debian:trixie), rsync

TAG="${1:?usage: $0 <tag> [--no-upload]}"
UPLOAD=1
[ "${2:-}" = --no-upload ] && UPLOAD=0
REPO="Domktorymysli/vclu"
: "${APT_KEY:?set APT_KEY to the signing key id}"
[ "$UPLOAD" = 0 ] || : "${APT_DEST:?set APT_DEST, e.g. user@host:/srv/apt.vclu.pl}"
APT_DIR="${APT_DIR:-$HOME/vclu-apt}"
SUITE="${SUITE:-stable}"
KEEP="${KEEP:-5}"
ARCHES="amd64 arm64 armhf"

POOL="$APT_DIR/pool/main/v/vclu"
DIST="$APT_DIR/dists/$SUITE"
mkdir -p "$POOL"

if [ -n "${DEBS_FROM:-}" ]; then
  cp "$DEBS_FROM"/*.deb "$POOL/"
else
  gh release download "$TAG" --repo "$REPO" --pattern '*.deb' --dir "$POOL" --skip-existing
fi

# Oldest versions out, per architecture
for arch in $ARCHES; do
  find "$POOL" -name "vclu_*_${arch}.deb" | sort -V | head -n "-$KEEP" | while read -r old; do
    echo "Removing $(basename "$old")"
    rm -f "$old"
  done
done

# Indexes: Packages per architecture, then Release over them
rm -rf "$DIST"
docker run --rm -v "$APT_DIR:/repo" -w /repo debian:trixie sh -ec "
  apt-get update -qq >/dev/null && apt-get install -y -qq apt-utils >/dev/null
  for arch in $ARCHES; do
    d=dists/$SUITE/main/binary-\$arch
    mkdir -p \$d
    apt-ftparchive --arch \$arch packages pool/main > \$d/Packages
    gzip -9kf \$d/Packages
  done
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=vCLU -o APT::FTPArchive::Release::Label=vCLU \
    -o APT::FTPArchive::Release::Suite=$SUITE -o APT::FTPArchive::Release::Codename=$SUITE \
    -o 'APT::FTPArchive::Release::Architectures=$ARCHES' -o APT::FTPArchive::Release::Components=main \
    release dists/$SUITE > /tmp/Release
  mv /tmp/Release dists/$SUITE/Release
  chown -R $(id -u):$(id -g) dists"

gpg --batch --yes --local-user "$APT_KEY" --clearsign -o "$DIST/InRelease" "$DIST/Release"
gpg --batch --yes --local-user "$APT_KEY" --armor --detach-sign -o "$DIST/Release.gpg" "$DIST/Release"
gpg --export "$APT_KEY" > "$APT_DIR/vclu.gpg"

echo "Repository in $APT_DIR:"
for arch in $ARCHES; do
  printf '  %-6s %s\n' "$arch" "$(grep -c '^Package:' "$DIST/main/binary-$arch/Packages") version(s)"
done

[ "$UPLOAD" = 1 ] || exit 0
# Packages first, indexes after: a client never sees an index naming a file not there yet
rsync -av "$APT_DIR/pool" "$APT_DIR/vclu.gpg" "$APT_DEST/"
rsync -av --delete "$APT_DIR/dists/" "$APT_DEST/dists/"
rsync -av --delete "$APT_DIR/pool/" "$APT_DEST/pool/"
echo "Published $TAG to $APT_DEST ($SUITE)"
