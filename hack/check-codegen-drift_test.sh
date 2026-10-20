#!/usr/bin/env bash
# check-codegen-drift_test.sh: Proof that the "codegen drift" CI gate fails on a
# types edit committed without regenerating (spec 013 G5,
# specs/done_013-expand-test-coverage/contracts/static-gates.md).
#
# The gate is: make generate manifests && make tidy && git diff --exit-code
# run inside a checkout. This script runs it in a throwaway detached worktree:
#
#   Case 1 (must FAIL): a probe commit edits
#     operator/api/v1alpha1/gameserver_types.go (doc comment + json tag of
#     Suspend) without regenerating. The gate must exit non-zero.
#   Case 2 (must PASS): the probe commit is dropped; the clean checkout must
#     pass the gate.
#
# The main working tree is never modified. The worktree and temp dir are
# removed on exit.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."
REPO_ROOT="$(pwd)"

TYPES_FILE="operator/api/v1alpha1/gameserver_types.go"
TAG_OLD='json:"suspend,omitempty"'
TAG_NEW='json:"suspendDriftProbe,omitempty"'
DOC_LINE='// DriftProbe: doc comment added by hack/check-codegen-drift_test.sh.'

tmp="$(mktemp -d)"
wt="$tmp/wt"

cleanup() {
  git -C "$REPO_ROOT" worktree remove --force "$wt" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

# gate DIR: regenerate, tidy, then require a clean tree.
gate() {
  local dir="$1"
  (cd "$dir" && make generate manifests && make tidy && git diff --exit-code)
}

git worktree add --detach "$wt" HEAD >/dev/null

echo "== Case 1: stale types edit committed without regenerating (expect FAIL)"

# Insert the doc comment above the first `Suspend bool` field and rename its
# json tag, on that same line only.
line_no="$(grep -n -m1 -F "	Suspend bool \`$TAG_OLD\`" "$wt/$TYPES_FILE" | cut -d: -f1 || true)"
if [[ -z "$line_no" ]]; then
  echo "✗ could not find the Suspend field in $TYPES_FILE" >&2
  exit 1
fi
awk -v ln="$line_no" -v old="$TAG_OLD" -v new="$TAG_NEW" -v doc="	$DOC_LINE" '
  NR == ln {
    print doc
    sub(old, new)
  }
  { print }
' "$wt/$TYPES_FILE" > "$tmp/types.go"
cat "$tmp/types.go" > "$wt/$TYPES_FILE"

if ! grep -q 'suspendDriftProbe' "$wt/$TYPES_FILE" || ! grep -q 'DriftProbe:' "$wt/$TYPES_FILE"; then
  echo "✗ probe edit did not apply to $TYPES_FILE" >&2
  exit 1
fi

git -C "$wt" -c user.name=drift-probe -c user.email=drift-probe@invalid \
  commit -q --no-gpg-sign -am "drift probe"

# Regeneration itself must succeed, so the failure below is the drift diff
# and not a broken toolchain.
(cd "$wt" && make generate manifests && make tidy)
if git -C "$wt" diff --exit-code; then
  echo "✗ gate passed on a stale types edit" >&2
  exit 1
fi
echo "✓ gate failed on a stale types edit"

echo
echo "== Case 2: clean checkout (expect PASS)"

git -C "$wt" reset -q --hard HEAD~1
git -C "$wt" clean -fdq

if ! gate "$wt"; then
  echo "✗ gate failed on a clean checkout" >&2
  exit 1
fi
echo "✓ gate passed on a clean checkout"
