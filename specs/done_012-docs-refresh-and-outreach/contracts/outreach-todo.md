# Contract: External Outreach Tracking (outreach.md)

**Status**: Specification (Ruled 2026-09-02 — Ready for Implementation)  
**Feature Branch**: `012-docs-refresh-and-outreach`  
**Applies To**: `specs/done_012-docs-refresh-and-outreach/outreach.md`

---

## File Location and Purpose (FR-020, Rule 16)

**Path**: `specs/done_012-docs-refresh-and-outreach/outreach.md`  
**Location rationale** (FR-020): The outreach-tracking file lives in the feature spec folder because it is a durable, traceable record of Gameplane's external-visibility submissions — part of the feature's deliverable alongside the docs refresh and comparison table. It is maintained as the authoritative record (SC-013) of submission status for all three target directories.

**Folder rename consequence** (Rule 16): When feature 012 is complete and the folder is renamed from `specs/done_012-docs-refresh-and-outreach/` to `specs/done_012-docs-refresh-and-outreach/`, the link in `docs/contributing.md` (FR-025) MUST be updated in the same commit to point to the new path. The contract itself becomes a binding record of the submission history and remains valid after the rename.

---

## Table Schema

The outreach.md file is a single GitHub-flavored markdown table with this structure:

```markdown
| Target | Submission URL | Status | Submitted Reference | Notes / History |
|--------|----------------|--------|---------------------|-----------------|
| [target name] | [URL or N/A] | [status vocab] | [link/PR#/email date] | [reason or acceptance history] |
```

### Column Definitions

| Column | Content | Notes |
|--------|---------|-------|
| **Target** | Name of external directory | One of: AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes |
| **Submission URL** | The portal URL where submission is made | Per-target; see templates below. May be "N/A" if target is deferred. |
| **Status** | Current submission state (see D-D vocabulary below) | Single most-recent state; all prior states recorded in Notes |
| **Submitted Reference** | Audit trail link for evidence (FR-024) | One of: GitHub PR URL, email date string, AlternativeTo submission link, or "pending" |
| **Notes / History** | Acceptance/rejection reason, date changes, or context | Free text; records full submission history and third-party decisions per FR-023 |

---

## Status Vocabulary and State Machine (D-D, FR-022, SC-014)

Status values are controlled and follow this state machine:

### Allowed Status Values

Each status value adheres to this format:

- **`pending`** — Submission has not been made. Target is eligible and authorized.
- **`in-progress [YYYY-MM-DD]`** — Submission is in flight (draft prepared, account created, form submitted to third party). Date is when work started.
- **`submitted [YYYY-MM-DD]`** — Submission has been completed (PR opened, form submitted, email sent). Date is the submission date. Terminal state.
- **`rejected [YYYY-MM-DD, reason]`** — Third party rejected the submission. Date and reason must be present. Terminal state.
- **`deferred [YYYY-MM-DD, reason]`** — Submission is not currently possible due to blockers (age requirement, eligibility unknown, etc.). Reason explains blocker and may include future-eligibility date. Terminal state.

### Valid State Transitions

```
pending ──> in-progress ──> submitted (terminal)
         │              │
         │              └──> rejected (terminal)
         │
         └──> deferred (terminal)
```

**Note**: A terminal state (`submitted`, `deferred`, `rejected`) cannot transition to another state. A deferred target may be re-evaluated and a new entry added (not state-changed) if circumstances change, but the original deferred entry remains and is not modified.

---

## Terminal States and Success Criteria (SC-014, FR-023)

**Terminal states** (SC-014): A target's status is terminal when it reaches one of: `submitted [date]` or `deferred [date, reason]`. No further status changes are made to that row unless the target is reopened (which requires a new entry and maintainer authorization, not a state change).

**Success definition** (SC-014, FR-023): Feature completion requires each of the three target directories to have a recorded terminal state. Acceptance by the third party (e.g., a PR merged by Awesome-Selfhosted) is **not** a success criterion and is never recorded as a status — it is recorded as a note in the "Notes / History" column only (FR-023).

### Example Terminal States

- `submitted [2026-09-15]` — PR opened to Awesome-Kubernetes on 2026-09-15. Whether or not it is merged is immaterial to the submission status.
- `deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]` — Awesome-Selfhosted blocker documented with future-eligibility date.
- `rejected [2026-09-10, insufficient documentation]` — Third party explicitly declined; reason captured.

---

## Commit Rule (FR-022)

Each status change MUST be committed to git as a separate, single-purpose commit. The commit message MUST:

1. Use the conventional-commit prefix `docs:`
2. State the target and new status in the subject line
3. Include the FR-022 evidence: the subject is exactly one status change, not bundled with unrelated changes

### Commit Message Format

```
docs: outreach [<target>] <status>

Changed <target> status to <status>.
Reason: <brief reason or context>

Co-Authored-By: [model name] <noreply@anthropic.com>
Claude-Session: [session URL]
```

### Example Commits

```
docs: outreach [AlternativeTo] submitted

Changed AlternativeTo status to submitted [2026-09-15].
Submitted via web form at https://alternativeto.net/add-app/
(internal ID pending verification once review completes).

docs: outreach [Awesome-Selfhosted] deferred

Changed Awesome-Selfhosted status from pending to deferred [2026-09-02].
Reason: First release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22.
```

---

## Audit Trail (FR-024)

The "Submitted Reference" column captures evidence of submission for later verification:

| Status | Expected Content in "Submitted Reference" |
|--------|-----------|
| `pending` | "pending" or blank (no submission yet) |
| `in-progress` | Name of file(s) being prepared, or short context (e.g., "account creation in progress") |
| `submitted` | GitHub PR URL (e.g., `https://github.com/awesome-selfhosted/awesome-selfhosted-data/pull/1234`), or email timestamp, or AlternativeTo submission link once available |
| `deferred` | "N/A" or brief cross-reference (e.g., "see OD-6 blocker analysis") |
| `rejected` | "N/A" (reason is in Notes column) |

The reference MUST be traceable: a human or maintainer reviewing the record should be able to verify the claim (e.g., visit the PR, check git history for email date, etc.).

---

## Link from docs/contributing.md (FR-025)

**Section placement**: Add a new section under "## Maintaining Gameplane" or "## Community & Visibility" (or reuse an existing section if appropriate).

**Link text and description**:

```markdown
## Community Visibility & Outreach

[External Directory Submissions](../specs/done_012-docs-refresh-and-outreach/outreach.md)

The project maintains a tracked list of external directory submissions
(AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) to grow
visibility and discoverability. See the outreach tracker for submission
status and history.
```

**Evidence**: FR-025 requires the outreach.md file to be linked from docs/contributing.md so maintainers can find it easily.

---

## Per-Target Submission Templates (R6-derived)

Each target has distinct submission requirements, blockers, and content formats. Below are templates and blocker assessments.

### Target 1: AlternativeTo

**Submission Portal**: https://alternativeto.net (web form, account-based)  
**Initial Status**: `pending` — No blockers identified

**Eligibility Blockers** (R6):

| Criterion | Status | Evidence (path:line) |
|-----------|--------|--------|
| Account requirement | PASS | Free account with verified email; no restrictions. |
| Beta status acceptance | PASS | Policy accepts "open beta" and "public beta"; rejects "Coming Soon" and "Early Access". Gameplane is v0.2.0-beta.8, a public beta. (R6:150–167) |
| License acceptance | PASS | AGPL-3.0-or-later is recognized as "Open Source" license type. (R6:189–190) |
| Required fields available | PASS | Platforms (Kubernetes clusters), License type, Description, Tags, English language all present. (R6:168–175) |

**Submission Template**:

```
Platforms:         Kubernetes (1.28+)
License Type:      Open Source (AGPL-3.0-or-later)
Description:       Kubernetes-native game server control panel. 
                   Features: idle auto-sleep, multi-cluster, RBAC, 
                   restic backups, OIDC auth, OCI game templates.
Tags:              [Kubernetes, Game Servers, DevOps, Self-Hosted, 
                   Control Panel, Container Orchestration]
Website:           https://github.com/GameplanePanel/Gameplane
Repository:        https://github.com/GameplanePanel/Gameplane
```

**OD-5 authorizations required**: Per OD-5, maintainer must create/authorize AlternativeTo account and submit the form. Agent prepares the submission content in outreach.md and updates status as directed.

**Submission URL**: https://alternativeto.net/add-app/

---

### Target 2: Awesome-Selfhosted

**Submission Portal**: https://github.com/awesome-selfhosted/awesome-selfhosted-data (PR to data repo)  
**Initial Status**: `deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]`

**Eligibility Blockers** (R6):

| Criterion | Status | Evidence (path:line) | Blocker |
|-----------|--------|--------|---------|
| 4+ months old | **BLOCKED** | First release 2026-06-22 (CHANGELOG.md:656); 4-month minimum means eligible from 2026-10-22. (CHANGELOG.md:656) | Hard blocker; no workaround |
| Active development | PASS | Daily commits visible; no inactivity. (R6:70) | None |
| Functional & maintained | PASS | Code compiles, CI passes, active contribution. (R6:70–72) | None |
| License accepted | PASS | AGPL-3.0-or-later recognized as free software. (R6:72) | None |
| Not generic container tool | PASS | Domain-specific game control panel, not generic K8s operator. (R6:73) | None |

**Key blocker timeline** (OD-6):
- **Earliest eligibility**: 2026-10-22 (4 months from first release 2026-06-22 per CHANGELOG.md:656)
- **Days remaining**: ~50 days from 2026-09-02
- **Terminal state**: `deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]`

**Deferred reason**: Per OD-6 recommendation, this target is included in outreach.md with a deferred status and future-eligibility date. When the date arrives (2026-10-22), the entry may be re-evaluated (but is not state-changed; a new process would be required).

**Submission Template** (prepared but not submitted until eligible):

```yaml
# awesome-selfhosted-data submission format
# File: software/gameplane.yml

Title:       Gameplane
URL:         https://github.com/GameplanePanel/Gameplane
License:     AGPL-3.0-or-later
Description: |
  Kubernetes-native game server control panel. Features:
  - Idle auto-sleep with configurable wake windows
  - Multi-cluster GameServer registration
  - RBAC with three built-in roles (admin/operator/viewer)
  - Restic backups to S3-compatible storage
  - OIDC authentication (Keycloak, Google, GitHub)
  - OCI bundle game templates via ModuleSource
  - Supports Minecraft, Terraria, and custom games
  - Helm chart deployment on k3s homelab to production
  
  Beta status: feature-complete for v1 scope, stabilized for external testing.

Category:    Games - Administrative Utilities & Control Panels
Tags:        [Kubernetes, Game Servers, Self-Hosted, DevOps, AGPL]
```

**Submission URL**: https://github.com/awesome-selfhosted/awesome-selfhosted-data/pulls

---

### Target 3: Awesome-Kubernetes

**Submission Portal**: https://github.com/ramitsurana/awesome-kubernetes (PR to main README)  
**Initial Status**: `deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]`

**Eligibility Blockers** (R6):

| Criterion | Status | Evidence (path:line) | Blocker |
|-----------|--------|--------|---------|
| 25+ GitHub stars | **UNKNOWN** | Not checked in R6; the project is only ~2 months old (first release 2026-06-22) with correspondingly limited time to accumulate stars/contributors, suggesting likely low counts. (R6:126–127) | **Likely blocker** |
| 3+ contributors | **UNKNOWN** | Not checked; repository appears small. (R6:127) | **Likely blocker** |
| Proper documentation | PASS | Comprehensive docs/, README.md, CONTRIBUTING.md, architecture.md. (R6:128) | None |
| Recognized org exception | NO | Individual repo (GameplanePanel/Gameplane), not org-hosted. Exception does not apply. (R6:129) | None |

**Blocker assessment** (OD-6):
- Metrics eligibility **not verified at time of deferral** (research scope did not include live GitHub metrics check).
- Per OD-6b ruling: defer without pre-check. Do not attempt submission until metrics improve or maintainer explicitly authorizes.
- **Terminal state**: `deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]`

**Deferred reason**: Per OD-6b ruling, eligibility metrics were not pre-checked and this target is deferred without a submission attempt. Revisit in a later release if metrics improve. No pre-check task or PR attempt in this feature.

**Submission Template** (if/when eligible):

```markdown
<!-- PR to awesome-kubernetes/README.md -->

Add under appropriate category (Infrastructure & Container Orchestration, 
Kubernetes Tools, or similar):

- [Gameplane](https://github.com/GameplanePanel/Gameplane) - Kubernetes-native 
  game server control panel with idle auto-sleep, RBAC, restic backups, OIDC auth, 
  and OCI bundle game templates. Self-hosted, runs on k3s homelab to production. 
  AGPL-3.0 open source. [BETA: v0.2.0-beta.8, feature-complete for v1 scope]
```

**Submission URL**: https://github.com/ramitsurana/awesome-kubernetes (PR target)

---

## Proposed Initial Content (OD-5, OD-6 resolved)

The outreach.md file content below is a proposed template. Per OD-5 and OD-6 rulings, this seed content may now be committed as outreach.md during implementation. Agents draft the exact submission content; the maintainer performs the actual submissions and updates status as work progresses.

The outreach.md file is created with this initial content (all three targets with statuses per OD-5/OD-6 rulings):

```markdown
# External Outreach Tracking

**Last updated**: [implement_date]  
**Feature**: 012 — Documentation Refresh, Comparison Table, and External Outreach

This file tracks submissions to external directories to grow Gameplane's 
visibility and discoverability. Status updates are committed to git 
(see [Feature Specification](../spec.md) FR-022 for details).

## Submission Status

| Target | Submission URL | Status | Submitted Reference | Notes / History |
|--------|---|---|---|---|
| AlternativeTo | https://alternativeto.net/add-app/ | pending | pending | No blockers identified. Ready for maintainer account creation and submission. See contract for submission template. |
| Awesome-Selfhosted | https://github.com/awesome-selfhosted/awesome-selfhosted-data/pulls | deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22] | N/A | First release 2026-06-22 (CHANGELOG.md:656); 4-month minimum requires wait until 2026-10-22. See contract for submission template. Re-evaluate on or after 2026-10-22. |
| Awesome-Kubernetes | https://github.com/ramitsurana/awesome-kubernetes | deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release] | N/A | Eligibility metrics (25+ GitHub stars and 3+ contributors) were not verified at deferral. Per OD-6b, no pre-check task or submission attempt in this feature. See contract for eligibility criteria. |

## Next Steps

1. **AlternativeTo**: Maintainer creates free account (requires email verification), prepares submission form with template content from contract, submits.
2. **Awesome-Selfhosted**: Deferred until 2026-10-22 (4-month eligibility date). No action required during this feature; re-evaluate after the date if desired.
3. **Awesome-Kubernetes**: Deferred per OD-6b; no pre-check task or submission attempt. Revisit in a later release if metrics improve or maintainer explicitly authorizes.
```

---

## "Done When" Criteria (SC-012, SC-013, SC-014)

Feature 012 is **complete with respect to outreach tracking** when ALL of the following are true:

### SC-012: Tracking Infrastructure

- [ ] File `specs/done_012-docs-refresh-and-outreach/outreach.md` exists in the repo.
- [ ] File contains a markdown table with columns: Target, Submission URL, Status, Submitted Reference, Notes / History.
- [ ] All three target directories are listed: AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes.
- [ ] Status values use only the vocabulary defined in D-D (no other status strings).
- [ ] Each status change in git history is a separate commit with conventional prefix `docs:` (FR-022).

### SC-013: Maintenance & Linkage

- [ ] File is linked from `docs/contributing.md` with a meaningful section and link text (FR-025).
- [ ] Link uses a path relative to the docs/ directory (e.g., `../specs/done_012-docs-refresh-and-outreach/outreach.md`).
- [ ] Link resolves when the docs are built or read.

### SC-014: Terminal States

- [ ] Each of the three targets (AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes) has one and only one status row.
- [ ] Each row's status is in a terminal state: `submitted [YYYY-MM-DD]` or `deferred [YYYY-MM-DD, reason]`.
- [ ] For `submitted` entries, the "Submitted Reference" column contains a GitHub PR URL, email date, or AlternativeTo submission link.
- [ ] For `deferred` entries, the "Submitted Reference" column is "N/A" and the Notes column explains the deferral reason.
- [ ] Acceptance or rejection by third parties is recorded in the Notes column only; it does not change the status (FR-023).

---

## Open Decisions & Recommendations (tied to OD-5, OD-6)

### OD-5: Submission Authorization (RESOLVED)

**Ruling**: Per OD-5, agents draft the exact submission content (templates, form fields, PR text) in outreach.md and supporting files. The maintainer reviews and performs the actual submission (account creation, form submission, PR opening). Status changes are then committed by the agent or maintainer as submission progresses.

**Implementation consequence**: outreach.md contains ready-to-use submission templates so the maintainer can copy/paste without further research. Seed content may be committed during implementation.

---

### OD-6: Target Eligibility & Acceptance (RESOLVED)

**Ruling**:

- **Awesome-Selfhosted** (OD-6a): Include in outreach.md with `deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]`. No submission attempt during this feature; re-evaluate on or after 2026-10-22.
- **Awesome-Kubernetes** (OD-6b): Include in outreach.md with `deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]`. Defer without pre-check. No submission attempt or pre-check task in this feature.
- **AlternativeTo** (OD-6c): Status `pending` awaiting maintainer account creation and submission. Draft submission form content per OD-5.

**Implementation consequence**: All three targets are included in the outreach.md seed content with terminal statuses. No decision branches or conditional logic required at implementation time.

---

## Complexity Tracking

This contract defines the tracking infrastructure for FR-020 to FR-025 and SC-012 to SC-014. Implementation involves:

- Creating the outreach.md file with seed content (no research required; template provided in this contract).
- Adding the link in docs/contributing.md (one-line addition).
- Status updates committed as work progresses (per FR-022).

**No automated tooling required**; status management is manual and git-tracked.

---

## Evidence Log

| Source | Finding | Location |
|--------|---------|----------|
| Feature Specification | Outreach tracking requirements | spec.md:50–62, FR-020 to FR-025, SC-012 to SC-014 |
| Orchestrator Decision D-D | Status vocabulary and state machine | "ORCHESTRATOR DECISIONS D-D" in session context |
| Orchestrator Decision OD-5 | Submission authorization | "SEEDED OPEN DECISIONS OD-5" in session context |
| Orchestrator Decision OD-6 | Eligibility assessment | "SEEDED OPEN DECISIONS OD-6" in session context |
| Research R6 | External directory eligibility criteria and blockers | R6-outreach.md (full findings) |
| CLAUDE.md Rule 16 | Completed feature folder rename + link update | CLAUDE.md:316–320 |
| CLAUDE.md Rule 11 | Regular commits, one per logical unit | CLAUDE.md:275–305 |
