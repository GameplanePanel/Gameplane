//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

func checkPublishedRuntime(t *testing.T, digestFile string, components ...string) {
	t.Helper()
	args := []string{filepath.Join("..", "..", "deploy", "kind", "verify-release-runtime.sh"), envInstance.Context, digestFile}
	check := exec.CommandContext(t.Context(), "bash", append(args, components...)...)
	out, err := check.CombinedOutput()
	if err != nil {
		t.Fatalf("published upgrade runtime image IDs: %v\n%s", err, out)
	}
	t.Logf("published upgrade image IDs:\n%s", out)
}

// Both PR CI and release CI must prove that the seeded world, PVC and account
// survived the upgrade. A Ready replacement pod alone does not establish this.
func verifyPopulatedUpgrade(t *testing.T, ns, gs, marker, adminUser, adminPass string, previousClaim types.UID, seedPod *corev1.Pod) *APIClient {
	t.Helper()
	ctx := t.Context()
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
	// Helm does not wait for this operator-owned StatefulSet. Its old pod
	// can still serve the marker while the operator reconciles the upgrade.
	envInstance.Eventually(t, 4*time.Minute, func() (bool, string) {
		sts, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get StatefulSet: " + err.Error()
		}
		status := sts.Status
		if status.UpdateRevision == "" || status.UpdateRevision == seedPod.Labels[appsv1.StatefulSetRevisionLabel] ||
			status.ObservedGeneration != sts.Generation || status.CurrentRevision != status.UpdateRevision ||
			status.UpdatedReplicas != 1 || status.ReadyReplicas != 1 {
			return false, fmt.Sprintf("waiting for upgraded game StatefulSet: %+v", status)
		}
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, seedPod.Name, metav1.GetOptions{})
		if err != nil {
			return false, "get replacement pod: " + err.Error()
		}
		if pod.UID == seedPod.UID || pod.DeletionTimestamp != nil || !metav1.IsControlledBy(pod, sts) ||
			pod.Labels[appsv1.StatefulSetRevisionLabel] != status.UpdateRevision {
			return false, "waiting for replacement pod at revision " + status.UpdateRevision
		}
		out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, "pod/"+pod.Name, "--", "cat", "/data/marker.txt")
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
