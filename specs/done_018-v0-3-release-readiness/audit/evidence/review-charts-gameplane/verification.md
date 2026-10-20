# T045 chart chunk: independent verification (opus)

Verifier input: `notes.md` in this directory (opus reviewer), candidates C-charts-gameplane-01 to -16. Held candidates for this component are verified separately, off-git (OD-019).

Method: I checked every candidate against master `13a859ff`. `git diff master HEAD` is empty for `charts/`, `deploy/`, `docs/`, `api/`, `operator/`, `agent/`, `telemetry-receiver/`, `web/nginx.conf.template` and the `Makefile`, so the branch and master agree for everything cited. I read each cited template, value and doc line plus the code the chart wires up: `operator/internal/controller/{gameserver_controller,backup_controller,restore_controller}.go`, `operator/internal/modsrc/oci.go`, `agent/cmd/main.go`, `agent/internal/auth/auth.go`, `telemetry-receiver/main.go`, `api/internal/audit/s3.go`, `api/cmd/main.go`. I rendered the chart with `helm template` (Helm v3.19.0): defaults as release `gameplane`; release `gp` in namespace `gpns`; and a copy of the chart with `values.yaml` replaced by `git show v0.2.0-beta.8:charts/gameplane/values.yaml`, which is the values set `helm upgrade --reuse-values` renders with. For C-15 I asked GHCR anonymously for manifests of every image the chart derives. No cluster was reachable from this session (no kube context is set), so nothing was run live. No test or lint suite was run, and no repo file other than this one was changed. I checked `audit/findings.md` and the other chunks' notes for duplicates: C-16 duplicates an operator candidate. C-05 is also raised by the agent review as C-agent-07. I keep it here because the defect is in the chart's `servicemonitors.yaml`, and the agent's TLS listener is correct as designed. C-06 is the item the aux-chunk verifier rejected C-telemetry-receiver-01 in favour of, and it stays kept here.

Severity follows research R3.

| Candidate | Verdict (kept/rejected) | Severity | Reason |
|---|---|---|---|
| C-charts-gameplane-01 | kept | S1 | Confirmed. The games Namespace is an ordinary release resource with no `helm.sh/resource-policy: keep` (`helm template` output, and `grep resource-policy` finds only the API PVC). Helm's uninstall deletes every non-kept resource, Namespace included. GameServers carry no finalizer (the only `AddFinalizer` in the operator is on Modules, `module_controller.go:68`), so namespace deletion completes and takes every GameServer, StatefulSet and `<gs>-data` PVC with it. Three docs promise the opposite. Data loss, so S1. The audit's own `procedures/upgrade.md:76-95` (step 3, reinstall at beta.8) runs `helm uninstall gameplane` on kubelab and then expects the existing GameServers to still be running. See the section below. |
| C-charts-gameplane-02 | kept | S3 | Confirmed by render. With release `gp`, the hook Role allows only `gp-api` (`crd-apply-hook.yaml:98`) and the `migrate-api-strategy` initContainer runs `kubectl patch deployment gp-api` (`:176`), but `api.yaml:200` always names the Deployment `gameplane-api`. The patch returns NotFound, the Job fails (`backoffLimit: 2`, `restartPolicy: Never`), and the pre-upgrade hook aborts the upgrade. This happens with the defaults `api.db.driver: sqlite` and `crds.autoApply.enabled: true`. No doc limits the release name. A workaround exists (`crds.autoApply.enabled=false` plus a manual CRD apply), so S3. |
| C-charts-gameplane-03 | kept | S3 | Reproduced. Rendering with the beta.8 values fails at `operator.yaml:283` (`.Values.capture.enabled`: nil pointer). After adding that key it fails at `operator.yaml:305` (`operator.gameDataStorage`). With `api.oidc.enabled=true` it also fails at `api.yaml:259` (`api.oidc.roleMappings`). Helm's `--reuse-values` replaces the new chart's defaults with the old release's chart values, so this is what `install.md:560-565` produces for every beta.8 user. The caution at `:570-575` gives the workaround (`--reset-then-reuse-values`), so S3. The audit's rc-deploy step (`tasks.md:96`) uses the same command. |
| C-charts-gameplane-04 | kept | S2 | Confirmed. Backup and Restore Jobs run in the GameServer's namespace (`backup_controller.go:245`, `restore_controller.go:139`) with a pod template that sets no labels (`:247-249`, `:141-143`). The only chart policy that selects them is `default-deny-egress` (`podSelector: {}`, DNS to kube-system only). `allow-game-public-egress` and `allow-agent-to-apiserver` select only `app.kubernetes.io/name: gameplane-game`. The operator creates no NetworkPolicy for Jobs. A restic repository is always remote in practice (the Job mounts only the game PVC, and the destination Secret's `repo` key is a URL, `destinations.go:147-152`). The e2e suite adds its own policy to make backups work (`test/e2e/fixtures/restic-server.yaml:61-85`). No doc or value mentions this. Severity S2, not S3: the core backup path fails on a default install, and neither of the two workarounds is documented or discoverable from the failure. One is a hand-written NetworkPolicy. The other is turning off the chart's isolation policies. |
| C-charts-gameplane-05 | kept | S3 | Confirmed. The operator always passes `--tls-cert/--tls-key/--tls-client-ca` to the agent (`gameserver_controller.go:2103-2106`), so `agent/cmd/main.go:185-192,231` serves the whole router, including `/metrics` (`:165`), through `ListenAndServeTLS` with `RequireAndVerifyClientCert` (`auth.go:108-113`). The PodMonitor (`servicemonitors.yaml:60-61`) has no scheme or TLS config, so Prometheus scrapes over plain HTTP and every target is down. This is an optional, off-by-default monitoring feature, so S3. Same defect as C-agent-07 (`evidence/review-agent/notes.md:138`). Keep one of the two. |
| C-charts-gameplane-06 | kept | S3 | Confirmed. The receiver's NetworkPolicy admits TCP 8080 only from `gameplane-api` pods (`telemetry-receiver.yaml:81-92`). `/metrics` is on the same listener (`telemetry-receiver/main.go:109-113`), and `servicemonitors.yaml` has no monitor for the receiver. With the default `networkPolicies.enabled: true`, no Prometheus can scrape the metrics that are the receiver's only output (`values.yaml:230-233`, `install.md:393-397`). `kubectl port-forward` is a workaround, so S3. F-019 covers only adding the policy, not this side effect. |
| C-charts-gameplane-07 | kept | S3 | Confirmed. The hook is `pre-upgrade` only (`crd-apply-hook.yaml:26-28`). Helm's `crds/` install skips any CRD that already exists, and uninstall leaves CRDs behind by design, so uninstall and then install of a newer chart keeps the old schemas. For beta.8 to this tree, `git diff --stat v0.2.0-beta.8 HEAD -- charts/gameplane/crds/` shows the gameservers (+100 lines) and gametemplates (+79) CRDs changed, including the new `spec.capture` block. The apiserver would prune that block until the next `helm upgrade`. A manual `kubectl apply --server-side -f charts/gameplane/crds/` works around it, so S3. |
| C-charts-gameplane-08 | kept | S3 | Confirmed. The default source is `type: oci` (`values.yaml:461`). `modsrc/oci.go:37-41` indexes only the names in `spec.oci.modules`, and the chart lists 16 (`values.yaml:480-496`). `modules/` has 30 directories, and the release workflow pushes every one (`release.yaml:305-313`, `modules/build.sh:206-210`). The 14 spec-015 modules never appear in a default catalog. T054 (`tasks.md:207`, OD § T054) bumps only the git `ref`, not this list. Admins can add names or a second ModuleSource, so S3. |
| C-charts-gameplane-09 | kept | S4 | Confirmed. `install.md:595-600` says CRDs are not updated on upgrade and gives a client-side `kubectl apply`. The same page says the pre-upgrade hook does it and "No manual `kubectl apply` step is needed" (`:528-532`, `:567-568`), and gives `--server-side` at `:539`. |
| C-charts-gameplane-10 | kept | S4 | Confirmed. `install.md:229` gives the default as `5368709120` (5 GiB). `values.yaml:545` is `943718400`, and `:540-544` explain it is kept under the capture emptyDir's 1 GiB `sizeLimit` (`gameserver_controller.go:1435`). The chart always passes the value (`operator.yaml:289`, `api.yaml:376-377`). |
| C-charts-gameplane-11 | kept | S4 | Confirmed. `install.md:176` says `git.ref` defaults to `main`. `values.yaml:473` pins `v0.2.0-beta.6`, and `:464-472` explain why `main` is deliberately not the default. `main` is only the template fallback for an emptied value (`modulesource.yaml:16`). F-028 / T054 change the pinned value and leave this doc line wrong either way, so this is not a duplicate. |
| C-charts-gameplane-12 | kept | S4 | Confirmed. `values.yaml:239-275` has no `api.oidc.displayName`, and `api.yaml:249-268` never passes `--oidc-display-name` (`api/cmd/main.go:468`) or `GAMEPLANE_OIDC_DISPLAY_NAME`. `helm template --set api.oidc.displayName=Keycloak` renders nothing for it. The location is wider than the notes say: `docs/oidc.md:167,231,268,284` use the same key in every walkthrough. It only affects the login-button label, so S4. |
| C-charts-gameplane-13 | kept | S4 | Confirmed. `values.yaml:205` and `install.md:357-358` say an empty region means "path-style requests". `NewS3Sink` sets an empty region to `us-east-1` (`s3.go:67-70`) and never sets `BucketLookup` (`:71-75`). The flag help (`api/cmd/main.go:497`) states the real behaviour. |
| C-charts-gameplane-14 | kept | S4 | Confirmed. `security.md:290-293` presents `podSecurity.enforceRestricted=false` as disabling `restricted` "cluster-wide", with a per-pod opt-in. The value only adds or omits the enforce label on the games Namespace (`namespaces.yaml:7-10`, `values.yaml:439-440`, default `false`). Pod Security Admission has no per-pod opt-in label. This is a different sentence from F-033 (`:282`). |
| C-charts-gameplane-15 | kept (narrowed) | S4 | The doc gap is confirmed. `install.md:23-25` lists the images `appVersion` pins as `{operator,api,agent}`, and `:69-72` names only those three (plus the chart) as the packages to make public. The chart derives 12 images from `image.registry`/`appVersion` (`_helpers.tpl:11-81`), and `web` is on by default (`values.yaml:288-289`). The reviewer's consequence is refuted: an anonymous GHCR manifest request for `web`, `sentinel`, `tunnel-*`, `mcp-server`, `audit-syslog-bridge` and `telemetry-receiver` at `0.2.0-beta.8`, and `capture-sidecar` at `edge`, returns 200, so every package is already public. What remains is an incomplete image list. That list is the only one install.md gives, and anyone mirroring images for `image.registry` would use it. |
| C-charts-gameplane-16 | rejected (duplicate) | n/a | Duplicate of **C-operator-22** step 1 (`evidence/review-operator/notes.md:288-293`), same file (`operator/config/crd/kustomization.yaml:3-10`), which belongs to the operator component. The fact is confirmed: the kustomization omits `gameplane.local_clusters.yaml` and `gameplane.local_networkcaptures.yaml`. If the operator-chunk verifier rejects C-operator-22, this item needs another look. |

### C-charts-gameplane-01

**Location:** `charts/gameplane/templates/namespaces.yaml:1-10` (games Namespace rendered as a normal release resource, no `helm.sh/resource-policy: keep`). The contradicted claims are at `docs/install.md:544-546`, `charts/gameplane/values.yaml:30-31` and `charts/gameplane/templates/crd-apply-hook.yaml:22-24`. The data PVC is owned by its GameServer (`operator/internal/controller/gameserver_controller.go:554`).

**Repro / observation:**
1. `helm template gameplane charts/gameplane -n gameplane-system` renders `kind: Namespace` `gameplane-games` with labels `app.kubernetes.io/instance`, `app.kubernetes.io/version` and `app.kubernetes.io/managed-by: Helm`, and no annotations. `grep -rn resource-policy charts/gameplane/templates/` finds only the API PVC (`api.yaml:187`).
2. `grep -rn AddFinalizer operator/internal/controller/` finds only `module_controller.go:68` (cluster-scoped Modules). GameServers have no finalizer that would hold namespace deletion after the operator is gone.
3. Live, on a scratch kind cluster only (never kubelab or any cluster holding real data): `make dev-up`, create a GameServer from a sample, write a file to its volume, then `helm uninstall gameplane -n gameplane-system`. `kubectl get ns gameplane-games` goes to `Terminating` and then NotFound. `kubectl get gameservers -A` and `kubectl get pvc -n gameplane-games` return nothing. With a `Delete` reclaim policy (k3s local-path, kind standard) the PV and its data are gone.
4. Only the CRDs and the API's `gameplane-api-data` PVC survive (`api.yaml:186-187`, `keep`). The API database still lists servers that no longer exist.
5. Blast radius inside this audit: `specs/done_018-v0-3-release-readiness/audit/procedures/upgrade.md:76-95` (baseline-beta8, step 3) runs `helm uninstall gameplane -n gameplane-system --keep-history` on kubelab and then expects `mc-fabric`, `soak-*` and `squad` to still be running. If kubelab's games namespace is Helm-owned (check with `kubectl get ns gameplane-games -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}'`), that step deletes those servers and their volumes. That step should not run until this is resolved or the namespace carries `helm.sh/resource-policy: keep`.

**Expected:** As `install.md:544-546` and `values.yaml:30-31` promise, `helm uninstall` leaves GameServers and their data in place. That means the games Namespace carries `helm.sh/resource-policy: keep` or is not a release resource. Otherwise, the docs warn plainly that uninstall destroys every game server and volume.

**Actual:** Uninstall deletes the games namespace, and every GameServer, StatefulSet, data PVC and per-server Secret in it.

### C-charts-gameplane-02

**Location:** `charts/gameplane/templates/crd-apply-hook.yaml:98` (`resourceNames: ["{{ .Release.Name }}-api"]`) and `:176` (`kubectl patch deployment {{ .Release.Name }}-api`), against `charts/gameplane/templates/api.yaml:200` (`name: gameplane-api`, hardcoded).

**Repro / observation:**
1. `helm template gp charts/gameplane -n gpns`. The only API Deployment is `name: gameplane-api`. The hook Role has `resourceNames: ["gp-api"]`, and the `migrate-api-strategy` initContainer's command is `kubectl patch deployment gp-api --type=merge ...`.
2. That initContainer renders whenever `api.db.driver` is `sqlite` (the default), inside the Job that `crds.autoApply.enabled: true` (the default) makes a `pre-upgrade` hook (`:26-28`, `:154-191`).
3. Live, on a scratch cluster: `helm install gp charts/gameplane -n gpns --create-namespace`, then `helm upgrade gp charts/gameplane -n gpns`. The initContainer logs `Error from server (NotFound): deployments.apps "gp-api" not found`. The Job fails after `backoffLimit: 2`, and Helm reports the pre-upgrade hook failed.

**Expected:** The hook patches the Deployment the chart renders (`gameplane-api`), or the docs say the release must be named `gameplane`.

**Actual:** A release with any other name installs but can never be upgraded while the defaults are on.

### C-charts-gameplane-03

**Location:** `charts/gameplane/templates/operator.yaml:283` (`.Values.capture.enabled`) and `:305` (`.Values.operator.gameDataStorage.storageClassName`); `charts/gameplane/templates/api.yaml:269` and `:369`; and, when OIDC is on, `api.yaml:259` (`.Values.api.oidc.roleMappings.*`). The documented command is at `docs/install.md:560-565`, with the caution at `:570-575`.

**Repro / observation:**
1. `git diff v0.2.0-beta.8 HEAD -- charts/gameplane/values.yaml` adds `operator.gameDataStorage`, `api.oidc.{groupsClaim,defaultRole,roleMappings}` and the whole `capture` block.
2. `helm upgrade --reuse-values` renders the new templates with the old release's chart values in place of the new defaults.
3. Simulate it: `cp -r charts/gameplane /tmp/c && git show v0.2.0-beta.8:charts/gameplane/values.yaml > /tmp/c/values.yaml && helm template gameplane /tmp/c -n gameplane-system`. This fails with `operator.yaml:283:26 ... <.Values.capture.enabled>: nil pointer evaluating interface {}.enabled`. Adding `--set capture.enabled=false` fails next at `operator.yaml:305:26 ... <.Values.operator.gameDataStorage.storageClassName>`. Adding `--set api.oidc.enabled=true` as well fails at `api.yaml:259:26 ... <.Values.api.oidc.roleMappings.admin>`.
4. The CI upgrade test does not catch this, because it passes `--set` without `--reuse-values` (`test/e2e/upgrade_e2e_test.go:110-120`).

**Expected:** The documented upgrade command works from the last release. Either the templates read new keys nil-safely (`dig`, or `default dict`), or install.md names `--reset-then-reuse-values` as the command for this upgrade.

**Actual:** `helm upgrade ... --reuse-values` from 0.2.0-beta.8 fails to render. The caution paragraph gives the fix only after the failure.

### C-charts-gameplane-04

**Location:** `charts/gameplane/templates/networkpolicies.yaml:21-37` (`default-deny-egress`, `podSelector: {}`, DNS to kube-system only), `:52-54` and `:153-155` (the only wider egress rules select `app.kubernetes.io/name: gameplane-game`). The Job pods are built at `operator/internal/controller/backup_controller.go:245-250` and `restore_controller.go:139-143`, with no pod labels.

**Repro / observation:**
1. Default install (`networkPolicies.enabled: true`) on a CNI that enforces NetworkPolicy (k3s's default does).
2. Add a backup destination with any network repository URL (`s3:`, `rest:`, `b2:`, `sftp:`) and take a restic Backup of a running server.
3. `kubectl get pod -n gameplane-games -l batch.kubernetes.io/job-name=<backup>` shows the pod labels. They are only the Job controller's, never `app.kubernetes.io/name: gameplane-game`. `kubectl get netpol -n gameplane-games` shows that only `default-deny-egress` selects that pod.
4. The restic container resolves the repository host through DNS but cannot connect. The Job fails, and the Backup goes to `Failed` (and after 15 minutes `GameplaneBackupFailed` fires, `install.md:299-300`). A Restore Job fails the same way.
5. The repo already knows this. `test/e2e/fixtures/restic-server.yaml:61-85` adds `allow-egress-to-restic` with the comment "Default game-namespace egress allows only DNS. Backup pods run in gameplane-games and need to reach the restic-server". `grep -rn -i 'networkpolic' docs/` finds nothing about backups.

**Expected:** A restic backup to a configured destination works on a default install. Either the Job pods get an egress allowance (a label plus a policy, or an operator-managed policy), or the docs give the policy an admin must add.

**Actual:** On a default install, restic backups and restores cannot reach any repository. CSI volume-snapshot backups are not affected.

### C-charts-gameplane-05

**Location:** `charts/gameplane/templates/servicemonitors.yaml:47-61` (`podMetricsEndpoints: [{ port: agent, interval: 30s }]`, no scheme or TLS). Agent side: `agent/cmd/main.go:165`, `:185-192`, `:231`; `agent/internal/auth/auth.go:108-113`; `operator/internal/controller/gameserver_controller.go:2103-2106`.

**Repro / observation:**
1. `helm template ... --set serviceMonitors.enabled=true` renders the `gameplane-agent` PodMonitor with only `port: agent` and `interval: 30s`. The Prometheus Operator's default scheme is `http`.
2. Every operator-built agent gets `--tls-cert`, `--tls-key` and `--tls-client-ca`. With the cert and key set, the agent serves every route on :8090 (including `/metrics`) through `ListenAndServeTLS`, and the `tls.Config` sets `ClientAuth: tls.RequireAndVerifyClientCert`.
3. Live: with kube-prometheus-stack installed and `serviceMonitors.enabled=true`, the `podMonitor/gameplane-games/gameplane-agent` targets in Prometheus show `down`, with the Go TLS server's "client sent an HTTP request to an HTTPS server" response. `gameplane_agent_*` returns no series. Switching to `scheme: https` alone would still fail the handshake, because Prometheus holds no certificate signed by the agent CA.

**Expected:** `install.md:239-241` and `:263-264` say the PodMonitor scrapes the agent metrics listed at `:266-275`. They reach Prometheus, or the docs say they don't.

**Actual:** On every chart install, every agent scrape target is down.

### C-charts-gameplane-06

**Location:** `charts/gameplane/templates/telemetry-receiver.yaml:72-93` (ingress NetworkPolicy); `telemetry-receiver/main.go:109-113` (`/metrics` on the same `:8080` mux as `/ingest`); `charts/gameplane/templates/servicemonitors.yaml` (no receiver monitor).

**Repro / observation:**
1. `helm template gp charts/gameplane -n gameplane-system --set api.telemetry.receiver.enabled=true --set serviceMonitors.enabled=true`. The NetworkPolicy `gameplane-telemetry-receiver` has one ingress rule: `podSelector app.kubernetes.io/name: gameplane-api` on TCP 8080, with no `namespaceSelector`. The render's only monitors are for the operator, the API and the agent.
2. Live: from a pod in the monitoring namespace, `curl -m 5 http://gameplane-telemetry-receiver.gameplane-system:8080/metrics` times out. `kubectl -n gameplane-system port-forward svc/gameplane-telemetry-receiver 8080` followed by the same `curl` against localhost returns the metrics.

**Expected:** `values.yaml:230-233` and `install.md:393-397` describe the receiver's `/metrics` as its output. A Prometheus can scrape it, through a scrape object and a policy rule for the monitoring namespace, or the docs name the extra policy needed.

**Actual:** With the default `networkPolicies.enabled: true`, no in-cluster Prometheus can reach the receiver's metrics.

### C-charts-gameplane-07

**Location:** `charts/gameplane/templates/crd-apply-hook.yaml:9-13,26-28` (hook is `pre-upgrade` only). The claim is at `docs/install.md:542-546`, with the related comment at `charts/gameplane/values.yaml:24-31`.

**Repro / observation:**
1. `git diff --stat v0.2.0-beta.8 HEAD -- charts/gameplane/crds/`: `gameplane.local_gameservers.yaml` +100 lines and `gameplane.local_gametemplates.yaml` +79 lines (including a new `spec.capture` object). `networkcaptures` is new.
2. Live, on a scratch cluster: `helm install gameplane oci://ghcr.io/valgulnecron/charts/gameplane --version 0.2.0-beta.8 -n gameplane-system --create-namespace`, then `helm uninstall gameplane -n gameplane-system` (CRDs stay), then `helm install gameplane ./charts/gameplane -n gameplane-system`. Helm logs that the existing CRDs are already present and skips them. The new `networkcaptures` CRD is created.
3. `kubectl get crd gameservers.gameplane.local -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.capture}'` prints nothing. A GameServer applied with `spec.capture.enabled: true` comes back without `spec.capture`, because the field is pruned.
4. The next `helm upgrade` runs the hook and fixes the schema.

**Expected:** `install.md:542-543` says a fresh install gets current CRDs from Helm's native `crds/` handling. A reinstall gets the chart's CRDs too, or the docs say to apply them by hand in this case.

**Actual:** Uninstall followed by install of a newer chart keeps the old CRD schemas until the first upgrade.

### C-charts-gameplane-08

**Location:** `charts/gameplane/values.yaml:461` (`type: oci`) and `:478-496` (`defaultModuleSource.oci.modules`, 16 names). The consumer is `operator/internal/modsrc/oci.go:37-41`, which indexes only the listed names.

**Repro / observation:**
1. `ls -d modules/*/ | wc -l` prints 30. `.github/workflows/release.yaml:305-313` runs `modules/build.sh push`, which loops over every directory with a `module.yaml` (`modules/build.sh:206-210`).
2. `sed -n 480,496p charts/gameplane/values.yaml` lists 16 names. The 14 missing are `ark-survival-evolved`, `arma-reforger`, `beammp`, `euro-truck-simulator-2`, `farming-simulator-25`, `fivem`, `hell-let-loose`, `left-4-dead-2`, `mount-and-blade-2-bannerlord`, `nuclear-option`, `squad`, `team-fortress-2`, `the-isle` and `tmodloader`.
3. `ociFetcher.Index` iterates `f.modules`, which the chart fills from that list (`modulesource.yaml:23-26`). Nothing lists the registry.
4. Live: on a default install, `kubectl get modulesource default -o jsonpath='{.status.modules[*].name}'` shows 16 names, and the dashboard's Modules page shows the same 16.

**Expected:** `specs/done_015-top-steam-game-modules/spec.md:25` (US1): an administrator selects TF2, FiveM, Squad, FS25, ETS2, BeamMP, Arma Reforger or L4D2 "via the Gameplane catalog". The default catalog lists every published module.

**Actual:** None of the 14 spec-015 modules appear in the default catalog. T054 changes only the git `ref`, which a default install does not use.

### C-charts-gameplane-09

**Location:** `docs/install.md:595-600`.

**Repro / observation:**
1. `sed -n 595,600p docs/install.md`: "CRDs are installed once by Helm and not updated on upgrade (by design). For CRD schema changes, run: `kubectl apply -f charts/gameplane/crds/`".
2. `sed -n 528,540p docs/install.md`: the pre-upgrade hook applies CRDs on every `helm upgrade`, "**No manual `kubectl apply` step is needed.**", and the manual command (for `autoApply.enabled=false` only) uses `--server-side`. `:567-568` repeats that CRDs update automatically.

**Expected:** One statement, matching `crd-apply-hook.yaml`. Any manual command uses `--server-side`.

**Actual:** The Upgrading section contradicts the CRD caveat section of the same page.

### C-charts-gameplane-10

**Location:** `docs/install.md:228-229`; compare `charts/gameplane/values.yaml:540-545`.

**Repro / observation:**
1. `sed -n 228,229p docs/install.md`: "default `5368709120` = 5 GiB".
2. `sed -n 545p charts/gameplane/values.yaml`: `defaultMaxSizeBytes: 943718400   # 900 MiB (safely under the 1 GiB emptyDir limit)`. `operator/internal/controller/gameserver_controller.go:1435` sets the capture volume's `SizeLimit` to 1Gi.

**Expected:** The docs give 943718400 (900 MiB) and say it must stay under the 1 GiB volume limit.

**Actual:** The docs give 5 GiB. An admin who sets that value gets captures that outgrow the emptyDir, and the pod is evicted.

### C-charts-gameplane-11

**Location:** `docs/install.md:176`; compare `charts/gameplane/values.yaml:464-473`.

**Repro / observation:**
1. `sed -n 176p docs/install.md`: "`git.ref` — git branch/tag (default `main`)".
2. `sed -n 473p charts/gameplane/values.yaml`: `ref: v0.2.0-beta.6`. The comment at `:464-472` explains that tracking `main` orphans installed Modules. `modulesource.yaml:16` falls back to `main` only when the value is emptied.

**Expected:** The docs describe the default as the pinned, tested module-repo tag.

**Actual:** The docs say `main`, the behaviour the pin was added to avoid.

### C-charts-gameplane-12

**Location:** `docs/install.md:127-128`, and `docs/oidc.md:167`, `:231`, `:268`, `:284` (every Helm walkthrough). Chart side: `charts/gameplane/values.yaml:239-275` (no key) and `charts/gameplane/templates/api.yaml:249-268` (no flag). The flag exists at `api/cmd/main.go:468`.

**Repro / observation:**
1. `grep -n displayName charts/gameplane/values.yaml charts/gameplane/templates/*.yaml` finds nothing.
2. `helm template gameplane charts/gameplane -n gameplane-system --set api.oidc.enabled=true --set api.oidc.issuer=https://x --set api.oidc.clientID=g --set api.oidc.displayName=Keycloak | grep -c oidc-display-name` prints `0`.
3. The API therefore keeps its default label, and the login button reads "Single sign-on".

**Expected:** The chart wires `api.oidc.displayName` to `--oidc-display-name`, or the docs drop the key.

**Actual:** The documented key is silently ignored.

Not a candidate here, but seen while checking this one: the same `docs/oidc.md` walkthroughs give `clientSecretRef` as a plain string, and `helm template ... --set api.oidc.clientSecretRef=keycloak-oidc-secret` fails at `api.yaml:339` ("can't evaluate field name"). None of them sets `api.oidc.enabled=true`. This belongs to the docs chunk.

### C-charts-gameplane-13

**Location:** `charts/gameplane/values.yaml:205`; `docs/install.md:357-358`. Code: `api/internal/audit/s3.go:67-75`.

**Repro / observation:**
1. `sed -n 205p charts/gameplane/values.yaml`: `region: ""  # S3 region, e.g. "us-east-1" (empty = path-style requests)`. `install.md:357-358` says the same.
2. `sed -n 67,75p api/internal/audit/s3.go`: an empty region becomes `us-east-1`, and `minio.Options` sets no `BucketLookup`, so the addressing style is minio-go's automatic default whatever the region.
3. `api/cmd/main.go:497` (flag help): "defaults to us-east-1 if empty".

**Expected:** The chart docs say an empty region means `us-east-1`.

**Actual:** The chart docs describe an addressing behaviour the code does not have.

### C-charts-gameplane-14

**Location:** `docs/security.md:290-293`. Chart: `charts/gameplane/templates/namespaces.yaml:7-10` and `charts/gameplane/values.yaml:439-440`.

**Repro / observation:**
1. `sed -n 290,293p docs/security.md`: "**Disable `restricted` cluster-wide** — ... set `podSecurity.enforceRestricted=false` ... other pods are not forced into `restricted` mode (they can still opt in per-pod via labels)."
2. `namespaces.yaml:7-10` adds `pod-security.kubernetes.io/enforce: restricted` to the games Namespace only when the value is true. Nothing else in the chart reads the value. Its default is already `false`.
3. Pod Security Admission is configured per namespace (labels) or per cluster (admission config). There is no per-pod opt-in label.

**Expected:** Option 3 says it removes the chart's `restricted` label from the games namespace (which option 2 already covers), and notes it is the default.

**Actual:** The docs claim a cluster-wide scope and a per-pod mechanism that neither the chart nor Kubernetes provides.

### C-charts-gameplane-15

**Location:** `docs/install.md:23-25` and `:69-74`. Chart: `charts/gameplane/templates/_helpers.tpl:11-81` and `charts/gameplane/values.yaml:288-289`.

**Repro / observation:**
1. `sed -n 23,25p docs/install.md`: "`appVersion` pins matching component images (`ghcr.io/valgulnecron/gameplane/{operator,api,agent}:<version>`)". `sed -n 69,72p` names only `gameplane/operator`, `gameplane/api`, `gameplane/agent` and `charts/gameplane` as the packages to make public.
2. `grep -n 'printf "%s/' charts/gameplane/templates/_helpers.tpl` lists 12 images derived from `image.registry` and the tag: operator, api, web, agent, audit-syslog-bridge, telemetry-receiver, sentinel, tunnel-frp, tunnel-tailscale, tunnel-playit, mcp-server and capture-sidecar. `web.enabled` is `true` by default.
3. For context, GHCR already serves all of them anonymously (a token from `https://ghcr.io/token?scope=repository:valgulnecron/gameplane/<c>:pull`, then a manifest GET, returns 200 for each). So today nothing fails to pull.

**Expected:** install.md lists every image the chart can pull under `image.registry` (at least `web`, which is on by default), so mirroring for a private registry or a fork's GHCR setup is complete.

**Actual:** The only image list in install.md names 3 of 12 images and leaves out one that a default install runs.
