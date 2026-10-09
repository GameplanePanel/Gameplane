# Open Decisions

**Status**: 3 open (OD-1, OD-2, OD-3); 0 ruled.

Per CLAUDE.md rule 10, an open value MUST NOT be committed as a settled contract until it is ruled here. Work proceeds on each recommended default except where noted.

---

### OD-1: What "module freshness drift" means

**Status**: OPEN

**Question**: Which check does the spec's "module freshness drift" gate (FR-001, SC-001) mean?

**Options**:
1. Submodule pointers (`modules/`, `website/`) must be on their upstream `main`, i.e. no unmerged submodule commit. Behind the tip is fine.
2. Go modules must be on their latest versions.
3. Submodule pointers must equal upstream `main`'s tip.

**Recommended default**: (1). It mechanizes the existing convention that pointer bumps happen only after the submodule PR merges. (2) duplicates Dependabot and turns every PR red on upstream releases; (3) blocks unrelated PRs whenever a submodule moves.

**Blocking**: G6 is not implemented until this is ruled. Every other gate proceeds.

---

### OD-2: hadolint `trustedRegistries`

**Status**: OPEN

**Question**: Should hadolint enforce a trusted-registry allowlist for `FROM` lines?

**Options**:
1. No allowlist; lint style and safety rules only.
2. Allowlist `docker.io`, `gcr.io/distroless`, `ghcr.io/gameplanepanel`, so a new base from any other registry fails.

**Recommended default**: (1) for the first gate PR. (2) can follow as its own change once the current base set is known from the first CI run.

---

### OD-3: When `e2e-postgres` runs

**Status**: OPEN

**Question**: Should the Postgres E2E leg run on every PR with an e2e scope, or only on `master` pushes and manual dispatch?

**Options**:
1. Every e2e-scoped PR (amd64 only).
2. `push: master` + `workflow_dispatch` only.

**Recommended default**: (1). A gate that never runs on PRs catches regressions only after merge. Revisit with (2) if the leg's first green runs show it slower than the slowest existing `e2e-go` leg.
