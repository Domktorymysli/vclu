#!/usr/bin/env bash
# SPDX SBOM of a release archive or .deb, run by GoReleaser (sboms in .goreleaser.yml) from dist/:
# syft catalogs the unpacked files (the Go modules in the binary), and the web libraries listed in
# web/static/vendor/VERSIONS are added, as they come without a package.json for syft to find.
#   sbom.sh <artifact> <document> <version>
set -euo pipefail

artifact=${1:?usage: sbom.sh <artifact> <document> <version>}
document=${2:?}
version=${3:?}

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
case $artifact in
  *.tar.gz) tar -xzf "$artifact" -C "$dir" ;;
  *.deb) dpkg-deb -x "$artifact" "$dir" ;;
  *) echo "sbom.sh: not an archive or .deb: $artifact" >&2; exit 1 ;;
esac

syft scan "dir:$dir" -q --source-name "$(basename "$artifact")" --source-version "$version" \
  -o "spdx-json=$dir.spdx.json"

vendor=$(find "$dir" -path '*/web/static/vendor/VERSIONS' | head -n 1)
if [ -z "$vendor" ]; then
  mv "$dir.spdx.json" "$document"
  exit 0
fi

grep -vE '^\s*(#|$)' "$vendor" | jq -R -s '
  split("\n") | map(select(. != "") | [splits("\\s+")] | {name: .[1], version: .[2]})' >"$dir.vendor.json"
jq --slurpfile vendor "$dir.vendor.json" '
  (first(.relationships[] | select(.spdxElementId == "SPDXRef-DOCUMENT" and .relationshipType == "DESCRIBES")
    | .relatedSpdxElement)) as $root
  | ($vendor[0] | map(. + {id: ("SPDXRef-Package-npm-vendor-" + (.name | gsub("[^A-Za-z0-9.-]"; "-")))})) as $libs
  | .packages += [$libs[] | {
      name, SPDXID: .id, versionInfo: .version, supplier: "NOASSERTION",
      downloadLocation: "NOASSERTION", filesAnalyzed: false,
      licenseConcluded: "NOASSERTION", licenseDeclared: "NOASSERTION", copyrightText: "NOASSERTION",
      comment: "Copied into web/static/vendor/",
      externalRefs: [{referenceCategory: "PACKAGE-MANAGER", referenceType: "purl",
                      referenceLocator: "pkg:npm/\(.name)@\(.version)"}]}]
  | .relationships += [$libs[] | {spdxElementId: $root, relationshipType: "CONTAINS", relatedSpdxElement: .id}]
  ' "$dir.spdx.json" >"$document"
