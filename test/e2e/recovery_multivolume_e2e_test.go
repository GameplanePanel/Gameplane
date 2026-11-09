//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Real Restic/PVC fixture: one world, a declared extra, a separate loader
// claim and a rebuildable cache. No API logins; CI operator bucket owns execution.
func TestBackup_MultiVolumeRecoveryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ns := "gameplane-games"
	suffix := time.Now().UnixNano()
	gs := fmt.Sprintf("recovery-%d", suffix)
	tmpl := gs + "-template"
	backup := gs + "-backup"
	ensureResticRepo(t)
	applyBusyboxTemplate(t, tmpl)
	patch := map[string]any{"spec": map[string]any{
		"storage":      map[string]any{"size": "1Gi", "mountPath": "/data", "extra": []any{map[string]any{"name": "world", "mountPath": "/world", "size": "1Gi", "backupRole": "data"}, map[string]any{"name": "cache", "mountPath": "/rebuild", "size": "1Gi", "backupRole": "rebuildable"}}},
		"versions":     []any{map[string]any{"id": "v1", "displayName": "Test loader", "image": "busybox:1.36", "loader": "paper", "default": true}},
		"capabilities": map[string]any{"mods": map[string]any{"loaders": map[string]any{"paper": map[string]any{"path": "mods"}}}},
	}}
	data, _ := json.Marshal(patch)
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Patch(ctx, tmpl, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	applyBusyboxGameServer(t, ns, gs, tmpl)
	ready := func(name string) {
		t.Helper()
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			for _, s := range pod.Status.ContainerStatuses {
				if s.Name == "game" && s.Ready {
					return true, ""
				}
			}
			return false, "game not ready"
		})
	}
	exec := func(script string) {
		t.Helper()
		if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "sh", "-c", "set -eu; "+script); err != nil {
			t.Fatalf("fixture command: %v %s", err, out)
		}
	}
	ready(gs)
	exec("echo primary-before > /data/marker; echo extra-before > /world/marker; echo mods-before > /data/mods/marker; echo cache-before > /rebuild/marker; sync")
	capture := func(name string) {
		t.Helper()
		obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup", "metadata": map[string]any{"name": name, "namespace": ns}, "spec": map[string]any{"serverRef": map[string]any{"name": gs}, "repoRef": map[string]any{"name": "e2e-restic-creds", "key": "repo"}, "quiesce": false, "powerPolicy": "stop-copy-restart", "volumePolicy": "all-data"}}}
		if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { deleteBackupCR(t, ns, name) })
		waitBackupSucceeded(t, ns, name, 6*time.Minute)
	}
	capture(backup)
	ready(gs)
	got, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Get(ctx, backup, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	volumes, _, _ := unstructured.NestedSlice(got.Object, "status", "captureManifest", "volumes")
	included, excluded := 0, 0
	for _, raw := range volumes {
		v := raw.(map[string]any)
		if v["included"] == true {
			included++
			if !strings.HasPrefix(v["snapshotRoot"].(string), "/volumes/") {
				t.Fatal("non-disjoint capture root")
			}
		} else {
			excluded++
			if v["recoveryPrerequisite"] == "" {
				t.Fatal("missing recovery prerequisite")
			}
		}
	}
	if included != 3 || excluded != 1 {
		t.Fatalf("wrong coverage: %d/%d", included, excluded)
	}
	exec("echo changed > /data/marker; echo changed > /world/marker; echo changed > /data/mods/marker; echo cache-after > /rebuild/marker; touch /data/stale /world/stale /data/mods/stale; sync")
	restore := gs + "-restore"
	createRestore(t, ns, restore, gs, backup)
	waitRestoreSucceeded(t, ns, restore, 6*time.Minute)
	ready(gs)
	exec("test $(cat /data/marker) = primary-before; test $(cat /world/marker) = extra-before; test $(cat /data/mods/marker) = mods-before; test $(cat /rebuild/marker) = cache-after; test ! -e /data/stale; test ! -e /world/stale; test ! -e /data/mods/stale")

	// Existing explicit suspend and idle intent survive both stopped capture and
	// a successful v2 restore. Verify Pod absence after owner cleanup.
	for _, mode := range []string{"suspended", "idle"} {
		t.Run(mode, func(t *testing.T) {
			body := map[string]any{"spec": map[string]any{"suspend": mode == "suspended", "idle": map[string]any{"enabled": mode == "idle"}}}
			asleep := time.Now().UTC().Format(time.RFC3339)
			if mode == "idle" {
				body["metadata"] = map[string]any{"annotations": map[string]any{"gameplane.local/idle-asleep-since": asleep}}
			}
			data, _ := json.Marshal(body)
			if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, gs, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
				t.Fatal(err)
			}
			envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
				_, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
				return apierrors.IsNotFound(err), fmt.Sprint(err)
			})
			capture(gs + "-" + mode)
			name := gs + "-restore-" + mode
			createRestore(t, ns, name, gs, backup)
			waitRestoreSucceeded(t, ns, name, 6*time.Minute)
			envInstance.Eventually(t, time.Minute, func() (bool, string) {
				obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, gs, metav1.GetOptions{})
				if err != nil {
					return false, err.Error()
				}
				if obj.GetAnnotations()["operations.gameplane.local/owner"] != "" {
					return false, "cleanup pending"
				}
				suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
				if suspended != (mode == "suspended") {
					t.Fatal("restore changed suspend intent")
				}
				if mode == "idle" && obj.GetAnnotations()["gameplane.local/idle-asleep-since"] != asleep {
					t.Fatal("restore changed idle intent")
				}
				_, err = envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
				if !apierrors.IsNotFound(err) {
					t.Fatalf("recovery woke parked game: %v", err)
				}
				return true, ""
			})
		})
	}

	// An incompatible existing target must fail before power mutation or a Job.
	for _, mode := range []string{"missing", "changed-path"} {
		t.Run(mode, func(t *testing.T) {
			target := gs + "-" + mode
			extra := []any{map[string]any{"name": "cache", "mountPath": "/rebuild", "size": "1Gi", "backupRole": "rebuildable"}}
			if mode == "changed-path" {
				extra = append(extra, map[string]any{"name": "world", "mountPath": "/redirected-world", "size": "1Gi", "backupRole": "data"})
			}
			obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "metadata": map[string]any{"name": target, "namespace": ns}, "spec": map[string]any{"templateRef": map[string]any{"name": tmpl}, "storage": map[string]any{"extra": extra}}}}
			if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Delete(ctx, target, metav1.DeleteOptions{})
			})
			ready(target)
			name := target + "-restore"
			createRestore(t, ns, name, target, backup)
			envInstance.Eventually(t, time.Minute, func() (bool, string) {
				rs, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, err.Error()
				}
				phase, _, _ := unstructured.NestedString(rs.Object, "status", "phase")
				return phase == "Failed", phase
			})
			targetObj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, target, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			suspended, _, _ := unstructured.NestedBool(targetObj.Object, "spec", "suspend")
			if suspended || targetObj.GetAnnotations()["restore.gameplane.local/owner"] != "" {
				t.Fatal("preflight failure mutated power or ownership")
			}
			if _, err := envInstance.K8s.BatchV1().Jobs(ns).Get(ctx, "restore-"+name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("preflight created writer: %v", err)
			}
		})
	}
}
