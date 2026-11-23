# Contract: HTTP endpoints

As merged in PR #630. All endpoints sit behind session authentication and the RBAC middleware (`api/internal/rbac/rbac.go`). The full API reference is `api/specs.md`.

## `GET /admin/installation`

Any authenticated user. Response `200`:

```json
{ "standalone": true, "localCluster": false, "version": "v0.3.0" }
```

Not reachable before login (Core rule 3).

## `GET /clusters`, `GET /fleet/*`

In standalone mode both succeed with no implicit `local` entry; an empty registry returns empty lists. The cluster view exposes only name, display name, phase, message, version and last check time, plus a boolean `canViewInventory`.

## `POST /clusters`

Requires `cluster:manage` (`GET /clusters` needs only a session). Body `{ "name", "displayName", "kubeconfig" }`.

- Standalone: the kubeconfig must pass `kube.ValidateStandaloneKubeconfig` and the remote access policy (HTTPS, verified TLS, no proxy, safe destination). Registration and credential commit in one transaction. Duplicate name returns `409`.
- Combined: unchanged Secret-then-CR path, with cleanup on failure.

## `PUT /clusters/{name}/kubeconfig`

Requires `cluster:manage`. Body `{ "kubeconfig" }`. `204` on success, `409` when the registration changed concurrently. Publishes a new immutable credential, swaps the reference under UID and previous-value checks, keeps gateway settings and grants, then deletes the old credential.

## `PUT /clusters/{name}/gateway`, `DELETE /clusters/{name}/gateway`

Requires `cluster:manage`. `PUT` body `{ "url", "caCert", "clientCert", "clientKey" }`; the URL must be an HTTPS origin that passes the remote access policy. `204` with no body. `DELETE` clears the gateway and removes only the API-generated gateway Secret.

## `DELETE /clusters/{name}`

Requires `cluster:manage`. Removes the registration and its API-owned kubeconfig and gateway credentials with UID and version preconditions.

## `/modules` and `/modules/*`

- Standalone: exactly one `?cluster=<registered, non-local>` is required (`400` when missing, repeated or `local`). `GET` needs `modules:read` and every other method needs `modules:manage`, as a cluster-wide grant on that cluster (`403` otherwise).
- Combined: local catalog as before; a remote selector returns `501`.
- `modules:read`, `modules:manage`, `templates:read` and `templates:write` are now checked as cluster-wide grants on the target cluster in both modes (`api/internal/auth/perms.go`).

## `GET /admin/system-logs/{component}`

Standalone: `501` with guidance to read container-runtime logs.

## Error and log surface

No response body, error or log line from these endpoints contains kubeconfig, token, certificate or key material. Remote lookup and connection failures use fixed messages.
