#!/usr/bin/env bash
# Local vCLU with the demo house: fresh data dir, no CLU, no MQTT.
# Usage: tools/demo/run.sh [port]   (default 18080). Ctrl+C stops it; data is in $DEMO_DIR.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PORT="${1:-18080}"
DEMO_DIR="${DEMO_DIR:-$(mktemp -d -t vclu-demo)}"

go build -o "$DEMO_DIR/vclu" "$ROOT/cmd/vclu"
mkdir -p "$DEMO_DIR/data/clu/modules"
cp -R "$ROOT/tools/demo/modules/." "$DEMO_DIR/data/clu/modules/"
# Templates and static files are looked up relative to the working directory.
ln -sfn "$ROOT/web" "$DEMO_DIR/web"

echo "vCLU demo: http://127.0.0.1:$PORT/d/home  (data: $DEMO_DIR)"
echo "Note: the web server listens on all interfaces, -ip binds only the CLU protocol."
cd "$DEMO_DIR"
# A demo run is no installation: no new-version check
export VCLU_UPDATE_CHECK="${VCLU_UPDATE_CHECK:-off}"
exec ./vclu -data-dir "$DEMO_DIR/data" -web-port "$PORT" -ip 127.0.0.1
