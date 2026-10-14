#!/usr/bin/env bash
# Fails on symbol-level govulncheck findings missing from .github/govulncheck-accepted.txt.
# Run from the repository root.
set -euo pipefail

accepted=.github/govulncheck-accepted.txt
out=$(mktemp)
trap 'rm -f "$out"' EXIT

# With -format json govulncheck exits 0 on findings; non-zero means the scan itself failed
go run golang.org/x/vuln/cmd/govulncheck@latest -format json ./... >"$out"

# Symbol level = the trace ends in a function, i.e. our code reaches the vulnerable symbol
found=$(jq -r 'select(.finding.trace[0].function) | .finding.osv' "$out" | sort -u)
known=$(grep -oE '^GO-[0-9]+-[0-9]+' "$accepted" | sort -u || true)
new=$(comm -23 <(echo "$found") <(echo "$known") | sed '/^$/d')

echo "Symbol-level findings: $(echo "${found:-none}" | paste -sd ' ' -)"
if [ -z "$new" ]; then
  echo "All accepted in $accepted."
  exit 0
fi

echo "Not accepted in $accepted:"
for id in $new; do
  jq -r --arg id "$id" 'select(.osv.id == $id) | .osv | "\(.id): \(.summary)\n  https://pkg.go.dev/vuln/\(.id)"' "$out"
  jq -r --arg id "$id" 'select(.finding.osv == $id and .finding.trace[0].function) | .finding.trace[0]
    | "  \(.module)@\(.version) \(.package).\(if .receiver then .receiver + "." else "" end)\(.function)"' "$out" | sort -u
done
exit 1
