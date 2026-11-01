#!/usr/bin/env bash
# Scans a Docker image's system packages (Alpine) with Trivy: a fixable HIGH or CRITICAL finding
# missing from .github/scan-accepted.txt fails. The Go binary in it is govulncheck's
# (.github/govulncheck.sh), so it is left out here. Run from the repository root.
#   .github/imagescan.sh <image>                      scan
#   .github/imagescan.sh <image> --format spdx-json -o <file>   an SBOM instead
set -euo pipefail

image=${1:?usage: imagescan.sh <image> [trivy options]}
shift

# Trivy pinned by version and checksum (the Linux amd64 build GitHub runners use)
TRIVY_VERSION=0.76.1
TRIVY_SHA256=c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0

dir=${RUNNER_TEMP:-$(mktemp -d)}/trivy-$TRIVY_VERSION
if [ ! -x "$dir/trivy" ]; then
  mkdir -p "$dir"
  tgz="$dir/trivy.tar.gz"
  curl -fsSL --retry 3 -o "$tgz" \
    "https://github.com/aquasecurity/trivy/releases/download/v$TRIVY_VERSION/trivy_${TRIVY_VERSION}_Linux-64bit.tar.gz"
  echo "$TRIVY_SHA256  $tgz" | sha256sum -c -
  tar -xzf "$tgz" -C "$dir" trivy
fi
trivy=$dir/trivy

if [ $# -gt 0 ]; then
  exec "$trivy" image --quiet "$@" "$image"
fi

accepted=.github/scan-accepted.txt
ignore=$(mktemp)
trap 'rm -f "$ignore"' EXIT
grep -oE '^[A-Za-z0-9-]+' "$accepted" >"$ignore" 2>/dev/null || true

"$trivy" image --quiet --scanners vuln --pkg-types os --severity HIGH,CRITICAL --ignore-unfixed \
  --ignorefile "$ignore" --exit-code 1 "$image"
echo "$image: no fixable HIGH or CRITICAL finding outside $accepted."
