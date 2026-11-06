package controller

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Removing the admission deadline must leave the old Backup pending and keep
// its owner/fence held, preventing the successor from creating its copy Job.
func TestBackupInitialSourceStartupGrace(t *testing.T) {
	for _, state := range []string{"absent", "pending", "terminating"} {
		for _, expired := range []bool{false, true} {
			name := state + "/within-grace"
			if expired {
				name = state + "/expired"
			}
			t.Run(name, func(t *testing.T) {
				first := guardBackup("first")
				first.CreationTimestamp = metav1.Now()
				if expired {
					first.CreationTimestamp = metav1.NewTime(time.Now().Add(-3 * time.Minute))
				}
				r, q := guardFixture(t, first, guardBackup("second"))
				ctx := t.Context()
				podKey := client.ObjectKey{Namespace: "ns", Name: "server-0"}
				var pod corev1.Pod
				if err := r.Get(ctx, podKey, &pod); err != nil {
					t.Fatal(err)
				}
				switch state {
				case "absent":
					if err := r.Delete(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				case "pending":
					pod.Status.Phase = corev1.PodPending
					if err := r.Status().Update(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				case "terminating":
					pod.Finalizers = []string{"test/hold"}
					if err := r.Update(ctx, &pod); err != nil {
						t.Fatal(err)
					}
					if err := r.Delete(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				}
				result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(first)})
				if err != nil || result.RequeueAfter <= 0 {
					t.Fatalf("source admission did not requeue: %+v, %v", result, err)
				}
				guardPass(t, r, "first")
				first = guardGetBackup(t, r, "first")
				if expired {
					if first.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || first.Status.CompletionTime == nil || !strings.Contains(first.Status.Message, "startup grace period expired") {
						t.Fatalf("expired source did not fail visibly: %+v", first.Status)
					}
				} else if first.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
					t.Fatalf("startup race failed before grace expired: %+v", first.Status)
				}
				if first.Annotations[backupPreparationAnnotation] != "" || q.quiesced != 0 || q.unquiesced != 0 {
					t.Fatalf("unready source attempted capture: preparation=%q agent=%+v", first.Annotations[backupPreparationAnnotation], q)
				}
				var jobs batchv1.JobList
				if err := r.List(ctx, &jobs); err != nil || len(jobs.Items) != 0 {
					t.Fatalf("unready source created copy workers: %+v, %v", jobs.Items, err)
				}
				serverKey := client.ObjectKey{Namespace: "ns", Name: "server"}
				var gs gameplanev1alpha1.GameServer
				var ss appsv1.StatefulSet
				if err := r.Get(ctx, serverKey, &gs); err != nil {
					t.Fatal(err)
				}
				if err := r.Get(ctx, serverKey, &ss); err != nil {
					t.Fatal(err)
				}
				if held := gs.Annotations[lifecycleOperationAnnotation] != ""; held == expired {
					t.Fatalf("wrong lifecycle owner after admission: %q", gs.Annotations[lifecycleOperationAnnotation])
				}
				if held := ss.Annotations[lifecycleOperationAnnotation] != ""; held == expired {
					t.Fatalf("wrong workload fence after admission: %q", ss.Annotations[lifecycleOperationAnnotation])
				}
				// A healthy source can recover during the grace period. After an
				// expired attempt, a successor must also be able to acquire it.
				switch state {
				case "absent":
					pod.ResourceVersion = ""
					if err := r.Create(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				case "pending":
					pod.Status.Phase = corev1.PodRunning
					if err := r.Status().Update(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				case "terminating":
					if err := r.Get(ctx, podKey, &pod); err != nil {
						t.Fatal(err)
					}
					pod.Finalizers = nil
					if err := r.Update(ctx, &pod); err != nil {
						t.Fatal(err)
					}
					pod.ResourceVersion, pod.DeletionTimestamp = "", nil
					pod.UID = "replacement-pod-uid"
					if err := r.Create(ctx, &pod); err != nil {
						t.Fatal(err)
					}
				}
				admitted := "first"
				if expired {
					admitted = "second"
				}
				guardPass(t, r, admitted)
				var job batchv1.Job
				if err := r.Get(ctx, client.ObjectKey{Namespace: "ns", Name: admitted}, &job); err != nil {
					t.Fatalf("healthy source could not proceed: %v", err)
				}
				if q.quiesced != 1 {
					t.Fatalf("healthy source was not quiesced once: %+v", q)
				}
				if expired {
					if err := r.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "first"}, &job); !apierrors.IsNotFound(err) {
						t.Fatalf("expired attempt started a worker: %v", err)
					}
				}
			})
		}
	}
}

func TestBackupHealthySourceAdmittedAfterStartupGrace(t *testing.T) {
	b := guardBackup("first")
	b.CreationTimestamp = metav1.NewTime(time.Now().Add(-3 * time.Minute))
	r, q := guardFixture(t, b)
	guardPass(t, r, "first")
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); err != nil {
		t.Fatalf("backup age rejected a ready source: %v", err)
	}
	if q.quiesced != 1 {
		t.Fatalf("ready source was not quiesced: %+v", q)
	}
}
