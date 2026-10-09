# Open Decisions

**Status**: 0 open; 3 ruled (OD-1, OD-2, OD-3 on 2026-10-09).

Per CLAUDE.md rule 10, an open value MUST NOT be committed as a settled contract until it is ruled here. Work proceeds on each recommended default except where noted.

---

### OD-1: What "module freshness drift" means

**Status**: RULED (2026-10-09, user)

**Question**: Which check does the spec's "module freshness drift" gate (FR-001, SC-001) mean?

**Options**:
1. Submodule pointers (`modules/`, `website/`) must be on their upstream `main`, i.e. no unmerged submodule commit. Behind the tip is fine.
2. Go modules must be on their latest versions.
3. Submodule pointers must equal upstream `main`'s tip.

**Recommended default**: (1). It mechanizes the existing convention that pointer bumps happen only after the submodule PR merges. (2) duplicates Dependabot and turns every PR red on upstream releases; (3) blocks unrelated PRs whenever a submodule moves.

**Ruling (2026-10-09, user)**: option (1), merged pointers. The `modules/` and `website/` gitlinks must be ancestors of their upstream `main`; being behind the tip is allowed. G6 is unblocked as specified in [contracts/static-gates.md](contracts/static-gates.md#g6-submodule-freshness).

---

### OD-2: hadolint `trustedRegistries`

**Status**: RULED (2026-10-09, user)

**Question**: Should hadolint enforce a trusted-registry allowlist for `FROM` lines?

**Options**:
1. No allowlist; lint style and safety rules only.
2. Allowlist `docker.io`, `gcr.io/distroless`, `ghcr.io/gameplanepanel`, so a new base from any other registry fails.

**Recommended default**: (1) for the first gate PR. (2) can follow as its own change once the current base set is known from the first CI run.

**Ruling (2026-10-09, user)**: option (1), no allowlist. `.hadolint.yaml` carries `failure-threshold: warning` only, with no `trustedRegistries` key and no `ignored:` list.

---

### OD-3: When `e2e-postgres` runs

**Status**: RULED (2026-10-09, user)

**Question**: Should the Postgres E2E leg run on every PR with an e2e scope, or only on `master` pushes and manual dispatch?

**Options**:
1. Every e2e-scoped PR (amd64 only).
2. `push: master` + `workflow_dispatch` only.

**Recommended default**: (1). A gate that never runs on PRs catches regressions only after merge. Revisit with (2) if the leg's first green runs show it slower than the slowest existing `e2e-go` leg.

**Ruling (2026-10-09, user)**: option (1). `e2e-postgres` runs on every PR whose change scope includes e2e (the same `ci_scope.py` output as `e2e-go`), amd64 only.
