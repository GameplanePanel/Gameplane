//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// securityRootSettingMu serializes the tests that flip the per-cluster
// allow-root-overrides ConfigMap. One test's "on" window must never land
// inside another test's "off" assertion. Shared with
// api_security_override_e2e_test.go.
var securityRootSettingMu sync.Mutex

// e2eHomeReleaseNamespace is the namespace the e2e suite installs the chart
// into (deploy/kind/e2e.sh, --namespace gameplane-system).
const e2eHomeReleaseNamespace = "gameplane-system"

// securitySwitchConfigMap is the per-cluster root-override switch (brief 00
// section 1). Helm creates it; the tests only patch it.
const securitySwitchConfigMap = "gameplane-security-settings"

// securityControlPlaneNamespace returns the operator's
// --control-plane-namespace, which is where the switch ConfigMap lives. It is
// read from the home operator Deployment rather than assumed, because the
// chart derives it (for the e2e install: <release>-credentials).
func securityControlPlaneNamespace(t *testing.T) string {
	t.Helper()
	d, err := envInstance.K8s.AppsV1().Deployments(e2eHomeReleaseNamespace).
		Get(context.Background(), "gameplane-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get operator deployment: %v", err)
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		for _, a := range c.Args {
			if v, ok := strings.CutPrefix(a, "--control-plane-namespace="); ok && v != "" {
				return v
			}
		}
	}
	t.Fatalf("operator deployment has no --control-plane-namespace argument")
	return ""
}

// setRootOverrideSwitch patches allowRootOverrides on the switch ConfigMap
// with kubectl. The ConfigMap must already exist; the patch never creates it.
func setRootOverrideSwitch(t *testing.T, ns, value string) {
	t.Helper()
	patch := fmt.Sprintf(`{"data":{"allowRootOverrides":%q}}`, value)
	out, err := envInstance.Kubectl(context.Background(),
		"patch", "configmap", securitySwitchConfigMap, "-n", ns, "--type", "merge", "-p", patch)
	if err != nil {
		t.Fatalf("patch %s allowRootOverrides=%s: %v: %s", securitySwitchConfigMap, value, err, out)
	}
}

// applySecurityTemplate creates a busybox GameTemplate whose module default is
// fsGroup 1000. Cleanup deletes it.
func applySecurityTemplate(t *testing.T, name string) {
	t.Helper()
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"displayName": "E2E security (" + name + ")",
			"game":        "busybox",
			"version":     "1",
			"image":       "busybox:1.36",
			"command":     []any{"sh", "-c", "sleep 100000"},
			"ports": []any{
				map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
			},
			"security": map[string]any{"fsGroup": int64(1000)},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(context.Background(), tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// createSecurityGameServer creates a GameServer on tmpl with an optional
// spec.security block. Cleanup deletes it.
func createSecurityGameServer(t *testing.T, ns, gs, tmpl string, security map[string]any) {
	t.Helper()
	spec := map[string]any{"templateRef": map[string]any{"name": tmpl}}
	if security != nil {
		spec["security"] = security
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gs, "namespace": ns},
		"spec":       spec,
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create gameserver %s/%s: %v", ns, gs, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), gs, metav1.DeleteOptions{})
	})
}

// getSecurityGameServer reads the GameServer.
func getSecurityGameServer(t *testing.T, ns, gs string) *unstructured.Unstructured {
	t.Helper()
	obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(context.Background(), gs, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get gameserver %s/%s: %v", ns, gs, err)
	}
	return obj
}

// patchSecurityGameServer applies a JSON merge patch to the GameServer.
func patchSecurityGameServer(t *testing.T, ns, gs, patch string) {
	t.Helper()
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Patch(context.Background(), gs, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch gameserver %s/%s %s: %v", ns, gs, patch, err)
	}
}

// securityPending reports status.security.pending.
func securityPending(obj *unstructured.Unstructured) bool {
	v, _, _ := unstructured.NestedBool(obj.Object, "status", "security", "pending")
	return v
}

// securityIDValue reads status.security.<which>.<field>.value, where which is
// "applied" or "desired". ok is false when no id is set (image default).
func securityIDValue(obj *unstructured.Unstructured, which, field string) (int64, bool) {
	v, found, err := unstructured.NestedInt64(obj.Object, "status", "security", which, field, "value")
	if err != nil || !found {
		return 0, false
	}
	return v, true
}

// expectedDesiredID is the id the operator resolved as the next start's value
// for field ("runAsUser" or "runAsGroup"). With no id set the container runs
// as the busybox image default, which is 0.
func expectedDesiredID(obj *unstructured.Unstructured, field string) string {
	if v, ok := securityIDValue(obj, "desired", field); ok {
		return strconv.FormatInt(v, 10)
	}
	return "0"
}

// gameServerConditionState returns the status and reason of a condition, and
// whether it exists.
func gameServerConditionState(obj *unstructured.Unstructured, condType string) (status, reason string, found bool) {
	conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != condType {
			continue
		}
		s, _ := m["status"].(string)
		r, _ := m["reason"].(string)
		return s, r, true
	}
	return "", "", false
}

// execID runs `id <flag>` in the server pod's game container and returns the
// trimmed output. The container is named so kubectl prints no
// "Defaulted container" notice into the output.
func execID(t *testing.T, ns, pod, flag string) string {
	t.Helper()
	out, err := envInstance.Kubectl(t.Context(), "exec", "-n", ns, "pod/"+pod, "-c", "game", "--", "id", flag)
	if err != nil {
		t.Fatalf("exec id %s in %s: %v: %s", flag, pod, err, out)
	}
	return strings.TrimSpace(out)
}

// waitPodGone waits until the named pod no longer exists.
func waitPodGone(t *testing.T, ns, pod string, timeout time.Duration) {
	t.Helper()
	envInstance.Eventually(t, timeout, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		return false, "pod " + pod + " still present"
	})
}

// restartSecurityGameServer suspends and resumes the server (a full restart).
// It waits for the old pod to be gone, then for a new Ready game container.
// oldUID is the previous pod UID; the new pod must differ from it.
func restartSecurityGameServer(t *testing.T, ns, gs string, oldUID types.UID) *corev1.Pod {
	t.Helper()
	pod := gs + "-0"
	patchSecurityGameServer(t, ns, gs, `{"spec":{"suspend":true}}`)
	waitStatefulSetReplicas(t, ns, gs, 0, 3*time.Minute)
	waitPodGone(t, ns, pod, 3*time.Minute)
	patchSecurityGameServer(t, ns, gs, `{"spec":{"suspend":false}}`)
	waitStatefulSetReplicas(t, ns, gs, 1, 3*time.Minute)
	p := waitGameContainerReady(t, ns, pod, 4*time.Minute)
	if oldUID != "" && p.UID == oldUID {
		t.Fatalf("pod %s kept UID %s across a restart", pod, oldUID)
	}
	return p
}

// TestGameServer_SecurityOverrideAppliesOnRestart walks one server through the
// override lifecycle with zero API logins: a run-as override applies on the
// next start (part 1), a change is pending until the next restart (part 2),
// and a root override is held by the per-cluster switch (part 3).
func TestGameServer_SecurityOverrideAppliesOnRestart(t *testing.T) {
	t.Parallel()

	ns := "gameplane-games"
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e10, 10)
	tmpl := "e2e-sec-restart-tmpl-" + suffix
	gs := "e2e-sec-restart-" + suffix
	pod := gs + "-0"

	applySecurityTemplate(t, tmpl)

	// Part 1: a run-as user override applies at the first start. The fsGroup
	// comes from the module default and the gid stays off the override.
	createSecurityGameServer(t, ns, gs, tmpl, map[string]any{
		"runAsUser": map[string]any{"value": int64(1001)},
	})
	first := waitGameContainerReady(t, ns, pod, 4*time.Minute)

	if got := execID(t, ns, pod, "-u"); got != "1001" {
		t.Fatalf("part 1: id -u = %s, want 1001", got)
	}
	obj := getSecurityGameServer(t, ns, gs)
	if got := execID(t, ns, pod, "-g"); got != expectedDesiredID(obj, "runAsGroup") {
		t.Fatalf("part 1: id -g = %s, want the module/image default %s", got, expectedDesiredID(obj, "runAsGroup"))
	}
	if first.Spec.SecurityContext == nil || first.Spec.SecurityContext.FSGroup == nil || *first.Spec.SecurityContext.FSGroup != 1000 {
		t.Fatalf("part 1: pod fsGroup is not the module default 1000: %+v", first.Spec.SecurityContext)
	}

	// Part 2: a changed override does not touch the running pod. It is
	// pending until the next restart.
	patchSecurityGameServer(t, ns, gs, `{"spec":{"security":{"runAsUser":{"value":2001}}}}`)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		o := getSecurityGameServer(t, ns, gs)
		if !securityPending(o) {
			return false, "status.security.pending not yet true"
		}
		if v, ok := securityIDValue(o, "applied", "runAsUser"); !ok || v != 1001 {
			return false, fmt.Sprintf("applied runAsUser = %d (ok=%v), want 1001", v, ok)
		}
		return true, ""
	})
	cur, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("part 2: get pod: %v", err)
	}
	if cur.UID != first.UID {
		t.Fatalf("part 2: pod UID changed on save: %s -> %s", first.UID, cur.UID)
	}

	second := restartSecurityGameServer(t, ns, gs, first.UID)
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		if securityPending(getSecurityGameServer(t, ns, gs)) {
			return false, "still pending after restart"
		}
		return true, ""
	})
	if got := execID(t, ns, pod, "-u"); got != "2001" {
		t.Fatalf("part 2: id -u after restart = %s, want 2001", got)
	}

	// Remove the override and restart: the module default applies again.
	patchSecurityGameServer(t, ns, gs, `{"spec":{"security":null}}`)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		if !securityPending(getSecurityGameServer(t, ns, gs)) {
			return false, "pending not yet true after removing the override"
		}
		return true, ""
	})
	third := restartSecurityGameServer(t, ns, gs, second.UID)
	obj = getSecurityGameServer(t, ns, gs)
	if got, want := execID(t, ns, pod, "-u"), expectedDesiredID(obj, "runAsUser"); got != want {
		t.Fatalf("part 2: id -u after removing the override = %s, want the module default %s", got, want)
	}
	if third.UID == second.UID {
		t.Fatalf("part 2: pod UID did not change on the final restart")
	}

	// Part 3: a root override is held while the cluster switch is off. The
	// switch is shared with the API test, so the lock covers the whole part.
	securityRootSettingMu.Lock()
	t.Cleanup(securityRootSettingMu.Unlock)

	cpNS := securityControlPlaneNamespace(t)
	setRootOverrideSwitch(t, cpNS, "false")
	t.Cleanup(func() {
		// Restore the default under the same lock, before the unlock above.
		setRootOverrideSwitch(t, cpNS, "false")
	})

	rootGS := gs + "-root"
	createSecurityGameServer(t, ns, rootGS, tmpl, nil)
	waitGameContainerReady(t, ns, rootGS+"-0", 4*time.Minute)

	patchSecurityGameServer(t, ns, rootGS, `{"spec":{"security":{"fsGroup":{"value":0}}}}`)
	patchSecurityGameServer(t, ns, rootGS, `{"spec":{"suspend":true}}`)
	waitStatefulSetReplicas(t, ns, rootGS, 0, 3*time.Minute)
	waitPodGone(t, ns, rootGS+"-0", 3*time.Minute)

	// Start with the switch off: the server must stay at zero replicas.
	patchSecurityGameServer(t, ns, rootGS, `{"spec":{"suspend":false}}`)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		o := getSecurityGameServer(t, ns, rootGS)
		status, reason, found := gameServerConditionState(o, "SecurityAllowed")
		if !found || status != "False" || reason != "RootOverrideDisallowed" {
			return false, fmt.Sprintf("SecurityAllowed status=%q reason=%q found=%v", status, reason, found)
		}
		return true, ""
	})
	envInstance.Consistently(t, 20*time.Second, 4*time.Second, func() (bool, string) {
		ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(context.Background(), rootGS, metav1.GetOptions{})
		if err != nil {
			return false, "get ss: " + err.Error()
		}
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			return false, "replicas left zero while the switch is off"
		}
		return true, ""
	})

	// Switch on: the held server starts.
	setRootOverrideSwitch(t, cpNS, "true")
	started := waitGameContainerReady(t, ns, rootGS+"-0", 4*time.Minute)

	// Switch off again: the running pod keeps its UID and restart count.
	restarts := gameContainerRestarts(started, "game")
	setRootOverrideSwitch(t, cpNS, "false")
	envInstance.Consistently(t, 20*time.Second, 4*time.Second, func() (bool, string) {
		p, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), rootGS+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		if p.UID != started.UID {
			return false, fmt.Sprintf("pod UID changed %s -> %s after the switch flipped off", started.UID, p.UID)
		}
		if got := gameContainerRestarts(p, "game"); got != restarts {
			return false, fmt.Sprintf("restart count changed %d -> %d", restarts, got)
		}
		return true, ""
	})
}

// gameContainerRestarts returns the restart count of the named container.
func gameContainerRestarts(p *corev1.Pod, name string) int32 {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == name {
			return cs.RestartCount
		}
	}
	return -1
}
