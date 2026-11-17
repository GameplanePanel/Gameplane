#!/usr/bin/env bash
# Export Pencil nodes (JSON + 2x PNG) with the pen.dev CLI, for sessions
# without the Pencil MCP (cloud). Read-only: the .pen file is never written.
#
# Usage: pen-cli-export.sh <file.pen> <json-dir> <png-dir> <node-id>... [--depth N]
# Needs: pen on PATH (npm install -g @pen.dev/cli) and PEN_CLI_KEY set.
set -euo pipefail

if [ "$#" -lt 4 ]; then
  echo "usage: $0 <file.pen> <json-dir> <png-dir> <node-id>... [--depth N]" >&2
  exit 2
fi

pen_file=$1 json_dir=$2 png_dir=$3
shift 3
depth=80
ids=()
while [ "$#" -gt 0 ]; do
  case $1 in
    --depth) depth=$2; shift 2 ;;
    *) ids+=("$1"); shift ;;
  esac
done

command -v pen >/dev/null || { echo "pen CLI not found: npm install -g @pen.dev/cli" >&2; exit 1; }
[ -n "${PEN_CLI_KEY:-}" ] || { echo "PEN_CLI_KEY is not set" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$json_dir" "$png_dir"

{
  for id in "${ids[@]}"; do
    printf 'execute({ input: "r=Get(\\"%s\\",{depth:%s,includePathGeometry:true}); Print(\\"@@JSON %s@@\\"+JSON.stringify(r)+\\"@@END@@\\")" })\n' "$id" "$depth" "$id"
    printf 'execute({ input: "Export([\\"%s\\"], \\"png\\", \\"%s\\")" })\n' "$id" "$work"
  done
  echo 'exit()'
} | pen interactive -i "$pen_file" -o "$work/scratch.pen" >"$work/log.txt" 2>&1

python3 -I - "$work/log.txt" "$json_dir" "${ids[@]}" <<'PY'
import json, re, sys
log, out = sys.argv[1], sys.argv[2]
text = open(log, encoding="utf-8").read()
for node_id in sys.argv[3:]:
    m = re.search(r"@@JSON " + re.escape(node_id) + r"@@(.*?)@@END@@", text, re.S)
    if not m:
        sys.exit(f"no JSON for {node_id}; see pen output")
    raw = m.group(1)
    data = json.loads(raw)
    if '"..."' in raw:
        sys.exit(f"{node_id}: elided content, raise --depth")
    with open(f"{out}/{node_id}.json", "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"json {out}/{node_id}.json")
PY

for id in "${ids[@]}"; do
  [ -s "$work/$id.png" ] || { echo "no PNG for $id" >&2; exit 1; }
  mv "$work/$id.png" "$png_dir/$id.png"
  echo "png  $png_dir/$id.png"
done
