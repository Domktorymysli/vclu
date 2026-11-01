#!/usr/bin/env bash
# What a release ships, scanned before it is published: the Linux binaries (Go toolchain and
# modules, govulncheck) and the Docker image (system packages, Trivy). Run from the repository
# root with the Go version the release builds with.
#
# The binaries are built as GoReleaser builds them but keep their symbols: in a stripped binary
# (-s -w) govulncheck sees only the modules and reports every vulnerable package in them, even
# ones never linked. Same source, toolchain and modules, so the same findings.
set -euo pipefail

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
fail=0

for target in amd64 arm64 arm/6 arm/7; do
  arch=${target%/*}
  arm=; [ "$arch" = arm ] && arm=${target#arm/}
  bin="$out/vclu-linux-$arch$arm"
  echo "=== linux/$target ==="
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=$arm go build -o "$bin" ./cmd/vclu
  .github/govulncheck.sh -mode=binary "$bin" || fail=1
done

# The release image (Dockerfile.release), amd64: the binary GoReleaser would put in its context
echo "=== image ==="
ctx="$out/image"
mkdir -p "$ctx/pkg/lua" "$ctx/docs" "$ctx/deploy"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o "$ctx/vclu" ./cmd/vclu
cp -r web "$ctx/web"
cp -r pkg/lua/runtime "$ctx/pkg/lua/runtime"
cp -r docs/ai "$ctx/docs/ai"
cp deploy/docker-entrypoint.sh "$ctx/deploy/"
docker build -q -f Dockerfile.release -t vclu-scan:release "$ctx" >/dev/null
.github/imagescan.sh vclu-scan:release || fail=1

exit "$fail"
