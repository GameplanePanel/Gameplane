# Contract: audit events

All sign-in events keep today's shape (`POST`, the callback path, target = email, else
username, else subject) and extend the reason string. One event per SSO sign-in that has
mapping on, including refusals (today a refusal and a guard skip write no audit event).

Reason format:

```
oidc role decision: provider=<name> rule=<n|default|none> group=<group|-> role=<role|deny> source=<sso|sso_pending|manual|new> result=<applied|unchanged|kept_manual|guard_last_manager|refused_deny|refused_missing_role|admitted_manual>
```

| result | When | HTTP status in event |
|---|---|---|
| `applied` | New user created, or SSO user's role changed | 200 |
| `unchanged` | SSO user, rule result equals stored role (event only if source moved from `sso_pending`) | 200 |
| `kept_manual` | Hand-set user; rule result differs from stored role | 200 |
| `admitted_manual` | Hand-set user let in although the result was deny or a missing role (OD-5) | 200 |
| `guard_last_manager` | Demotion skipped by the last-manager guard | 200 |
| `refused_deny` | New or SSO user, no match, fallback deny | 403 |
| `refused_missing_role` | New or SSO user, matched rule or fallback names a missing role (OD-2) | 403 |

A hand-set user whose rule result equals their stored role writes no event (nothing to
explain), matching today's "only on change" rule for SSO users.

Setting a role by hand and returning a user to SSO control are both `PATCH /users/{id}`
calls, which the existing audit middleware (`api/cmd/main.go:314`) already records with
actor, path, target and status (FR-017). This spec adds no new mechanism there.
