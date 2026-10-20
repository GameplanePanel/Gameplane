# Data Model: 024 Documentation Refresh, Feature Catalog, and Roadmap Status

No runtime data. The "entities" are structured documentation records.

## Feature catalog entry (row in `FEATURES.md` and `features.mdx`)

| Field | Rule |
|---|---|
| area | The `##` heading the row sits under; one of the areas in [research R6](./research.md#r6-building-the-capability-inventory). |
| capability | Short name of one thing a user or operator can do. Unique within its area. Plain text, no links, no pipe character. |
| maturity | Exactly one of `stable`, `beta`, `experimental`, `planned`, `in progress`. |
| default | `on`, `off` (opt-in), or `n/a` (planned / in progress / tooling with no install default). |
| description | One sentence. May differ between repo and website. |
| docs | Link to the page documenting it. Repo: relative path (`docs/x.md#anchor`). Website: relative docs link (`./x/`). Planned and in-progress rows link to the roadmap. |

Validation (from spec FR-002, Clarification Q1):
- `stable` requires `default = on` and a bucketed e2e test (recorded in `feature-evidence.md`).
- `default = off` forbids `stable`.
- `planned` / `in progress` require `default = n/a` and a roadmap link.
- A withdrawn feature has no row.

## Feature evidence record (`feature-evidence.md`, review aid)

area, capability, maturity, e2e test name or `none`, default source (`values.yaml` key, build tag, module field), notes. One record per catalog row.

## Roadmap item (`docs/roadmap.md`, website `roadmap.mdx`)

| Field | Rule |
|---|---|
| title | Plain words. |
| status | `shipped (<release>)`, `in progress`, `blocked`, `planned`, `out of scope`. |
| reference | Release, spec folder or PR. |
| blocker | Required when status is `blocked`: what blocks it and what unblocks it. |

The roadmap carries one status date. Sections in order: current release and status date; shipped in v0.3.0 and since; in progress (one line per open spec, OD-2); blocking v1; wanted for v1; known gaps; out of scope; join protocol coverage. Historical entries keep their `doc-versions: historical` markers.

State transitions: `planned → in progress → shipped`; `in progress ↔ blocked`; any → `out of scope` only on a maintainer decision.

## Stale fact record (`stale-facts.md`, review aid)

page (repo and path), old value, new value, confirming source (file:line, command output or spec). One record per correction (FR-012).
