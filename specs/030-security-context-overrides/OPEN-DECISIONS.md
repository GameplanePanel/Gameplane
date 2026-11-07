# Open decisions: spec 030 security context overrides

Each item stays OPEN until valgul answers that exact question.

## OD-1: Who may set security overrides? (SETTLED: B)

**Ruling (valgul, 2026-11-07):** B, a new `servers:security` permission, granted to
admin by default and to no other built-in role.

- A. Anyone with `servers:write` on that server's namespace.
- B. A new permission `servers:security`, granted to admin by default and to no other
  built-in role. (Recommended: running a game as another uid, or root, is a node-level
  risk, and a separate key lets an admin hand it out deliberately.)
- C. Admins only, no new permission.

## OD-2: Is run-as user or group 0 (root) allowed as an override? (OPEN)

- A. Never: overrides must be 1 or more; a module that needs root simply sets no
  run-as user.
- B. Allowed, but only for users who hold the OD-1 permission, with an extra warning in
  the dashboard. (Recommended: some images, like Palworld's, must start as root to drop
  privileges, and "use image default" already covers most of those; 0 stays possible
  for the rare image that needs it.)
- C. Allowed only if a cluster-wide setting turns it on (off by default).

## OD-3: Does changing an override on an existing server restart it? (OPEN)

- A. No: it applies on the next restart and the dashboard says so. (Recommended:
  matches how other settings that change the pod behave, and avoids kicking players
  without warning.)
- B. Yes: saving restarts the server right away, after a confirmation.
