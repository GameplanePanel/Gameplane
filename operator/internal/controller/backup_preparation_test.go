package controller

import (
	"context"
	"errors"
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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
				// Queue age must not spend the preparation budget before ownership.
				first.CreationTimestamp = metav1.NewTime(time.Now().Add(-3 * time.Minute))
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
				first = guardGetBackup(t, r, "first")
				deadline := first.Annotations[backupPreparationDeadlineAnnotation]
				if first.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || deadline == "" {
					t.Fatalf("queued source lost its fresh admission budget: %+v", first)
				}
				if expired {
					first.Annotations[backupPreparationDeadlineAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
					if err := r.Update(ctx, first); err != nil {
						t.Fatal(err)
					}
				}
				guardPass(t, r, "first")
				first = guardGetBackup(t, r, "first")
				if expired {
					if first.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || first.Status.CompletionTime == nil || !strings.Contains(first.Status.Message, "preparation deadline exceeded") {
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

func TestBackupUngatedSourceStartupDeadlineSurvivesRestart(t *testing.T) {
	for _, mode := range []string{"csi-pod", "csi-workload", "legacy-job"} {
		t.Run(mode, func(t *testing.T) {
			b := guardBackup("first")
			if mode != "legacy-job" {
				b.Spec.Strategy = "volume-snapshot"
			}
			b.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
			r, q := guardFixture(t, b)
			if err := r.Delete(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns"}}); err != nil {
				t.Fatal(err)
			}
			if mode == "csi-workload" {
				if err := r.Delete(t.Context(), &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "legacy-job" {
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace,
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(b, gameplanev1alpha1.GroupVersion.WithKind("Backup"))}}}
				if err := r.Create(t.Context(), job); err != nil {
					t.Fatal(err)
				}
			}
			guardPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			deadline := b.Annotations[backupSourceStartupDeadlineAnnotation]
			if _, err := time.Parse(time.RFC3339Nano, deadline); err != nil || b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || q.quiesced != 0 {
				t.Fatalf("source did not receive a fresh owned startup budget: %v %+v", err, b)
			}
			if b.Annotations[backupPreparationDeadlineAnnotation] != "" {
				t.Fatal("ungated capture unexpectedly acquired a prepared-worker deadline")
			}
			r = &BackupReconciler{Client: r.Client, Scheme: r.Scheme, AgentClient: q}
			guardPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if b.Annotations[backupSourceStartupDeadlineAnnotation] != deadline {
				t.Fatal("operator restart extended the initial source deadline")
			}
			var legacyWorker *corev1.Pod
			if mode == "legacy-job" {
				// Model a dependent that can outlive foreground Job deletion.
				legacyWorker = &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy-worker", Namespace: "ns", Labels: map[string]string{backupUIDLabel: string(b.UID)}},
					Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if err := r.Create(t.Context(), legacyWorker); err != nil {
					t.Fatal(err)
				}
			}
			b.Annotations[backupSourceStartupDeadlineAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			if err := r.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			guardPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || !strings.Contains(b.Status.Message, "startup grace period expired") || q.quiesced != 0 || q.unquiesced != 0 {
				t.Fatalf("startup deadline did not fail before capture: %+v %+v", b.Status, q)
			}
			var gs gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
				t.Fatal(err)
			}
			if legacyWorker != nil {
				var job batchv1.Job
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); !apierrors.IsNotFound(err) {
					t.Fatalf("expired legacy startup did not cancel its Job: %v", err)
				}
				if gs.Annotations[lifecycleOperationAnnotation] == "" || b.Annotations[backupCopyAbortAnnotation] == "" {
					t.Fatal("expired legacy startup released ownership before its worker drained")
				}
				if err := r.Delete(t.Context(), legacyWorker); err != nil {
					t.Fatal(err)
				}
				guardPass(t, r, b.Name)
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
					t.Fatal(err)
				}
			}
			if gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[backupGuardAnnotation] != "" {
				t.Fatal("expired startup retained lifecycle ownership")
			}
		})
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

func TestBackupSourceStartupDeadlinePersistenceFailureRetries(t *testing.T) {
	b := guardBackup("first")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	if err := r.Delete(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns"}}); err != nil {
		t.Fatal(err)
	}
	base := r.Client.(client.WithWatch)
	failure := apierrors.NewServiceUnavailable("deadline persistence unavailable")
	r.Client = interceptor.NewClient(base, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if backup, ok := obj.(*gameplanev1alpha1.Backup); ok && backup.Annotations[backupSourceStartupDeadlineAnnotation] != "" {
			return failure
		}
		return c.Update(ctx, obj, opts...)
	}})
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); !errors.Is(err, failure) {
		t.Fatalf("failed first deadline persistence was not retried: %v", err)
	}
	b = guardGetBackup(t, r, b.Name)
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || b.Annotations[backupSourceStartupDeadlineAnnotation] != "" || q.quiesced != 0 {
		t.Fatalf("write failure was treated as admitted expiry: %+v %+v", b, q)
	}
	r.Client = base
	guardPass(t, r, b.Name)
	if got := guardGetBackup(t, r, b.Name); got.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || got.Annotations[backupSourceStartupDeadlineAnnotation] == "" {
		t.Fatalf("healthy retry did not persist its startup budget: %+v", got)
	}
}
