# Quickstart: validate SFTP access to game servers

Manual validation on a dev cluster once every delivery PR in [plan.md](plan.md) has merged. Automated coverage of the same scenarios lives in the E2E bucket `sftp` and the `multicluster` addition (tasks T029, T035). Routes and payloads: [contracts/api.md](contracts/api.md); tables and annotations: [data-model.md](data-model.md).

## Prerequisites

- `make dev-up` cluster with images from `make images` loaded (`make dev-load`), including `sftp` and the `sftp/` submodule initialised (`hack/submodules.sh init`).
- An OpenSSH `sftp` client; optionally FileZilla or WinSCP for the GUI checks.
- Three dashboard users: `admin`, `alice` (role with `servers:read` + `servers:write` on `gameplane-games`), `bob` (`servers:read` only). Two servers in `gameplane-games`: `survival` and `creative`, and one server `lobby` in a second games namespace `events` that only `alice` can read.

## 1. Off by default (US4, SC-007)

```sh
helm template charts/gameplane | grep -c 'gameplane-sftp'   # expect 0
```

Expected: no SFTP Deployment, Service, Secret or NetworkPolicy; the profile page shows no SFTP section.

## 2. Enable SFTP

```sh
helm upgrade gameplane charts/gameplane --reuse-values \
  --set sftp.enabled=true --set sftp.serviceType=NodePort --set sftp.port=2222
kubectl -n gameplane get secret gameplane-sftp-host-key
```

Expected: the SFTP pod is Ready; restarting it (`kubectl rollout restart deploy/gameplane-sftp`) keeps the same host key fingerprint (US4 scenario 2).

## 3. Credentials (US3)

As `alice` in the dashboard: profile → SFTP. Check the address, port, both username forms and the host key fingerprint are shown. Add an SSH public key; generate a password with length 32 and copy it (shown once). As `bob`, generate a password only.

Expected: the stored password is never shown again; regenerating makes the old one fail at the next login.

## 4. Browse, download, resume (US1)

```sh
sftp -P 2222 alice@<node-ip>
sftp> ls                      # survival  creative  _namespaces
sftp> ls _namespaces/events   # lobby
sftp> get survival/world.zip  # interrupt with Ctrl-C half way, then:
sftp> reget survival/world.zip
```

Expected: `bob` sees only `survival` and `creative`; dotfiles never appear; the resumed file's checksum matches `kubectl exec` `sha256sum` in the pod (SC-003).

## 5. Per-server login (OD-3)

```sh
sftp -P 2222 'alice+survival@<node-ip>'      # lands in survival's files
sftp -P 2222 'alice+events/lobby@<node-ip>'  # lands in lobby's files
sftp -P 2222 'bob+events/lobby@<node-ip>'    # fails like a wrong password
```

## 6. Upload, rename, delete, permissions (US2)

```sh
sftp> put big-world.zip survival/      # 2 GiB; interrupt, then: reput
sftp> rename survival/a.txt survival/b.txt
sftp> mkdir survival/new && rmdir survival/new
sftp> rm creative/x.txt                # as bob: Permission denied
sftp> rename survival creative2        # refused: server folders are read-only
```

Then in the dashboard: Files tab shows the uploaded file; Audit log shows each change with source `sftp` (SC-005). A WinSCP upload with its default `*.filepart` temp-and-rename succeeds.

## 7. Dashboard streaming upload (OD-6)

Upload a 1 GiB file in the Files tab. Expected: per-file progress and a working cancel; cancelling leaves any existing file of that name unchanged (FR-016, FR-018b).

## 8. Stopped and sleeping servers (US5)

1. Stop `survival` in the dashboard. In `sftp`, `ls survival`: files appear within the readiness bound; `kubectl get pods -l app.kubernetes.io/name=gameplane-files` shows one pod; `survival` stays stopped and no player port opens.
2. Leave it idle past `operator.filesIdleTimeout`: the file-only pod is gone.
3. Open `survival` again, then start it from the dashboard: the SFTP session gets "server is starting"; the game pod starts only after the file-only pod is deleted.
4. Let `creative` fall asleep, then `ls creative`: it is not woken.

## 9. Revocation (US3, SC-006)

With `alice` connected, revoke her SSH key in the dashboard (or an admin disables her). Expected: the session closes within 60 s and a new login with that key fails.

## 10. Remote cluster (OD-5)

On a registered remote cluster with the gateway installed: create an SFTP service token for it in the central panel (Clusters → SFTP), install the chart there with `sftp.enabled=true`, `sftp.apiURL=<central API>`, `sftp.serviceTokenSecret=<secret>`. Connect with the same `alice` credentials.

Expected: only that cluster's servers are listed; files move through the central API; stopping the central API makes logins fail with a clear log line on the remote endpoint.
