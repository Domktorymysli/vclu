#!/usr/bin/env bash
# A release's security record, vclu-X.Y.Z-security.json, kept as a release asset (SECURITY.md,
# "Security records"): what was built, with what, every scan of it and what they found. Only
# public information: advisory ids and published GitHub security advisories.
#
#   security-record.sh scan <record> <scan dir> <release|weekly> <tag> <commit> <go version>
#       adds the scan whose results the scan scripts left in <scan dir> (SCAN_OUT, scanlib.sh)
#       and packs their raw output into vclu-X.Y.Z-scan-<time>-<kind>.tar.gz next to <record>;
#       a finding not accepted yet becomes an open advisory. Creates the record if missing.
#   security-record.sh sboms <record> <file>...
#       names the release's SBOMs with their SHA-256
#   security-record.sh fixed <tag> <advisory id> <fixed in tag> [<GHSA id>]
#   security-record.sh not-affected <tag> <advisory id> <reason>
#       closes an open advisory in the record of release <tag> and uploads the record again (gh)
set -euo pipefail

now=$(date -u +%FT%TZ)

case ${1:-} in
scan)
  record=$2 dir=$3 kind=$4 tag=$5 commit=$6 go=$7
  v=${tag#v}
  [ -s "$dir/results.jsonl" ] || { echo "security-record.sh: no results in $dir" >&2; exit 1; }
  raw="vclu-$v-scan-$(date -u +%Y%m%dT%H%MZ)-$kind.tar.gz"
  tar -czf "$(dirname "$record")/$raw" -C "$dir" .

  [ -s "$record" ] || jq -n --arg v "$v" --arg tag "$tag" --arg commit "$commit" --arg go "$go" '
    {schema: 1, product: "vclu", version: $v, tag: $tag, commit: $commit, go: $go,
     policy: "https://github.com/Domktorymysli/vclu/blob/main/SECURITY.md",
     sboms: [], scans: [], advisories: []}' >"$record"

  run=
  [ -z "${GITHUB_RUN_ID:-}" ] || run="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID"
  jq --slurpfile results "$dir/results.jsonl" --arg kind "$kind" --arg date "$now" --arg run "$run" \
    --arg raw "$raw" '
    .scans += [{date: $date, kind: $kind, run: $run,
                result: (if all($results[]; .result == "pass") then "pass" else "fail" end),
                raw: $raw, results: $results}]
    | .advisories += [$results[] as $r | ($r.findings - $r.accepted)[]
        | {id: ., noticed: $date, scanner: $r.scanner, status: "open"}]
    | .advisories |= unique_by(.id)' "$record" >"$record.tmp"
  mv "$record.tmp" "$record"
  ;;
sboms)
  record=$2
  shift 2
  sboms=$(for f in "$@"; do
    jq -n --arg file "$(basename "$f")" --arg sha "$(sha256sum "$f" | cut -d' ' -f1)" '{file: $file, sha256: $sha}'
  done | jq -s .)
  jq --argjson sboms "$sboms" '.sboms = $sboms' "$record" >"$record.tmp"
  mv "$record.tmp" "$record"
  ;;
fixed | not-affected)
  tag=$2 id=$3
  if [ "$1" = fixed ]; then
    change=$(jq -n --arg in "${4:?fixed in which tag}" --arg ghsa "${5:-}" \
      '{status: "fixed", fixed_in: $in} + (if $ghsa == "" then {} else {ghsa: $ghsa} end)')
  else
    change=$(jq -n --arg why "${4:?why it does not affect vclu}" '{status: "not affected", reason: $why}')
  fi
  record="vclu-${tag#v}-security.json"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  gh release download "$tag" -p "$record" -D "$tmp"
  jq -e --arg id "$id" 'any(.advisories[]; .id == $id)' "$tmp/$record" >/dev/null ||
    { echo "security-record.sh: $id is not in $record" >&2; exit 1; }
  jq --arg id "$id" --argjson change "$change" --arg date "$now" '
    .advisories |= map(if .id == $id then . + $change + {closed: $date} else . end)' \
    "$tmp/$record" >"$tmp/new.json"
  mv "$tmp/new.json" "$tmp/$record"
  gh release upload "$tag" "$tmp/$record" --clobber
  ;;
*)
  sed -n '2,14p' "$0" >&2
  exit 2
  ;;
esac
