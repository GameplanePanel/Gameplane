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
  namespace=gameplane-system
  label="gameplane-$component"
  container="$component"
  case "$component" in
    operator) container=manager ;;
    agent) namespace=gameplane-games; label=gameplane-game ;;
    api|web) ;;
    *) echo "Unsupported runtime component: $component" >&2; exit 1 ;;
  esac
  ids=""
  for ((attempt=0; attempt<60; attempt++)); do
    pods="$(kubectl --context "$context" -n "$namespace" get pods -l "app.kubernetes.io/name=$label" -o json)"
    # A matching new replica must not mask another replica still running an old image.
    if ids="$(jq -er --arg image "$image" --arg name "$container" '
      [.items[] | select(.metadata.deletionTimestamp == null) | {
        spec: [.spec.containers[] | select(.name == $name) | .image],
        status: [.status.containerStatuses[]? | select(.name == $name)]
      }] | select(length > 0 and all(.[];
        .spec == [$image] and (.status | length) == 1 and
        .status[0].ready and (.status[0].imageID | type == "string" and length > 0)))
      | .[].status[0].imageID' <<< "$pods")"; then
      break
    fi
    sleep 2
  done
  if [[ -z "$ids" ]]; then
    echo "Expected every $label pod to run a Ready $container from $image in $context" >&2
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
