#!/usr/bin/env bash
# Accept only the signed index, native-platform manifest or image config
# digest derived from that index. Runtimes differ in which ID they expose.
set -euo pipefail
context="$1"
shift
for component in "$@"; do
  row="$(awk -F '\t' -v name="$component" '$1 == name' "$GAMEPLANE_RELEASE_DIGESTS")"
  test -n "$row"
  IFS=$'\t' read -r _ image index manifest config <<< "$row"
  for ((attempt=0; attempt<60; attempt++)); do
    pods="$(kubectl --context "$context" get pods -A -o json)"
    ids="$(jq -r --arg image "$image" '.items[] | select(.metadata.deletionTimestamp == null)
      | .status.containerStatuses[]? | select(.ready and .image == $image) | .imageID' <<< "$pods")"
    [[ -n "$ids" ]] && break
    sleep 2
  done
  if [[ -z "$ids" ]]; then
    echo "No Ready container running $image in $context" >&2
    exit 1
  fi
  while read -r id; do
    digest="${id##*@}"
    digest="${digest##*://}"
    if [[ "$digest" != "$index" && "$digest" != "$manifest" && "$digest" != "$config" ]]; then
      echo "Unexpected runtime image ID for $image: $id" >&2
      exit 1
    fi
    printf '%s %s %s\n' "$context" "$image" "$id"
    printf '\n- `%s` ran `%s` as `%s`\n' "$context" "$image" "$id" >> "$GITHUB_STEP_SUMMARY"
  done <<< "$ids"
done
