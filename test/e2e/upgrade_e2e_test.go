//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

// TestUpgrade_FromPreviousRelease covers the single most dangerous operation a
// user performs and the only one CI never exercised: `helm upgrade`.
//
// The cluster this runs against was installed by deploy/kind/upgrade.sh from
// the PUBLISHED chart and PUBLISHED GHCR images of the previous release — not
// from the working tree. The test seeds real state into it, upgrades to the
// working-tree chart, and asserts nothing was lost.
//
// It is deliberately NOT t.Parallel(): it upgrades the whole Helm release out
// from under everything in the namespace, so it gets its own cluster and its
// own CI job (the same reason the multicluster bucket has one).
//
// The upgrade checks:
//
//  1. CRDs. Helm installs crds/ on first install and NEVER updates them on
//     upgrade. The chart works around that with a pre-upgrade hook
//     (crds.autoApply), which until now had zero e2e coverage of the path it
//     was written for. Asserted by diffing the live CRD schema before/after.
//  2. Workloads. A running GameServer and its data must survive.
//  3. The database. Migrations run against a populated SQLite PVC on startup;
//     a user created before the upgrade must still be able to log in after.
//  4. The new components must actually come up clean.
//  5. The live credential migration preflight must reject missing or altered
//     OIDC Secrets and module uploads, then accept faithful copies.
//
// A final phase (F-218) then covers the install-side twin of (1): a fresh
// `helm install` onto CRDs an uninstalled older release left behind must
// also bring them up to date. It runs last so steps 1-6 start from the old
// release's genuinely stale schema.
func TestUpgrade_FromPreviousRelease(t *testing.T) {
	ctx := t.Context()
	ns := "gameplane-games"
	tmpl := "e2e-upgrade-busybox"
	gs := "e2e-upgrade-target"

	const (
		adminUser = "upgradeadmin"
		adminPass = "upgrade-admin-pw-1"
		marker    = "gameplane-upgrade-marker-payload"
	)

	// ---- 1. seed state into the OLD release ------------------------------

	// Bootstrap the admin BEFORE upgrading. Logging in with these same
	// credentials afterwards is what proves the database and its migrations
	// survived: the user row only exists in the pre-upgrade SQLite file.
	envInstance.BootstrapAdmin(t, adminUser, adminPass)

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	waitGameContainerReady(t, ns, gs+"-0", 2*time.Minute)
	beforeIdentity := readUpgradeIdentity(ctx, t, "gameplane-agent-ca")
	legacyReadBinding := nativeReadBinding(t)
	checkNativeCatalogAccess(t, tmpl, true)

	// `sync` so the bytes are on the PVC's backing store before the pod is
	// restarted by the upgrade, not sitting in the page cache.
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0",
		"sh", "-c", "echo -n "+marker+" > /data/marker.txt && sync"); err != nil {
		t.Fatalf("write marker: %v\n%s", err, out)
	}

	// ---- 2. work out what the upgrade is supposed to change --------------

	// Rather than hardcoding a field name that goes stale, compute the drift:
	// which properties does the working-tree CRD declare that the installed
	// (old-release) CRD does not? Those are exactly what the pre-upgrade hook
	// has to apply, and asserting on them cannot rot.
	const crdName = "gametemplates.gameplane.local"
	liveBefore := crdSpecProperties(t, crdName)
	wantAfter := manifestSpecProperties(t, "gameplane.local_gametemplates.yaml")

	var newProps []string
	for p := range wantAfter {
		if _, ok := liveBefore[p]; !ok {
			newProps = append(newProps, p)
		}
	}
	if len(newProps) == 0 {
		// Not a failure, but the CRD half of this test would be vacuous, and a
		// silently-vacuous assertion is worse than none. Say so loudly.
		t.Logf("NOTE: the working-tree GameTemplate CRD declares no spec property "+
			"the installed release lacks (%d properties, identical set) — the CRD "+
			"assertion below can only prove the hook did not REMOVE anything.",
			len(wantAfter))
	} else {
		t.Logf("upgrade must add %d GameTemplate spec propert(ies) to the live CRD: %s",
			len(newProps), strings.Join(newProps, ", "))
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	tag := envInstance.Tag
	if tag == "" {
		tag = "e2e"
	}

	// ---- 3. upgrade to the working tree ----------------------------------
	// Exercise the SSA migration even when the published fixture already uses
	// Recreate: Kubernetes defaults rollingUpdate on this merge patch.
	if out, err := envInstance.Kubectl(ctx, "-n", "gameplane-system", "patch", "deployment", "gameplane-operator",
		"--type=merge", "-p", `{"spec":{"strategy":{"type":"RollingUpdate"}}}`); err != nil {
		t.Fatalf("seed operator RollingUpdate strategy: %v\n%s", err, out)
	}

	upgradeArgs := []string{"upgrade", "gameplane",
		filepath.Join(repoRoot, "charts", "gameplane"),
		"--kube-context", envInstance.Context,
		"--namespace", "gameplane-system",
		"--set", "image.registry=gameplane-test",
		"--set", "image.tag=" + tag,
		"--set", "ingress.enabled=false",
		"--set", "web.enabled=false",
		"--set", "operator.agentImage=gameplane-test/agent:" + tag,
		// The published release's busybox template and volume use the legacy profile.
		"--set", "podSecurity.enforceRestricted=false",
		"--set", "api.resources.limits.memory=1Gi",
		"--set", "api.credentialsNamespace=" + credentialsNS,
		"--set", "operator.leaderElect=false",
		"--set", "defaultModuleSource.enabled=false",
		"--wait", "--timeout", "6m",
	}
	verifyUpgradeMigration(ctx, t, upgradeArgs)
	upgrade := exec.CommandContext(ctx, "helm", upgradeArgs...)
	upgrade.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("KUBECONFIG"))
	if out, err := upgrade.CombinedOutput(); err != nil {
		t.Fatalf("helm upgrade to the working-tree chart failed: %v\n%s", err, out)
	}
	operator, err := envInstance.K8s.AppsV1().Deployments("gameplane-system").Get(ctx, "gameplane-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read upgraded operator: %v", err)
	}
	if operator.Spec.Strategy.Type != "Recreate" || operator.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("operator strategy migration retained RollingUpdate fields: %+v", operator.Spec.Strategy)
	}

	afterIdentity := readUpgradeIdentity(ctx, t, "gameplane-agent-ca")
	for name, before := range beforeIdentity {
		if afterIdentity[name] != before {
			t.Errorf("upgrade changed %s identity: before %s, after %s", name, before, afterIdentity[name])
		}
	}

	// ---- 4. the CRDs were actually updated -------------------------------

	// This is the hook's literal contract. Without it Helm leaves the old
	// release's CRD schema in place and any new field silently vanishes on
	// apply — the failure mode the hook exists to prevent.
	liveAfter := crdSpecProperties(t, crdName)
	for _, p := range newProps {
		if _, ok := liveAfter[p]; !ok {
			t.Errorf("GameTemplate CRD is missing spec property %q after upgrade — the "+
				"crds.autoApply pre-upgrade hook did not apply the new schema", p)
		}
	}
	for p := range liveBefore {
		if _, ok := liveAfter[p]; !ok {
			t.Errorf("GameTemplate CRD LOST spec property %q across the upgrade", p)
		}
	}

	// ---- 5. the workload and its data survived ---------------------------

	got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(ctx, gs, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GameServer %s/%s is gone after the upgrade: %v", ns, gs, err)
	}
	if suspend, _, _ := unstructured.NestedBool(got.Object, "spec", "suspend"); suspend {
		t.Errorf("GameServer was suspended by the upgrade; spec.suspend should be untouched")
	}

	waitPVCBound(t, ns, gs+"-data", 2*time.Minute)
	waitGameContainerReady(t, ns, gs+"-0", 4*time.Minute)

	// Retry the read rather than exec'ing once. An upgrade re-renders the
	// StatefulSet, so the pod keeps its name but is legitimately replaced —
	// a Ready observation can be of the outgoing pod, and the exec then
	// lands mid-restart ("container not found", "no running task found").
	// That is the workload behaving correctly, not a regression, so the
	// assertion tolerates the churn and waits for the volume to be readable.
	// What is being proven is that the bytes survived, not that they were
	// readable at one exact instant.
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "cat", "/data/marker.txt")
		if err != nil {
			return false, "exec: " + err.Error() + " out=" + out
		}
		// kubectl mixes its "Defaulted container ..." stderr preamble into
		// CombinedOutput, so check for the marker as a substring.
		if !strings.Contains(out, marker) {
			return false, "marker not in output yet: " + out
		}
		return true, ""
	})

	// ---- 6. the database and its migrations survived ---------------------

	// Deliberately after the upgrade: the API pod was replaced, so this both
	// re-establishes a port-forward against the NEW pod and proves the new
	// binary's migrations ran against the old release's populated SQLite file
	// without dropping the user rows.
	client := envInstance.APIClient(t, adminUser, adminPass)
	defer client.Close()

	resp, body, err := client.Get("/servers/" + gs + "?namespace=" + ns)
	if err != nil {
		t.Fatalf("read the pre-upgrade server through the upgraded API: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /servers/%s after upgrade: status %d body %s", gs, resp.StatusCode, body)
	}
	if !strings.Contains(string(body), gs) {
		t.Errorf("upgraded API does not return the pre-upgrade GameServer %q; body=%s", gs, body)
	}
	resp.Body.Close()

	// ---- 6b. telemetry stays off for an install that never opted in ------
	//
	// The previous release had no telemetry choice saved for this install, so
	// the upgrade must not turn reporting on or show the notice (spec 022 US1
	// scenario 5): migration 015 seeds consent_source "legacy".
	resp, body, err = client.Get("/admin/telemetry/notice")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /admin/telemetry/notice after upgrade: %v status %d body %s", err, resp.StatusCode, body)
	}
	resp.Body.Close()
	var notice struct {
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal(body, &notice); err != nil {
		t.Fatalf("decode notice: %v\n%s", err, body)
	}
	if notice.Pending {
		t.Errorf("telemetry notice is pending after an upgrade; want pending:false, body=%s", body)
	}
	resp, body, err = client.Get("/admin/config")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /admin/config after upgrade: %v status %d body %s", err, resp.StatusCode, body)
	}
	resp.Body.Close()
	var cfgAll struct {
		Telemetry *struct {
			SendMetrics bool `json:"sendMetrics"`
		} `json:"telemetry"`
	}
	if err := json.Unmarshal(body, &cfgAll); err != nil {
		t.Fatalf("decode config: %v\n%s", err, body)
	}
	// Absent (nothing was ever saved) or sendMetrics false. The upgrade may add
	// "extended":false next to a saved value, so only sendMetrics is compared.
	if cfgAll.Telemetry != nil && cfgAll.Telemetry.SendMetrics {
		t.Errorf("telemetry sendMetrics is true after an upgrade with no saved choice; body=%s", body)
	}

	checkNativeRBACUpgrade(t, ns, gs, tmpl, legacyReadBinding)
	localClient := envInstance.APIClient(t, adminUser, adminPass)
	defer localClient.Close()
	waitAgentReachable(t, localClient, gs)
	verifyLocalClusterDiscovery(t, localClient)

	if os.Getenv("GAMEPLANE_E2E_ARGOCD") == "1" {
		t.Run("ArgoCD", func(t *testing.T) {
			checkArgoUpgrade(t, ns, gs, marker, adminUser, adminPass, credentialsNS)
			checkNativeCatalogAccess(t, tmpl, false)
		})
	}

	// ---- 7. F-218: a fresh `helm install` over leftover, STALE CRDs ------
	//
	// Deliberately LAST, after steps 3-6 have proven the pre-upgrade hook
	// against the old release's genuinely stale schema: running any
	// working-tree install before step 3 would server-side-apply the current
	// CRDs and make step 4 pass whether or not the pre-upgrade hook works.
	//
	// The scenario: a user uninstalls an older release (`helm uninstall`
	// never deletes CRDs) and later `helm install`s the new chart. Helm 3's
	// native crds/ install silently skips every CRD that already exists, so
	// only the crds.autoApply hook firing on pre-install can bring the
	// leftover schema up to date. Helm 4's crds/ install is a server-side
	// apply under manager "helm" that updates them itself, provided no other
	// manager holds the fields it changes. To reproduce it, the cluster's CRDs are put
	// back to the PREVIOUS release's exact content with no bundle stamp
	// (releases that predate the stamp carry none), the working-tree release
	// is uninstalled, and the working-tree chart is installed over them.

	// The GameServer carries an operator finalizer, so remove it while the
	// operator is still running; otherwise uninstalling the release (which
	// owns the games Namespace) would leave that Namespace stuck terminating.
	if err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Delete(ctx, gs, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete GameServer %s/%s before uninstall: %v", ns, gs, err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		_, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gs, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		if err != nil {
			return false, "get GameServer: " + err.Error()
		}
		return false, "GameServer still present (finalizer pending)"
	})

	uninstall := exec.CommandContext(ctx, "helm", "uninstall", "gameplane",
		"--namespace", "gameplane-system", "--wait", "--timeout", "5m")
	uninstall.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("KUBECONFIG"))
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Fatalf("helm uninstall of the working-tree release failed: %v\n%s", err, out)
	}

	// Put the previous release's CRDs back, exactly as its chart ships them
	// AND with the ownership a real install of it leaves: a server-side
	// apply under field manager "helm", which is what Helm 4's crds/ install
	// does (and the manager the crds.autoApply hook now applies under, so it
	// is also the state an upgrade-then-uninstall leaves). Being the same
	// manager that owns the working-tree schema from step 3, this apply
	// removes the properties the old release lacks, so the schema really is
	// stale. A `kubectl replace` here would instead hand .spec.versions to a
	// "kubectl-replace" manager no real flow produces, and Helm 4's crds/
	// apply would then conflict with it on reinstall.
	fromVersion := os.Getenv("GAMEPLANE_UPGRADE_FROM")
	if fromVersion == "" {
		fromVersion = defaultUpgradeFromVersion
	}
	showCRDs := exec.CommandContext(ctx, "helm", "show", "crds", upgradeFromChartRef, "--version", fromVersion)
	showCRDs.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("KUBECONFIG"))
	oldCRDs, err := showCRDs.Output()
	if err != nil {
		t.Fatalf("helm show crds %s --version %s: %v", upgradeFromChartRef, fromVersion, err)
	}
	if !strings.Contains(string(oldCRDs), "gameservers.gameplane.local") {
		t.Fatalf("helm show crds %s --version %s returned no gameservers CRD:\n%s",
			upgradeFromChartRef, fromVersion, oldCRDs)
	}
	oldCRDFile := filepath.Join(t.TempDir(), "old-release-crds.yaml")
	if err := os.WriteFile(oldCRDFile, oldCRDs, 0o600); err != nil {
		t.Fatalf("write %s: %v", oldCRDFile, err)
	}
	if out, err := envInstance.Kubectl(ctx, "apply", "--server-side", "--force-conflicts",
		"--field-manager=helm", "-f", oldCRDFile); err != nil {
		t.Fatalf("re-apply the %s release's CRDs: %v\n%s", fromVersion, err, out)
	}

	// Strip the bundle stamp from every Gameplane CRD, including any the old
	// release did not ship (which the replace above therefore left stamped).
	crds, err := envInstance.Dyn.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list CRDs: %v", err)
	}
	for i := range crds.Items {
		c := &crds.Items[i]
		if group, _, _ := unstructured.NestedString(c.Object, "spec", "group"); group != "gameplane.local" {
			continue
		}
		if _, ok := c.GetAnnotations()[crdBundleStampAnnotation]; !ok {
			continue
		}
		if out, err := envInstance.Kubectl(ctx, "annotate", "crd", c.GetName(),
			crdBundleStampAnnotation+"-"); err != nil {
			t.Fatalf("strip %s from CRD %s: %v\n%s", crdBundleStampAnnotation, c.GetName(), err, out)
		}
	}

	// Pin down that the leftover state really is stale, or the assertions
	// below would pass vacuously.
	if got := liveCRDStamp(ctx, t, "gameservers.gameplane.local"); got != "" {
		t.Fatalf("live gameservers CRD still carries %s=%q after stripping it", crdBundleStampAnnotation, got)
	}
	staleProps := crdSpecProperties(t, crdName)
	for _, p := range newProps {
		if _, ok := staleProps[p]; ok {
			t.Fatalf("GameTemplate CRD still declares %q after re-applying the %s release's CRDs; "+
				"the reinstall assertion below would be vacuous", p, fromVersion)
		}
	}

	// --timeout without --wait: hooks block `helm install` until they finish
	// regardless of --wait, so the CRD-apply Job has completed and the schema
	// is settled when this returns. operator.replicas=0 / api.enabled=false
	// because only the hook matters here. gamesNamespace is fresh so the
	// install cannot race the uninstalled release's games Namespace deletion.
	chartStamp := manifestCRDStamp(t, "gameplane.local_gameservers.yaml")
	reinstall := exec.CommandContext(ctx, "helm", "install", "gameplane",
		filepath.Join(repoRoot, "charts", "gameplane"),
		"--namespace", "gameplane-system",
		"--create-namespace",
		"--set", "image.registry=gameplane-test",
		"--set", "image.tag="+tag,
		"--set", "ingress.enabled=false",
		"--set", "web.enabled=false",
		"--set", "operator.agentImage=gameplane-test/agent:"+tag,
		"--set", "operator.leaderElect=false",
		"--set", "operator.replicas=0",
		"--set", "operator.controlPlaneNamespace="+credentialsNS,
		"--set", "api.enabled=false",
		"--set", "gamesNamespace=e2e-crd-reinstall-games",
		"--set", "defaultModuleSource.enabled=false",
		"--timeout", "3m",
	)
	reinstall.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("KUBECONFIG"))
	if out, err := reinstall.CombinedOutput(); err != nil {
		t.Fatalf("helm install of the working-tree chart over leftover CRDs failed: %v\n%s", err, out)
	}

	liveAfterReinstall := crdSpecProperties(t, crdName)
	for p := range wantAfter {
		if _, ok := liveAfterReinstall[p]; !ok {
			t.Errorf("GameTemplate CRD is missing spec property %q after a fresh `helm install` "+
				"over CRDs the %s release left behind; the crds.autoApply hook did not fire "+
				"on pre-install (F-218)", p, fromVersion)
		}
	}
	// The schema check above would also pass if the hook fired on EVERY
	// install. Pin down exactly which path brought the schema up to date,
	// and that it left the CRDs carrying the chart's stamp, which is what
	// keeps the next genuinely fresh install from firing the hook.
	// TestHelmInstall_CRDApplyHookSkippedOnFreshInstall covers the other
	// half on a fresh cluster.
	//   - Helm 3: crds/ skipped the leftover CRDs, so the hook must have fired
	//     on pre-install BECAUSE they carried no stamp.
	//   - Helm 4: crds/ server-side-applied them (stamp included) before the
	//     templates were rendered, so the stamps matched and the hook must
	//     NOT have fired on pre-install.
	wantEvents := "pre-install,pre-upgrade"
	if helmMajorVersion(ctx, t) >= 4 {
		wantEvents = "pre-upgrade"
	}
	if got := crdApplyHookEvents(ctx, t, "gameplane", "gameplane-system"); got != wantEvents {
		t.Errorf("install over unstamped leftover CRDs rendered the crd-apply hook for %q, "+
			"want %q (F-218)", got, wantEvents)
	}
	if got := liveCRDStamp(ctx, t, "gameservers.gameplane.local"); got != chartStamp {
		t.Errorf("live gameservers CRD %s = %q after installing over leftover CRDs, want the chart's %q",
			crdBundleStampAnnotation, got, chartStamp)
	}

	// This release now has only an operator. Enabling the native panel moves
	// kubeconfigs from the operator's override and uploads from the release NS.
	const nextNS = "e2e-next-credentials"
	core := envInstance.K8s.CoreV1()
	create := metav1.CreateOptions{}
	if _, err := core.Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nextNS}}, create); err != nil {
		t.Fatal(err)
	}
	kubeconfig := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "e2e-operator-kubeconfig", Labels: map[string]string{"gameplane.local/cluster-kubeconfig": "true"},
	}}
	if _, err := core.Secrets(credentialsNS).Create(ctx, kubeconfig, create); err != nil {
		t.Fatal(err)
	}
	enableAPI := append(upgradeArgs, "--set", "api.credentialsNamespace="+nextNS)
	migrationDryRun(ctx, t, enableAPI, "operator kubeconfig", "Migrate Secret "+credentialsNS+"/"+kubeconfig.Name)
	if _, err := core.Secrets(nextNS).Create(ctx, kubeconfig, create); err != nil {
		t.Fatal(err)
	}
	migrationDryRun(ctx, t, enableAPI, "operator upload", "Migrate module ConfigMap gameplane-system/module-upload-e2e-migration")
}

// verifyUpgradeMigration exercises live Helm lookups against the published
// release. Each dry run uses the same chart and values as the actual upgrade.
func verifyUpgradeMigration(ctx context.Context, t *testing.T, upgradeArgs []string) {
	t.Helper()
	const sourceNS = "gameplane-system"
	core := envInstance.K8s.CoreV1()
	create := metav1.CreateOptions{}
	if _, err := core.Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: credentialsNS}}, create); err != nil {
		t.Fatal(err)
	}
	immutable := true
	oidc := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-upgrade-oidc", Labels: map[string]string{
			"gameplane.local/auth-provider": "true", "gameplane.local/managed-by": "gameplane-api",
		}},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"clientSecret": []byte("synthetic-upgrade-fixture")},
	}
	if _, err := core.Secrets(sourceNS).Create(ctx, oidc, create); err != nil {
		t.Fatal(err)
	}
	upload := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "module-upload-e2e-migration", Labels: map[string]string{
			"gameplane.local/module-upload": "true", "gameplane.local/module-name": "e2e-migration",
		}},
		Data:       map[string]string{"module.yaml": "apiVersion: gameplane.local/module/v1\nname: e2e-migration\nversion: 1.0.0\ngame: busybox\n"},
		BinaryData: map[string][]byte{"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n  displayName: Migration\n  game: busybox\n  version: 1.0.0\n  image: busybox:1.36\n")},
	}
	if _, err := core.ConfigMaps(sourceNS).Create(ctx, upload, create); err != nil {
		t.Fatal(err)
	}
	dryRun := func(stage, blockedObject string) {
		t.Helper()
		migrationDryRun(ctx, t, upgradeArgs, stage, blockedObject)
	}
	secretMarker := "Migrate Secret " + sourceNS + "/" + oidc.Name
	dryRun("missing OIDC copy", secretMarker)
	secretCopy := oidc.DeepCopy()
	secretCopy.Data["clientSecret"] = []byte("incorrect-fixture")
	secretCopy, err := core.Secrets(credentialsNS).Create(ctx, secretCopy, create)
	if err != nil {
		t.Fatal(err)
	}
	dryRun("altered OIDC data", secretMarker)
	secretCopy.Data = oidc.Data
	delete(secretCopy.Labels, "gameplane.local/managed-by")
	secretCopy, err = core.Secrets(credentialsNS).Update(ctx, secretCopy, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dryRun("missing OIDC management label", secretMarker)
	secretCopy.Labels = oidc.Labels
	secretCopy, err = core.Secrets(credentialsNS).Update(ctx, secretCopy, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Secrets(sourceNS).Patch(ctx, oidc.Name, types.MergePatchType, []byte(`{"immutable":true}`), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	dryRun("mutable copy of immutable OIDC Secret", secretMarker)
	secretCopy.Immutable = &immutable
	if _, err := core.Secrets(credentialsNS).Update(ctx, secretCopy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	uploadMarker := "Migrate module ConfigMap " + sourceNS + "/" + upload.Name
	dryRun("missing upload copy", uploadMarker)
	uploadCopy := upload.DeepCopy()
	delete(uploadCopy.BinaryData, "template.yaml")
	uploadCopy, err = core.ConfigMaps(credentialsNS).Create(ctx, uploadCopy, create)
	if err != nil {
		t.Fatal(err)
	}
	dryRun("incomplete upload bundle", uploadMarker)
	uploadCopy.BinaryData = upload.BinaryData
	uploadCopy.Labels["gameplane.local/module-name"] = "wrong-module"
	uploadCopy, err = core.ConfigMaps(credentialsNS).Update(ctx, uploadCopy, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dryRun("wrong upload module name", uploadMarker)
	uploadCopy.Labels = upload.Labels
	uploadCopy, err = core.ConfigMaps(credentialsNS).Update(ctx, uploadCopy, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ConfigMaps(sourceNS).Patch(ctx, upload.Name, types.MergePatchType, []byte(`{"immutable":true}`), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	dryRun("mutable copy of immutable upload", uploadMarker)
	uploadCopy.Immutable = &immutable
	if _, err := core.ConfigMaps(credentialsNS).Update(ctx, uploadCopy, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	dryRun("matching OIDC Secret and module upload", "")
}

// migrationDryRun requires the preflight to name the expected unmigrated
// object so a failure elsewhere in the chart cannot satisfy a rejection test.
func migrationDryRun(ctx context.Context, t *testing.T, upgradeArgs []string, stage, blockedObject string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "helm", append(upgradeArgs, "--dry-run=server")...)
	// A successful dry run can render private keys. Discard stdout and use
	// stderr only to identify the rejected object, never as test output.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if blockedObject == "" {
		if err != nil {
			t.Fatalf("%s: expected server dry run to succeed: %v", stage, err)
		}
	} else if err == nil || !strings.Contains(stderr.String(), blockedObject) {
		t.Fatalf("%s: expected preflight rejection naming %s, command error: %v", stage, blockedObject, err)
	}
}

// waitGameContainerReady polls until the named pod's "game" container reports
// Ready. Shared by the seed and post-upgrade phases.
func waitGameContainerReady(t *testing.T, ns, pod string, timeout time.Duration) {
	t.Helper()
	ctx := context.Background()
	envInstance.Eventually(t, timeout, func() (bool, string) {
		p, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == "game" && cs.Ready {
				return true, ""
			}
		}
		return false, "game container not ready yet"
	})
}

// crdSpecProperties returns the top-level property names under the CRD's
// spec schema, as the apiserver currently serves it.
func crdSpecProperties(t *testing.T, crdName string) map[string]struct{} {
	t.Helper()
	crd, err := envInstance.Dyn.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get CRD %s: %v", crdName, err)
	}
	versions, _, err := unstructured.NestedSlice(crd.Object, "spec", "versions")
	if err != nil || len(versions) == 0 {
		t.Fatalf("CRD %s has no spec.versions: %v", crdName, err)
	}
	v, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("CRD %s spec.versions[0] is not an object", crdName)
	}
	props, _, err := unstructured.NestedMap(v, "schema", "openAPIV3Schema", "properties", "spec", "properties")
	if err != nil {
		t.Fatalf("CRD %s: read spec properties: %v", crdName, err)
	}
	return keySet(props)
}

// manifestSpecProperties reads the same property set out of the working-tree
// chart's crd-manifests/ copy — the exact files the pre-upgrade hook applies
// (CI already guards crd-manifests/ against crds/ drift).
func manifestSpecProperties(t *testing.T, file string) map[string]struct{} {
	t.Helper()
	path := filepath.Join("..", "..", "charts", "gameplane", "crd-manifests", file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	obj := &unstructured.Unstructured{Object: doc}
	versions, _, err := unstructured.NestedSlice(obj.Object, "spec", "versions")
	if err != nil || len(versions) == 0 {
		t.Fatalf("%s has no spec.versions: %v", path, err)
	}
	v, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("%s spec.versions[0] is not an object", path)
	}
	props, _, err := unstructured.NestedMap(v, "schema", "openAPIV3Schema", "properties", "spec", "properties")
	if err != nil {
		t.Fatalf("%s: read spec properties: %v", path, err)
	}
	return keySet(props)
}

func keySet(m map[string]any) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// crdBundleStampAnnotation is the annotation `make manifests`
// (hack/sync-chart-crds.sh) stamps on every chart CRD; crd-apply-hook.yaml
// compares the live CRD's value with the chart's to decide whether a
// `helm install` lands on stale, leftover CRDs (F-218).
const crdBundleStampAnnotation = "gameplane.local/crd-bundle-sha256"

// upgradeFromChartRef and defaultUpgradeFromVersion mirror CHART_REF and
// FROM_VERSION in deploy/kind/upgrade.sh: the published chart the upgrade
// cluster was installed from. CI sets GAMEPLANE_UPGRADE_FROM for both.
// Releases up to v0.3.0 predate the GameplanePanel org move and stay
// under the original GHCR owner.
const (
	upgradeFromChartRef       = "oci://ghcr.io/valgulnecron/charts/gameplane"
	defaultUpgradeFromVersion = "0.3.0"
)

// crdApplyHookEvents returns the helm.sh/hook annotation Helm stored for the
// release's crds.autoApply Job, i.e. which lifecycle events that release
// rendered the CRD-apply hook for. Helm stores every hook with the release
// whatever its events, so this is present even when the hook never ran.
func crdApplyHookEvents(ctx context.Context, t *testing.T, release, namespace string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "helm", "get", "hooks", release, "--namespace", namespace)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+os.Getenv("KUBECONFIG"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm get hooks %s -n %s: %v", release, namespace, err)
	}
	for _, doc := range strings.Split(string(out), "\n---") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			continue
		}
		u := unstructured.Unstructured{Object: obj}
		if u.GetKind() == "Job" && u.GetName() == release+"-crd-apply" {
			return u.GetAnnotations()["helm.sh/hook"]
		}
	}
	t.Fatalf("release %s in %s has no %s-crd-apply Job hook (is crds.autoApply enabled?)",
		release, namespace, release)
	return ""
}

// helmMajorVersion returns the major version of the helm binary on PATH
// (3 or 4). Helm 3 and 4 install crds/ differently (create-and-skip vs
// server-side apply), which decides whether the crd-apply hook is needed on
// an install over leftover CRDs.
func helmMajorVersion(ctx context.Context, t *testing.T) int {
	t.Helper()
	out, err := exec.CommandContext(ctx, "helm", "version", "--template", "{{.Version}}").Output()
	if err != nil {
		t.Fatalf("helm version: %v", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		t.Fatalf("parse helm version %q: %v", out, err)
	}
	return n
}

// manifestCRDStamp reads the bundle-hash stamp from the working-tree chart's
// crd-manifests/ copy of the named CRD file.
func manifestCRDStamp(t *testing.T, file string) string {
	t.Helper()
	path := filepath.Join("..", "..", "charts", "gameplane", "crd-manifests", file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	stamp := (&unstructured.Unstructured{Object: doc}).GetAnnotations()[crdBundleStampAnnotation]
	if stamp == "" {
		t.Fatalf("%s carries no %s annotation; run `make manifests`", path, crdBundleStampAnnotation)
	}
	return stamp
}

// liveCRDStamp returns the bundle-hash stamp on the live CRD ("" if absent).
func liveCRDStamp(ctx context.Context, t *testing.T, crdName string) string {
	t.Helper()
	crd, err := envInstance.Dyn.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get CRD %s: %v", crdName, err)
	}
	return crd.GetAnnotations()[crdBundleStampAnnotation]
}
