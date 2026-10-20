# Review: charts/gameplane/

- **Date**: 2026-09-24
- **Reviewer tier**: opus (verification pending)
- **Checked against**: `docs/install.md`, `docs/networking.md`, `docs/security.md` (the parts that describe chart defaults), the comments in `charts/gameplane/values.yaml` and the templates, `specs/done_006-install-time-config/spec.md` (FR list), `specs/done_015-top-steam-game-modules/spec.md` (US1), and the code the chart wires up (`api/cmd/main.go`, `operator/cmd/main.go`, `agent/cmd/main.go`, `operator/internal/controller/*`). The chart has no `specs.md` of its own.

## Scope reviewed

Read in full:
- `charts/gameplane/Chart.yaml`, `charts/gameplane/values.yaml`
- All 18 templates: `_helpers.tpl`, `api.yaml`, `audit-syslog-bridge.yaml`, `clusterops-rbac.yaml`, `crd-apply-hook.yaml`, `grafana-dashboard.yaml`, `ingress.yaml`, `mcp-server.yaml`, `module-cosign-key.yaml`, `modulesource.yaml`, `mtls.yaml`, `namespaces.yaml`, `networkpolicies.yaml`, `operator.yaml`, `prometheusrule.yaml`, `servicemonitors.yaml`, `telemetry-receiver.yaml`, `web.yaml`
- `docs/install.md` (all 623 lines), `docs/networking.md` (all 195 lines)
- `docs/security.md` lines 55-304 and 555-790 (client IP, authorization, API→agent, NetworkPolicies, pod security, capture exception, mcp-server, secrets, OIDC role mappings, pre-auth screens)

Compared, not read line by line:
- `charts/gameplane/crds/*.yaml` (9) and `charts/gameplane/crd-manifests/*.yaml` (9): byte-compared with `cmp` against each other and against `operator/config/crd/*.yaml`
- `charts/gameplane/dashboards/gameplane-operator.json`: parsed, metric names extracted and checked against `operator/internal/controller/metrics.go`

Cross-checked (targeted reads or greps): `api/cmd/main.go` (flags, env, router), `api/internal/audit/s3.go:40-110`, `api/internal/auth/oidc.go:100-130,420-470`, `agent/cmd/main.go:140-235`, `agent/internal/auth/auth.go:100-112`, `operator/cmd/main.go` (flags), `operator/internal/controller/{gameserver_controller,backup_controller,restore_controller,gameserver_tunnel,agent_certs,cluster_controller,metrics}.go` (targeted), `operator/config/rbac/role.yaml`, `operator/config/crd/kustomization.yaml`, `mcp-server/internal/kube/client.go:50-80`, `web/nginx.conf.template` (first ~120 lines), `telemetry-receiver/main.go` (grep), `test/e2e/fixtures/restic-server.yaml`, `test/e2e/upgrade_e2e_test.go` (helm flags), `Makefile` (manifests, images, dev-* targets), `modules/` directory listing, `.github/workflows/release.yaml:290-313`, `cosign.pub`.

Not reviewed: `docs/security.md` lines 1-54 and 305-554 (auth, module supply chain, runtime mods, notifications, audit log integrity, GitHub Actions), which don't describe chart defaults. Chart rendering was checked with `helm template` only (a render, no lint and no install).

## Method

1. Read every template and value. Traced each value to the flag or env var it becomes, and confirmed the flag exists in the target binary.
2. Byte-compared the three CRD copies.
3. Compared the NetworkPolicies with the pod labels the operator actually sets, and with the ports and TLS mode of the endpoints the chart's monitors scrape.
4. Rendered the chart with `helm template` three ways: default values; release name `gp`; and a copy of the chart whose `values.yaml` was replaced by `git show v0.2.0-beta.8:charts/gameplane/values.yaml`. The third render simulates what `helm upgrade --reuse-values` does, because Helm then uses the previous release's chart values in place of the new defaults.
5. Checked every sentence in install.md, networking.md and the chart-related parts of security.md that names a Helm key, default, template behaviour or `helm` lifecycle step against the templates.

## Observations (no finding)

- The CRD copies are in sync. All 9 files in `crds/`, `crd-manifests/` and `operator/config/crd/` are byte-identical. `Makefile:294,298` copies both chart directories from `operator/config/crd/`.
- Every flag the chart passes exists in its binary. API: all 26 flags and the `GAMEPLANE_*` env vars in `api.yaml` are defined in `api/cmd/main.go`. Operator: all flags in `operator.yaml` are defined in `operator/cmd/main.go`, and `--zap-log-level` is bound through `zap.Options.BindFlags` (line 253). The `operator.logLevel` comment (values.yaml:46, "warn not supported") matches controller-runtime v0.25.1 `levelStrings` (debug/info/error/panic).
- The chart's own guards work as documented: `operator.addressManager` validation (operator.yaml:293-295), the empty `gameIngress.fromCIDRs` refusal (operator.yaml:309-311), `localModules` needing a claim or a hostPath (operator.yaml:372-373), syslog bridge `addr` (audit-syslog-bridge.yaml:7-9), and the cosign key being present when verification is on (module-cosign-key.yaml:2-4). The `existingClaim` PVC-prune guard (api.yaml:169-177) is consistent with Helm checking `resource-policy: keep` on the live object.
- The chart's cosign key (values.yaml:508-512) is byte-identical to `cosign.pub`, as its comment requires.
- The ModuleSource template's `verify.key.name` (modulesource.yaml:32-36) matches `VerifySpec.Key` (`LocalObjectReference`), and the `cosign.pub` data key matches the CRD doc comment (`modulesource_types.go:87-90`).
- The MCP server's RBAC (mcp-server.yaml:27-36) grants exactly what `mcp-server/internal/kube/client.go:60-68` reads: 7 CRD kinds plus pods, events and pods/log. One wording nit: values.yaml:395 says "the 7 Gameplane CRDs", but the chart ships 9 (clusters and networkcaptures aren't readable by the MCP server). Noted here, not raised.
- The PrometheusRule and the Grafana dashboard reference only metrics that exist (`gameplane_gameservers{phase}` and `gameplane_backups{phase}` at `operator/internal/controller/metrics.go:36-46`, plus controller-runtime and workqueue series). The alert list in install.md:292-300 matches prometheusrule.yaml.
- networking.md's flavor descriptions (metallb annotations, the cilium label and annotation, `none`) match the `operator.addressManager` values comment and install.md:200-215.
- The API and operator RBAC in the chart covers everything the generated `operator/config/rbac/role.yaml` lists. The chart is broader where the controllers create workloads (statefulsets, deployments, secrets in the games namespace), and those markers under-declare. The chart is what runs, so this is not raised.
- The `crd-apply-hook.yaml:15-19` comment says the gametemplates CRD needs server-side apply because of the client-side annotation limit. The CRD's JSON is currently ~81 KB, well under the 256 KiB annotation limit, so this claim is stale today but harmless. The hook's CRD ConfigMap is ~381 KB, under the 1 MiB ConfigMap limit.
- The `Makefile:295-297` and `Makefile:381-383` comments say "pre-install/pre-upgrade hook", but the hook is pre-upgrade only (crd-apply-hook.yaml:9-13, 27). This is in the Makefile, outside this chunk; noted for the hack reviewer.

## Candidate findings

held candidates: 5 (see OD-019)

### C-charts-gameplane-01: `helm uninstall` deletes the games namespace, and with it every GameServer and its world data, which contradicts three docs that promise GameServers survive
- **Location**: `charts/gameplane/templates/namespaces.yaml:1-10`; contradicted claims at `docs/install.md:544-546`, `charts/gameplane/values.yaml:30-31`, `charts/gameplane/templates/crd-apply-hook.yaml:22-24`
- **Category**: correctness
- **Suggested severity**: S1
- **Observation / repro**:
  1. `namespaces.yaml` renders `kind: Namespace` `gameplane-games` as a normal release resource, with no `helm.sh/resource-policy: keep` annotation. Confirmed in `helm template`: the Namespace carries `app.kubernetes.io/managed-by: Helm` and no annotations. `deploy/kind/up.sh:206-209` also states the chart "created and owned" this namespace.
  2. `helm uninstall gameplane -n gameplane-system` deletes every resource in the release manifest that isn't marked keep, including this Namespace. `make dev-down CLUSTER=remote` does exactly this (`Makefile:395`).
  3. Namespace deletion removes every namespaced object in it: GameServers, StatefulSets, the `<gs>-data` PVCs, backup-destination Secrets and RCON Secrets. The data PVC also has the GameServer as its controller owner (`operator/internal/controller/gameserver_controller.go:554`), so it is garbage-collected either way. With a `Delete` reclaim policy (the default for most dynamic provisioners), the world data is gone.
- **Expected**: install.md:544-546 says "CRDs are never owned or deleted by Helm here, so `helm uninstall` leaves your GameServers intact." values.yaml:30-31 says "It never deletes CRDs, so `helm uninstall` still leaves your GameServers intact." Either the games namespace survives uninstall, or the docs warn that uninstall destroys all game servers and their volumes.
- **Actual**: Uninstall removes the namespace, and every GameServer and PVC with it. Only the CRDs and the API's SQLite PVC (api.yaml:186-187, `keep`) survive. The result is a database that references servers that no longer exist.

### C-charts-gameplane-02: every `helm upgrade` of a release not named `gameplane` fails in the pre-upgrade hook
- **Location**: `charts/gameplane/templates/crd-apply-hook.yaml:98` and `:176` (`{{ .Release.Name }}-api`) vs `charts/gameplane/templates/api.yaml:200` (hardcoded `name: gameplane-api`)
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. `helm template gp charts/gameplane -n gpns` renders the Deployment `gameplane-api`. It also renders the hook Role with `resourceNames: ["gp-api"]` and the initContainer `kubectl patch deployment gp-api` (render lines 8239 and 8314-8315).
  2. With the default `api.db.driver: sqlite` and `crds.autoApply.enabled: true`, `helm upgrade gp ...` runs the pre-upgrade Job. The `migrate-api-strategy` initContainer patches a Deployment that doesn't exist and exits non-zero. The Job has `restartPolicy: Never` and `backoffLimit: 2`, so it fails, and Helm aborts the upgrade (pre-upgrade hook failed).
  3. Every upgrade is affected, not only the one from ≤ beta.5 that the migration exists for. The docs, e2e.sh, upgrade.sh and the upgrade test all use the release name `gameplane`, so CI never exercises another name.
- **Expected**: The hook targets the Deployment the chart actually renders (`gameplane-api`), or the chart documents that the release must be named `gameplane`.
- **Actual**: Any other release name can't upgrade. Workarounds are to set `crds.autoApply.enabled=false` and apply CRDs by hand, or to use `api.db.driver=postgres`.

### C-charts-gameplane-03: the documented upgrade command (`--reuse-values`) fails to render when upgrading from 0.2.0-beta.8
- **Location**: `charts/gameplane/templates/operator.yaml:283` (`.Values.capture.enabled`), `operator.yaml:305` and `api.yaml:269` (`.Values.operator.gameDataStorage.storageClassName`), `api.yaml:369` (`.Values.capture.enabled`); documented command at `docs/install.md:560-565`
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. `capture.*` and `operator.gameDataStorage.*` were added to values.yaml after tag `v0.2.0-beta.8` (`git diff v0.2.0-beta.8 HEAD -- charts/gameplane/values.yaml`).
  2. `helm upgrade --reuse-values` renders the new templates against the previous release's chart values plus its user values. The new chart's defaults are dropped, which install.md:570-573 itself explains.
  3. Simulated by copying the chart, swapping in `git show v0.2.0-beta.8:charts/gameplane/values.yaml` and running `helm template`: `Error: template: gameplane/templates/operator.yaml:283:26: executing ... at <.Values.capture.enabled>: nil pointer evaluating interface {}.enabled`. After adding `capture.enabled`, the next failure is `operator.yaml:305:26 ... <.Values.operator.gameDataStorage.storageClassName>: nil pointer`.
  4. install.md:561-565 gives `helm upgrade ... --reuse-values` as *the* upgrade command. This audit's own rc-deploy procedure uses it too (`specs/done_018-v0-3-release-readiness/tasks.md:96`, `:128`; `research.md:52`). The CI upgrade test (`test/e2e/upgrade_e2e_test.go:110-120`) passes `--set` without `--reuse-values`, so CI doesn't catch this.
- **Expected**: The documented upgrade path from the last release works, either through nil-safe lookups in the templates (`dig` / `default dict`) or through docs that name `--reset-then-reuse-values` as the command for this release.
- **Actual**: The primary documented command fails for every beta.8 user. The caution at install.md:570-575 gives the workaround (`--reset-then-reuse-values`) only after the failure.

### C-charts-gameplane-04: with the default NetworkPolicies, restic backup and restore Jobs can reach only DNS, so backups to any network repository fail
- **Location**: `charts/gameplane/templates/networkpolicies.yaml:21-37` (`default-deny-egress`, `podSelector: {}`, DNS only) and `:146-169` (`allow-game-public-egress` selects only `app.kubernetes.io/name: gameplane-game`); Job pods are built at `operator/internal/controller/backup_controller.go:245-248` and `restore_controller.go:139-143` with no pod labels
- **Category**: correctness
- **Suggested severity**: S2 (S3 if the verifier counts an undocumented hand-written NetworkPolicy as a workaround)
- **Observation / repro**:
  1. Default install: `networkPolicies.enabled: true`.
  2. A backup destination is a restic repository URL (`api/internal/handlers/destinations.go:62-66,147-152`, key `repo`). The Job only mounts the game PVC, so in practice every repository is remote (`s3:`, `rest:`, `b2:`, `sftp:`).
  3. The Backup and Restore Job pods carry only the Job controller's labels. They don't match `gameplane-game`, so the only egress they get is `default-deny-egress`: port 53 to kube-system.
  4. The e2e suite works around this with its own policy (`test/e2e/fixtures/restic-server.yaml:61-85`), whose comment reads: "Default game-namespace egress allows only DNS. Backup pods run in gameplane-games and need to reach the restic-server".
  5. No chart value or doc tells users to add one. security.md:177-181 and :217-223 describe the DNS-only default but never mention backup Jobs.
- **Expected**: A restic backup to a configured destination works on a default install, or the docs state the required extra egress policy.
- **Actual**: On a default install, restic backup and restore Jobs can't reach their repository. CSI volume-snapshot backups aren't affected.

### C-charts-gameplane-05: the agent PodMonitor can't scrape anything, because the agent port requires a client certificate
- **Location**: `charts/gameplane/templates/servicemonitors.yaml:47-61` (`podMetricsEndpoints: [{ port: agent, interval: 30s }]`, no scheme or TLS config); `agent/cmd/main.go:165,185-192`; `agent/internal/auth/auth.go:108-111`; `operator/internal/controller/gameserver_controller.go:2104-2106`
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. The operator always starts the agent with `--tls-cert/--tls-key/--tls-client-ca`. The agent then serves its whole router on :8090, including `/metrics`, over TLS with `ClientAuth: tls.RequireAndVerifyClientCert`.
  2. The chart's PodMonitor has Prometheus scrape `http://<pod>:8090/metrics`: plain HTTP, no client certificate. The agent's TLS listener rejects this. `scheme: https` alone would also fail, because Prometheus holds no certificate signed by the agent CA.
  3. Separately, `allow-api-to-agent` (networkpolicies.yaml:76-102) admits only the API and operator pods to 8090. Prometheus reaches the port only through the RFC1918 fallback of `allow-kubelet-probes`, and stops reaching it once `kubeletCIDRs` or `probePorts` is narrowed as the docs recommend.
- **Expected**: install.md:239-241 says `serviceMonitors.enabled` adds "a `PodMonitor` that scrapes per-GameServer agent metrics from game pods", and install.md:263-264 says the agent metrics are "scraped from port 8090 in each game pod when `serviceMonitors.enabled: true`". The agent metrics listed at install.md:266-275 should be scrapeable, or the docs should say they aren't.
- **Actual**: Every agent scrape target is down on a chart-provisioned (mTLS) install. No `gameplane_agent_*` series ever reach Prometheus.

### C-charts-gameplane-06: the bundled telemetry receiver's `/metrics` can't be scraped with the default NetworkPolicies (a new aspect of SECURITY_AUDIT.md §6)
- **Location**: `charts/gameplane/templates/telemetry-receiver.yaml:72-93`; `telemetry-receiver/main.go:52,112` (`/metrics` on the same `:8080` listener as `/ingest`)
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. `api.telemetry.receiver.enabled=true` with the default `networkPolicies.enabled=true`.
  2. The receiver's NetworkPolicy admits TCP 8080 only from pods labelled `app.kubernetes.io/name: gameplane-api`, and the chart ships no ServiceMonitor for the receiver.
  3. Prometheus scraping `gameplane-telemetry-receiver:8080/metrics` is dropped by the policy. SECURITY_AUDIT.md §6 added this policy to restrict `/ingest`; its side effect on `/metrics` is not tracked there.
- **Expected**: values.yaml:230-233 says the receiver "accepts reports on /ingest and exposes aggregate Prometheus metrics on /metrics", and install.md:393-397 says the same. Those metrics are the receiver's only output, so they should be reachable by a Prometheus.
- **Actual**: The only way to read the metrics is `kubectl port-forward`. A Prometheus in the cluster can't scrape them.

### C-charts-gameplane-07: reinstalling after an uninstall keeps the old CRD schema, because the CRD hook doesn't run on install
- **Location**: `charts/gameplane/templates/crd-apply-hook.yaml:9-13,26-28` (hook is `pre-upgrade` only); claim at `docs/install.md:542-546` and `charts/gameplane/values.yaml:24-29`
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. Install chart version N, `helm uninstall` (CRDs are deliberately left behind), then `helm install` chart version N+1.
  2. On install, Helm's native `crds/` handling creates missing CRDs but skips any that already exist ("CRD … is already present. Skipping."). The autoApply Job is `pre-upgrade` only, so it doesn't run.
  3. The cluster keeps version N's CRD schemas. The apiserver prunes any field added in N+1 from objects the new API or operator writes, until the next `helm upgrade` runs the hook.
- **Expected**: install.md:542-543 says "A fresh install gets its CRDs from Helm's native `crds/` handling", which implies current CRDs on every install.
- **Actual**: This holds only when no Gameplane CRDs exist yet. Uninstall-then-install, the path users take to reset a broken release, gets stale schemas with no warning.

### C-charts-gameplane-08: the default OCI catalog lists 16 of the 30 published modules, so the 14 modules added for spec 015 never appear in the default Modules page
- **Location**: `charts/gameplane/values.yaml:478-496` (`defaultModuleSource.oci.modules`)
- **Category**: correctness
- **Suggested severity**: S3
- **Observation / repro**:
  1. `modules/` has 30 module directories. The release workflow pushes every one of them to `ghcr.io/<owner>/gameplane-modules` (`.github/workflows/release.yaml:311-313`, `modules/build.sh:206` iterates `"$MODULES_DIR"/*/`).
  2. The default `type: oci` source discovers only the names listed. values.yaml:478-479: "Each name is an OCI repo under url/ above; the operator discovers that module's versions from its tags."
  3. The list has 16 names. It omits `ark-survival-evolved`, `arma-reforger`, `beammp`, `euro-truck-simulator-2`, `farming-simulator-25`, `fivem`, `hell-let-loose`, `left-4-dead-2`, `mount-and-blade-2-bannerlord`, `nuclear-option`, `squad`, `team-fortress-2`, `the-isle` and `tmodloader`.
- **Expected**: `specs/done_015-top-steam-game-modules/spec.md:25`: "An administrator selects any game from the top 100 Steam list (such as Team Fortress 2, FiveM, Squad, Farming Simulator 25, Euro Truck Simulator 2, BeamMP, Arma Reforger, or Left 4 Dead 2) via the Gameplane catalog".
- **Actual**: None of these appear in the default catalog on a default v0.3 install. Admins must add names to `oci.modules` or create a second ModuleSource. This is separate from the tracked git `ref` pin and from README/website counts; the T054 resolution updates doc counts, not this list.

### C-charts-gameplane-09: install.md's Upgrading section says CRDs are never updated on upgrade and must be applied by hand, contradicting its own CRD-hook section
- **Location**: `docs/install.md:595-600`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: install.md:595-600 reads: "CRDs are installed once by Helm and not updated on upgrade (by design). For CRD schema changes, run: `kubectl apply -f charts/gameplane/crds/`". The same page, at 528-532, says: "`crds.autoApply` (enabled by default) ships a **pre-upgrade hook** that runs `kubectl apply --server-side` over the current CRDs on every `helm upgrade` ... **No manual `kubectl apply` step is needed.**" Lines 567-568 repeat this. The late paragraph also uses client-side apply, while line 539 uses `--server-side`.
- **Expected**: One consistent statement, matching crd-apply-hook.yaml.
- **Actual**: The page contradicts itself.

### C-charts-gameplane-10: install.md gives the wrong default for `capture.defaultMaxSizeBytes` (5 GiB), a value the chart warns would get the pod evicted
- **Location**: `docs/install.md:228-229` vs `charts/gameplane/values.yaml:540-545`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: install.md: "`capture.defaultMaxSizeBytes` — default maximum file size per capture in bytes (default `5368709120` = 5 GiB)". values.yaml:545: `defaultMaxSizeBytes: 943718400   # 900 MiB (safely under the 1 GiB emptyDir limit)`. values.yaml:541-544 adds that the default "is clamped below the capture emptyDir sizeLimit (1 GiB, hardcoded in the operator) to prevent pod eviction". The chart always passes the value (api.yaml:376-377, operator.yaml:289).
- **Expected**: 943718400 (900 MiB).
- **Actual**: The docs state a value that exceeds the capture volume's 1 GiB `sizeLimit`, and an admin copying it would trigger the eviction the values comment warns about.

### C-charts-gameplane-11: install.md says `defaultModuleSource.git.ref` defaults to `main`; the chart default is a pinned tag (a new aspect of the tracked F-(c) ref item)
- **Location**: `docs/install.md:176` vs `charts/gameplane/values.yaml:464-473`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: install.md:176 reads "`git.ref` — git branch/tag (default `main`)". values.yaml:473 sets `ref: v0.2.0-beta.6`, and values.yaml:464-472 explains why tracking `main` is deliberately not the default ("tracking main means a module version bump upstream can orphan every already-installed Module CR"). `main` is only the template fallback when the value is emptied (modulesource.yaml:16). The tracked item concerns which tag the value pins; this one is that the docs describe the default as `main`, which was the behaviour the pin was introduced to avoid.
- **Expected**: The docs name the pinned tag, or say "pinned to the tested module-repo tag".
- **Actual**: The docs say `main`.

### C-charts-gameplane-12: install.md lists `api.oidc.displayName` as a Helm setting; the chart has no such value and never passes the flag
- **Location**: `docs/install.md:127-128`; `charts/gameplane/values.yaml:239-275`; `charts/gameplane/templates/api.yaml:249-268`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: install.md:127-128: "Core connection: `issuer` / `clientID` / `clientSecretRef` / `redirectURL` / `displayName` — OIDC provider credentials and endpoints." `api/cmd/main.go:468` defines `--oidc-display-name` (env `GAMEPLANE_OIDC_DISPLAY_NAME`, default "Single sign-on"). values.yaml has no `api.oidc.displayName`, and api.yaml's OIDC block passes only issuer, client id, redirect URL, groups claim, default role and role mappings. `--set api.oidc.displayName=Keycloak` renders nothing.
- **Expected**: Either the chart wires `api.oidc.displayName` to `--oidc-display-name`, or the docs drop it.
- **Actual**: The setting is silently ignored, and the login button keeps "Single sign-on".

### C-charts-gameplane-13: the chart docs say an empty S3 `region` means "path-style requests"; the code treats empty as `us-east-1`, and region doesn't select the addressing style
- **Location**: `charts/gameplane/values.yaml:205`; `docs/install.md:357-358`; code at `api/internal/audit/s3.go:67-75`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: values.yaml:205 reads `region: ""  # S3 region, e.g. "us-east-1" (empty = path-style requests)`, and install.md:357-358 matches. `NewS3Sink` does `if region == "" { region = "us-east-1" }` and builds `minio.Options{..., Region: region}` without setting `BucketLookup`. The addressing style is minio-go's auto default, not a function of region. The API's own flag help (`api/cmd/main.go:497`) says "defaults to us-east-1 if empty", which contradicts the chart docs.
- **Expected**: The chart docs say an empty region means `us-east-1`.
- **Actual**: The docs describe behaviour the code doesn't have.

### C-charts-gameplane-14: security.md describes `podSecurity.enforceRestricted=false` as a cluster-wide switch with per-pod opt-in; the chart only labels the games namespace
- **Location**: `docs/security.md:290-293`; `charts/gameplane/templates/namespaces.yaml:7-10`; `charts/gameplane/values.yaml:440`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: security.md option 3 reads: "**Disable `restricted` cluster-wide** — ... set `podSecurity.enforceRestricted=false` in the Helm values. Captures will work, and other pods are not forced into `restricted` mode (they can still opt in per-pod via labels)." The value adds or omits `pod-security.kubernetes.io/enforce: restricted` on the games Namespace only (values.yaml:440: "label gamesNamespace with ..."). It has no cluster-wide effect, and Pod Security Admission has no per-pod opt-in label. Option 3 is therefore the same action as option 2. This is a different sentence from C-docs-02 (line 282, "default is true").
- **Expected**: The option describes the games-namespace label only.
- **Actual**: The docs state a scope and a mechanism the chart doesn't have.

### C-charts-gameplane-15: install.md names only `{operator,api,agent}` as the images a release pins and the GHCR packages to make public, but a default install also pulls `web`
- **Location**: `docs/install.md:23-25`, `docs/install.md:69-74`
- **Category**: docs-drift
- **Suggested severity**: S4
- **Observation / repro**: install.md:23-25: "The chart's `appVersion` pins matching component images (`ghcr.io/valgulnecron/gameplane/{operator,api,agent}:<version>`)". install.md:69-72: "The maintainer makes the `gameplane/operator`, `gameplane/api`, `gameplane/agent`, and `charts/gameplane` packages public once ... so anonymous `helm install` / `docker pull` works." `web.enabled: true` is the default (values.yaml:288-289), and `_helpers.tpl:19-21` resolves `{registry}/web:{appVersion}`. The chart also derives sentinel, tunnel-frp/tailscale/playit, capture-sidecar, mcp-server, audit-syslog-bridge and telemetry-receiver images from the same registry and tag (`_helpers.tpl:31-81`).
- **Expected**: The list includes `web` (and the on-demand images).
- **Actual**: Following the note literally leaves `gameplane/web` private, and the default install's web pod then fails to pull anonymously.

### C-charts-gameplane-16: `operator/config/crd/kustomization.yaml` lists 7 of the 9 CRDs
- **Location**: `operator/config/crd/kustomization.yaml:3-10`
- **Category**: dead-code
- **Suggested severity**: S4
- **Observation / repro**: The kustomization's `resources:` names gametemplates, gameservers, backups, backupschedules, restores, modulesources and modules. `gameplane.local_clusters.yaml` and `gameplane.local_networkcaptures.yaml`, which sit in the same directory and ship in the chart, are missing. Nothing in the repo consumes the kustomization: grepping `.go`, `.sh`, the Makefile and YAML for `config/crd` finds only controller-gen output paths and comments.
- **Expected**: The kustomization lists all 9 CRDs, or is removed as unused.
- **Actual**: `kubectl apply -k operator/config/crd` installs neither the Cluster nor the NetworkCapture CRD.

## Questions (not findings)

- The hook image `registry.k8s.io/kubectl:v1.36.3` (values.yaml:38) against the "Kubernetes 1.28+" prerequisite (install.md:5): kubectl's skew policy supports ±1 minor version. `kubectl apply --server-side` and `kubectl patch` probably still work against 1.28-1.34, but that is outside the supported skew. Should the doc or the pin change?
- On kubelab, will the rc-deploy `helm upgrade --reuse-values` (tasks.md:96) render? Only if the chart stored in kubelab's release already has `capture` and `operator.gameDataStorage` keys (see C-charts-gameplane-03). `helm get values --all -n gameplane-system gameplane` shows this before T018 runs. The baseline snapshot (`evidence/baseline/helm-values.json`) has only user-supplied values, so it can't answer the question.
- `default-deny-egress` (networkpolicies.yaml:31-37) admits DNS only to pods in the `kube-system` namespace. Clusters using NodeLocal DNSCache (a link-local, hostNetwork listener) or DNS outside kube-system would lose name resolution in the games namespace. Is that a supported-topology limitation to document?
- `gameEgress.ports` defaults to TCP 80/443 (values.yaml:345-347). Some dedicated servers (Steam titles) register with master servers or authenticate over other ports and UDP. Does any shipped module need more than 80/443 outbound? The answer would come from the live module rows, not the chart.
- install.md:102 says "`operator.replicas` — leader-elected, safe at 2+". values.yaml:53 lets `operator.leaderElect` be false, and the chart doesn't refuse `replicas > 1` without it. The same goes for `api.replicas > 1` with `db.driver=sqlite` (values.yaml:112), which the chart doesn't refuse either. Should either combination be guarded?
