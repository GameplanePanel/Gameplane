# Quickstart: validate spec 037

Run on a Kind dev cluster (`make dev-up`) with a second Kind cluster registered as a remote (see `docs/multicluster-ui.md`). CI runs the E2E versions of these checks; this guide is for manual review.

## 1. Break-glass scope (Story 1, Story 4)

```sh
helm upgrade gameplane charts/gameplane -n gameplane-system --reuse-values \
  --set mcpServer.enabled=true --set 'operator.gameNamespaces={tenant-b}'
SA=system:serviceaccount:gameplane-system:gameplane-mcp-server
kubectl auth can-i list pods -n kube-system --as=$SA        # no
kubectl auth can-i get pods/log -n default --as=$SA         # no
kubectl auth can-i list pods -n gameplane-games --as=$SA    # yes
kubectl auth can-i list pods -n tenant-b --as=$SA           # yes
kubectl auth can-i list pods -n gameplane-system --as=$SA   # yes
kubectl auth can-i list gametemplates --as=$SA              # yes
kubectl get clusterrole gameplane-mcp-server-read           # NotFound after upgrade
```

Then in a break-glass session, `list_pods` with no namespace returns only those three namespaces, and `list_pods namespace=kube-system` returns "namespace not in scope".

## 2. Tokens and per-user reads (Story 2)

1. Create user `ops-a` with a role holding `servers:read` in `gameplane-games` only, and user `owner-b` with no role but owning one server in `tenant-b`.
2. Each signs in, opens Settings → MCP tokens, creates a token.
3. Run `GAMEPLANE_PANEL_URL=https://<panel> GAMEPLANE_MCP_TOKEN=<token> mcp-server serve` from an MCP client for each.
4. Expect: `ops-a` lists servers, pods, events and logs in `gameplane-games` only; `Backups` → permission denied. `owner-b` lists only their server (through `/users/me/servers`), its pods and logs. Neither can read `gameplane-system`.
5. An admin removes `ops-a`'s role → `ops-a`'s next tool call is refused. Revoke `owner-b`'s token → next call "not authenticated".
6. `curl -X POST -H "Authorization: Bearer <token>" https://<panel>/servers` → `403`. Audit log shows `mcp-session` and `mcp-refused` rows.

## 3. Multicluster (Story 3)

Give `ops-a` a supplemental grant on the remote cluster for `gameplane-games`. `list_gameplane_resources kind=GameServer` with no cluster returns groups for both clusters; with `cluster=<unknown>` it returns "cluster not found or not permitted". Stop the remote cluster's gateway: the call still answers, listing it under `unavailable`.

## 4. Docs

The MCP server README, `mcp-server/specs.md` and `docs/security.md` describe both modes and no longer mention a cluster-wide grant (SC-005).
