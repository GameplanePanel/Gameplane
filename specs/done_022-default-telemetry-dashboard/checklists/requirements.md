# Specification Quality Checklist: Default telemetry destination and telemetry dashboard

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-10-06
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Iteration 1 left two clarification markers. In iteration 2 (2026-10-06) the user answered them:
  - Q1: opt-out for new installs, with a first-login notice.
  - Q2: a private receiver-hosted dashboard plus a public summary API limited to headline counts.

  The answers are recorded in spec.md → Clarifications. All items pass.
- Iteration 3 (2026-10-06) added extended telemetry:
  - Q3: all four categories (environment, game usage, feature adoption, install ID).
  - Q4: two tiers, both on by default for new installs.

  FRs were renumbered to FR-001 through FR-034. OD-4 (activity-record expiry) was added. No new markers; all items still pass.
- Iteration 4 (2026-10-06) added report signing at the user's request:
  - Q5: the key is derived from a random secret in the install's database plus the install ID.
  - Q6: mismatches are refused, and the install rotates its ID on a claim conflict.

  It added US7, FR-035 to FR-038 (appended, so earlier numbers are unchanged), SC-014 and SC-015, and updated FR-011, FR-012, FR-013 and FR-015. All items still pass.
- Iteration 5 (2026-10-06), from the analysis remediation during implement:
  - Q7: turning basic off also turns extended off, and the UI disables the extended switch (FR-003, FR-019).
  - Q8: operational metrics move behind the dashboard credential (FR-030).
  - Q9: a provider runbook is added (T093).
  - Q10: the provider dashboard is designed in `telemetry-receiver/telemetry-dashboard.pen`.

  No new markers; all items still pass.
- The domain (OD-1) has been ruled (2026-10-07): telemetry.gameplane.net. The data-handling statement URL (OD-2 dependent, T096 on website side) is https://gameplane.net/telemetry/.
- The Assumptions section cites repo process (Pencil design-first, E2E coverage, `telemetry-receiver/specs.md` upkeep) as governance constraints. These describe process, not implementation, and are kept on purpose.
- Size bands in US4 scenario 3 restate the provider's existing published buckets, so they are a product contract rather than an implementation choice.
