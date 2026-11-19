#!/usr/bin/env bash
# Fenced physical recovery drill for the disposable PostgreSQL kind job.
# The restored database runs on a new volume. The original claim is retained.
set -euo pipefail
context=$1
viewer=$2
report=$3
[[ $context == kind-gameplane-e2e && $viewer =~ ^[1-9][0-9]*$ ]]
k=(kubectl --context "$context" -n gameplane-system)
fixture_label=$("${k[@]}" get sts gameplane-postgres -o jsonpath='{.metadata.labels.app\.kubernetes\.io/component}')
[[ $fixture_label == e2e-fixture ]]
source=gameplane-postgres-0
target=postgres-pitr
sql() {
    "${k[@]}" exec -i "$1" -- psql -X -qAt -v ON_ERROR_STOP=1 -U gameplane -d gameplane "${@:2}"
}
# Allocate the replacement before fencing. It has a distinct Service selector
# and cannot receive application connections until reconciliation has finished.
"${k[@]}" get pvc "data-$source" -o json | jq --arg name "$target" '{
    apiVersion:"v1",kind:"PersistentVolumeClaim",metadata:{name:$name},
    spec:{accessModes:.spec.accessModes,storageClassName:.spec.storageClassName,
          resources:{requests:{storage:.spec.resources.requests.storage}}}}' | "${k[@]}" create -f -
"${k[@]}" get sts gameplane-postgres -o json | jq --arg name "$target" '{
    apiVersion:"v1",kind:"Pod",metadata:{name:$name,labels:{"app.kubernetes.io/name":$name}},
    spec:{automountServiceAccountToken:false,restartPolicy:"Never",
      securityContext:{runAsNonRoot:true,runAsUser:70,runAsGroup:70,fsGroup:70,seccompProfile:{type:"RuntimeDefault"}},
      containers:[{name:"postgres",image:.spec.template.spec.containers[0].image,
        ports:.spec.template.spec.containers[0].ports,
        command:["sh","-ec","mkdir -p /data/db /data/wal; chmod 0700 /data/db; while [ ! -f /data/start ]; do sleep 1; done; exec postgres -D /data/db"],
        securityContext:{allowPrivilegeEscalation:false,capabilities:{drop:["ALL"]}},
        volumeMounts:[{name:"recovery",mountPath:"/data"}]}],
      volumes:[{name:"recovery",persistentVolumeClaim:{claimName:$name}}]}}' | "${k[@]}" create -f -
"${k[@]}" wait --for=condition=Ready "pod/$target" --timeout=120s
"${k[@]}" exec "$target" -- mkdir -p /data/db /data/wal

# Pin WAL on the original instance between the physical base backup and loss.
sql "$source" -c "SELECT pg_create_physical_replication_slot('gameplane_pitr', true)" >/dev/null
first=$(sql "$source" -c "SELECT pg_walfile_name(restart_lsn) FROM pg_replication_slots WHERE slot_name='gameplane_pitr'")
"${k[@]}" exec "$source" -- sh -ec '
    backup=$(mktemp -d)
    pg_basebackup -U gameplane -D "$backup" -Fp -X stream -c fast
    tar -C "$backup" -cf - .
' |
    "${k[@]}" exec -i "$target" -- tar -xf - -C /data/db
"${k[@]}" exec "$target" -- pg_verifybackup /data/db
# This identity exists only in WAL after the base backup. The external
# revocation ledger says it was revoked after the recovery target.
sql "$source" -v viewer="$viewer" <<'SQL'
INSERT INTO oidc_links(user_id,issuer,subject) VALUES (:viewer,'https://pitr.invalid','revoked-subject');
INSERT INTO oidc_links(user_id,issuer,subject) VALUES (:viewer,'https://retained.pitr.invalid','retained-subject');
SQL
point=$(sql "$source" -c "SELECT pg_create_restore_point('gameplane_fenced_restore')")
point_time=$(date +%s)
sql "$source" <<'SQL'
DELETE FROM oidc_links WHERE issuer='https://pitr.invalid' AND subject='revoked-subject';
INSERT INTO users(username,role) VALUES ('pitr-after-target','viewer');
SQL
loss_time=$(date +%s)
"${k[@]}" scale deployment/gameplane-api --current-replicas=1 --replicas=0
"${k[@]}" wait --for=delete pod -l app.kubernetes.io/name=gameplane-api --timeout=120s
last=$(sql "$source" -c 'SELECT pg_walfile_name(pg_switch_wal())')
[[ $first =~ ^[0-9A-F]{24}$ && $last =~ ^[0-9A-F]{24}$ ]]
# Only closed segments are copied. The slot keeps all files in range.
"${k[@]}" exec "$source" -- sh -ec '
    cd "$PGDATA/pg_wal"
    for file in ????????????????????????; do
        [ "$file" \< "$1" ] || [ "$file" \> "$2" ] || echo "$file"
    done | tar -cf - -T -
' sh "$first" "$last" | "${k[@]}" exec -i "$target" -- tar -xf - -C /data/wal
"${k[@]}" scale sts/gameplane-postgres --current-replicas=1 --replicas=0
"${k[@]}" wait --for=delete "pod/$source" --timeout=120s

"${k[@]}" exec -i "$target" -- sh -ec 'cat >> /data/db/postgresql.auto.conf; touch /data/db/recovery.signal /data/start' <<'CONFIG'
restore_command = 'cp /data/wal/%f %p'
recovery_target_name = 'gameplane_fenced_restore'
recovery_target_action = 'promote'
CONFIG
for _ in {1..90}; do
    if [[ $(sql "$target" -c 'SELECT NOT pg_is_in_recovery()' 2>/dev/null) == t ]]; then break; fi
    sleep 1
done
[[ $(sql "$target" -c 'SELECT NOT pg_is_in_recovery()') == t ]]
[[ $(sql "$target" -c "SELECT count(*) FROM users WHERE username='pitr-after-target'") == 0 ]]
[[ $(sql "$target" -c "SELECT count(*) FROM oidc_links WHERE issuer='https://pitr.invalid' AND subject='revoked-subject' AND user_id=$viewer") == 1 ]]
sql "$target" <<'SQL'
BEGIN;
DELETE FROM oidc_links WHERE issuer='https://pitr.invalid' AND subject='revoked-subject';
DELETE FROM sessions;
DELETE FROM api_tokens;
DELETE FROM api_runtime_keys WHERE purpose='login_device';
UPDATE share_links SET revoked_at=to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') WHERE revoked_at IS NULL;
COMMIT;
SQL
[[ $(sql "$target" -c "SELECT count(*) FROM api_runtime_keys WHERE purpose='login_device'") == 0 ]]
[[ $(sql "$target" -c "SELECT count(*) FROM oidc_links WHERE issuer='https://pitr.invalid'") == 0 ]]
[[ $(sql "$target" -c "SELECT count(*) FROM oidc_links WHERE issuer='https://retained.pitr.invalid' AND subject='retained-subject' AND user_id=$viewer") == 1 ]]
"${k[@]}" patch service gameplane-postgres --type=merge -p '{"spec":{"selector":{"app.kubernetes.io/name":"postgres-pitr"}}}'
"${k[@]}" scale deployment/gameplane-api --current-replicas=0 --replicas=1
"${k[@]}" rollout status deployment/gameplane-api --timeout=180s
jq -n --arg lsn "$point" --argjson point "$point_time" --argjson loss "$loss_time" \
    '{restore_target_lsn:$lsn,recovery_point_age_seconds:($loss-$point),loss_unix_seconds:$loss}' > "$report"
