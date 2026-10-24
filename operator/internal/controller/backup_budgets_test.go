package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func budgetPass(t *testing.T, r *BackupReconciler, name string) {
	t.Helper()
	for range 4 {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
}

// Model the Job controller and kubelet explicitly: no worker may be authorized
// merely because the Job exists or an informer has an old Ready observation.
func budgetReadyWorker(t *testing.T, r *BackupReconciler, name, uid string) *corev1.Pod {
	t.Helper()
	ctx := context.Background()
	var job batchv1.Job
	if err := r.Get(ctx, client.ObjectKey{Namespace: "ns", Name: name}, &job); err != nil {
		t.Fatal(err)
	}
	if job.UID == "" {
		job.UID = types.UID(name + "-job-uid")
		if err := r.Update(ctx, &job); err != nil {
			t.Fatal(err)
		}
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-worker", Namespace: "ns", UID: types.UID(uid), Labels: job.Spec.Template.Labels,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}},
		Spec: job.Spec.Template.Spec, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	pod.Spec.NodeName = "node-a"
	if err := r.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	return pod
}

func TestBackupWorkerPreparesBeforeQuiesceAndPinsAuthorization(t *testing.T) {
	r, q := guardFixtureWorkers(t, false, guardBackup("prepared"))
	budgetPass(t, r, "prepared")
	if q.quiesced != 0 {
		t.Fatal("save-off preceded worker readiness")
	}
	b := guardGetBackup(t, r, "prepared")
	if b.Status.CaptureManifest != nil {
		t.Fatal("capture metadata recorded before worker preparation")
	}
	var gate corev1.ConfigMap
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: backupStartConfigMapName(b)}, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("unprepared worker received start authorization")
	}
	worker := budgetReadyWorker(t, r, b.Name, "prepared-worker-uid")
	budgetPass(t, r, b.Name)
	b = guardGetBackup(t, r, b.Name)
	if q.quiesced != 1 || b.Status.CaptureManifest == nil || b.Status.CaptureManifest.State != "Recorded" || b.Status.CaptureManifest.QuiesceOutcome != "confirmed" {
		t.Fatalf("prepared copy not quiesced/recorded: %+v, %+v", q, b.Status)
	}
	if volumes := b.Status.CaptureManifest.Volumes; len(volumes) != 1 || volumes[0].ClaimName != "server-data" || !volumes[0].Included || volumes[0].Classification != "data" {
		t.Fatalf("prepared worker lost capture-time coverage: %+v", volumes)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(&gate), &gate); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gate.Data[backupStartTokenKey], string(worker.UID)) || !strings.Contains(gate.Data[backupStartTokenKey], string(b.UID)) {
		t.Fatalf("unbound authorization: %q", gate.Data[backupStartTokenKey])
	}
	deadline := b.Annotations[backupQuiesceDeadlineAnnotation]
	r = &BackupReconciler{Client: r.Client, Scheme: r.Scheme, AgentClient: q, QuiesceTimeout: time.Hour}
	budgetPass(t, r, b.Name)
	if got := guardGetBackup(t, r, b.Name).Annotations[backupQuiesceDeadlineAnnotation]; got != deadline {
		t.Fatal("restart extended admitted quiesce deadline")
	}
}

func TestBackupPreparedWorkerUsesTemplateAdmittedBeforeOwnership(t *testing.T) {
	for _, adoptBeforeClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("adopt-before-claim=%t", adoptBeforeClaim), func(t *testing.T) {
			r, q := guardFixtureWorkers(t, false, guardBackup("prepared"))
			base := r.Client.(client.WithWatch)
			r.APIReader = base
			var gs gameplanev1alpha1.GameServer
			if err := base.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
				t.Fatal(err)
			}
			oldGroup, nextGroup := int64(1000), int64(2000)
			installed := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template", UID: "old-catalog-uid"},
				Spec: gameplanev1alpha1.GameTemplateSpec{Security: &gameplanev1alpha1.GameSecuritySpec{FSGroup: &oldGroup}}}
			if err := base.Create(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			revisions := &GameServerReconciler{Client: base, APIReader: base, Scheme: r.Scheme}
			oldPin, err := revisions.saveTemplateSnapshot(t.Context(), &gs, installed)
			if err != nil {
				t.Fatal(err)
			}
			gs.Annotations = map[string]string{effectiveTemplateAnnotation: oldPin}
			if err := base.Update(t.Context(), &gs); err != nil {
				t.Fatal(err)
			}
			// Reinstalling the catalog can change both its UID and filesystem
			// settings without admitting that content for this GameServer.
			if err := base.Delete(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			installed = &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template", UID: "new-catalog-uid"},
				Spec: gameplanev1alpha1.GameTemplateSpec{Security: &gameplanev1alpha1.GameSecuritySpec{FSGroup: &nextGroup}}}
			if err := base.Create(t.Context(), installed); err != nil {
				t.Fatal(err)
			}
			nextPin, err := revisions.saveTemplateSnapshot(t.Context(), &gs, installed)
			if err != nil {
				t.Fatal(err)
			}
			adopted := false
			r.Client = interceptor.NewClient(base, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if b, ok := obj.(*gameplanev1alpha1.Backup); ok && adoptBeforeClaim && !adopted && b.Annotations[backupTargetAnnotation] != "" {
					// Finish a rollout after Reconcile reads the GameServer but
					// before the Backup claims its shared lifecycle ownership.
					var live gameplanev1alpha1.GameServer
					if err := c.Get(ctx, client.ObjectKeyFromObject(&gs), &live); err != nil {
						return err
					}
					live.Annotations[effectiveTemplateAnnotation] = nextPin
					if err := c.Update(ctx, &live); err != nil {
						return err
					}
					adopted = true
				}
				return c.Update(ctx, obj, opts...)
			}})
			budgetPass(t, r, "prepared")
			var job batchv1.Job
			if err := base.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "prepared"}, &job); err != nil {
				t.Fatal(err)
			}
			want := oldGroup
			if adoptBeforeClaim {
				want = nextGroup
			}
			security := job.Spec.Template.Spec.SecurityContext
			if adopted != adoptBeforeClaim || security == nil || security.FSGroup == nil || *security.FSGroup != want {
				t.Fatalf("prepared worker did not use the admitted source: adopted=%t security=%+v want fsGroup=%d", adopted, security, want)
			}
			if q.quiesced != 0 {
				t.Fatal("template resolution bypassed worker readiness")
			}
			budgetReadyWorker(t, r, "prepared", "ready-worker-uid")
			budgetPass(t, r, "prepared")
			b := guardGetBackup(t, r, "prepared")
			if b.Annotations[backupAuthorizedWorkerAnnotation] == "" || b.Status.CaptureManifest == nil || b.Status.CaptureManifest.State != "Recorded" || q.quiesced != 1 {
				t.Fatalf("pinned preparation did not reach authorized capture: %+v %+v", b, q)
			}
		})
	}
}

func TestBackupPreparedWorkerRejectsMissingEffectiveTemplate(t *testing.T) {
	r, q := guardFixtureWorkers(t, false, guardBackup("prepared"))
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	gs.Annotations = map[string]string{effectiveTemplateAnnotation: strings.Repeat("a", 64)}
	if err := r.Update(t.Context(), &gs); err != nil {
		t.Fatal(err)
	}
	// A readable catalog must not substitute for the missing admitted content.
	if err := r.Create(t.Context(), &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template"}}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "prepared"}})
	if err == nil || !strings.Contains(err.Error(), "read pinned template snapshot") || q.quiesced != 0 {
		t.Fatalf("missing pin did not fail before save-off: %v %+v", err, q)
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "prepared"}, &job); !apierrors.IsNotFound(err) {
		t.Fatalf("missing pin created a worker: %v", err)
	}
}

func TestBackupPreparationDeadlineNeverDisablesSaving(t *testing.T) {
	r, q := guardFixtureWorkers(t, false, guardBackup("expired"))
	budgetPass(t, r, "expired")
	b := guardGetBackup(t, r, "expired")
	patchBackupAnnotations(b, map[string]string{backupPreparationDeadlineAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err := r.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, b.Name)
	b = guardGetBackup(t, r, b.Name)
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.quiesced != 0 {
		t.Fatalf("expired preparation disabled saving: %+v %+v", b.Status, q)
	}
}

func TestBackupRawAndStoppedCopiesKeepFullJobBudget(t *testing.T) {
	for _, mode := range []string{"raw", "no-agent", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			b := guardBackup("first")
			b.Spec.Quiesce = mode != "raw"
			r, q := guardFixture(t, b)
			if mode == "no-agent" {
				r.AgentClient = nil
			}
			if mode == "stopped" {
				r, q = stoppedBackupFixture(t, true)
			}
			r.QuiesceTimeout, r.JobActiveDeadlineSeconds = time.Second, 7200
			budgetPass(t, r, "first")
			b = guardGetBackup(t, r, "first")
			deadline, err := time.Parse(time.RFC3339Nano, b.Annotations[backupQuiesceDeadlineAnnotation])
			if err != nil || time.Until(deadline) < time.Hour || b.Annotations[backupAuthorizedWorkerAnnotation] == "" || q.quiesced != 0 {
				t.Fatalf("non-quiesced copy lost its full budget/authorization: %v %+v %+v", err, b.Annotations, q)
			}
		})
	}
}

func TestBackupRawSourceWaitsForInitialScheduling(t *testing.T) {
	for _, initiallyAbsent := range []bool{false, true} {
		t.Run(fmt.Sprintf("absent=%t", initiallyAbsent), func(t *testing.T) {
			b := guardBackup("starting")
			b.Spec.Quiesce = false
			r, q := guardFixtureWorkers(t, false, b)
			var pod corev1.Pod
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server-0"}, &pod); err != nil {
				t.Fatal(err)
			}
			pod.Spec.NodeName = ""
			if err := r.Update(t.Context(), &pod); err != nil {
				t.Fatal(err)
			}
			if initiallyAbsent {
				if err := r.Delete(t.Context(), &pod); err != nil {
					t.Fatal(err)
				}
			}
			budgetPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			var job batchv1.Job
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); !apierrors.IsNotFound(err) || b.Annotations[backupWorkerSourceAnnotation] != "" || q.quiesced != 0 {
				t.Fatalf("starting source was bound or quiesced: job=%v annotations=%v calls=%+v", err, b.Annotations, q)
			}
			pod.Spec.NodeName = "node-a"
			if initiallyAbsent {
				pod.ResourceVersion = ""
				if err := r.Create(t.Context(), &pod); err != nil {
					t.Fatal(err)
				}
			} else if err := r.Update(t.Context(), &pod); err != nil {
				t.Fatal(err)
			}
			budgetPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); err != nil || b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
				t.Fatalf("normal source scheduling failed backup: job=%v status=%+v", err, b.Status)
			}
			// Once captured in the worker's immutable environment, the source
			// may not silently change to a different pod at the same name.
			if err := r.Delete(t.Context(), &pod); err != nil {
				t.Fatal(err)
			}
			pod.UID, pod.ResourceVersion = "replacement-pod", ""
			if err := r.Create(t.Context(), &pod); err != nil {
				t.Fatal(err)
			}
			budgetPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Annotations[backupAuthorizedWorkerAnnotation] != "" {
				t.Fatalf("replacement source escaped immutable binding: %+v", b)
			}
		})
	}
}

func TestBackupPreparedWorkerFailurePreservesJobMessage(t *testing.T) {
	r, _ := guardFixtureWorkers(t, false, guardBackup("failed-worker"))
	budgetPass(t, r, "failed-worker")
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "failed-worker"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
	if err := r.Status().Update(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, job.Name)
	b := guardGetBackup(t, r, job.Name)
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Status.Message != "backup job reported Failed" || b.Annotations[backupCopyAbortAnnotation] != b.Status.Message {
		t.Fatalf("prepared worker lost failure compatibility/invalidation: %+v", b)
	}
}

func TestBackupQueuedCopyDoesNotSpendPreparationBudget(t *testing.T) {
	r, _ := guardFixture(t, guardBackup("first"), guardBackup("second"))
	budgetPass(t, r, "first")
	budgetPass(t, r, "second")
	second := guardGetBackup(t, r, "second")
	if second.Annotations[backupPreparationDeadlineAnnotation] != "" || second.Annotations[backupAuthorizedWorkerAnnotation] != "" {
		t.Fatal("queued Backup spent preparation budget before owning the source")
	}
}

func TestBackupQuiesceDeadlineDrainsBeforeSaveOn(t *testing.T) {
	r, q := guardFixtureWorkers(t, false, guardBackup("expired"))
	budgetPass(t, r, "expired")
	worker := budgetReadyWorker(t, r, "expired", "worker-uid")
	budgetPass(t, r, "expired")
	b := guardGetBackup(t, r, "expired")
	patchBackupAnnotations(b, map[string]string{backupQuiesceDeadlineAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err := r.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, b.Name)
	b = guardGetBackup(t, r, b.Name)
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.unquiesced != 0 {
		t.Fatalf("deadline bypassed worker drain: %+v %+v", b.Status, q)
	}
	if err := r.Delete(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, b.Name)
	if q.unquiesced != 1 {
		t.Fatalf("save-on not retried after drain: %+v", q)
	}
}

func TestBackupDeletionRevokesAuthorizationBeforeTerminatingWorkerDrains(t *testing.T) {
	r, q := guardFixtureWorkers(t, false, guardBackup("cancel"))
	budgetPass(t, r, "cancel")
	worker := budgetReadyWorker(t, r, "cancel", "worker-uid")
	budgetPass(t, r, "cancel")
	worker.Finalizers = []string{"test/hold"}
	if err := r.Update(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	b := guardGetBackup(t, r, "cancel")
	if err := r.Delete(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, "cancel")
	var gate corev1.ConfigMap
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: b.Namespace, Name: backupStartConfigMapName(b)}, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" || q.unquiesced != 0 {
		t.Fatal("deletion retained authorization or bypassed terminating worker")
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(worker), worker); err != nil {
		t.Fatal(err)
	}
	worker.Finalizers = nil
	if err := r.Update(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	budgetPass(t, r, "cancel")
	if q.unquiesced != 1 {
		t.Fatal("deletion did not restore saving after drain")
	}
}

func TestBackupRecoveryRevokesTokenBeforeCancellingWorkers(t *testing.T) {
	r, _ := guardFixtureWorkers(t, false, guardBackup("orphan"))
	budgetPass(t, r, "orphan")
	budgetReadyWorker(t, r, "orphan", "orphan-worker")
	budgetPass(t, r, "orphan")
	b := guardGetBackup(t, r, "orphan")
	deletionObserved := false
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*batchv1.Job); ok {
			var gate corev1.ConfigMap
			if err := c.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: backupStartConfigMapName(b)}, &gate); err != nil {
				t.Fatal(err)
			}
			if gate.Data[backupStartTokenKey] != "" {
				t.Fatal("recovery cancelled Job before revoking authorization")
			}
			deletionObserved = true
		}
		return c.Delete(ctx, obj, opts...)
	}})
	busy, err := r.backupWorkersLive(t.Context(), b.Namespace, backupIdentity{Name: b.Name, UID: b.UID}, true)
	if err != nil || !busy || !deletionObserved {
		t.Fatalf("recovery bypassed worker cancellation/drain: %t %t %v", busy, deletionObserved, err)
	}
}

func TestBackupCSIDeadlineInvalidatesLateReady(t *testing.T) {
	for _, status := range []*snapshotv1.VolumeSnapshotStatus{nil, {ReadyToUse: ownerBoolPtr(false)}} {
		b := guardBackup("snapshot")
		b.Spec.Strategy = "volume-snapshot"
		r, q := guardFixture(t, b)
		budgetPass(t, r, b.Name)
		b = guardGetBackup(t, r, b.Name)
		patchBackupAnnotations(b, map[string]string{backupCSIDeadlineAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
		if err := r.Update(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		var vs snapshotv1.VolumeSnapshot
		if err := r.Get(context.Background(), client.ObjectKeyFromObject(b), &vs); err != nil {
			t.Fatal(err)
		}
		vs.Status = status
		if err := r.Status().Update(context.Background(), &vs); err != nil {
			t.Fatal(err)
		}
		budgetPass(t, r, b.Name)
		b = guardGetBackup(t, r, b.Name)
		if b.Annotations[backupSnapshotAbortAnnotation] != "true" || b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Status.SnapshotID != "" || q.unquiesced != 1 {
			t.Fatalf("CSI abort did not invalidate before cleanup: %+v %+v", b, q)
		}
		vs.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: ownerBoolPtr(true)}
		if _, err := r.observeVolumeSnapshot(context.Background(), b, &vs); err != nil {
			t.Fatal(err)
		}
		if got := guardGetBackup(t, r, b.Name); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Status.SnapshotID != "" {
			t.Fatal("late readiness revived aborted capture")
		}
	}
}

func TestBackupWorkerDoesNotTrustCachedReadinessOrReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement-%t", replacement), func(t *testing.T) {
			r, q := guardFixtureWorkers(t, false, guardBackup("copy"))
			budgetPass(t, r, "copy")
			worker := budgetReadyWorker(t, r, "copy", "first-worker")
			if replacement {
				q.afterQuiesce = func() {
					if err := r.Delete(t.Context(), worker); err != nil {
						t.Fatal(err)
					}
					budgetReadyWorker(t, r, "copy", "replacement-worker")
				}
			} else {
				r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if err := cl.List(ctx, list, opts...); err != nil {
						return err
					}
					if pods, ok := list.(*corev1.PodList); ok {
						for i := range pods.Items {
							if pods.Items[i].UID == worker.UID {
								pods.Items[i].Status.Conditions = nil
							}
						}
					}
					return nil
				}})
			}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "copy"}}); err != nil {
				t.Fatal(err)
			}
			b := guardGetBackup(t, r, "copy")
			var gate corev1.ConfigMap
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: backupStartConfigMapName(b)}, &gate); err != nil {
				t.Fatal(err)
			}
			if gate.Data[backupStartTokenKey] != "" {
				t.Fatal("stale readiness granted copy authorization")
			}
			if !replacement && q.quiesced != 0 {
				t.Fatal("cached Ready disabled saving")
			}
			if replacement && q.quiesced != 1 {
				t.Fatal("fixture did not replace worker during RPC")
			}
		})
	}
}

func TestPreparedBackupPlacementAndResources(t *testing.T) {
	for _, hard := range []bool{false, true} {
		r := &BackupReconciler{HardSameNode: hard, BackupResources: &corev1.ResourceRequirements{}}
		b := guardBackup("copy")
		source := &backupWorkerSource{NodeName: "node-a", Tolerations: []corev1.Toleration{{Key: "games", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}}
		spec := r.buildPreparedBackupPodSpec(b, &gameplanev1alpha1.GameTemplate{}, source, time.Now().Add(time.Minute))
		if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken || len(spec.Tolerations) != 1 || spec.Tolerations[0].Key != "games" {
			t.Fatal("worker credentials or tolerations contract lost")
		}
		if spec.Affinity == nil || len(spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution) != 1 {
			t.Fatal("same-node preference missing")
		}
		if (spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil) != hard {
			t.Fatal("hard placement opt-in not respected")
		}
		if len(spec.Containers[0].Resources.Requests) != 0 || spec.Containers[0].ReadinessProbe == nil {
			t.Fatal("resource clearing or preparation probe missing")
		}
		source.NodeName = ""
		if stopped := r.buildPreparedBackupPodSpec(b, &gameplanev1alpha1.GameTemplate{}, source, time.Now()); stopped.Affinity != nil {
			t.Fatal("stopped source invented a game node")
		}
	}
}

// Execute the real gate script in CI with only its mount paths redirected to
// temp directories. A fake restic records whether source capture was invoked.
func TestBackupWorkerScriptRejectsExpiredAndReplayedTokens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worker image uses a POSIX shell; executed by Linux CI")
	}
	for _, tokenKind := range []string{"valid", "expired", "other-worker", "other-backup", "other-source", "repo-failure"} {
		t.Run(tokenKind, func(t *testing.T) {
			dir := t.TempDir()
			restic := "#!/bin/sh\nif [ \"$1\" = snapshots ]; then [ \"$REPO_FAIL\" != true ]; exit $?; fi\nprintf copied > \"$COPY_MARKER\"\n"
			if err := os.WriteFile(filepath.Join(dir, "restic"), []byte(restic), 0o700); err != nil {
				t.Fatal(err)
			}
			backup, pod, source, expiry := "backup", "worker", "source", time.Now().Add(time.Minute).Unix()
			switch tokenKind {
			case "expired":
				expiry = time.Now().Add(-time.Minute).Unix()
			case "other-worker":
				pod = "old-worker"
			case "other-backup":
				backup = "old-backup"
			case "other-source":
				source = "old-source"
			}
			tokenFile, prepared, copied := filepath.Join(dir, "token"), filepath.Join(dir, "prepared"), filepath.Join(dir, "copied")
			if err := os.WriteFile(tokenFile, fmt.Appendf(nil, "v1 %s %s %s %d\n", backup, pod, source, expiry), 0o600); err != nil {
				t.Fatal(err)
			}
			script := strings.NewReplacer("/data", dir, "/start/start", tokenFile, "/tmp/prepared", prepared).Replace(resticWorkerScript)
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script, "backup-worker", "backup", dir)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "BACKUP_UID=backup", "POD_UID=worker", "SOURCE_ID=source", fmt.Sprintf("PREPARATION_EXPIRES=%d", time.Now().Add(time.Second).Unix()), "COPY_MARKER="+copied, fmt.Sprintf("REPO_FAIL=%t", tokenKind == "repo-failure"))
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("gate did not terminate: %s", output)
			}
			_, copiedErr := os.Stat(copied)
			if tokenKind == "valid" {
				if err != nil || copiedErr != nil {
					t.Fatalf("valid token did not start copy: %v %s", err, output)
				}
			} else if err == nil || copiedErr == nil {
				t.Fatalf("invalid token/preparation started capture: %v %s", err, output)
			}
			if tokenKind == "repo-failure" {
				if _, err := os.Stat(prepared); err == nil {
					t.Fatal("repository failure became Ready")
				}
			}
		})
	}
}

func TestBackupCSIAbortReclaimsOnlyVerifiedDeleteContent(t *testing.T) {
	for _, mode := range []string{"delete", "retain", "foreign-reference", "replacement-snapshot", "delete-error"} {
		t.Run(mode, func(t *testing.T) {
			b := guardBackup("snapshot")
			b.Spec.Strategy = "volume-snapshot"
			r, q := guardFixture(t, b)
			budgetPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			var vs snapshotv1.VolumeSnapshot
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &vs); err != nil {
				t.Fatal(err)
			}
			contentName := "content"
			content := &snapshotv1.VolumeSnapshotContent{ObjectMeta: metav1.ObjectMeta{Name: contentName, UID: "content-uid"}, Spec: snapshotv1.VolumeSnapshotContentSpec{DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
				VolumeSnapshotRef: corev1.ObjectReference{Name: vs.Name, Namespace: vs.Namespace, UID: vs.UID}}}
			if mode == "retain" {
				content.Spec.DeletionPolicy = snapshotv1.VolumeSnapshotContentRetain
			}
			if mode == "foreign-reference" {
				content.Spec.VolumeSnapshotRef.UID = "foreign-snapshot"
			}
			if err := r.Create(t.Context(), content); err != nil {
				t.Fatal(err)
			}
			vs.Status = &snapshotv1.VolumeSnapshotStatus{BoundVolumeSnapshotContentName: &contentName}
			if err := r.Status().Update(t.Context(), &vs); err != nil {
				t.Fatal(err)
			}
			if mode == "delete" {
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					options := &client.DeleteOptions{}
					for _, option := range opts {
						option.ApplyToDelete(options)
					}
					if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != obj.GetUID() {
						t.Fatalf("cleanup deletion lacks exact UID precondition: %+v", options)
					}
					return c.Delete(ctx, obj, opts...)
				}})
			}
			if mode == "replacement-snapshot" {
				if err := r.Delete(t.Context(), &vs); err != nil {
					t.Fatal(err)
				}
				vs.UID, vs.ResourceVersion = "replacement-uid", ""
				if err := r.Create(t.Context(), &vs); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "delete-error" {
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
						return fmt.Errorf("driver deletion unavailable")
					}
					return c.Delete(ctx, obj, opts...)
				}})
			}
			patchBackupAnnotations(b, map[string]string{backupCSIDeadlineAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
			if err := r.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			// First request deletion, then separately observe its completion.
			// A single pass must not claim backend reclamation was observed.
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
				t.Fatal(err)
			}
			b = guardGetBackup(t, r, b.Name)
			if q.unquiesced != 1 || b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Status.SnapshotID != "" {
				t.Fatalf("reclamation blocked invalidation/save-on: %+v %+v", b.Status, q)
			}
			condition := meta.FindStatusCondition(b.Status.Conditions, "SnapshotCleaned")
			if condition == nil {
				t.Fatal("missing cleanup condition")
			}
			err := r.Get(t.Context(), client.ObjectKeyFromObject(content), content)
			switch mode {
			case "delete":
				if !apierrors.IsNotFound(err) || condition.Status != metav1.ConditionFalse || condition.Reason != "SnapshotCleanupPending" {
					t.Fatalf("deletion request was not recorded before observing absence: %v %+v", err, condition)
				}
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(&vs), &vs); !apierrors.IsNotFound(err) {
					t.Fatalf("snapshot remains after deletion: %v", err)
				}
				budgetPass(t, r, b.Name)
				b = guardGetBackup(t, r, b.Name)
				condition = meta.FindStatusCondition(b.Status.Conditions, "SnapshotCleaned")
				if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "SnapshotCleanupComplete" {
					t.Fatalf("observed resource absence did not complete reclamation: %+v", condition)
				}
			case "retain":
				if err != nil || condition.Reason != "SnapshotCleanupRetained" || !strings.Contains(condition.Message, "content-uid") {
					t.Fatalf("Retain identity was lost: %v %+v", err, condition)
				}
			case "foreign-reference", "replacement-snapshot":
				if err != nil || condition.Reason != "SnapshotCleanupUnverified" {
					t.Fatalf("foreign resource was touched: %v %+v", err, condition)
				}
			case "delete-error":
				if condition.Reason != "SnapshotCleanupPending" || !strings.Contains(condition.Message, "ns/snapshot") || !strings.Contains(condition.Message, string(vs.UID)) {
					t.Fatalf("delete failure not visible: %+v", condition)
				}
			}
		})
	}
}
