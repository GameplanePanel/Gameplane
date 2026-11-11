#!/usr/bin/env bash
# The management bucket owns this disposable kind cluster and its operator.
set -euo pipefail
cluster="${GAMEPLANE_E2E_CLUSTER:?management kind cluster required}"
context="kind-${cluster}"
[[ "$cluster" == gameplane-e2e ]]
[[ "$(kubectl config current-context)" == "$context" ]]
k() { kubectl --context="$context" "$@"; }
ns=gameplane-games
panel=gameplane-system
game=aux-worker-drill
local_game=$game
csi_game=${game}-csi
key=gameplane.local/aux-drill
image=gameplane-test/placement-relay:ci
op=deployment/gameplane-operator
[[ "$(k -n "$panel" get "$op" -o jsonpath='{.spec.template.spec.containers[0].image}')" == gameplane-test/operator:* ]]
work=$(mktemp -d)
mapfile -t workers < <(k get nodes -o json | jq -r '.items[] | select(.metadata.labels["node-role.kubernetes.io/control-plane"] == null and (.spec.unschedulable != true)) | .metadata.name')
[[ ${#workers[@]} == 2 ]]
games=${workers[0]}
aux=${workers[1]}
k get nodes -l node-role.kubernetes.io/control-plane -o json | jq -e 'any(.items[].spec.taints[]; .key=="node-role.kubernetes.io/control-plane" and .effect=="NoSchedule")' >/dev/null
k get "$op" -n "$panel" -o json | jq '[{op:"replace",path:"/spec/template",value:.spec.template}]' > "$work/restore.json"
cleanup() {
  k uncordon "$aux" >/dev/null || true
  for node in "$games" "$aux"; do
    k taint node "$node" "$key:NoSchedule-" >/dev/null || true
    k label node "$node" "$key-" >/dev/null || true
  done
  k delete gameserver "$local_game" "$csi_game" -n "$ns" --ignore-not-found --wait=false
  k delete gametemplate "$local_game" --ignore-not-found --wait=false
  k patch "$op" -n "$panel" --type=json --patch-file "$work/restore.json" >/dev/null
  k rollout status "$op" -n "$panel" --timeout=3m
}
trap cleanup EXIT
wait_for() {
  local until_time=$((SECONDS + 150))
  until "$@"; do
    if (( SECONDS >= until_time )); then
      k get pods -n "$ns" -o wide
      k get events -n "$ns" --sort-by=.lastTimestamp | tail -25
      echo "timed out waiting for $*" >&2
      return 1
    fi
    sleep 2
  done
}
# A local HTTP responder supplies each provider's health contract without
# registering test identities with public relay providers.
cat > "$work/Dockerfile" <<'EOF'
FROM busybox:1.36
ENTRYPOINT ["sh","-ec","mkdir -p /tmp/site; echo ok >/tmp/site/healthz; echo ready >/tmp/site/readyz; exec httpd -f -p 8091 -h /tmp/site"]
EOF
docker build --quiet -t "$image" "$work"
kind load docker-image --name "$cluster" "$image"
k label node "$games" "$key=games"
k label node "$aux" "$key=management"
# Install the upstream Kubernetes CSI distributed reference driver only in
# the disposable kind cluster. The external provisioner and driver manifests
# come from immutable revisions. Game Pods still mount ordinary PVCs.
csi_base=https://raw.githubusercontent.com/kubernetes-csi/csi-driver-host-path/fd04e6e0ba610170ec488864ded0df919986ebdf/deploy/kubernetes-distributed/hostpath
k apply -f https://raw.githubusercontent.com/kubernetes-csi/external-provisioner/12a344a40072d655cb9374a93483305f1a1be557/deploy/kubernetes/rbac.yaml
for component in driverinfo plugin storageclass-fast; do
  k apply -f "$csi_base/csi-hostpath-$component.yaml"
done
k -n default rollout status daemonset/csi-hostpathplugin --timeout=4m
# Provision the node-bound game claim before adding any taint to the worker.
# kind's local-path storage helper does not tolerate arbitrary custom taints.
k apply -f - <<EOF
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata: {name: $game}
spec:
  displayName: Worker placement
  game: worker-placement
  version: '1'
  image: busybox:1.36
  command: [sh, -ec, 'test -f /data/world || cat /proc/sys/kernel/random/uuid >/data/world; exec httpd -f -p 12345 -h /data']
  storage: {size: 64Mi, mountPath: /data}
  resources: {requests: {cpu: 10m, memory: 8Mi}, limits: {memory: 32Mi}}
  ports: [{name: game, containerPort: 12345, advertise: true, protocol: TCP, wakeProtocol: generic}]
---
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata: {name: $game, namespace: $ns}
spec:
  templateRef: {name: $game}
  nodeSelector: {$key: games}
  idle: {enabled: true, afterMinutes: 60, wakeOnConnect: true}
---
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata: {name: $csi_game, namespace: $ns}
spec:
  templateRef: {name: $game}
  nodeSelector: {$key: games}
  storage: {storageClassName: csi-hostpath-fast, size: 64Mi, mountPath: /data}
  idle: {enabled: true, afterMinutes: 60, wakeOnConnect: true}
EOF
world() { k -n "$ns" exec "$game-0" -c game -- cat /data/world; }
has_world() { [[ -n "$(world 2>/dev/null)" ]]; }
wait_for has_world
game=$csi_game
wait_for has_world
game=$local_game
policy=$(jq -nc --arg key "$key" '{poolLabel:$key,poolValue:"games",minCPURequest:"10m",maxCPURequest:"1",minMemoryRequest:"8Mi",maxMemoryLimit:"32Mi"}')
jq -nc --arg key "$key" '{spec:{template:{spec:{nodeSelector:{($key):"management"},tolerations:[{key:$key,operator:"Equal",value:"management",effect:"NoSchedule"}]}}}}' > "$work/pool.json"
k -n "$panel" patch "$op" --type=merge --patch-file "$work/pool.json"
args=$(k -n "$panel" get "$op" -o json | jq -c --arg p "$policy" --arg key "$key" --arg image "$image" '
 .spec.template.spec.containers[0].args | map(select(test("^--(game-workload-policy|auxiliary-worker-pool|tunnel-(frp|tailscale|playit)-image)=") | not)) +
 ["--game-workload-policy="+$p,"--auxiliary-worker-pool="+$key+"=management","--tunnel-frp-image="+$image,"--tunnel-tailscale-image="+$image,"--tunnel-playit-image="+$image] |
 [{op:"replace",path:"/spec/template/spec/containers/0/args",value:.}]')
k -n "$panel" patch "$op" --type=json -p "$args"
k rollout status "$op" -n "$panel" --timeout=3m
for pool in games management; do
  node=$games; [[ "$pool" != management ]] || node=$aux
  k taint node "$node" "$key=$pool:NoSchedule"
done
gsuid=$(k -n "$ns" get gameserver "$game" -o jsonpath='{.metadata.uid}')
k -n "$ns" create secret generic "$game" --from-literal=token=fixture --from-literal=authKey=fixture --from-literal=secretKey=fixture --dry-run=client -o json |
 jq --arg game "$game" --arg uid "$gsuid" '.metadata.ownerReferences=[{apiVersion:"gameplane.local/v1alpha1",kind:"GameServer",name:$game,uid:$uid,controller:true}]' | k apply -f -
patch_game() { k -n "$ns" patch gameserver "$game" --type=merge -p "$1" >/dev/null; }
pod_on() {
  k -n "$ns" get pods -l "app.kubernetes.io/instance=$game,app.kubernetes.io/name=gameplane-$1" -o json |
    jq -er --arg node "$2" '.items[] | select(.metadata.deletionTimestamp==null and .spec.nodeName==$node and any(.status.conditions[]?;.type=="Ready" and .status=="True")) | .metadata.name' | head -1
}
for provider in frp tailscale playit; do
  cfg=$(jq -nc --arg p "$provider" --arg cred "$game" '{enabled:true,provider:$p,credentialsSecretRef:{name:$cred},frp:null,tailscale:null,playit:null} |
  .[$p]=(if $p=="frp" then {serverAddr:"127.0.0.1",remotePorts:[{name:"game",remotePort:12345}]} else {} end) | {spec:{networking:{tunnel:.}}}')
  patch_game "$cfg"
  provider_ready() {
    k -n "$ns" get deployment "$game-tunnel" -o json | jq -e --arg p "$provider" 'any(.spec.template.spec.containers[0].env[]; .name=="TUNNEL_TYPE" and .value==$p)' >/dev/null &&
      k -n "$ns" rollout status "deployment/$game-tunnel" --timeout=8s >/dev/null && pod_on tunnel "$aux" >/dev/null
  }
  wait_for provider_ready
  echo "$provider relay ready on $aux"
done
k drain "$aux" --pod-selector="app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=$game" \
  --ignore-daemonsets --delete-emptydir-data --timeout=3m
unschedulable() {
  k -n "$ns" get pods -l "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=$game" -o json |
    jq -e 'any(.items[]; .metadata.deletionTimestamp==null and any(.status.conditions[]?; .type=="PodScheduled" and .reason=="Unschedulable"))' >/dev/null
}
wait_for unschedulable
k uncordon "$aux"
wait_for pod_on tunnel "$aux"
relay=$(pod_on tunnel "$aux")
nodeIP=$(k get node "$games" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')
exercise_modes() {
  local marker claim pv mode worker endpoint service_type
  marker=$(world)
  claim=$(k -n "$ns" get pvc "$game-data" -o jsonpath='{.metadata.uid}')
  pv=$(k -n "$ns" get pvc "$game-data" -o jsonpath='{.spec.volumeName}')
  k get pv "$pv" -o json | jq -e '.spec.nodeAffinity.required.nodeSelectorTerms | length > 0' >/dev/null
  if [[ "$game" == "$csi_game" ]]; then
    k get pv "$pv" -o json | jq -e '.spec.csi.driver=="hostpath.csi.k8s.io" and (.spec.csi.volumeHandle|length>0)' >/dev/null
  fi
  for mode in ClusterIP NodePort LoadBalancer Hostport; do
    patch_game "$(jq -nc --arg mode "$mode" '{spec:{networking:{expose:$mode}}}')"
    # Wait for this exposure's desired game before parking it.
    mode_ready() {
      service_type=$mode; [[ "$mode" != Hostport ]] || service_type=ClusterIP
      [[ "$(k -n "$ns" get service "$game" -o jsonpath='{.spec.type}')" == "$service_type" ]] &&
      k -n "$ns" get sts "$game" -o json | jq -e --arg mode "$mode" '(.status.readyReplicas==1) and
         (([.spec.template.spec.containers[]|select(.name=="game")|.ports[]|.hostPort // 0]|max > 0)==($mode=="Hostport"))' >/dev/null
    }
    wait_for mode_ready
    k -n "$ns" annotate gameserver "$game" "gameplane.local/idle-asleep-since=$(date -u +%FT%TZ)" --overwrite
    endpoint=$game.$ns.svc:12345; worker=$aux
    if [[ "$mode" == Hostport ]]; then
      worker=$games
      endpoint=$nodeIP:12345
    elif [[ "$mode" == NodePort ]]; then
      endpoint=$nodeIP:$(k -n "$ns" get service "$game" -o jsonpath='{.spec.ports[0].nodePort}')
    elif [[ "$mode" == LoadBalancer ]]; then
      lb_assigned() { [[ -n "$(k -n "$ns" get service "$game" -o jsonpath='{.status.loadBalancer.ingress[0].ip}')" ]]; }
      wait_for lb_assigned
      endpoint=$(k -n "$ns" get service "$game" -o jsonpath='{.status.loadBalancer.ingress[0].ip}'):12345
    fi
    wait_for pod_on waker "$worker"
    parked() {
      [[ "$(k -n "$ns" get sts "$game" -o jsonpath='{.spec.replicas}')" == 0 &&
         -z "$(k -n "$ns" get pod "$game-0" --ignore-not-found -o name)" ]]
    }
    wait_for parked
    join_world() { [[ "$(k -n "$ns" exec "$relay" -- wget -T 6 -qO- "http://$endpoint/world" 2>/dev/null)" == "$marker" ]]; }
    wait_for join_world
    [[ "$(k -n "$ns" get pod "$game-0" -o jsonpath='{.spec.nodeName}')" == "$games" ]]
    [[ "$(k -n "$ns" get pvc "$game-data" -o jsonpath='{.metadata.uid}')" == "$claim" ]]
    echo "$mode waker=$worker relay=$aux game=$games preserved PVC=$claim world=$marker"
  done
}
exercise_modes
# Release the physical host port on this worker before the CSI-backed game
# exercises the same public endpoint.
patch_game '{"spec":{"networking":{"expose":"ClusterIP"}}}'
hostport_released() {
  k -n "$ns" get pod "$game-0" -o json | jq -e '[.spec.containers[]|.ports[]?|.hostPort // 0] | all(. == 0)' >/dev/null
}
wait_for hostport_released
game=$csi_game
exercise_modes
