package controller

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/ValgulNecron/gameplane/operator/api/v1alpha1"
)

func TestRestoreReleaseRecoversRestampedStatefulSetGuard(t *testing.T) {
	for _, requested := range []int32{0, 1} {
		t.Run(fmt.Sprintf("requested-%d", requested), func(t *testing.T) {
			r, _, _ := restoreLifecycleFixture(t)
			lifecyclePasses(t, r, "first", 8)
			job := lifecycleJobs(t, r)[0]
			job.Status.Succeeded = 1
			if err := r.Status().Update(t.Context(), &job); err != nil {
				t.Fatal(err)
			}

			// Run the workload callback after cleanup removes its marker but
			// before the restore controller releases the live GameServer guard.
			restamped := false
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				ss, ok := obj.(*appsv1.StatefulSet)
				if !ok || restamped || ss.Annotations[restoreGuardAnnotation] != "" {
					return c.Update(ctx, obj, opts...)
				}
				if err := c.Update(ctx, obj, opts...); err != nil {
					return err
				}
				var gs gameplanev1alpha1.GameServer
				if err := c.Get(ctx, client.ObjectKeyFromObject(ss), &gs); err != nil {
					return err
				}
				gr := &GameServerReconciler{Client: c, APIReader: c, Scheme: r.Scheme}
				if err := gr.reconcileStatefulSet(ctx, &gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
					return err
				}
				var fenced appsv1.StatefulSet
				if err := c.Get(ctx, client.ObjectKeyFromObject(ss), &fenced); err != nil {
					return err
				}
				if *fenced.Spec.Replicas != 0 || fenced.Annotations[restoreGuardAnnotation] != restoreIdentityJSON("first", "restore-first") {
					return fmt.Errorf("workload resumed before live restore guard release")
				}
				restamped = true
				return nil
			}})
			lifecyclePasses(t, r, "first", 4)
			gs := lifecycleServer(t, r)
			if !restamped || gs.Spec.Suspend || gs.Annotations[restoreGuardAnnotation] != "" {
				t.Fatal("successful restore did not release its restamped workload fence")
			}
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Annotations[restoreGuardAnnotation] == "" || *ss.Spec.Replicas != 0 {
				t.Fatal("interleaving did not leave the stale marker to recover")
			}
			gs.Spec.Suspend = requested == 0
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
			if err := gr.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, requested); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Annotations[restoreGuardAnnotation] != "" || *ss.Spec.Replicas != requested {
				t.Fatalf("stale restore fence remains: marker=%q replicas=%d, want %d", ss.Annotations[restoreGuardAnnotation], *ss.Spec.Replicas, requested)
			}
		})
	}
}

func TestRestoreStaleWorkloadMarkerPreservesLiveBlockers(t *testing.T) {
	for _, blocker := range []string{"replacement-restore", "wipe-guard", "pending-wipe", "wipe-worker"} {
		t.Run(blocker, func(t *testing.T) {
			r, _, _ := restoreLifecycleFixture(t)
			gs := lifecycleServer(t, r)
			gs.Spec.Suspend = false
			stale := gs.DeepCopy()
			gs.Annotations = make(map[string]string)
			switch blocker {
			case "replacement-restore":
				gs.Annotations[restoreGuardAnnotation] = restoreIdentityJSON("second", "restore-second")
			case "wipe-guard":
				gs.Annotations[wipeGuardAnnotation] = "wipe"
			case "pending-wipe":
				gs.Annotations[WipeRequestedAnnotation] = "wipe"
			case "wipe-worker":
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "wipe-worker", Namespace: gs.Namespace, Labels: map[string]string{wipeUIDLabel: string(gs.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if err := r.Create(t.Context(), pod); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			ss.Annotations = map[string]string{restoreGuardAnnotation: restoreIdentityJSON("first", "restore-first")}
			if err := r.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
			if err := gr.reconcileStatefulSet(t.Context(), stale, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if *ss.Spec.Replicas != 0 {
				t.Fatal("stale restore-marker recovery bypassed the live blocker")
			}
			if ss.Annotations[restoreGuardAnnotation] != gs.Annotations[restoreGuardAnnotation] || ss.Annotations[wipeGuardAnnotation] != gs.Annotations[wipeGuardAnnotation] {
				t.Fatalf("workload did not adopt live guard ownership: %v", ss.Annotations)
			}
		})
	}
}

func TestRestoreStaleWorkloadMarkerRefusesReplacedIdentity(t *testing.T) {
	for _, replaced := range []string{"gameserver", "statefulset-owner"} {
		t.Run(replaced, func(t *testing.T) {
			r, _, _ := restoreLifecycleFixture(t)
			gs := lifecycleServer(t, r)
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			ss.Annotations = map[string]string{restoreGuardAnnotation: restoreIdentityJSON("first", "restore-first")}
			if replaced == "statefulset-owner" {
				ss.OwnerReferences[0].UID = "replacement-uid"
			} else {
				if err := r.Delete(t.Context(), gs); err != nil {
					t.Fatal(err)
				}
				replacement := gs.DeepCopy()
				replacement.ResourceVersion = ""
				replacement.UID = "replacement-uid"
				if err := r.Create(t.Context(), replacement); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			revision := ss.ResourceVersion
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
			if err := gr.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err == nil {
				t.Fatal("stale workload reconcile accepted a replaced identity")
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.ResourceVersion != revision || *ss.Spec.Replicas != 0 || ss.Annotations[restoreGuardAnnotation] != restoreIdentityJSON("first", "restore-first") {
				t.Fatal("failed identity check mutated the workload")
			}
		})
	}
}
