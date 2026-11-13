# Open decisions: spec 030 security context overrides

All three decisions were answered by valgul on 2026-11-07.

## OD-1: Who may set security overrides? (SETTLED: B)

**Ruling (valgul, 2026-11-07):** B, a new `servers:security` permission, granted to
admin by default and to no other built-in role.

- A. Anyone with `servers:write` on that server's namespace.
- B. A new permission `servers:security`, granted to admin by default and to no other
  built-in role. (Recommended: running a game as another uid, or root, is a node-level
  risk, and a separate key lets an admin hand it out deliberately.)
- C. Admins only, no new permission.

## OD-2: Is run-as user or group 0 (root) allowed as an override? (SETTLED: C)

**Ruling (valgul, 2026-11-07):** C, root overrides are refused until an admin turns on
a cluster-wide setting, which is off by default. Like every override, setting 0 also
needs `servers:security` (OD-1). Clarified 2026-11-13 (valgul): filesystem group 0 counts as root too. A "use image default" override of run-as user or group also needs the setting.

- A. Never: overrides must be 1 or more; a module that needs root simply sets no
  run-as user.
- B. Allowed, but only for users who hold the OD-1 permission, with an extra warning in
  the dashboard. (Recommended: some images, like Palworld's, must start as root to drop
  privileges, and "use image default" already covers most of those; 0 stays possible
  for the rare image that needs it.)
- C. Allowed only if a cluster-wide setting turns it on (off by default).

## OD-3: Does changing an override on an existing server restart it? (SETTLED: A)

**Ruling (valgul, 2026-11-07):** A, a change is stored and applies on the next restart;
the dashboard says so. Saving never restarts the server.

- A. No: it applies on the next restart and the dashboard says so. (Recommended:
  matches how other settings that change the pod behave, and avoids kicking players
  without warning.)
- B. Yes: saving restarts the server right away, after a confirmation.

---

The decisions below were raised while planning (2026-11-13). They are **OPEN**: the
plan builds on the recommended option and says so, but nothing is settled until
valgul rules. Each is isolated, so a different ruling changes one task, not the design.

## OD-4: What happens to saved root overrides when an admin turns the root setting off? (SETTLED: A)

**Ruling (valgul, 2026-11-13):** A, refuse to start them. Running servers keep running;
a server holding a root override does not start while the setting is off, and its status
says why, until the override is cleared or the setting is turned back on.

Raised by CodeRabbit on PR #956. The spec says root values (run-as user, run-as group or
filesystem group 0, or "use image default" for run-as user or group) are refused unless
the cluster setting is on. It does not say what happens to a server that already holds
a saved root override when an admin later turns the setting off.

- A. Refuse to start them: running servers keep running; on the next start a server with
  a root override does not start and its status says why, until the override is cleared
  or the setting is turned back on. (Recommended: fails closed, nothing changes silently,
  and the admin can revoke root at once. Needs the operator to learn the setting, for
  example an API-owned ConfigMap the reconciler reads.)
- B. Fall back to the module default: on the next start the root override is ignored and
  the module's value applies, with a warning in status; the game may lose access to its
  files.
- C. Block turning it off: the setting cannot be switched off while any server still
  holds a root override; the admin clears those first.

Plan impact: A and B need the operator to read the setting (research.md R11 currently
assumes only the API reads it); C adds a cluster-wide scan in the config handler.

## OD-5: How do overrides interact with restricted workload mode? (OPEN, asked 2026-11-13)

The operator's restricted workload mode (`--restricted-workloads`,
`operator/internal/controller/workload_security.go:15`) requires every template to
declare a positive run-as user and filesystem group, and rejects the server otherwise.
An override can now produce a value the mode refuses (a root value, or "use image
default" for run-as user), and the API does not know whether the operator runs in
that mode.

- A. The reconciler validates the effective (desired) values with the same rule and
  reports the server as failed with the existing message until the override is fixed;
  a pod that is already running keeps running with its applied values. (Recommended:
  one rule in one place, the reconciler; matches how an invalid template is handled
  today (`gameserver_controller.go:334`).)
- B. The reconciler ignores an override the mode refuses, applies the module default
  instead, and reports that on the status.
- C. The API also refuses such overrides at write time, which needs the API to be told
  the operator mode (a chart value passed to both).

## OD-6: Is the extra confirmation for a risky value enforced by the API or only by the dashboard? (OPEN, asked 2026-11-13)

FR-006a says overriding a value a module marked as risky "MUST need an extra
confirmation that shows the module's reason". The dashboard can show a confirmation,
but a direct API call has no dialog.

- A. Dashboard only: the dashboard asks, the API accepts the override from any
  `servers:security` holder.
- B. API enforced: a request that sets a risky value must carry an explicit
  acknowledgement (for example a `confirmRisky=true` query parameter), otherwise it is
  refused with a message naming the module's reason.
- C. Dashboard confirmation plus an audit field that records whether the value was
  risky at the time of the change, without refusing anything. (Recommended: the permission,
  the root rule and the audit entry are the enforced boundary; a risky flag is advice
  from a module author, and recording it in the audit costs one word.)

Plan impact if the ruling differs: B adds a template read to the create and update
handlers and one error code; C adds one word to the audit reason.
