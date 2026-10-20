#!/bin/sh
# check-telemetry-catalog.sh: Verify that telemetryschema/catalog.txt lists
# exactly the official modules in charts/gameplane/values.yaml
# (defaultModuleSource.oci.modules), in the same order (spec 022, research R15).
#
# Purpose:
#   The telemetry receiver folds any game name that is not in the embedded
#   catalog into "custom". If the chart's official module list and the catalog
#   drift apart, a new official module would be counted as custom. This check
#   fails make lint and CI on any difference.
#
# To fix a failure, make catalog.txt match the chart list (one name per line,
#   same order, no blank lines) and keep telemetryschema tests in step.
#
# Test overrides:
#   VALUES_FILE:  values.yaml to read (default: charts/gameplane/values.yaml)
#   CATALOG_FILE: catalog to compare (default: telemetryschema/catalog.txt)
#
# Exit codes:
#   0 = catalog.txt matches values.yaml
#   1 = mismatch (the diff is printed), or a file is missing or yields no modules

set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
VALUES_FILE=${VALUES_FILE:-$ROOT/charts/gameplane/values.yaml}
CATALOG_FILE=${CATALOG_FILE:-$ROOT/telemetryschema/catalog.txt}

# extract_modules prints the names under defaultModuleSource.oci.modules, one
# per line. It is a line-based scan keyed on the chart's fixed indentation:
# a top-level key at column 0, "oci:" at 2 spaces, "modules:" at 4 spaces and
# list items "- name" at 6 spaces.
extract_modules() {
    awk '
        /^defaultModuleSource:/ { top = 1; next }
        top && /^[^ #]/ { exit }
        top && /^  [A-Za-z]/ { oci = ($0 ~ /^  oci:/); mods = 0; next }
        top && oci && /^    modules:/ { mods = 1; next }
        top && oci && mods && /^      - / {
            name = $0
            sub(/^      - /, "", name)
            sub(/[ \t]*#.*$/, "", name)
            sub(/[ \t]+$/, "", name)
            print name
            next
        }
        top && oci && mods && /^    [A-Za-z]/ { mods = 0 }
    ' "$1"
}

for f in "$VALUES_FILE" "$CATALOG_FILE"; do
    if [ ! -f "$f" ]; then
        echo "check-telemetry-catalog: $f not found" >&2
        exit 1
    fi
done

expected=$(mktemp)
trap 'rm -f "$expected"' EXIT HUP INT TERM
extract_modules "$VALUES_FILE" >"$expected"

if [ ! -s "$expected" ]; then
    echo "check-telemetry-catalog: no modules found under defaultModuleSource.oci.modules in $VALUES_FILE" >&2
    exit 1
fi

if diff -u "$expected" "$CATALOG_FILE" >&2; then
    echo "check-telemetry-catalog: catalog.txt matches values.yaml ($(wc -l <"$expected" | tr -d ' ') modules)"
    exit 0
fi
echo "check-telemetry-catalog: telemetryschema/catalog.txt differs from defaultModuleSource.oci.modules in values.yaml (- values.yaml, + catalog.txt)" >&2
exit 1
