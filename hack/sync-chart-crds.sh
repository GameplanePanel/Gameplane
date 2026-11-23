#!/usr/bin/env bash
# sync-chart-crds.sh: copy the controller-gen CRDs into the conditional CRD
# subchart and the parent's crd-manifests/, stamping each with a bundle hash.
#
# Purpose:
#   crd-manifests/ is what the chart's crds.autoApply hook applies; the
#   subchart copy is the separately published gameplane-crds chart (the parent
#   chart no longer bundles it). The stamp `gameplane.local/crd-bundle-sha256`
#   records which generated CRD set a live CRD came from.
#
#   The hash covers every generated CRD file, so a schema change to ANY
#   Gameplane CRD changes the stamp on all of them.
#
# Usage:
#   hack/sync-chart-crds.sh     (run by `make manifests`)
#
# CI re-runs it and fails if the committed chart copies differ.
set -euo pipefail

cd "$(dirname "$0")/.."

src=operator/config/crd
annotation=gameplane.local/crd-bundle-sha256

shopt -s nullglob
files=("$src"/gameplane.local_*.yaml)
if [ "${#files[@]}" -eq 0 ]; then
  echo "sync-chart-crds: no CRDs found under $src" >&2
  exit 1
fi
# Glob expansion order depends on the locale; force C-locale order so the
# hash is identical on every machine. A `while read` loop rather than
# `mapfile`, which needs bash 4 (macOS ships bash 3.2).
sorted=()
while IFS= read -r f; do
  sorted+=("$f")
done < <(printf '%s\n' "${files[@]}" | LC_ALL=C sort)
files=("${sorted[@]}")

# sha256sum is GNU coreutils; macOS/BSD ship `shasum -a 256` instead.
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum; }
else
  sha256() { shasum -a 256; }
fi
# Git may check out these text files with CRLF on Windows. Hash and copy the
# canonical LF representation so the stamp matches Linux code generation.
hash=$(sed $'s/\r$//' "${files[@]}" | sha256 | cut -d' ' -f1)

for f in "${files[@]}"; do
  # controller-gen always emits exactly one top-level metadata.annotations
  # block (two-space indent); schema-level `annotations:` keys sit deeper.
  if [ "$(sed $'s/\r$//' "$f" | grep -c '^  annotations:$')" != 1 ]; then
    echo "sync-chart-crds: $f has no single metadata.annotations block to stamp" >&2
    exit 1
  fi
  base=$(basename "$f")
  for dir in charts/gameplane/charts/gameplane-crds/crds charts/gameplane/crd-manifests; do
    awk -v line="    ${annotation}: ${hash}" '
      { sub(/\r$/, ""); print }
      /^  annotations:$/ && !done { print line; done = 1 }
    ' "$f" > "$dir/$base"
  done
done

echo "sync-chart-crds: stamped ${#files[@]} CRDs with ${annotation}=${hash}"
