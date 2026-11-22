# Open decisions: spec 039 SFTP access to game servers

Only a decision valgul answers is written here as settled. OD-1 to OD-4 were raised on 2026-11-22 and asked as decision cards; OD-5 followed from the OD-3 answer. OD-1 to OD-5 are settled. Planning raised OD-6 and OD-7 on 2026-11-22: both are settled. No decision is open. Planning starts once they are settled.

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

## OD-5: How does a remote cluster's SFTP endpoint reach permissions and files? (SETTLED: A, valgul 2026-11-22)

OD-1 routes every SFTP operation through the Gameplane API, and OD-3 puts one SFTP endpoint in each cluster. On a registered remote cluster only the gateway runs; users, roles and the audit log live in the central API, which today reaches the remote cluster and never the other way round.

- A. Every cluster's SFTP endpoint calls the central API, exactly as on the panel's own cluster; the API reaches the files through the remote gateway as it does for the Files tab. Each remote cluster must be able to reach the central API over HTTPS. File data travels remote → central → remote. (Recommended: one component and one code path everywhere, and OD-1's reasons hold on every cluster.)
- B. A remote endpoint sends file data straight to its own cluster's gateway, and asks the central API only to log the user in, check each operation and write the audit entry. Data stays in the remote cluster, but permission handling splits across two paths and the gateway gains a new client.
- C. SFTP runs only on the cluster that hosts the panel API for now; remote clusters get SFTP in a later spec.

## OD-6: How large can one SFTP upload be? (SETTLED: B, valgul 2026-11-22)

Raised by planning. The dashboard and the agent cap one upload at 64 MiB today (`agent/internal/files/files.go`, `maxUploadFileBytes`), and the gateway caps request bodies too. Worlds and modpacks are often bigger, which is the main reason #1051 asks for SFTP.

- A. A new streaming upload route on the agent, API and gateway used by SFTP only, with its own Helm limit (`sftp.maxUploadSize`, default 20 GiB) and a free-space check before writing. The dashboard keeps 64 MiB. (Recommended: SFTP can move whole worlds, and the dashboard's tested upload path does not change.)
- B. Same as A, and the dashboard upload also moves to the streaming route with the same limit. Bigger change, needs a design pass for progress on large uploads.
- C. Keep 64 MiB everywhere. Smallest change, but worlds must be split or uploaded another way.

**Settled:** B, by decision card. The limit is one Helm value shared by SFTP and the dashboard (`files.maxUploadSize`, default 20 GiB). The dashboard upload moves to the streaming route and shows progress for large files (designed first). The multipart `files/upload` route stays mounted for one release for API clients and is then removed in a later change.

## OD-7: Where does the SFTP server's code live? (SETTLED: A, valgul 2026-11-22)

Raised by planning. The other optional components (MCP server, tunnel, audit syslog bridge, capture sidecar) live in their own repositories, checked out here as submodules, with images still built from this repository.

- A. A new split repository `GameplanePanel/sftp`, submodule `sftp/`, set up like `GameplanePanel/mcp` (own `go.mod`, shared `go-component.yaml` CI, `specs.md`). valgul creates the empty repository; this session cannot. (Recommended: matches every other optional component, and the server only talks to the API over HTTPS, so it shares no Go code with this repository.)
- B. A new Go module `sftp/` in this repository, added to `go.work`. No new repository and no submodule bump step, but goes against the component split done in #995.
