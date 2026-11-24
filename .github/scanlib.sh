# Sourced by the scan scripts. With SCAN_OUT set (a directory), each scan keeps its raw output
# there and adds one line to $SCAN_OUT/results.jsonl, which .github/security-record.sh turns into
# a release's security record. Without SCAN_OUT nothing is kept.

# scan_raw <name>: the path to keep a scan's raw output at, or nothing without SCAN_OUT
scan_raw() {
  [ -n "${SCAN_OUT:-}" ] || return 0
  mkdir -p "$SCAN_OUT"
  echo "$SCAN_OUT/$(echo "$1" | tr -c 'A-Za-z0-9._-\n' '_')"
}

# scan_result <scanner> <version> <target> <pass|fail> <raw file> <findings> <accepted> [<extra json>]
# findings and accepted are public advisory ids, one per line
scan_result() {
  [ -n "${SCAN_OUT:-}" ] || return 0
  local extra=${8:-}
  [ -n "$extra" ] || extra="{}"
  jq -cn --arg scanner "$1" --arg version "$2" --arg target "$3" --arg result "$4" \
    --arg raw "$(basename "$5")" --arg findings "$6" --arg accepted "$7" --argjson extra "$extra" \
    --arg date "$(date -u +%FT%TZ)" '
    def ids: split("\n") | map(select(. != "")) | unique;
    {scanner: $scanner, version: $version, target: $target, date: $date, result: $result,
     findings: ($findings | ids), accepted: ($accepted | ids), raw: $raw} + $extra' \
    >>"$SCAN_OUT/results.jsonl"
}
