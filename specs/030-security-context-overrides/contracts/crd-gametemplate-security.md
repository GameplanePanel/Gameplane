# Contract: GameTemplate `spec.security` (defaults, ownership check, risky flags)

**Requirements**: FR-001, FR-002a, FR-006a, FR-013. **Research**: R3, R8, R14. **Data model**: [data-model.md](../data-model.md).

Go type: `GameSecuritySpec` in `operator/api/v1alpha1/gametemplate_types.go:185` (referenced from `GameTemplateSpec.Security` at `:177`). Same regeneration and commit rule as the GameServer contract (CLAUDE.md rule 7). The JSON Schema for module authors is regenerated from the CRD by `make module-schema` into the module repo's `.schema/gametemplate.schema.json`.

## Shape

```yaml
# modules/palworld/template.yaml (illustration)
spec:
  security:
    fsGroup: 1000
    volumeOwnershipCheck: OnlyWhenWrong   # optional; this is also what omitting it means
    risky:
      runAsUser:
        reason: "The image must start as root to drop privileges; forcing a user breaks its start."
```

| Field | Type | Rule |
|---|---|---|
| `runAsUser`, `runAsGroup`, `fsGroup` | int64, optional | 0 to 4294967295 (unchanged). |
| `volumeOwnershipCheck` | enum, optional | `EveryStart` or `OnlyWhenWrong`. Omitted means `OnlyWhenWrong`. Has an effect only when `fsGroup` is set. |
| `risky` | object, optional | Members `runAsUser`, `runAsGroup`, `fsGroup`, `volumeOwnershipCheck`, each `{ reason?: string }`, `reason` at most 200 characters. |

A `risky` member can name a setting the template does not set. Presence means "overriding this is risky"; an absent member means not risky. There is no way to forbid an override (spec clarification 2026-11-07).

## Behavior

- **Operator**: reads the four values as module defaults (research R4). It never reads `risky`.
- **API**: reads `risky` only to put the marker in the audit row of an override change (research R11); it never refuses a request because of it.
- **API**: no change; `GET /templates` already returns the whole template, so `risky` reaches the dashboard through the template it loads for the wizard and the settings screen.
- **Dashboard**: for each overridden setting whose template has a `risky` member, shows the extra confirmation with `reason` before saving (OD-6, settled C: dashboard only; the API refuses nothing on this basis and the audit row records that the value was risky).
- **Default flip**: a template with a `fsGroup` and no `volumeOwnershipCheck` now means `OnlyWhenWrong`. A module whose data volume depends on every file being re-owned each start sets `volumeOwnershipCheck: EveryStart` (guidance in `docs/module-authoring.md`, FR-013).

## Validators that must accept and check it

| Where | Check |
|---|---|
| CRD | Enum and `reason` length (`MaxLength=200`). |
| `modules/.schema/gametemplate.schema.json` (module repo, regenerated) | Same, for editors. |
| `/home/user/module/validate.py` | `reason` over 200 characters is an error; a `risky` member that is not one of the four settings is an error; `volumeOwnershipCheck` set while the template sets no `fsGroup` is a warning (it has no effect). Existing runAsUser rules (`validate.py:610-660`, `:903-958`) are unchanged. |
| `gp-module/internal/validator/rules.go` | The same three checks, so `gp-module validate` agrees with the CI validator. |
| `gp-module/internal/preview` | The dry-run preview prints the resolved module defaults, the ownership check, and the risky settings with their reasons. |

## Docs

`docs/module-authoring.md`, section "Security context" (`:1137-1173`): the block is now a default; servers may override it; the new fields; when to mark a value risky (the image only works with that uid, or must start as root); how to word a reason (one sentence, what breaks); when to set `EveryStart`. The field table gains `volumeOwnershipCheck` and `risky`.
