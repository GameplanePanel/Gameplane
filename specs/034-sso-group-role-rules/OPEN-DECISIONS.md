# Open decisions: spec 034 SSO group role rules

Settled already (valgul, 2026-11-11, decision cards): groups map to any role through an
ordered rule list, first match wins, old keys keep working; roles set by hand survive
sign-in, SSO-set roles keep syncing, and there is a "Return to SSO control" action.

All seven decisions below were answered by valgul on 2026-11-11.

## OD-1: What if a provider has both old keys and new rules? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, accepted on the bundled decision card for OD-1, OD-3, OD-6 and OD-7.

- A. Refuse to save it (dashboard) or to start (Helm flags) with a clear error. Old keys
  alone are read as rules in the order admin, operator, viewer. The dashboard rewrites
  old keys as rules the next time the provider is saved. (Recommended: one source of
  truth per provider, and no hidden merge order to explain.)
- B. Allow both: rules first, then the old keys as rules after them.

## OD-2: What happens when a rule or fallback names a role that does not exist? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, block it and refuse sign-in.

- A. Block it everywhere it can be blocked, and fail closed where it cannot: the
  dashboard refuses to save such a rule; deleting a custom role is refused while any
  rule, fallback or Helm override uses it; at sign-in, if the first matching rule's role
  is missing (a Helm values typo), the sign-in is refused and the audit log names the
  rule. (Recommended: never hand out a role nobody defined, and never fall through to a
  later rule that might grant more.)
- B. Same blocks, but at sign-in skip the broken rule and keep walking the list.

## OD-3: How does the dashboard override the Helm provider's rules? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, accepted on the bundled decision card for OD-1, OD-3, OD-6 and OD-7.

- A. The override replaces the whole Helm rule list (and fallback role); "Reset to Helm
  default" removes the override. (Recommended: per-role overrides stop making sense once
  rules are an ordered list.)
- B. Keep per-role overrides for the old keys only, and allow no override of Helm rules.

## OD-4: What role source do existing users get when the database upgrades? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, SSO-linked users start as From SSO, local users as Set by hand.

- A. Users linked to an SSO account start as "From SSO"; local users start as "Set by
  hand". Nothing changes at upgrade, and sign-in behaves exactly as today until an admin
  sets someone's role by hand. (Recommended: no surprise change in who gets demoted.)
- B. Every existing user starts as "Set by hand", so no role changes at sign-in until an
  admin returns users to SSO control one by one.

## OD-5: Does "refuse sign-in" still apply to a user whose role was set by hand? (SETTLED: B)

**Ruling (valgul, 2026-11-11):** B, a hand-set user always signs in with their hand-set role, even when no rule matches and the fallback is refuse. This also covers a missing-role rule (OD-2): its refusal applies only to SSO-managed and new users.

The case: the provider's fallback is "refuse sign-in", and a hand-set user is in no
mapped group.

- A. Yes, they are refused. Rules decide who may sign in at all; a hand-set role only
  protects what role they get once in. (Recommended: removing someone from every IdP
  group still locks them out.)
- B. No, a hand-set user always gets in with their hand-set role.

## OD-6: When does the role change after "Return to SSO control"? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, accepted on the bundled decision card for OD-1, OD-3, OD-6 and OD-7.

- A. At the user's next sign-in. Until then they keep their current role, and the Users
  page says "From SSO (applies at next sign-in)". (Recommended: Gameplane only learns the
  user's groups at sign-in, so it has nothing current to compute from before that.)
- B. Immediately, from the groups saved at the user's last sign-in (needs storing every
  user's last groups).

## OD-7: Can the fallback role be any role, including custom ones? (SETTLED: A)

**Ruling (valgul, 2026-11-11):** A, accepted on the bundled decision card for OD-1, OD-3, OD-6 and OD-7.

- A. Yes: any existing role, or "refuse sign-in". (Recommended: matches "any role" for
  rules; a "login-only" fallback is a common setup.)
- B. No: keep viewer, operator, admin or refuse.
