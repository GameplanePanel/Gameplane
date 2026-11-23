# Feature Specification: SFTP access to game servers

**Feature Branch**: `spec-040/sftp-access`

**Created**: 2026-11-22

**Status**: Draft (all open decisions settled, see `OPEN-DECISIONS.md`)

**Input**: Issue [#1051](https://github.com/GameplanePanel/Gameplane/issues/1051) "Add SFTP server for file access", and valgul's design idea on it: "Give the sidecar a new internal API to list, stat, download and upload files. Then run one global SFTP server that uses this API. You connect to {sftp-cluster-ip} and see one folder per game server your account can access. Each folder is backed by that server pod's sidecar API."

## Background

Today the only way to reach a game server's files is the dashboard's Files tab (`web/src/routes/tabs/Files.tsx`). The API proxies each request (`api/internal/ws/dialer.go`, `/servers/{name}/files/*`) over mTLS to the in-pod agent sidecar (`agent/internal/files/`), which serves `list`, `read`, `download`, `archive`, `write`, `create`, `upload`, `mkdir` and `delete` below the data root, with dotfiles and symlink escapes refused. Reads need `servers:read`, writes need `servers:write` (or ownership of the server), and both work on remote clusters through the agent gateway.

Server admins want to use desktop SFTP clients (FileZilla, WinSCP, `sftp`, editor plugins) to move worlds, mods and configs. The reporter on #1051 runs a separate sftpgo pod on the game PVCs as a workaround, which bypasses Gameplane permissions and the audit log. In the issue thread the reporter agreed that one SFTP address per cluster, with each user restricted to their own servers, is the ideal shape.

The agent's file API misses operations SFTP clients rely on: metadata for one path (stat), rename/move, reading from an offset (resume a download) and writing at an offset or appending (resume an upload, and clients that write in chunks).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Browse and download server files with an SFTP client (Priority: P1)

A user with read access to some game servers opens their SFTP client, connects to the one Gameplane SFTP address with their Gameplane identity, and sees one folder per server they can read. They open a server folder, browse it and download files.

**Why this priority**: Read access is the smallest useful slice and proves the whole path: authentication, permission filtering, the server folder tree and the sidecar file API.

**Independent Test**: Give a user `servers:read` on server A only. Connect with an SFTP client. The root lists only A. Download a 1 GiB world file from A and compare checksums.

**Acceptance Scenarios**:

1. **Given** a user who can read servers A and B but not C, **When** they connect and list the root, **Then** they see folders for A and B only, and nothing reveals that C exists.
2. **Given** a connected user, **When** they open A's folder, **Then** they see the same files and folders as the dashboard Files tab, with sizes and modification times, and no dotfiles.
3. **Given** an interrupted download of a large file, **When** the client resumes it, **Then** the transfer continues from where it stopped and the result matches the original.
4. **Given** a user whose read access to A is removed while connected, **When** they next list or open anything in A, **Then** the request is refused.

---

### User Story 2 - Upload, edit, rename and delete files (Priority: P1)

A user with write access to a server uploads a mod jar, edits a config file in place from their editor's SFTP plugin, renames a folder and deletes old logs, all from the SFTP client.

**Why this priority**: Uploading worlds and mods is the main reason people ask for SFTP; read-only access only half solves #1051.

**Independent Test**: Give a user `servers:write` on server A and `servers:read` on server B. Upload, overwrite, rename, mkdir and delete in A: all succeed and each shows in the dashboard Files tab and the audit log. Try the same in B: all are refused, and B is unchanged.

**Acceptance Scenarios**:

1. **Given** write access to A, **When** the user uploads a 2 GiB file, **Then** it lands in A complete, and a client that uploads to a temporary name and renames it at the end (WinSCP default) also succeeds.
2. **Given** write access to A, **When** an upload is interrupted and the client resumes it, **Then** the finished file matches the source.
3. **Given** read-only access to B, **When** the user tries to upload, rename, delete or make a folder in B, **Then** the client gets "permission denied" and B is unchanged.
4. **Given** any user, **When** they try to create, rename or delete a top-level server folder, or reach a path outside the server's data root (`..`, symlinks, dotfiles), **Then** the request is refused.
5. **Given** any write through SFTP, **When** an admin opens the audit log, **Then** the entry names the user, the server, the operation, the path and that it came from SFTP.

---

### User Story 3 - Manage personal SFTP credentials (Priority: P2)

A user, including one who signs in with SSO and has no Gameplane password, opens their profile in the dashboard, adds an SSH public key or generates an SFTP password (OD-2), sees the SFTP address and the server's host key fingerprint, and can revoke the credential at any time. An admin can see and revoke any user's SFTP credentials.

**Why this priority**: Without it SSO users cannot connect at all, and a leaked credential could not be cut off. P2 only because P1 can be tested with a credential seeded by an admin.

**Independent Test**: As an SSO user, add a credential in the profile and connect with it. Revoke it: the next login fails and any open session ends within one minute. Disable the user: the same happens.

**Acceptance Scenarios**:

1. **Given** a signed-in user, **When** they open their profile, **Then** they see the SFTP address, port, their SFTP username and the host key fingerprint to check on first connect.
2. **Given** a user revokes a credential or an admin disables the user, **When** a session using it is open, **Then** it is closed within one minute and new logins with it fail.
3. **Given** five failed logins in a row for one username or source address, **When** more attempts follow, **Then** they are slowed down and refused the same way the dashboard login rate limit does, and the failure message never says whether the user exists.

---

### User Story 4 - Admin turns SFTP on and exposes it (Priority: P2)

A cluster admin enables SFTP with one Helm value, picks how it is exposed (port and Service type, as for other Gameplane ports) and gives users one address.

**Why this priority**: Needed for anyone to use the feature, but small and mostly chart work.

**Independent Test**: Install with SFTP disabled: no SFTP pod, Service or port exists. Enable it with a LoadBalancer Service on port 2222: a client connects and sees the expected tree.

**Acceptance Scenarios**:

1. **Given** a default install, **When** the chart renders, **Then** no SFTP workload, Service, credential screen or network allowance exists.
2. **Given** SFTP is enabled, **When** the SFTP pod restarts or is upgraded, **Then** its host key is unchanged, so clients do not warn about a changed key.
3. **Given** SFTP is enabled on two clusters of a multicluster panel, **When** a user connects to each cluster's address, **Then** each shows only that cluster's servers the user can read, with the same credentials; the remote endpoint reaches files through the central API (OD-5).

---

### User Story 5 - Edit files of a stopped or sleeping server (Priority: P2)

A user stops a server, or it falls asleep when idle, and then opens its folder over SFTP (or in the dashboard Files tab) to replace the world or fix a config that keeps it from starting. The files are there without starting the game.

**Why this priority**: Replacing a world or a broken config is safest while the game is not running, and today it needs the game running. P2 because P1 already covers running servers.

**Independent Test**: Stop server A. Open its folder over SFTP: files list within the readiness bound, no player port opens and A stays stopped. Upload a file, wait past the idle time: the file-only pod is gone. Open A again, then start A from the dashboard: the SFTP session gets "server is starting", and A starts with the uploaded file in place.

**Acceptance Scenarios**:

1. **Given** server A is stopped, **When** a user with read access opens A's folder, **Then** they see A's files and A stays stopped.
2. **Given** server A is asleep, **When** a user opens A's folder, **Then** A is not woken, and players connecting to A still get the normal wake behaviour.
3. **Given** files of stopped A are open over SFTP, **When** someone starts A, **Then** file access ends first and A starts normally.
4. **Given** no file operation on stopped A for the idle time, **Then** the file-only pod is removed.

---

### Edge Cases

- A server is stopped or asleep, so its sidecar is not running: a file-only pod serves its files (OD-4, FR-013).
- A user starts the server while someone has its stopped files open over SFTP (FR-013c).
- A server's volumes can only be attached to one node: the file-only pod lands where the volumes can attach.
- Two servers share a name in different namespaces: they appear under different `/_namespaces/` paths, or one of them as `/<server>` when it is in the default games namespace (OD-3).
- A server is created, deleted or renamed while a user is connected: the root listing reflects it on the next listing; operations on a deleted server fail cleanly.
- The server's pod restarts mid-transfer: the transfer fails with a clear error and can be resumed.
- A volume fills up during an upload: the client gets an error and no half-written file replaces the old one.
- A user has hundreds of servers: listing the root still answers within the success criterion.
- A path holds characters SFTP clients send in other encodings: names are treated as UTF-8 and invalid names are refused, not mangled.
- Symlinks inside the data root: shown neither as followable links nor as escape routes, matching the dashboard.
- Very many concurrent sessions or transfers from one user: capped so one user cannot starve others.
- The remote cluster's agent gateway is down: that cluster's folders fail with a clear error, the rest keep working.

## Requirements *(mandatory)*

### Functional Requirements

**Endpoint and exposure**

- **FR-001**: Gameplane MUST offer one SFTP endpoint per cluster (OD-3), off by default and enabled with one Helm value; when off, nothing SFTP-related is deployed or shown.
- **FR-001a**: On every cluster, the SFTP endpoint MUST send logins and file operations to the central Gameplane API, which reaches the files through its existing path to that cluster (OD-1, OD-5). An endpoint on a remote cluster MUST be configured with the central API's address and trust, and MUST refuse logins with a clear log line when it cannot reach it.
- **FR-002**: The endpoint's port and Service type MUST be configurable in Helm. Per-server ports and port pools are out of scope.
- **FR-003**: The endpoint MUST present a stable host key that survives restarts and upgrades, and the dashboard MUST show its fingerprint.
- **FR-004**: The endpoint MUST speak SFTP only: no shell, no command execution, no port forwarding, no SCP.

**Authentication**

- **FR-005**: Users MUST authenticate with an SFTP credential tied to their Gameplane user, which works for local and SSO users alike: an SSH public key or a generated SFTP password (OD-2). The dashboard login password MUST NOT be accepted.
- **FR-006**: Users MUST be able to add, list and remove their own SFTP SSH public keys in their profile; admins with `users:manage` MUST be able to list and remove any user's.
- **FR-006a**: Each user MUST be able to have at most one generated SFTP password. Gameplane generates it, shows it exactly once, and stores only a hash. It never expires. The user can only regenerate it (the old one stops working at once) or revoke it; admins with `users:manage` can revoke any user's.
- **FR-006b**: A generated SFTP password MUST be 20 to 40 characters long, with the length picked by the user within that range, and each character drawn uniformly at random from the 94 printable ASCII characters (space excluded) using a cryptographically secure source.
- **FR-007**: Revoking a credential, disabling or deleting the user MUST end that user's open SFTP sessions within one minute and refuse new logins.
- **FR-008**: Failed logins MUST be rate limited per username and per source address at least as strictly as the dashboard login, and MUST NOT reveal whether a user exists (core rule 3).

**Folder tree and permissions**

- **FR-009**: After a login as `<user>`, the root MUST list one folder per game server on that endpoint's cluster that the file-access check (FR-011a) lets the user list, and nothing else: `/<server>` for the cluster's default games namespace and `/_namespaces/<namespace>/<server>` for other namespaces (OD-3).
- **FR-009a**: A login as `<user>+<server>` or `<user>+<namespace>/<server>` MUST open directly in that server's files, with the same credentials and permission checks as FR-009. If the user cannot read that server, or it does not exist, the login MUST fail with the same generic message as a wrong password.
- **FR-010**: Inside a server folder, the user MUST see exactly what the dashboard Files tab shows for that server: the same data root, with dotfiles hidden and symlink escapes refused.
- **FR-011**: Every SFTP operation and every Files tab operation MUST be allowed or refused by the same file-access check (FR-011a), on every operation, not only at login.
- **FR-011a**: All file permission decisions (which servers a user sees, and each list, stat, read, download, create, write, upload, rename, mkdir and delete, with the paths involved) MUST go through one shared file-access check in the API. Neither the SFTP component nor any route may test a permission key itself. Today the check maps reads to `servers:read` and changes to `servers:write` or ownership/collaboration, exactly as the Files tab does now; when spec 039 (per-server access) lands, its evaluator replaces that mapping inside the check, and SFTP follows without code changes.
- **FR-011b**: Where the check can refuse single paths (spec 039 path grants), a folder listing MUST leave out entries the user may not list, and a rename MUST be allowed only when the user may remove the source and create or overwrite the destination.
- **FR-012**: The root and the server folders themselves MUST be read-only: users cannot create, rename or delete them.
- **FR-013**: When a server is stopped or asleep, opening its folder (or logging in to it directly) MUST make its files available through a short-lived file-only pod that the operator runs with the server's volumes and the agent's file API only: no game process, no console, no game ports (OD-4).
- **FR-013a**: File access MUST NOT wake or start the game server, and the file-only pod MUST NOT be reachable by players.
- **FR-013b**: The file-only pod MUST be removed after a configurable idle time with no file operation (default proposed in planning), and at once when the server is deleted.
- **FR-013c**: Starting or waking the server MUST end file-only access first: open transfers to that server fail with a clear "server is starting" error, and the game pod starts only after the file-only pod is gone, so the two never mount the volumes at the same time.
- **FR-013d**: The dashboard Files tab MUST use the same file-only access for a stopped or asleep server, so it behaves like SFTP.
- **FR-013e**: Waiting for a file-only pod MUST NOT hang a client: if it is not ready within a bounded time, the operation fails with a clear "files are starting, try again" error.

**File operations**

- **FR-014**: The SFTP endpoint MUST support listing a folder, stat of one path, download with resume from an offset, upload, upload resume (write at an offset or append), overwrite, rename/move within one server, mkdir, delete of files and of empty folders, and recursive delete as the client requests it.
- **FR-015**: The agent sidecar's file API MUST gain the operations FR-014 needs that it lacks today: stat, rename/move, read from an offset, and write at an offset or append. The new operations MUST keep the existing guarantees: rooted at the data root, no dotfiles, no symlink following, and safe against a path changing between check and use.
- **FR-016**: An upload that fails or is cancelled MUST NOT leave a partly written file in place of an existing file of the same name.
- **FR-017**: Changing file modes, owners and timestamps (`chmod`, `chown`, `utime`) MUST either be accepted as a no-op or refused consistently; it MUST NOT change ownership inside the volume.
- **FR-018**: Transfers MUST NOT be cut off by the short per-request timeouts used for small file operations; they MUST be limited only by the file upload size limit (OD-6), the volume's free space and an idle timeout.
- **FR-018a**: One upload size limit MUST apply to SFTP and the dashboard Files tab alike, configurable in Helm with a default of 20 GiB, and an upload that would not fit in the volume's free space MUST be refused before writing (OD-6).
- **FR-018b**: The dashboard Files tab MUST upload through the same streaming path as SFTP, show progress for each file, and let the user cancel; a cancelled or failed upload follows FR-016.
- **FR-019**: Each user MUST be limited to a configurable number of concurrent sessions and transfers.

**Audit and observability**

- **FR-020**: Every change made through SFTP MUST be written to the audit log with the user, cluster, namespace, server, operation, path(s) and source `sftp`, through the same sinks as dashboard file changes (including the syslog bridge).
- **FR-021**: Logins (success and failure) and session ends MUST be logged; failures in the audit log follow the same privacy rule as dashboard logins.
- **FR-022**: The SFTP component MUST expose metrics for active sessions, logins by result, and bytes transferred.

**Docs**

- **FR-023**: User docs MUST explain how to enable SFTP, connect with common clients, verify the host key and manage credentials; `docs/security.md` MUST cover the new endpoint in the threat model.

### Key Entities

- **SFTP SSH key**: belongs to one Gameplane user; has a name, the public key, its fingerprint, a creation time and a last-used time; can be removed. A user can have several.
- **SFTP password**: at most one per Gameplane user; stored only as a hash with its creation and last-used time; never expires; can be regenerated or revoked.
- **SFTP session**: one authenticated client connection, bound to one user and one credential; ends on disconnect, idle timeout, revoke or user disable.
- **Server folder**: a virtual folder in the SFTP tree that stands for one game server (cluster, namespace, name) and maps to that server's sidecar data root.
- **Host key**: the endpoint's long-lived SSH host key, stored as a Kubernetes Secret, with its fingerprint shown in the dashboard.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can go from "SFTP enabled" to browsing a server's files in a desktop SFTP client in under 3 minutes, following only the dashboard profile page.
- **SC-002**: Listing the root for a user with 200 servers answers in under 2 seconds; listing a folder of 1,000 files answers in under 2 seconds.
- **SC-003**: A 2 GiB upload and download each complete with matching checksums, and each resumes correctly after a forced disconnect at 50%.
- **SC-004**: In a permission test matrix (read-only, write, owner, collaborator, no access, remote cluster), 100% of operations are allowed or refused exactly as the dashboard Files tab allows or refuses them, and a code search finds no permission key tested outside the file-access check.
- **SC-005**: 100% of SFTP changes in the test run appear in the audit log with source `sftp`.
- **SC-006**: A revoked credential or disabled user loses an open session within 60 seconds.
- **SC-007**: A default install deploys no SFTP workload and exposes no new port.

## Assumptions

- One address per cluster (OD-3) matches what the reporter asked for in #1051; dedicated per-server ports or port pools are a possible later spec.
- File permissions reuse the existing catalog (`servers:read` to read, `servers:write` or ownership to change), behind the file-access check (FR-011a); no new permission is added. Reconciling with spec 039's per-server roles and `files.*` actions is deferred (valgul, 2026-11-23): 039 plugs its evaluator into the check, and adds an action for rename or keeps the delete-plus-create rule of FR-011b.
- The SFTP component is optional and off by default, like the MCP server and the audit syslog bridge.
- The dotfile, symlink and data-root rules are the same as the dashboard Files tab (the upload size limit is shared, OD-6); SFTP gets no wider view of the volume than the dashboard.
- Host key type is a modern one (Ed25519) generated once at install and kept in a Secret.
- The Files tab needs no new screen; it gains offline access through FR-013d and streaming uploads with progress through FR-018b (both states designed first). The other new dashboard UI is the profile's SFTP section (and admin revoke on the Users page), which is designed in `design.fig` first (core rule 1).

## Out of Scope

- SCP, rsync, FTP/FTPS and WebDAV.
- Shell or command access to the game container.
- Per-server ports, port pools and per-server hostnames.
- One endpoint that spans several clusters.
- Keeping remote-cluster file data inside that cluster (OD-5 chose a round trip through the central API).
- Access to files outside a server's data root, other volumes, backups or snapshots.
- Changing file ownership or modes inside the volume.
