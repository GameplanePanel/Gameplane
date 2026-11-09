//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// A real workload remains unchanged while a new shared template is installed,
// then adopts only on an exact request while retaining its PVC and saved data.
func TestGameServer_TemplateOnRequestRetainsData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const ns, name, template = "gameplane-games", "e2e-template-stage", "e2e-template-stage"
	applyBusyboxTemplate(t, template)
	setConfigFile := func(contents string) {
		t.Helper()
		// The template controller updates status concurrently with both
		// spec changes. Retry the complete read-modify-write on conflict.
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			tmpl, err := envInstance.Dyn.Resource(gameTemplateGVR).Get(ctx, template, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := unstructured.SetNestedSlice(tmpl.Object, []any{map[string]any{"path": "revision.txt", "template": contents}}, "spec", "configFiles"); err != nil {
				return err
			}
			_, err = envInstance.Dyn.Resource(gameTemplateGVR).Update(ctx, tmpl, metav1.UpdateOptions{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setConfigFile("old")
	gs := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "metadata": map[string]any{"name": name, "namespace": ns}, "spec": map[string]any{"templateRef": map[string]any{"name": template}, "templateRollout": map[string]any{"policy": "OnRequest"}}}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Create(ctx, gs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	get := func() *unstructured.Unstructured {
		t.Helper()
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return obj
	}
	var old string
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		obj := get()
		running, _, _ := unstructured.NestedString(obj.Object, "status", "templateRevision", "running")
		effective, _, _ := unstructured.NestedString(obj.Object, "status", "templateRevision", "effective")
		old = effective
		return effective != "" && running == effective && obj.GetAnnotations()["operations.gameplane.local/owner"] == "", "waiting for initial pinned workload"
	})
	pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldPod := pod.UID
	pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldPVC := pvc.UID
	if out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, name+"-0", "-c", "game", "--", "sh", "-c", "printf retained > /data/save-marker; cat /data/revision.txt"); err != nil || strings.TrimSpace(out) != "old" {
		t.Fatalf("seed persisted data: %s %v", out, err)
	}
	setConfigFile("new")
	var desired string
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		obj := get()
		desired, _, _ = unstructured.NestedString(obj.Object, "status", "templateRevision", "desired")
		effective, _, _ := unstructured.NestedString(obj.Object, "status", "templateRevision", "effective")
		return desired != "" && desired != old && effective == old, "waiting for pending revision"
	})
	// Check after the controller reported processing the update, rather than
	// relying on an arbitrary sleep before asserting an unchanged Pod.
	pod, err = envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
	if err != nil || pod.UID != oldPod {
		t.Fatalf("catalog update replaced staged Pod: %v", err)
	}
	if out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, name+"-0", "-c", "game", "--", "cat", "/data/revision.txt"); err != nil || strings.TrimSpace(out) != "old" {
		t.Fatalf("pending config changed: %s %v", out, err)
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"templateRollout": map[string]any{"requestedRevision": desired}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		obj := get()
		running, _, _ := unstructured.NestedString(obj.Object, "status", "templateRevision", "running")
		current, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return running == desired && current.UID != oldPod && obj.GetAnnotations()["operations.gameplane.local/owner"] == "", fmt.Sprintf("running revision=%s", running)
	})
	pvc, err = envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name+"-data", metav1.GetOptions{})
	if err != nil || pvc.UID != oldPVC {
		t.Fatalf("adoption replaced PVC: %v", err)
	}
	if out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, name+"-0", "-c", "game", "--", "sh", "-c", "cat /data/save-marker; printf ':'; cat /data/revision.txt"); err != nil || strings.TrimSpace(out) != "retained:new" {
		t.Fatalf("persisted contents after adoption: %s %v", out, err)
	}
}
