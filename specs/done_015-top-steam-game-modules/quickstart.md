# Quickstart: Validating & Deploying Top Steam Game Modules

**Feature**: `015-top-steam-game-modules`  
**Date**: 2026-09-03  
**Status**: Completed  

---

## 1. Prerequisites

- Python 3.9+ with `jsonschema` and `pyyaml` installed.
- Access to a Kubernetes / k3s cluster with Gameplane CRDs installed (or local `kubectl`).
- Docker / Podman (optional, for local OCI inspection).

---

## 2. Validation Workflows

### Scenario 1: Run Static Preflight Validation Across All Modules

Verify that all module templates satisfy container user invariants, entrypoint non-shadowing, image digest pinning, and schema conformance:

```bash
# Run preflight validation from the repository root
python3 modules/validate.py

# Expected outcome:
# All 26 modules evaluated with 0 errors and 0 unacknowledged warnings
# Exit code: 0
```

### Scenario 2: Validate a Specific New Module (e.g., `team-fortress-2`)

```bash
# Run validator targeted to a specific module
python3 modules/validate.py --module team-fortress-2

# Verify schema validation on module.yaml and template.yaml
jsonschema -i modules/team-fortress-2/module.yaml modules/.schema/module.schema.json
jsonschema -i modules/team-fortress-2/template.yaml modules/.schema/gametemplate.schema.json
```

### Scenario 3: Verify Mandatory Specification Documentation (Principle IV)

```bash
# Verify that every game directory in modules/ contains a valid specs.md
for mod in modules/*/; do
  if [ -d "$mod" ] && [ -f "$mod/module.yaml" ]; then
    if [ ! -s "$mod/specs.md" ]; then
      echo "ERROR: Missing or empty specs.md in $mod"
      exit 1
    fi
  fi
done
echo "All module specifications present and valid."
```

### Scenario 4: Deploy a Sample GameServer to a Test Cluster

```bash
# Apply the GameTemplate to the cluster
kubectl apply -f modules/team-fortress-2/template.yaml

# Create a GameServer instance from the sample manifest
kubectl apply -f modules/team-fortress-2/samples/gameserver.yaml

# Verify pod scheduling, persistent volume mount, and port readiness
kubectl get gameservers
kubectl get pods -l gameplane.local/game=tf2
```

### Scenario 5: Verify RCON Command & Graceful Save-on-Shutdown

```bash
# Test RCON connectivity via the Gameplane CLI or test utility
kubectl exec -it deployment/gameplane-agent -- gameplane-ctl rcon tf2-sample "status"

# Initiate graceful server termination and inspect pre-stop save logs
kubectl delete gameserver tf2-sample
kubectl logs -l gameplane.local/game=tf2 -c gameserver --tail=50
```
