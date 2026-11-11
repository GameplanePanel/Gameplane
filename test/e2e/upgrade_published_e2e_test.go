//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// The previous published chart was installed by deploy/kind/upgrade.sh. Use
// the downloaded candidate and let it choose its own registry and image tags.
func upgradePublishedChart(t *testing.T, chart string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "helm", "upgrade", "gameplane", chart,
		"--kube-context", envInstance.Context, "--namespace", "gameplane-system",
		"--set", "web.enabled=true", "--set", "ingress.enabled=false",
		"--set", "api.resources.limits.memory=1Gi", "--set", "defaultModuleSource.enabled=false",
		// The seeded legacy template uses the original busybox security profile.
		"--set", "podSecurity.enforceRestricted=false", "--wait", "--timeout", "6m")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("upgrade to published chart: %v\n%s", err, out)
	}
}

func checkPublishedRuntime(t *testing.T) {
	t.Helper()
	check := exec.CommandContext(t.Context(), "bash", filepath.Join("..", "..", "deploy", "kind", "verify-release-runtime.sh"),
		envInstance.Context, "api", "operator", "web", "agent")
	out, err := check.CombinedOutput()
	if err != nil {
		t.Fatalf("published upgrade runtime image IDs: %v\n%s", err, out)
	}
	t.Logf("published upgrade image IDs:\n%s", out)
}

// Both PR CI and release CI must prove that the seeded world, PVC and account
// survived the upgrade. A Ready replacement pod alone does not establish this.
func verifyPopulatedUpgrade(t *testing.T, ns, gs, marker, adminUser, adminPass string, previousClaim types.UID) *APIClient {
	t.Helper()
	ctx := context.Background()
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
	claim, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil || claim.UID != previousClaim {
		t.Fatalf("upgrade replaced the game data claim or made it unavailable: %v", err)
	}
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
	t.Cleanup(client.Close)

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

	return client
}
