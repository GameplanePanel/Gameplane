# Contract: new API read routes (spec 037)

All are GET, JSON unless noted, take the usual `?cluster=` selector (absent = local cluster; unknown or not permitted = the same `403 server.forbidden` as today's `scope.ErrForbiddenCluster`), and strip `metadata.managedFields`.

## `GET /pods?namespace&labelSelector`

- RBAC: segment `pods`, permission `servers:read` (namespaced) in the requested cluster and namespace.
- `namespace` must be one of `scope.AllowedNamespaces`, or the control-plane namespace (see below). Anything else → `403 server.namespace.forbidden`.
- Result: `{items: [Pod]}`.

## `GET /pods/{name}?namespace`

- Same rules as `/pods`. Result: one Pod. Missing → `404`.

## `GET /kube-events?namespace&fieldSelector&labelSelector`

- RBAC: segment `kube-events`, `servers:read` (namespaced).
- Result: `{items: [Event], truncated: bool, total: int}`, newest first by `lastTimestamp` then `eventTime`, at most 100 items.

## `GET /servers/{name}/pods`

- RBAC: `servers:read` via the existing `/servers/{name}` rule, including owner and collaborator fallback.
- Result: `{items: [Pod]}` for pods labelled as that server's workload (StatefulSet pod, and backup or restore Job pods of that server).

## `GET /servers/{name}/logs/pod/tail?pod&container&tailLines&previous`

- RBAC: as `/servers/{name}/pods`. `pod` must be one of the pods `/servers/{name}/pods` returns (else `404`).
- `tailLines` default 200, max 5000; result is `text/plain`, the newest 256 KiB, prefixed with a one-line notice when cut.

## Control-plane namespace (FR-012)

- `namespace=<control-plane namespace>` on `/pods`, `/pods/{name}` and `/kube-events` is allowed only when the cluster is the local one and the caller has `*` there. Others → `403 server.forbidden` (same body as any other denial).
- API and operator logs: existing `/admin/system-logs/{api|operator}?tailLines` (already admin-only, local only).

## Out of scope

No route returns Secrets, ConfigMaps, nodes' raw objects, or anything outside the namespaces above. No route writes.
