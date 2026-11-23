#!/usr/bin/env bash
# Render the SFTP endpoint in its modes and check the objects it owns (spec 040, US4).
set -euo pipefail
cd "$(dirname "$0")/../../.."
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
render() { # render <out> [helm --set args...]
  local out="$1"; shift
  helm template gameplane charts/gameplane --namespace gameplane-system "$@" > "$out"
}
render "$scratch/off.yaml" --set sftp.enabled=false
render "$scratch/local.yaml" --set sftp.enabled=true
render "$scratch/nodeport.yaml" --set sftp.enabled=true --set sftp.serviceType=NodePort --set sftp.nodePort=30222 --set sftp.publicAddress=files.example.com:30222
render "$scratch/lb.yaml" --set sftp.enabled=true --set sftp.serviceType=LoadBalancer --set sftp.loadBalancerIP=203.0.113.10
render "$scratch/remote.yaml" --set api.enabled=false --set sftp.enabled=true \
  --set sftp.apiURL=https://panel.example.com --set sftp.serviceTokenSecret=panel-sftp-token
render "$scratch/hostkey.yaml" --set sftp.enabled=true --set sftp.hostKeySecret=panel-host-key
python3 - "$scratch" <<'PY'
import pathlib,sys,yaml
root=pathlib.Path(sys.argv[1])
SFTP_NAMES={'gameplane-sftp','gameplane-sftp-host-key','gameplane-sftp-service-token'}

def docs(name):
    return [d for d in yaml.safe_load_all((root/name).read_text()) if d]

def find(all_docs, kind, name):
    hits=[d for d in all_docs if d.get('kind')==kind and d['metadata']['name']==name]
    assert len(hits)<=1, (kind, name, len(hits))
    return hits[0] if hits else None

def env_of(all_docs):
    dep=find(all_docs,'Deployment','gameplane-sftp')
    assert dep is not None, 'missing Deployment gameplane-sftp'
    return {e['name']:e.get('value') for e in dep['spec']['template']['spec']['containers'][0].get('env') or []}

# 1. Off: nothing the SFTP endpoint owns is rendered, and the API gets no SFTP env.
off=docs('off.yaml')
leftovers=[(d['kind'], d['metadata']['name']) for d in off
           if d.get('kind') in ('Deployment','Service','Secret','NetworkPolicy') and d['metadata']['name'] in SFTP_NAMES]
assert not leftovers, leftovers
for d in off:
    if d.get('kind')=='Deployment' and d['metadata']['name']=='gameplane-api':
        names={e['name'] for c in d['spec']['template']['spec']['containers'] for e in c.get('env') or []}
        assert not any(n.startswith('GAMEPLANE_SFTP_') for n in names), names

# 2. Local, default ClusterIP: Deployment, Service, both Secrets, NetworkPolicy.
local=docs('local.yaml')
svc=find(local,'Service','gameplane-sftp')
assert svc is not None and svc['spec']['type']=='ClusterIP', svc
assert 'nodePort' not in svc['spec']['ports'][0], svc['spec']['ports']
assert svc['spec']['ports'][0]['port']==2222, svc['spec']['ports']
hk=find(local,'Secret','gameplane-sftp-host-key')
assert hk is not None and hk['metadata']['annotations']['helm.sh/resource-policy']=='keep', hk
tok=find(local,'Secret','gameplane-sftp-service-token')
assert tok is not None and tok['metadata']['annotations']['helm.sh/resource-policy']=='keep', tok
env=env_of(local)
assert env['GAMEPLANE_SFTP_API_URL']=='http://gameplane-api.gameplane-system.svc', env
assert env['GAMEPLANE_SFTP_SERVICE_TOKEN_FILE']=='/etc/gameplane/sftp-token/token', env
np=find(local,'NetworkPolicy','gameplane-sftp')
assert np is not None, 'missing NetworkPolicy gameplane-sftp'
assert np['spec']['ingress'][0]['ports']==[{'protocol':'TCP','port':2222}], np['spec']['ingress']

# 3. NodePort: the fixed node port and the public address reach the chart output.
nodeport=docs('nodeport.yaml')
svc=find(nodeport,'Service','gameplane-sftp')
assert svc['spec']['type']=='NodePort', svc['spec']
assert svc['spec']['ports'][0]['nodePort']==30222, svc['spec']['ports']
api=[d for d in nodeport if d.get('kind')=='Deployment' and d['metadata']['name']=='gameplane-api']
assert api, 'missing Deployment gameplane-api'
api_env={e['name']:e.get('value') for c in api[0]['spec']['template']['spec']['containers'] for e in c.get('env') or []}
assert api_env['GAMEPLANE_SFTP_PUBLIC_ADDRESS']=='files.example.com:30222', api_env

# 4. LoadBalancer: the requested external IP is set; no node port.
lb=find(docs('lb.yaml'),'Service','gameplane-sftp')
assert lb['spec']['type']=='LoadBalancer' and lb['spec']['loadBalancerIP']=='203.0.113.10', lb['spec']
assert 'nodePort' not in lb['spec']['ports'][0], lb['spec']['ports']

# 5. Remote (gateway-only, api.enabled=false): the pod logs in to the central panel with the
#    operator's token Secret, the chart makes no local token Secret, and egress is limited to
#    the central API's port.
remote=docs('remote.yaml')
env=env_of(remote)
assert env['GAMEPLANE_SFTP_API_URL']=='https://panel.example.com', env
assert find(remote,'Secret','gameplane-sftp-service-token') is None, 'remote mode must not render the local token Secret'
dep=find(remote,'Deployment','gameplane-sftp')
vols={v['name']:v for v in dep['spec']['template']['spec']['volumes']}
assert vols['service-token']['secret']['secretName']=='panel-sftp-token', vols['service-token']
np=find(remote,'NetworkPolicy','gameplane-sftp')
egress_ports=[p['port'] for rule in np['spec']['egress'] for p in rule.get('ports') or []]
assert 443 in egress_ports, egress_ports

# 6. Host key Secret: the API reads it from the release namespace, so its Role must grant get on that
#    Secret name only. The default name is rendered by the chart; a custom name is supplied by the operator
#    and the API env and the Role both carry it.
def api_env_of(all_docs):
    dep=find(all_docs,'Deployment','gameplane-api')
    assert dep is not None, 'missing Deployment gameplane-api'
    return {e['name']:e.get('value') for c in dep['spec']['template']['spec']['containers'] for e in c.get('env') or []}

def check_host_key_role(all_docs, secret_name):
    role=find(all_docs,'Role','gameplane-api-sftp-host-key')
    assert role is not None, 'missing Role gameplane-api-sftp-host-key'
    assert role['metadata']['namespace']=='gameplane-system', role['metadata']
    assert role['rules']==[{'apiGroups':[''],'resources':['secrets'],'resourceNames':[secret_name],'verbs':['get']}], role['rules']
    rb=find(all_docs,'RoleBinding','gameplane-api-sftp-host-key')
    assert rb is not None and rb['roleRef']['name']=='gameplane-api-sftp-host-key', rb
    assert rb['subjects']==[{'kind':'ServiceAccount','name':'gameplane-api','namespace':'gameplane-system'}], rb['subjects']

check_host_key_role(local, 'gameplane-sftp-host-key')
env=api_env_of(local)
assert env['GAMEPLANE_SFTP_HOST_KEY_SECRET']=='gameplane-sftp-host-key', env
assert env['GAMEPLANE_SFTP_ENABLED']=='true' and env['GAMEPLANE_SFTP_PORT']=='2222', env
assert env['GAMEPLANE_SFTP_PUBLIC_ADDRESS']=='', env

hostkey=docs('hostkey.yaml')
env=api_env_of(hostkey)
assert env['GAMEPLANE_SFTP_HOST_KEY_SECRET']=='panel-host-key', env
check_host_key_role(hostkey, 'panel-host-key')
assert find(hostkey,'Secret','gameplane-sftp-host-key') is None, 'custom hostKeySecret must not render the chart host key'
print('sftp chart checks ok')
PY
