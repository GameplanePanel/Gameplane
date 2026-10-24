//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Exercise real PVC data and restic for two consecutive protected copies while
// the agent is absent. This test needs no API login and runs in the operator bucket.
func TestBackup_StoppedCopiesRemainRestorable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ns, gs, tmpl := "gameplane-games", "e2e-stopped-backup", "e2e-stopped-backup-template"
	ensureResticRepo(t)
	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "game" && status.Ready {
				return true, ""
			}
		}
		return false, "game container not ready"
	})
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "sh", "-c", "echo stopped-backup-marker > /data/marker.txt && sync"); err != nil {
		t.Fatalf("write marker: %v\n%s", err, out)
	}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, gs, types.MergePatchType, []byte(`{"spec":{"suspend":true}}`), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
			return false, "workload not drained"
		}
		_, err = envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		return apierrors.IsNotFound(err), fmt.Sprintf("pod absence: %v", err)
	})
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("e2e-stopped-copy-%d", i)
		backup := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup",
			"metadata": map[string]any{"name": name, "namespace": ns},
			"spec":     map[string]any{"serverRef": map[string]any{"name": gs}, "repoRef": map[string]any{"name": "e2e-restic-creds", "key": "repo"}, "strategy": "restic-snapshot", "quiesce": true},
		}}
		if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Create(ctx, backup, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { deleteBackupCR(t, ns, name) })
		var snapshotID string
		envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
			// Check throughout the copy, not only after its terminal result.
			_, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
			if !apierrors.IsNotFound(err) {
				t.Fatalf("backup woke game or absence could not be confirmed: %v", err)
			}
			got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
			if phase == "Failed" {
				t.Fatalf("stopped backup failed: %+v", got.Object["status"])
			}
			if phase != "Succeeded" {
				return false, "phase=" + phase
			}
			var preparation struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal([]byte(got.GetAnnotations()["backup.gameplane.local/preparation"]), &preparation); err != nil || preparation.Outcome != "already-stopped" {
				t.Fatalf("missing stopped-source proof: %+v, %v", preparation, err)
			}
			if got.GetAnnotations()["backup.gameplane.local/quiesce-attempted"] != "" {
				t.Fatal("stopped source contacted absent agent")
			}
			snapshotID, _, _ = unstructured.NestedString(got.Object, "status", "snapshotID")
			return snapshotID != "", "snapshot ID pending"
		})
		if !regexp.MustCompile(`^[0-9a-f]{8,64}$`).MatchString(snapshotID) {
			t.Fatalf("unexpected snapshot ID %q", snapshotID)
		}
		runResticJob(t, "restic restore "+snapshotID+" --target /tmp/stopped-restore --retry-lock 5m && test \"$(cat /tmp/stopped-restore/data/marker.txt)\" = stopped-backup-marker")
	}
	got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, gs, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	suspended, _, _ := unstructured.NestedBool(got.Object, "spec", "suspend")
	if !suspended {
		t.Fatal("backup changed manual suspend intent")
	}
}
