# Unreleased operational changes

## Auxiliary resource requests and management placement (#724)

Agent/config-init (25m CPU, 64Mi), sentinel (25m, 32Mi), tunnel (25m, 64Mi), and
Restic backup/init/restore (100m, 128Mi) now request resources on every install.
These are initial scheduling reservations, not workload capacity estimates.
No new CPU or memory limits are imposed. Existing forget/prune limits remain.

Upgrades change generated Pod templates and can restart game/auxiliary pods or
leave them Pending when capacity is insufficient. Review capacity and schedule
the upgrade accordingly. The four `operator.*Resources` chart values provide
complete overrides; explicit empty objects clear defaults, while null/missing
keys use the defaults even with `--reuse-values`.

Chart-managed pods now accept placement, topology-spread and priority settings,
with optional PDBs for Deployments. API remains a singleton and its PDB uses
`maxUnavailable: 1`; node drains can cause API downtime. Operator replicas greater
than one require leader election. Disabling election on a singleton uses Recreate.
See [production placement](production-placement.md) for upgrade controls and a
worker-pool example.
