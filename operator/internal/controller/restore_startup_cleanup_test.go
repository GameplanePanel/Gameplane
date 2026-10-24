package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func startupCleanupFixture(t *testing.T) (*RestoreReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.Restore, *appsv1.StatefulSet, *corev1.Pod) {
	t.Helper()
	r, gs, rs := restoreLifecycleFixture(t)
	rs.Annotations = map[string]string{restoreTargetAnnotation: restoreIdentityJSON(gs.Name, gs.UID)}
	rs.Finalizers = []string{restoreCleanupFinalizer}
	if err := r.Update(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	op := lifecycleOperation{lifecycleIdentity: restoreLifecycleIdentity(gs, rs)}
	op.InitialStartup = true
	gs.Spec.Suspend = false
	gs.Annotations = map[string]string{restoreGuardAnnotation: restoreIdentityJSON(rs.Name, rs.UID), lifecycleOperationAnnotation: lifecycleOperationJSON(op)}
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	ss := &appsv1.StatefulSet{}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), ss); err != nil {
		t.Fatal(err)
	}
	one := int32(1)
	ss.Spec.Replicas = &one
	ss.Spec.Template.Spec.Containers = []corev1.Container{{Name: "game", Image: "restored-image"}}
	if err := r.Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.Replicas = 1
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "startup-pod", Finalizers: []string{"test/hold"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: ss.Name, UID: ss.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	return r, gs, rs, ss, pod
}

func TestRestoreStartupCleanupDrainsBeforeRelease(t *testing.T) {
	for _, outcome := range []string{"failed", "deleted", "succeeded"} {
		t.Run(outcome, func(t *testing.T) {
			r, _, rs, ss, pod := startupCleanupFixture(t)
			rs.Status.Phase = gameplanev1alpha1.RestorePhaseFailed
			if outcome == "succeeded" {
				rs.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
			}
			if err := r.Status().Update(t.Context(), rs); err != nil {
				t.Fatal(err)
			}
			if outcome == "deleted" {
				if err := r.Delete(t.Context(), rs); err != nil {
					t.Fatal(err)
				}
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
					t.Fatal(err)
				}
			}
			res, err := r.cleanupRestore(t.Context(), rs)
			if err != nil {
				t.Fatal(err)
			}
			gs := lifecycleServer(t, r)
			if outcome == "succeeded" {
				if gs.Spec.Suspend || gs.Annotations[lifecycleOperationAnnotation] != "" || res.RequeueAfter != 0 {
					t.Fatalf("successful startup was stopped: %+v %+v", gs, res)
				}
				return
			}
			op, held, err := lifecycleOwner(gs)
			if err != nil || !held || op.InitialStartup || !gs.Spec.Suspend || res.RequeueAfter == 0 {
				t.Fatalf("failed startup released or retained permission to start: %+v %+v %v", gs, res, err)
			}
			// An observed scale-down does not prove a terminating Pod drained.
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := r.Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			ss.Status.Replicas, ss.Status.ObservedGeneration = 0, ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			if res, err := r.cleanupRestore(t.Context(), rs); err != nil || res.RequeueAfter == 0 || lifecycleServer(t, r).Annotations[lifecycleOperationAnnotation] == "" {
				t.Fatalf("terminating startup Pod lost exclusion: %+v %v", res, err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(pod), pod); err != nil {
				t.Fatal(err)
			}
			pod.Finalizers = nil
			if err := r.Update(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := r.cleanupRestore(t.Context(), rs); err != nil {
					t.Fatal(err)
				}
			}
			gs = lifecycleServer(t, r)
			if !gs.Spec.Suspend || gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[restoreGuardAnnotation] != "" {
				t.Fatalf("drained failed startup did not settle suspended: %+v", gs)
			}
		})
	}
}

func TestOrphanRestoreStartupDrainsBeforeNextOwnerProceeds(t *testing.T) {
	r, gs, rs, ss, pod := startupCleanupFixture(t)
	rs.Finalizers = nil
	if err := r.Update(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	b := guardBackup("next")
	if err := r.Create(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	want := backupLifecycleIdentity(gs, b, false)
	if ready, err := claimLifecycleOperation(t.Context(), r.Client, r.apiReader(), gs, want, nil); err != nil || ready {
		t.Fatalf("orphan startup admitted another operation: %t %v", ready, err)
	}
	gr := &GameServerReconciler{Client: r.Client, APIReader: r.apiReader(), Scheme: r.Scheme}
	if replicas, _, err := gr.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake); err != nil || replicas != 0 {
		t.Fatalf("orphan recovery did not request zero replicas: %d %v", replicas, err)
	}
	// Even a stale callback asking for one replica must stop the old process
	// without changing the template before the successor owns clean data.
	if err := gr.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
		t.Fatal(err)
	}
	if *ss.Spec.Replicas != 0 || ss.Spec.Template.Spec.Containers[0].Image != "restored-image" {
		t.Fatalf("orphan recovery did not preserve the stopped template: %+v", ss.Spec)
	}
	ss.Status.Replicas, ss.Status.ObservedGeneration = 0, ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if ready, err := claimLifecycleOperation(t.Context(), r.Client, r.apiReader(), gs, want, nil); err != nil || ready {
		t.Fatalf("observed scale-down bypassed live startup Pod: %t %v", ready, err)
	}
	pod.Finalizers = nil
	if err := r.Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	ready := false
	for range 2 {
		var err error
		ready, err = claimLifecycleOperation(t.Context(), r.Client, r.apiReader(), gs, want, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
	}
	if !ready {
		t.Fatal("drained predecessor did not permit successor")
	}
}
