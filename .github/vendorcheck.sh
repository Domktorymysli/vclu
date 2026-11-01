#!/usr/bin/env bash
# Checks web/static/vendor/ against OSV: every library has a line in VERSIONS, the line matches
# the files, and no known vulnerability is missing from .github/scan-accepted.txt.
# Run from the repository root.
set -euo pipefail

vendor=web/static/vendor
manifest=$vendor/VERSIONS
accepted=.github/scan-accepted.txt
fail=0

lines=$(grep -vE '^\s*(#|$)' "$manifest")

for dir in "$vendor"/*/; do
  name=$(basename "$dir")
  if ! awk -v d="$name" '$1 == d { found = 1 } END { exit !found }' <<<"$lines"; then
    echo "$vendor/$name has no line in $manifest"
    fail=1
  fi
done

queries=()
while read -r dir pkg version file; do
  if [ ! -f "$vendor/$dir/$file" ]; then
    echo "$manifest: $vendor/$dir/$file does not exist"
    fail=1
  elif ! grep -qF "$version" "$vendor/$dir/$file"; then
    echo "$manifest: $vendor/$dir/$file does not name version $version"
    fail=1
  fi
  queries+=("$(jq -cn --arg n "$pkg" --arg v "$version" '{package: {name: $n, ecosystem: "npm"}, version: $v}')")
done <<<"$lines"

body=$(printf '%s\n' "${queries[@]}" | jq -cs '{queries: .}')
results=$(curl -fsS --retry 3 -X POST -H 'Content-Type: application/json' -d "$body" https://api.osv.dev/v1/querybatch)

known=$(grep -oE '^[A-Za-z0-9-]+' "$accepted" 2>/dev/null | sort -u || true)
i=0
while read -r dir pkg version file; do
  for id in $(jq -r --argjson i "$i" '.results[$i].vulns[]?.id' <<<"$results"); do
    vuln=$(curl -fsS --retry 3 "https://api.osv.dev/v1/vulns/$id")
    ids=$(jq -r '[.id] + (.aliases // []) | .[]' <<<"$vuln")
    if grep -qxF -f <(echo "$ids") <<<"$known"; then
      echo "$pkg@$version: $id accepted in $accepted"
      continue
    fi
    echo "$pkg@$version: $id $(jq -r '.summary // ""' <<<"$vuln")"
    echo "  https://osv.dev/vulnerability/$id"
    fail=1
  done
  i=$((i + 1))
done <<<"$lines"

[ "$fail" = 0 ] && echo "Vendored libraries: no known vulnerability outside $accepted."
exit "$fail"
