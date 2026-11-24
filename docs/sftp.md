# SFTP access to game server files

Gameplane can serve the files of your game servers over SFTP, so you can use
FileZilla, WinSCP or the `sftp` command to upload worlds and mods, and download
backups. SFTP is off by default. It is an optional component, like the MCP
server and the audit syslog bridge.

Every login and every file operation goes through the Gameplane API as the
signed-in user. The SFTP pod keeps no game data. Permissions, ownership and
the audit log work the same as in the Files tab, so a user can only reach
what they could already reach in the dashboard.

## Turn it on

SFTP is controlled by Helm values. Enable it with `helm upgrade --reuse-values`:

```sh
helm upgrade gameplane charts/gameplane --reuse-values \
  --set sftp.enabled=true
```

By default the endpoint is a `ClusterIP` Service on port `2222`, which only
reaches the cluster from inside. To accept connections from your network, pick
an exposure:

| Value | Use it when |
| --- | --- |
| `sftp.serviceType: LoadBalancer` | Your cloud or load balancer gives the Service an address. Set `sftp.loadBalancerIP` to request one. |
| `sftp.serviceType: NodePort` | Clients can reach a node address. Set `sftp.nodePort` (30000-32767) to fix the port. |
| `sftp.publicAddress` | Set this to the address users should type, for example `files.example.com:2222`. The profile page shows it. |

Other values:

- `sftp.port` (default `2222`): the port the SFTP server listens on.
- `sftp.idleTimeout` (default `15m`): an idle SFTP session is closed after this.
- `sftp.maxSessionsPerUser` (default `4`) and `sftp.maxTransfersPerUser` (default `8`).
- `sftp.maxPreAuthPerIP` (default `8`): connections one address may have open
  while logging in; more are closed at once. Raise it if many users reach the
  pod from one address, for example behind a load balancer that hides client
  addresses.
- Metrics: with `serviceMonitors.enabled=true` the chart adds a
  `gameplane-sftp-metrics` Service (port 9102, in-cluster only) and a
  ServiceMonitor. Without it the SFTP pod serves no metrics outside the pod.
- `files.maxUploadSize` (default `20Gi`): the largest single file one upload can
  send, through SFTP and the dashboard. It accepts Kubernetes quantities
  (`Ki`, `Mi`, `Gi`, `Ti`, `K`, `M`, `G`, `T`) or a plain byte count. Uploads
  stream to the game's storage, and the agent refuses a file that would not fit
  in the free space.

The chart also creates a Secret named `gameplane-sftp-host-key` with the SSH
host key. It is created once and kept when you uninstall, so clients do not
see a changed key after an upgrade. To bring your own key, set
`sftp.hostKeySecret` to a Secret with a `host_key` entry in PEM format.
For GitOps or offline renders (`helm template`, Argo CD), set `sftp.hostKeySecret`
and the service token secret explicitly, because the chart cannot look up
existing Secrets there and would otherwise generate new values on every render.

Check that the pod is running:

```sh
kubectl -n gameplane get deploy gameplane-sftp
```

With `sftp.enabled` false (the default), the chart creates none of the SFTP
objects, and the profile page has no SFTP section.

## Set up your credentials

Each user manages their own SFTP access in the dashboard, under **Settings,
SFTP**. SSO users do too: there is no separate SFTP account to create. The page
shows:

- the address and port,
- the two username forms (below),
- the host key fingerprint, so you can check the server you connect to,
- your SSH public keys,
- whether you have a generated password.

**SSH keys.** Click **Add key** and paste one public key (one line, starting
with `ssh-ed25519`, `ecdsa-sha2-` or `ssh-rsa`). Keys must be ed25519, ECDSA
(nistp256, nistp384 or nistp521) or RSA with at least 3072 bits. You can keep up
to 20 keys. A key cannot be added to two accounts. Removing a key closes any
session that used it.

**Generated password.** Click **Generate password**, choose a length from 20 to
40, and copy it. The password is shown once. Gameplane stores only a hash, so
it cannot be shown again. The password does not expire.

You have one password at a time. **Regenerate** replaces it, and the old one
stops working at the next login. **Revoke** removes it and closes the sessions
that used it.

The dashboard login password is never accepted by SFTP.

## Connect

Use the address and port from the profile page. Use one of your usernames
(see the next section) and either your key or your generated password.

**`sftp` command:**

```sh
sftp -P 2222 alice@files.example.com
sftp -P 2222 -i ~/.ssh/id_ed25519 alice@files.example.com
```

**FileZilla:** protocol **SFTP - SSH File Transfer Protocol**, host, port,
username, and either the password or a key file (Edit, Settings, SFTP, Add key
file).

**WinSCP:** file protocol **SFTP**, host, port and username. Enter the password,
or choose a private key under **Advanced, Authentication**.

The first time you connect, the client asks you to accept the host key.
Compare the fingerprint it shows with the one on your profile page. They must
match exactly, and it is in the `SHA256:` form. If they differ, do not log in:
someone else may be answering on that address, or the endpoint was rebuilt
with a new key.

WinSCP and FileZilla upload to a temporary `*.filepart` name and rename the
file when it is complete. That works.

## Usernames and what you see

The username chooses where the session starts.

- `alice` opens the root of the cluster endpoint. Servers in the default games
  namespace appear as folders at the top, and servers in other namespaces
  appear under `_namespaces/<namespace>/`. Example: `_namespaces/events/lobby`.
- `alice+survival` opens straight in the files of the server `survival` in the
  default games namespace.
- `alice+events/lobby` opens straight in the files of `lobby` in the namespace
  `events`.

Both forms use the same password or key and the same permission checks. You
only see the servers you can read.

`alice` stands for your panel username, shown on the profile page. An account
created through SSO is usually named by its email address, for example
`alice@example.com`. OpenSSH splits the host at the last `@`, so
`sftp -P 2222 alice@example.com@sftp.example.com` and
`sftp -P 2222 alice@example.com+survival@sftp.example.com` work as written.
A username that itself contains `+` cannot be used over SFTP, because `+`
starts the server part.

Permissions:

- Reading needs `servers:read` on the server, or the usual ownership or
  collaborator access.
- Writing, renaming and deleting need `servers:write`, or ownership or
  collaborator access, the same as in the Files tab.
- The root and the server folders are read-only over SFTP. You create, rename
  and delete files inside a server, not the server folder itself.
- Dotfiles are never listed or created over SFTP.
- An SFTP session has no shell, no commands and no port forwarding. It only
  moves files.

A login that is refused for any reason (wrong password, revoked key, no access
to the server in the username, a disabled account) gets the same generic
refusal from the SFTP server. The client shows only a failed login. This is on
purpose, so a refusal does not reveal which servers exist.

## Remote clusters

Each cluster has its own SFTP endpoint. A remote cluster shows only its own
servers, so two clusters mean two addresses and two host keys. The same
credentials work on each endpoint, as long as the user can read the servers
there.

An admin sets up a remote endpoint under **Clusters**, in the cluster's SFTP
drawer:

1. Click **Create service token**. The token is shown once. Gameplane keeps
   two active tokens per cluster at most, so revoke an old one before making a
   third.
2. Install the chart on the remote cluster with SFTP on and the central panel
   as its API:

   ```sh
   helm upgrade gameplane charts/gameplane --reuse-values \
     --set sftp.enabled=true \
     --set sftp.apiURL=https://panel.example.com \
     --set sftp.serviceTokenSecret=gameplane-sftp-remote-token
   ```

   The Secret `gameplane-sftp-remote-token` must hold the token in its `token`
   key. Add `sftp.apiCASecret` if the panel uses a private CA.
3. Set the endpoint address and host key fingerprint in the drawer, so the
   users of that cluster see the right address.

The remote endpoint sends each login and file operation to the central API, and
the API reaches the files through the remote gateway. The remote cluster must
be able to reach the central API over HTTPS. `sftp.apiURL` must start with
`https://`; the chart refuses an `http://` address, and the SFTP pod does not
follow redirects. If the API cannot be reached, logins fail, and the SFTP pod
logs a clear message about the API. Open sessions are closed after about 2
minutes without a successful check with the API.

## Stopped and sleeping servers

You can edit the files of a stopped server over SFTP, and the Files tab shows
them too. Gameplane starts a small file-only pod that mounts the server's
storage. The game does not start, and no player port opens. A sleeping server
is not woken up by browsing its files.

- The first `ls` may take up to a minute while the pod starts. If the pod does
  not come up in time, the request fails and you can retry.
- If you start the server while files are open, the SFTP session reports that
  the server is starting. The game starts after the file-only pod is removed.
- The file-only pod is removed after `operator.filesIdleTimeout` (default
  `10m`) without file access.

## Revoking access

Revoking a key, revoking or regenerating the password, disabling a user, or
deleting a user closes their open SFTP sessions. This happens within about 60
seconds. New logins with the revoked credential fail straight away.

Admins do this from the user's row in **Users**, with `users:manage`.

## Troubleshooting

**The connection is refused or times out.** Check that the Service has an
address (`kubectl -n gameplane get svc gameplane-sftp`), that the port is open
on your firewall or load balancer, and that `sftp.enabled` is true.

**Login fails with the right password.** A password is replaced when
you regenerate it, so make sure you copied the newest one. Check that you can
read the server in the username. For `alice+survival`, `survival` must be in
the default games namespace. For `alice+events/lobby`, the namespace must be
spelled right. Check that the key is still on your profile.

**Too many attempts.** The API limits logins per user and per source address.
Wait a minute and try again.

**The host key changed warning.** Compare the fingerprint with your profile
page. If it differs, the endpoint's key changed, for example after the
`gameplane-sftp-host-key` Secret was deleted. Do not accept the new key until
an admin confirms it.

**"Permission denied" on upload or rename.** You can read the server but do not
have `servers:write` on it, or the path is a server folder itself. Server
folders are read-only over SFTP.

**An upload stops with "file too large".** The file is bigger than
`files.maxUploadSize`. Raise the value with `helm upgrade --reuse-values`.

**Files of a stopped server do not appear.** Wait a minute and retry. If it keeps failing,
check that the `gameplane-files` pod in the games namespace is running.

**The session closes on its own.** It was idle for longer than
`sftp.idleTimeout`, the key or password was revoked, the SFTP pod could not
confirm the session with the API for 2 minutes, or the server pod could not be
reached. The SFTP pod's logs show which.

Logs: `kubectl -n gameplane logs deploy/gameplane-sftp`. Audit entries for SFTP
file changes show `source: sftp` in the audit log.

## Related

- [Security notes](security.md) for the threat model of optional components.
- [Install](install.md) for the full list of Helm values.
- The Files tab in the dashboard uses the same permissions and limits.
