//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Real scheduler, RWO PVC and repository failure/recovery on two schedulable
// nodes. Kind's local-path volume supplies node affinity; attachable CSI
// drivers still need their own multi-attach qualification. Zero API logins.
func TestBackup_PreparationAndSameNodeCopy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	nodes, err := envInstance.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eligible := 0
	for _, node := range nodes.Items {
		ready, tainted := false, node.Spec.Unschedulable
		for _, condition := range node.Status.Conditions {
			ready = ready || (condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue)
		}
		for _, taint := range node.Spec.Taints {
			tainted = tainted || taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute
		}
		if ready && !tainted {
			eligible++
		}
	}
	if eligible < 2 {
		t.Fatal("backup preparation requires the management Kind profile with two schedulable workers")
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ns, gs, template, name := "gameplane-games", "prepared-source-"+suffix, "prepared-template-"+suffix, "prepared-copy-"+suffix
	ensureResticRepo(t)
	applyBusyboxTemplate(t, template)
	applyBusyboxGameServer(t, ns, gs, template)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	var sourceNode string
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "game" && status.Ready {
				sourceNode = pod.Spec.NodeName
				return sourceNode != "", "source has no node"
			}
		}
		return false, "game container not ready"
	})
	pvc, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil || len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Fatalf("fixture is not RWO: %v %+v", err, pvc)
	}
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "sh", "-c", "echo prepared-copy-marker > /data/prepared-marker.txt && sync"); err != nil {
		t.Fatalf("write marker: %v %s", err, out)
	}
	repo, err := envInstance.K8s.CoreV1().Secrets(ns).Get(ctx, "e2e-restic-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo = repo.DeepCopy()
	repo.ObjectMeta = metav1.ObjectMeta{Name: "prepared-repo-" + suffix, Namespace: ns}
	// Authentication failure against the warm repository is deterministic.
	// A refused HTTP endpoint exercises Restic's network retry budget and may
	// keep the init container running longer than this preparation assertion.
	validPassword := append([]byte(nil), repo.Data["password"]...)
	repo.Data["password"] = []byte("incorrect-repository-password-" + suffix)
	if _, err := envInstance.K8s.CoreV1().Secrets(ns).Create(ctx, repo, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = envInstance.K8s.CoreV1().Secrets(ns).Delete(cleanupCtx, repo.Name, metav1.DeleteOptions{})
	})
	backup := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup", "metadata": map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{"serverRef": map[string]any{"name": gs}, "repoRef": map[string]any{"name": repo.Name, "key": "repo"}, "quiesce": true}}}
	if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Create(ctx, backup, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteBackupCR(t, ns, name) })
	var failedWorker *corev1.Pod
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if got.GetAnnotations()["backup.gameplane.local/quiesce-attempted"] != "" || got.GetAnnotations()["backup.gameplane.local/authorized-worker"] != "" {
			t.Fatal("repository preparation failure disabled saving or authorized copy")
		}
		pods, err := envInstance.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
		if err != nil {
			return false, err.Error()
		}
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Spec.NodeName != "" && pod.Spec.NodeName != sourceNode {
				t.Fatalf("RWO worker scheduled away from source: %s vs %s", pod.Spec.NodeName, sourceNode)
			}
			for _, status := range pod.Status.InitContainerStatuses {
				if status.Name == "restic-init" && ((status.State.Terminated != nil && status.State.Terminated.ExitCode != 0) || (status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.ExitCode != 0)) {
					failedWorker = pod
					return true, ""
				}
			}
		}
		return false, "waiting for actual repository preparation failure"
	})
	repo, err = envInstance.K8s.CoreV1().Secrets(ns).Get(ctx, repo.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	repo.Data["password"] = validPassword
	if _, err := envInstance.K8s.CoreV1().Secrets(ns).Update(ctx, repo, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	uid := failedWorker.UID
	if err := envInstance.K8s.CoreV1().Pods(ns).Delete(ctx, failedWorker.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
		t.Fatal(err)
	}
	var snapshotID string
	envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase == "Failed" {
			t.Fatalf("copy failed after repository recovery: %+v", got.Object["status"])
		}
		snapshotID, _, _ = unstructured.NestedString(got.Object, "status", "snapshotID")
		return phase == "Succeeded" && snapshotID != "", "phase=" + phase
	})
	pods, err := envInstance.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + name})
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodSucceeded {
			completed = true
			if pod.Spec.NodeName != sourceNode || pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
				t.Fatal("completed worker violated placement or credential boundary")
			}
		}
	}
	if !completed {
		t.Fatal("no completed real restic worker")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8,64}$`).MatchString(snapshotID) {
		t.Fatalf("unexpected snapshot identity %q", snapshotID)
	}
	runResticJob(t, fmt.Sprintf("restic restore %s --target /tmp/prepared-restore --retry-lock 5m && test \"$(cat /tmp/prepared-restore/data/prepared-marker.txt)\" = prepared-copy-marker", snapshotID))
}
