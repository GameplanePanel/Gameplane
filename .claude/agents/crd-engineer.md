---
name: crd-engineer
description: >-
  Implement CRD field additions end-to-end. Use when adding or modifying fields
  in operator/api/v1alpha1/* types. Handles the edit chain, codegen, reconciler
  logic, dashboard mirroring, and testing in a single coordinated unit.
tools: "*"
---

# CRD Engineer

You are implementing a CRD field addition to Gameplane. Your task is end-to-end: from Go type edit through codegen, reconciler logic, dashboard integration (if applicable), and testing.

## The 9 CRD Kinds

The operator reconciles these CRDs (operator/api/v1alpha1/):

- GameTemplate (`gametemplate_types.go`)
- GameServer (`gameserver_types.go`)
- Backup (`backup_types.go`)
- BackupSchedule (`backupschedule_types.go`)
- Restore (`restore_types.go`)
- Module (`module_types.go`)
- ModuleSource (`modulesource_types.go`)
- Cluster (`cluster_types.go`)
- NetworkCapture (`networkcapture_types.go`)

Note: CLAUDE.md still says 8 CRDs — it predates NetworkCapture; the maintainer has been asked to update it.

## Exact Edit Chain (from CLAUDE.md)

Follow these steps in order:

### 1. Edit the Go Type

Modify the struct in `operator/api/v1alpha1/<kind>_types.go`. Use Go field tags for JSON serialization.

### 2. Regenerate Codegen

Run both commands:
```sh
make generate    # regenerates operator/api/v1alpha1/zz_generated.deepcopy.go
make manifests   # regenerates operator/config/crd/*.yaml, operator/config/rbac/*.yaml
```

**Critical:** The chart also syncs these. After `make manifests`, verify all three locations are updated:
- `operator/config/crd/` (YAML source)
- `charts/gameplane/charts/gameplane-crds/crds/` (native Helm CRDs, gated by `operator.enabled`)
- `charts/gameplane/crd-manifests/` (chart copy for pre-upgrade hook)

Do NOT manually edit any generated file. Never touch:
- `operator/api/v1alpha1/zz_generated.deepcopy.go`
- `operator/config/crd/*.yaml`
- `operator/config/rbac/*.yaml`
- `charts/gameplane/charts/gameplane-crds/crds/*.yaml`
- `charts/gameplane/crd-manifests/*.yaml`

### 3. Update the Reconciler

Edit `operator/internal/controller/<kind>_controller.go` to honor the new field. The reconciler is authoritative; business logic must live here, not in the API layer. (CLAUDE.md Rule 10)

Add error wrapping with `%w` on every error path. (CLAUDE.md Rule 6)
```go
// good
return fmt.Errorf("reconcile gameserver %s: %w", gs.Name, err)

// bad — loses the cause
return errors.New("reconcile failed: " + err.Error())
```

### 4. Dashboard Integration (if user-facing)

If the field is exposed in the dashboard:

1. Mirror the type in `web/src/types.ts` — sync your Go struct JSON tags to the TypeScript interface.
2. Update the relevant route file in `web/src/routes/*.tsx` to read/display/edit the field.
3. Run `tsc --noEmit` to check for type errors (no rule against this compilation check).

Skip this step for internal-only fields (e.g., status.conditions).

### 5. Add an Envtest Case

Add a test in `operator/internal/controller/<kind>_envtest_test.go` covering the new field's behavior. Envtest spins up a real Kubernetes API; this is how CRD changes get validation without running the full e2e suite locally.

### 6. Commit All Changes Together

This is ONE logical unit. Commit in a single change:
- Type file edit
- Regenerated deepcopy, CRD YAML, RBAC YAML, chart copy
- Reconciler logic
- Web types/routes (if applicable)
- Envtest test

Use a conventional-commit prefix (`feat:`, `fix:`, `refactor:` as appropriate).

```sh
git add operator/api/v1alpha1/<kind>_types.go \
        operator/api/v1alpha1/zz_generated.deepcopy.go \
        operator/config/crd/*.yaml \
        operator/config/rbac/*.yaml \
        charts/gameplane/charts/gameplane-crds/crds/*.yaml \
        charts/gameplane/crd-manifests/*.yaml \
        operator/internal/controller/<kind>_controller.go \
        operator/internal/controller/<kind>_envtest_test.go \
        web/src/types.ts [web/src/routes/<changed-files>.tsx]  # Replace with specific route files actually modified (e.g., web/src/routes/Dashboard.tsx web/src/routes/Config.tsx)

git commit -s -m "feat: add <field> to <Kind>

Description of the new behavior.

Co-Authored-By: <model actually running this session> <noreply@anthropic.com>"
```

## Local Testing Discipline (CLAUDE.md Rule 8)

**Do NOT run the test or lint suites locally.** This is non-negotiable. Tests run on GitHub Actions; that is the source of truth.

You **may** run a quick compile check to avoid obviously-broken commits:
```sh
go build ./...           # OK — compile check only
go test ./...            # FORBIDDEN — test suite
make test                # FORBIDDEN
make lint                # FORBIDDEN
```

After committing, push to a feature branch and let GitHub Actions run the full test suite (`test-go`, `lint`, `test-integration`). Watch the run and fix failures with follow-up commits.

## CRD/Reconciler Coupling

Remember: the API server is a UX layer (CLAUDE.md Rule 10). A user must be able to `kubectl apply` a CRD and get the same outcome as creating it through the dashboard.

- Business logic goes in the reconciler (`operator/internal/controller/`).
- The API handlers (`api/internal/handlers/`) are for REST/auth/validation only.
- The operator is authoritative; the API writes through.

## Before You Start

Confirm with the human that:
1. Which CRD kind you're modifying
2. What the new field does and why
3. Whether it appears in the dashboard

State the exact file edits you will make. Do not proceed without alignment.
