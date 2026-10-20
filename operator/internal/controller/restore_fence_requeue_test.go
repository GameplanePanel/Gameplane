package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestRestoreFenceWriteRequeuesImmediatelyAndRechecksWriters(t *testing.T) {
	for _, phase := range []gameplanev1alpha1.RestorePhase{gameplanev1alpha1.RestorePhaseSuspending, gameplanev1alpha1.RestorePhaseRunning} {
		t.Run(string(phase), func(t *testing.T) {
			r, _, rs := restoreLifecycleFixture(t)
			lifecyclePasses(t, r, "first", 1) // Persist target identity first.
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			rs.Status.Phase = phase
			if err := r.Status().Update(t.Context(), rs); err != nil {
				t.Fatal(err)
			}
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rs)}
			result, err := r.Reconcile(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result != (ctrl.Result{RequeueAfter: time.Nanosecond}) {
				t.Fatalf("successful fence write must requeue immediately: %+v", result)
			}
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Annotations[restoreGuardAnnotation] != restoreIdentityJSON(rs.Name, rs.UID) {
				t.Fatal("immediate requeue did not persist the workload fence")
			}
			if len(lifecycleJobs(t, r)) != 0 {
				t.Fatal("restore Job created before rechecking the fenced workload")
			}
			// A writer can arrive between the fence write and the next pass.
			// Immediate requeue must not bypass the fresh safety checks.
			writer := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "late-writer", Namespace: "ns"},
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
					Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "server-data"}},
				}}}}},
			}
			if err := r.Create(t.Context(), writer); err != nil {
				t.Fatal(err)
			}
			result, err = r.Reconcile(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result != (ctrl.Result{RequeueAfter: 5 * time.Second}) {
				t.Fatalf("live writer should wait for drainage: %+v", result)
			}
			jobs := lifecycleJobs(t, r)
			if len(jobs) != 1 || jobs[0].Name != "late-writer" {
				t.Fatal("restore overlapped a writer arriving after fence acquisition")
			}
			if err := r.Delete(t.Context(), writer); err != nil {
				t.Fatal(err)
			}
			lifecyclePasses(t, r, "first", 4)
			jobs = lifecycleJobs(t, r)
			if len(jobs) != 1 || jobs[0].Name != "restore-first" {
				t.Fatal("restore did not proceed after writer drained")
			}
		})
	}
}
