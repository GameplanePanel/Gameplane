# Open decisions

- **Collisions with an explicit override.** An override `servicePort` that
  another server already holds is honored and recorded, as overrides were
  before pools; neither server is refused. Refusing the override (a condition
  on the server that set it) is the alternative, if maintainers prefer it.
- **An exhausted server's existing Service.** It is left as it was, not
  deleted, so a pool shrink doesn't take down a running server's Service
  before an admin notices the condition. Deleting it is the alternative.
