package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
)

func stoppedBackupFixture(t *testing.T, suspended bool) (*BackupReconciler, *guardQuiescer) {
	t.Helper()
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	ctx := t.Context()
	var gs gameplanev1alpha1.GameServer
	key := client.ObjectKey{Namespace: "ns", Name: "server"}
	if err := r.Get(ctx, key, &gs); err != nil {
		t.Fatal(err)
	}
	gs.Spec.Suspend = suspended
	if !suspended {
		gs.Annotations = map[string]string{IdleAsleepSinceAnnotation: "2026-10-21T00:00:00Z"}
	}
	if err := r.Update(ctx, &gs); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(ctx, key, &ss); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	ss.Spec.Replicas = &zero
	if err := r.Update(ctx, &ss); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns"}}); err != nil {
		t.Fatal(err)
	}
	return r, q
}

// A regression to unconditional agent quiesce would fail the first admission;
// releasing before worker drain would let the second Job appear prematurely.
func TestBackupStoppedCopiesWithoutWakingAndReleasesForNextCopy(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "suspended"}[suspended], func(t *testing.T) {
			r, q := stoppedBackupFixture(t, suspended)
			q.quiesceErr = errors.New("absent agent")
			guardPass(t, r, "first")
			first := guardGetBackup(t, r, "first")
			var prep backupPreparation
			if err := json.Unmarshal([]byte(first.Annotations[backupPreparationAnnotation]), &prep); err != nil {
				t.Fatal(err)
			}
			if prep.Outcome != "already-stopped" || prep.TargetUID != "server-uid" || prep.WorkloadUID != "workload-uid" || prep.PodUID != "" {
				t.Fatalf("wrong source proof: %+v", prep)
			}
			if first.Status.CaptureManifest == nil || first.Status.CaptureManifest.QuiesceOutcome != "already-stopped" {
				t.Fatalf("stopped source was not recorded at admission: %+v", first.Status.CaptureManifest)
			}
			var job batchv1.Job
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); err != nil {
				t.Fatal(err)
			}
			// A restart and concurrent wake request must preserve the zero-replica fence.
			r = &BackupReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme, AgentClient: q, LogReader: r.LogReader}
			var gs gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations == nil {
				gs.Annotations = map[string]string{}
			}
			gs.Annotations[IdleWakeRequestedAnnotation] = "wake"
			if err := r.Update(t.Context(), &gs); err != nil {
				t.Fatal(err)
			}
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
			if err := gr.reconcileStatefulSet(t.Context(), &gs, singlePortTemplate(true, "minecraft"), nil, nil, 1); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
				t.Fatal("wake crossed the backup fence")
			}
			guardPass(t, r, "second")
			var second batchv1.Job
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "second"}, &second); !apierrors.IsNotFound(err) {
				t.Fatalf("second copy admitted before drain: %v", err)
			}
			job.Status.Succeeded = 1
			if err := r.Status().Update(t.Context(), &job); err != nil {
				t.Fatal(err)
			}
			guardPass(t, r, "first")
			if got := guardGetBackup(t, r, "first"); got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || got.Status.SnapshotID == "" {
				t.Fatalf("not restorable: %+v", got.Status)
			}
			first = guardGetBackup(t, r, "first")
			if condition := backupConsistencyCondition(first); condition.Status != metav1.ConditionTrue || condition.Reason != "StoppedSource" {
				t.Fatalf("stopped copy claims wrong consistency: %+v", condition)
			}
			guardPass(t, r, "second")
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "second"}, &second); err != nil {
				t.Fatal(err)
			}
			if q.quiesced != 0 || q.unquiesced != 0 {
				t.Fatalf("stopped copy contacted absent agent: %+v", q)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
				t.Fatal(err)
			}
			if gs.Spec.Suspend != suspended || gs.Annotations[IdleWakeRequestedAnnotation] != "wake" || (!suspended && gs.Annotations[IdleAsleepSinceAnnotation] != "2026-10-21T00:00:00Z") {
				t.Fatal("copy changed power intent")
			}
		})
	}
}

func TestBackupStoppedRequiresLiveDrainProof(t *testing.T) {
	for _, tc := range []string{"desired-one", "observed-replicas", "stale-generation", "terminating-game", "foreign-writer", "pending-writer-job", "read-error"} {
		t.Run(tc, func(t *testing.T) {
			r, q := stoppedBackupFixture(t, true)
			base := r.Client
			r.APIReader = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					if ss, ok := obj.(*appsv1.StatefulSet); ok {
						switch tc {
						case "desired-one":
							one := int32(1)
							ss.Spec.Replicas = &one
						case "observed-replicas":
							ss.Status.Replicas = 1
						case "stale-generation":
							ss.Generation = 2
							ss.Status.ObservedGeneration = 1
						}
					}
					return nil
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if err := c.List(ctx, list, opts...); err != nil {
						return err
					}
					if pods, ok := list.(*corev1.PodList); ok {
						if tc == "read-error" {
							return errors.New("live list failed")
						}
						if tc == "terminating-game" {
							now := metav1.Now()
							pods.Items = append(pods.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns", DeletionTimestamp: &now}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}})
						}
						if tc == "foreign-writer" {
							pods.Items = append(pods.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "ns"}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "server-data"}}}}}})
						}
					}
					if jobs, ok := list.(*batchv1.JobList); ok && tc == "pending-writer-job" {
						jobs.Items = append(jobs.Items, batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "ns"}, Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "server-data"}}}}}}}})
					}
					return nil
				},
			})
			_, _ = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "first"}})
			var job batchv1.Job
			if err := base.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); !apierrors.IsNotFound(err) {
				t.Fatalf("copy admitted without drain proof: %v", err)
			}
			if q.quiesced != 0 {
				t.Fatal("no proven live process to quiesce")
			}
		})
	}
}

func TestBackupLiveTimeoutRetainsOwnerUntilOriginalPodGone(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	q.quiesceErr = errors.New("live agent unreachable")
	q.unquiesceErr = errors.New("live agent still unreachable")
	_, _ = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "first"}})
	first := guardGetBackup(t, r, "first")
	if first.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("timeout did not fail closed: %+v", first.Status)
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); !apierrors.IsNotFound(err) {
		t.Fatal("live timeout created copy")
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	op, _, err := lifecycleOwner(&gs)
	if err != nil || op.QuiescePodUID != "game-pod-uid" || !op.NeedsUnquiesce {
		t.Fatalf("cleanup identity not durable: %+v %v", op, err)
	}
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "second"}, &job); !apierrors.IsNotFound(err) {
		t.Fatal("timeout released the live process owner")
	}
	cleanupAttempts := q.unquiesced
	// The replacement must not receive its predecessor's delayed save-on.
	if err := r.Delete(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns", UID: "replacement"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if q.unquiesced != cleanupAttempts {
		t.Fatal("replacement inherited original process cleanup")
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" {
		t.Fatal("absent original process kept owner forever")
	}
}

func TestBackupOldAgentFailsBeforeCleanupIntent(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	q.checkErr = errors.New("identity endpoint: status 404")
	_, _ = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "first"}})
	b := guardGetBackup(t, r, "first")
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Annotations[annoQuiesceAttempted] != "" || q.quiesced != 0 {
		t.Fatalf("unsupported identity created cleanup debt: backup=%+v agent=%+v", b, q)
	}
	guardPass(t, r, "first")
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" {
		t.Fatal("old agent failure blocked the workload upgrade")
	}
}

func TestBackupDisabledAgentPreservesRawCopyWithoutClaimingQuiesce(t *testing.T) {
	r, _ := guardFixture(t, guardBackup("first"))
	r.AgentClient = &agent.Client{Disabled: true}
	guardPass(t, r, "first")
	b := guardGetBackup(t, r, "first")
	if b.Annotations[backupPreparationAnnotation] != "" || b.Annotations[annoQuiesceAttempted] != "" {
		t.Fatal("disabled agent claimed preparation or quiesce")
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatalf("disabled client prevented raw copy: %v", err)
	}
	if err := callBackupQuiesce(t.Context(), r.AgentClient, "ns", "server", "", true); err == nil {
		t.Fatal("disabled client cleared an existing cleanup obligation")
	}
}

func TestBackupDisabledAgentCannotBypassPendingLiveQuiesce(t *testing.T) {
	r, _ := guardFixture(t, guardBackup("first"))
	b := guardGetBackup(t, r, "first")
	if ok, err := r.claimBackupTarget(t.Context(), b); err != nil || !ok {
		t.Fatalf("claim: %t %v", ok, err)
	}
	gs, err := r.ownedBackupTarget(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := r.prepareBackupSource(t.Context(), b, gs); err != nil || !ready {
		t.Fatalf("prepare: %t %v", ready, err)
	}
	patchBackupAnnotations(b, map[string]string{annoQuiesceAttempted: "pending"})
	if err := r.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	r.AgentClient = &agent.Client{Disabled: true}
	if err := r.maybeQuiesce(t.Context(), b); err == nil || b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("disabled client bypassed pending quiesce: %v %+v", err, b.Status)
	}
}

func TestBackupSourceReplacementFailsBeforeCopyOrSuccess(t *testing.T) {
	for _, duringRPC := range []bool{true, false} {
		t.Run(map[bool]string{true: "during-quiesce", false: "during-copy"}[duringRPC], func(t *testing.T) {
			r, q := guardFixture(t, guardBackup("first"))
			replace := func() {
				var old corev1.Pod
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server-0"}, &old); err != nil {
					t.Fatal(err)
				}
				if err := r.Delete(t.Context(), &old); err != nil {
					t.Fatal(err)
				}
				old.UID, old.ResourceVersion = "replacement", ""
				if err := r.Create(t.Context(), &old); err != nil {
					t.Fatal(err)
				}
			}
			if duringRPC {
				q.afterQuiesce = replace
			} else {
				guardPass(t, r, "first")
				var job batchv1.Job
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); err != nil {
					t.Fatal(err)
				}
				job.Status.Succeeded = 1
				if err := r.Status().Update(t.Context(), &job); err != nil {
					t.Fatal(err)
				}
				replace()
			}
			guardPass(t, r, "first")
			if got := guardGetBackup(t, r, "first"); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Status.SnapshotID != "" {
				t.Fatalf("replacement allowed a restorable copy: %+v", got.Status)
			}
			if duringRPC {
				var job batchv1.Job
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); !apierrors.IsNotFound(err) {
					t.Fatal("replacement during RPC created a copy")
				}
			}
			if q.unquiesced != 0 {
				t.Fatal("replacement received the old process cleanup")
			}
		})
	}
}

func TestBackupCleanupRequiresOriginalProcessExit(t *testing.T) {
	for _, state := range []string{"absent", "replacement", "terminal", "terminating", "unready", "legacy-running", "read-error"} {
		t.Run(state, func(t *testing.T) {
			r, _ := guardFixture(t)
			uid := types.UID("game-pod-uid")
			var pod corev1.Pod
			key := client.ObjectKey{Namespace: "ns", Name: "server-0"}
			if err := r.Get(t.Context(), key, &pod); err != nil {
				t.Fatal(err)
			}
			reader := interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if state == "read-error" {
					return errors.New("apiserver unavailable")
				}
				if state == "absent" {
					return apierrors.NewNotFound(corev1.Resource("pods"), key.Name)
				}
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				p := obj.(*corev1.Pod)
				switch state {
				case "replacement":
					p.UID = "successor"
				case "terminal":
					p.Status.Phase = corev1.PodFailed
				case "terminating":
					now := metav1.Now()
					p.DeletionTimestamp = &now
				case "unready":
					p.Status.Phase = corev1.PodPending
				}
				return nil
			}})
			if state == "legacy-running" {
				uid = ""
			}
			gone, err := backupQuiesceProcessGone(t.Context(), reader, "ns", "server", uid)
			wantGone := state == "absent" || state == "replacement" || state == "terminal"
			if gone != wantGone || (err != nil) != (state == "read-error") {
				t.Fatalf("exit proof = %t, %v; want gone %t", gone, err, wantGone)
			}
		})
	}
}
