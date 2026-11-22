#!/usr/bin/env bash
# Render the MCP server RBAC and check that it stays namespaced, read-only and in scope.
set -euo pipefail
cd "$(dirname "$0")/../../.."
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
helm template gameplane charts/gameplane --namespace gameplane-system \
  --set mcpServer.enabled=true --set 'operator.gameNamespaces={tenant-b}' > "$scratch/enabled.yaml"
helm template gameplane charts/gameplane --namespace gameplane-system \
  --set mcpServer.enabled=false > "$scratch/disabled.yaml"
python3 - "$scratch" <<'PY'
import pathlib,sys,yaml
root=pathlib.Path(sys.argv[1])
SA='gameplane-mcp-server'
RELEASE_NS='gameplane-system'
EXPECTED_NS=['gameplane-system','gameplane-games','tenant-b']
READ_VERBS={'get','list','watch'}

def docs(path):
    return [d for d in yaml.safe_load_all(path.read_text()) if d]

def is_sa_subject(subject):
    return subject.get('kind')=='ServiceAccount' and subject.get('name')==SA

def sa_objects(all_docs):
    return [d for d in all_docs if d.get('kind') in ('ClusterRoleBinding','RoleBinding')
            and any(is_sa_subject(s) for s in d.get('subjects') or [])]

enabled=docs(root/'enabled.yaml')
# Map each SA binding to the role it references.
bound=[]
for b in sa_objects(enabled):
    ref=b['roleRef']
    bound.append((b['kind'], b['metadata'].get('namespace'), ref['kind'], ref['name']))

def rules_for(kind, name, namespace=None):
    for d in enabled:
        if d.get('kind')==kind and d['metadata']['name']==name:
            if kind=='Role' and d['metadata'].get('namespace')!=namespace:
                continue
            return d.get('rules') or []
    raise AssertionError(f'missing {kind} {name} {namespace}')

# 1. No ClusterRole bound to the SA grants pods, events or pods/log.
for bkind, bns, rkind, rname in bound:
    if rkind!='ClusterRole':
        continue
    for rule in rules_for('ClusterRole', rname):
        resources=set(rule.get('resources') or [])
        assert not resources & {'pods','events','pods/log'}, (rname, rule)

# 2. Catalog ClusterRole has exactly gametemplates, modules, modulesources.
catalog=[b for b in bound if b[0]=='ClusterRoleBinding' and b[2]=='ClusterRole']
assert len(catalog)==1, catalog
catalog_rules=rules_for('ClusterRole', catalog[0][3])
assert len(catalog_rules)==1, catalog_rules
assert set(catalog_rules[0]['resources'])=={'gametemplates','modules','modulesources'}, catalog_rules
assert catalog_rules[0]['apiGroups']==['gameplane.local'], catalog_rules

# 3. Namespaced Roles with the expected name exist and are bound to the SA, once per namespace.
role_ns=sorted(ns for bkind, ns, rkind, rname in bound
               if bkind=='RoleBinding' and rkind=='Role' and rname=='gameplane-mcp-server-read')
assert role_ns==sorted(EXPECTED_NS), role_ns
for ns in EXPECTED_NS:
    rules_for('Role','gameplane-mcp-server-read',ns)

# 4. No rule bound to the SA has a verb other than get/list/watch (pods/log allows get only).
for bkind, bns, rkind, rname in bound:
    for rule in rules_for(rkind, rname, bns if rkind=='Role' else None):
        verbs=set(rule.get('verbs') or [])
        assert verbs<=READ_VERBS, (rname, rule)
        if set(rule.get('resources') or [])=={'pods/log'}:
            assert verbs=={'get'}, rule

# 5. Deployment env lists exactly the in-scope namespaces.
deployments=[d for d in enabled if d.get('kind')=='Deployment' and d['metadata']['name']=='gameplane-mcp-server']
assert len(deployments)==1
env={e['name']:e.get('value') for e in deployments[0]['spec']['template']['spec']['containers'][0].get('env') or []}
assert sorted(env.get('GAMEPLANE_MCP_NAMESPACES','').split(','))==sorted(EXPECTED_NS), env

# 6. Disabled: no object references the SA.
disabled=docs(root/'disabled.yaml')
leftovers=[(d['kind'], d['metadata']['name']) for d in disabled
           if d.get('kind') in ('ServiceAccount','Deployment','Role','RoleBinding','ClusterRole','ClusterRoleBinding')
           and ('mcp-server' in d['metadata']['name'] or d['metadata']['name']==SA)]
assert not leftovers, leftovers
assert not sa_objects(disabled), sa_objects(disabled)
print('mcp-server RBAC scope ok')
PY
