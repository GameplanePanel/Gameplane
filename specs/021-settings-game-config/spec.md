# Feature Specification: Edit game configuration from server Settings

**Feature Branch**: `feat/settings-game-config`

**Created**: 2026-10-05

**Status**: Implemented (pending CI + review)

**Input**: Finding #15 (spec drift: `web/specs.md` invariant 8 says Settings renders `spec.configSchema`, but no Settings section edited `spec.config`) and the accepted UX in the session scout notes, with settled answers Q1-Q7 below. Also adds the 404 page frame `Ne5TA`.

## Clarifications

### Session 2026-10-05 (user, accepted UX)

- Q1: How are template password values handled? -> A: The API redacts them (PR #565, marker `__gameplane_redacted__`); the UI is write-only. Moving passwords out of the CR into a Secret stays open (OPEN-DECISIONS OD-1).
- Q2: Restart behaviour? -> A: Always restart on save; no "apply later" option (the operator has no such mode).
- Q3: API-side `configSchema` validation? -> A: No. The operator stays authoritative; the dashboard validates client-side only.
- Q4: Placement? -> A: Separate "Game configuration" section after Version and before Resources.
- Q5: Orphaned keys? -> A: Shown with a "Remove" affordance.
- Q6: `target: file` fields? -> A: Styled with a "Written to file" note; bool fields keep the wizard's select (no switch).
- Q7: Permission? -> A: Gate on `access.canWrite` (`servers:write`), as the rest of Settings.

## User Scenarios & Testing

### User Story 1 - Change a game setting on an existing server (P1)
A server admin opens Settings > Game configuration, changes difficulty and max players, and saves; the server restarts with the new values.
**Independent Test**: edit a field, Save, assert the PUT body's `spec.config` contains only non-default keys.
**Acceptance**: 1. Given a template with a configSchema, the section appears between Version and Resources. 2. Given a value equal to the schema default, the key is not stored. 3. Given an invalid value (e.g. 900 for an int with max 255), the inline error "Must be between 1 and 255." shows and Save is disabled.

### User Story 2 - Passwords stay secret (P1)
**Acceptance**: 1. A stored password renders as an empty input with placeholder "Unchanged — type to replace" and the note "Write-only"; the marker is never visible. 2. Saving without touching it sends the marker back, and the API keeps the stored value. 3. Typing a new value replaces it. 4. A stored OPTIONAL password shows a "Remove password" button; pressing it shows "Password will be removed" with an "Undo" button and the note "Removed when you save. Undo to keep the stored password.", and Save deletes the password (the key is sent absent). Undo restores "unchanged". Required passwords have no Remove button. Emptying a field the user typed in still returns to "unchanged" (an accidental backspace never deletes a password).

### User Story 3 - Clean up after a template change (P2)
**Acceptance**: keys in `spec.config` that the template no longer declares are listed with "No longer in the template" and a Remove button; if the operator reports `invalid config:` the message is shown in a danger alert.

### User Story 4 - Unknown URLs (P3)
**Acceptance**: an unknown path inside the app renders "Page not found" in the app shell with a "Go to dashboard" button; unauthenticated visitors are redirected to `/login` by the layout guard and never see it.

### Edge Cases
- Read-only users (no `servers:write`): fields disabled, no restart alert, footer shows "You need permission to change this server's settings.".
- Template fetch fails or has no schema: section hidden (unless stray keys exist).
- API fail-closed redaction (template unreadable) returns the marker for every key; non-password keys then show an empty input. Accepted, rare.
- A required password with a stored marker is valid without retyping and cannot be removed.
- Removing then typing a new value replaces the password (the removed state ends). Discard or a successful save resets the removed state (the section remounts).

## Requirements

### Functional Requirements
- **FR-001**: `GameConfigSection` (key `config`, label "Game configuration") MUST appear after Version and before Resources when `template.spec.configSchema` has entries, or when `spec.config` has keys (so strays can be removed).
- **FR-002**: Fields MUST render from the schema via the shared `ConfigFields` component, used by both the wizard and Settings, with no per-game branching.
- **FR-003**: The section MUST write only non-default values; a value equal to the default (or empty with no default) removes the key; an empty map is written as absent.
- **FR-004**: Client validation MUST cover required, enum, int (integer, min, max), bool, string/password length; messages as in `web/src/lib/validation.ts`; invalid state MUST disable Save via `onValidityChange`.
- **FR-005**: Password values MUST be write-only; the redaction marker MUST never be rendered. A stored optional password MUST be removable only through the explicit Remove action, with a visible "will be removed" state and Undo before Save (OD-2).
- **FR-006**: The section MUST state that saving restarts the server, and MUST NOT offer an apply-later mode.
- **FR-007**: Orphaned keys MUST be listed with Remove; an `invalid config:` Ready-condition message MUST be surfaced.
- **FR-008**: Without `servers:write` the section MUST be read-only with the permission note.
- **FR-009**: The router MUST render a "Page not found" page inside the app shell for unmatched URLs, with the auth guard unchanged.

### Key Entities
`GameServer.spec.config` (map string to string), `GameTemplate.spec.configSchema[]` (adds client-side knowledge of `min`, `max`, `minLength`, `maxLength`), the redaction marker.

## Success Criteria
- **SC-001**: A game setting can be changed after creation without kubectl.
- **SC-002**: No password value or marker is ever visible in the UI.
- **SC-003**: Web coverage gates (92/76/82/92) still pass.

## Assumptions
- Operator behaviour is unchanged: config changes roll the StatefulSet (`config-hash`).
- Design frames `OC804`, `QFEg9`, `wxINm`, `e9FnC`, `Ne5TA`, `EV8mp` are canonical; deviations: "Written to file" instead of a file path (the schema has none), no per-field Reset, extra invalid-config alert.
