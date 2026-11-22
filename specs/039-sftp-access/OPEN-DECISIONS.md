# Open decisions: spec 039 SFTP access to game servers

Only a decision valgul answers is written here as settled. OD-1 to OD-4 were raised on 2026-11-22 and asked as decision cards. OD-1 and OD-2 are settled; OD-3 and OD-4 are open. Planning starts once they are settled.

## OD-1: Where does the SFTP server run, and how does it reach the sidecars? (SETTLED: A, valgul 2026-11-22)

- A. New optional component (its own image and Deployment, `sftp.enabled`), which logs users in and sends every file operation to the Gameplane API as that user. The API applies roles, ownership and per-cluster grants exactly as for the Files tab, writes the audit log, and reaches local and remote sidecars through its existing proxy and agent gateway. (Recommended: one place enforces permissions and audit, multicluster comes for free, and the agent's mTLS port stays reachable only from the API and operator.)
- B. Built into the API process as a second listener. No new image, but the API pod gets a public SSH port and long-lived transfer sessions next to dashboard traffic.
- C. New component that dials each sidecar directly with its own mTLS client certificate and only asks the API for the user's permissions. One hop less, but permission and audit rules are duplicated, a new network allowance to every game pod is needed, and remote clusters would need their own SFTP install.

## OD-2: Which credentials can an SFTP client log in with? (SETTLED: B with refinements, valgul 2026-11-22)

The dashboard password cannot be used (SSO users have none, and it would bypass SSO). This sets what the profile's SFTP section offers.

- A. SSH public keys only: the user pastes one or more public keys in their profile. Most secure, but many game-server admins expect a password in FileZilla.
- B. SSH public keys and generated SFTP passwords: the dashboard generates a random password once (shown one time, stored hashed, revocable, optional expiry), like personal MCP tokens. (Recommended: works for every client and every user, and a leaked password is revocable without touching the account.)
- C. Generated SFTP passwords only. Simplest screen, but no key-based login for scripted use.

**Settled (valgul's words, 2026-11-22):** "account should be able to add ssh key for sftp and generate a random password for sftp use this password will be hashed and can only be regenerated or revoked it never expire. generated password will be 20-40 char of full valid char". Recorded as:

- A user can add SSH public keys for SFTP.
- A user can have one generated SFTP password. It is stored only as a hash, shown once at generation, and can only be regenerated (replacing the old one) or revoked. It never expires.
- The generated password is 20 to 40 characters long, drawn from every printable ASCII character except space (94 characters). The user picks the length in that range; planning proposes a default.

## OD-3: How are server folders named in the SFTP root? (OPEN)

A server is identified by cluster, namespace and name, and names can repeat across namespaces and clusters.

- A. `/<server>` for servers on the local cluster in the default games namespace, and `/_clusters/<cluster>/<namespace>/<server>` for every other server (the `_` prefix cannot collide with a Kubernetes name). (Recommended: one folder deep for the common single-cluster install, still stable and unambiguous everywhere.)
- B. Always `/<cluster>/<namespace>/<server>`. Fully uniform, but three folders deep for everyone.
- C. A per-server login (`<user>.<server>`) that opens straight in that server's files, with no folder tree, as suggested earlier in the issue thread. Needs a separate saved connection per server in the client.

## OD-4: What happens with a stopped or sleeping server? (OPEN)

Its sidecar is not running, so its files cannot be reached the way FR-015 describes. The dashboard Files tab has the same gap today.

- A. The folder is listed, but opening it fails with "server is not running, start it from the dashboard". (Recommended: no new pods or wake side effects; editing files while stopped can be its own spec and would then fix the Files tab too.)
- B. Opening the folder wakes an asleep server (stopped servers still fail as in A). Uses game-server resources just to browse files.
- C. The operator starts a short-lived file-only pod with the server's volumes while the server is stopped, so files can be edited offline. Most useful, but needs new operator logic, care with single-node volumes, and is a larger change than SFTP itself.
