# Open decisions: spec 039 SFTP access to game servers

Only a decision valgul answers is written here as settled. OD-1 to OD-4 were raised on 2026-11-22 and asked as decision cards; OD-5 followed from the OD-3 answer. OD-1 to OD-4 are settled; OD-5 is open. Planning starts once they are settled.

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

## OD-3: How are server folders named in the SFTP root? (SETTLED: A and C together, per cluster, valgul 2026-11-22)

A server is identified by cluster, namespace and name, and names can repeat across namespaces and clusters.

- A. `/<server>` for servers on the local cluster in the default games namespace, and `/_clusters/<cluster>/<namespace>/<server>` for every other server (the `_` prefix cannot collide with a Kubernetes name). (Recommended: one folder deep for the common single-cluster install, still stable and unambiguous everywhere.)
- B. Always `/<cluster>/<namespace>/<server>`. Fully uniform, but three folders deep for everyone.
- C. A per-server login (`<user>.<server>`) that opens straight in that server's files, with no folder tree, as suggested earlier in the issue thread. Needs a separate saved connection per server in the client.

**Settled (valgul's words, 2026-11-22):** "sftp are per cluster so two cluster get 2 different sftp server use the short local and per server login so both are valid". Recorded as:

- Each cluster has its own SFTP endpoint, and it shows only that cluster's servers. Two clusters mean two SFTP addresses. (How a remote cluster's endpoint reaches permissions and files is OD-5.)
- Logging in as `<user>` shows a tree: `/<server>` for servers in the cluster's default games namespace, and `/_namespaces/<namespace>/<server>` for servers in other namespaces. The `_` prefix cannot collide with a Kubernetes name.
- Logging in as `<user>+<server>` (default games namespace) or `<user>+<namespace>/<server>` opens straight in that server's files. The login is split at the last `+`; Gameplane usernames (`[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}`) and Kubernetes names never contain `+`, so the split is unambiguous. The separator is a proposal from planning; valgul's answer settles both login forms, not the character.
- Both forms take the same credentials and the same permission checks.

## OD-4: What happens with a stopped or sleeping server? (SETTLED: C, valgul 2026-11-22)

Its sidecar is not running, so its files cannot be reached the way FR-015 describes. The dashboard Files tab has the same gap today.

- A. The folder is listed, but opening it fails with "server is not running, start it from the dashboard". (Recommended: no new pods or wake side effects; editing files while stopped can be its own spec and would then fix the Files tab too.)
- B. Opening the folder wakes an asleep server (stopped servers still fail as in A). Uses game-server resources just to browse files.
- C. The operator starts a short-lived file-only pod with the server's volumes while the server is stopped, so files can be edited offline. Most useful, but needs new operator logic, care with single-node volumes, and is a larger change than SFTP itself.

**Settled:** C, by decision card. The spec's FR-013 and FR-013a to FR-013e record the defaults planning chose around it (no wake, start ends file access, idle teardown, the Files tab uses the same pod); valgul can change any of them at review.

## OD-5: How does a remote cluster's SFTP endpoint reach permissions and files? (OPEN)

OD-1 routes every SFTP operation through the Gameplane API, and OD-3 puts one SFTP endpoint in each cluster. On a registered remote cluster only the gateway runs; users, roles and the audit log live in the central API, which today reaches the remote cluster and never the other way round.

- A. Every cluster's SFTP endpoint calls the central API, exactly as on the panel's own cluster; the API reaches the files through the remote gateway as it does for the Files tab. Each remote cluster must be able to reach the central API over HTTPS. File data travels remote → central → remote. (Recommended: one component and one code path everywhere, and OD-1's reasons hold on every cluster.)
- B. A remote endpoint sends file data straight to its own cluster's gateway, and asks the central API only to log the user in, check each operation and write the audit entry. Data stays in the remote cluster, but permission handling splits across two paths and the gateway gains a new client.
- C. SFTP runs only on the cluster that hosts the panel API for now; remote clusters get SFTP in a later spec.
