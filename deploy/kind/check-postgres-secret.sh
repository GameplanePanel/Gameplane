#!/usr/bin/env bash
# Runs after bootstrap, login and a populated Helm upgrade in e2e-postgres.
set -euo pipefail
k=(kubectl --context "$1" --namespace gameplane-system)
h=(helm --kube-context "$1" --namespace gameplane-system)
deployment="$("${k[@]}" get deployment gameplane-api -o json)"
api="$(jq -ce '.spec.template.spec.containers[] | select(.name == "api")' <<< "$deployment")"
jq -e '
  (.args | all(startswith("--db-dsn") | not)) and
  ([.env[] | select(.name == "GAMEPLANE_DB_DSN")] ==
   [{"name":"GAMEPLANE_DB_DSN","valueFrom":{"secretKeyRef":{"name":"gameplane-postgres","key":"dsn"}}}])
' <<< "$api" >/dev/null

# The password marker belongs only in the separately provisioned Kind fixture.
canary=e2e-postgres-password
pod="$("${k[@]}" get pods -l app.kubernetes.io/name=gameplane-api -o json)"
logs="$("${k[@]}" logs deployment/gameplane-api -c api)"
revisions="$("${h[@]}" history gameplane -o json | jq -r '.[].revision')"
for revision in $revisions; do
  values="$("${h[@]}" get values gameplane --revision "$revision" --all)"
  manifest="$("${h[@]}" get manifest gameplane --revision "$revision")"
  if [[ "$values$manifest" == *"$canary"* ]]; then
    echo "PostgreSQL credential found in Helm release revision $revision" >&2
    exit 1
  fi
done
if [[ "$deployment$pod$logs" == *"$canary"* ]]; then
  echo "PostgreSQL credential found in API metadata or logs" >&2
  exit 1
fi

# Use the installed API image and rendered env selector on disposable pods.
# Break one field at a time without disturbing the healthy API writer.
for field in name key; do
  probe="missing-dsn-$field"
  jq --arg field "$field" --arg probe "$probe" '{
    apiVersion: "v1", kind: "Pod", metadata: {name: $probe},
    spec: {restartPolicy: "Never", automountServiceAccountToken: false,
      containers: [{name: "api", image, imagePullPolicy, args: ["--help"],
        securityContext, env: [.env[] | select(.name == "GAMEPLANE_DB_DSN")
          | .valueFrom.secretKeyRef[$field] = $probe]}]}}' <<< "$api" | "${k[@]}" create -f -
  "${k[@]}" wait --for='jsonpath={.status.containerStatuses[0].state.waiting.reason}=CreateContainerConfigError' \
    "pod/$probe" --timeout=90s
  "${k[@]}" delete pod "$probe" --wait=false
done
