package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestLifecycleAllBackupVariantsExcludeRestoreAndWipe(t *testing.T) {
	for _, quiesce := range []bool{false, true} {
		for _, strategy := range []string{"restic-snapshot", "volume-snapshot"} {
			t.Run(fmt.Sprintf("%s/quiesce=%t", strategy, quiesce), func(t *testing.T) {
				b := guardBackup("copy")
				b.Spec.Quiesce, b.Spec.Strategy = quiesce, strategy
				r, _ := guardFixture(t, b)
				if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
					t.Fatalf("claim backup: claimed=%t err=%v", claimed, err)
				}
				var gs gameplanev1alpha1.GameServer
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
					t.Fatal(err)
				}
				rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "ns", UID: "restore-uid"}}
				if err := r.Create(t.Context(), rs); err != nil {
					t.Fatal(err)
				}
				rr := &RestoreReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
				if claimed, err := rr.claimRestoreTarget(t.Context(), rs, &gs); err != nil || claimed {
					t.Fatalf("restore overlapped backup: claimed=%t err=%v", claimed, err)
				}
				gs.Annotations[WipeRequestedAnnotation] = "wipe"
				if err := r.Update(t.Context(), &gs); err != nil {
					t.Fatal(err)
				}
				gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
				if changed, err := gr.ensureWipeGuard(t.Context(), &gs); err != nil || changed || gs.Annotations[wipeGuardAnnotation] != "" {
					t.Fatalf("wipe overlapped backup: changed=%t err=%v", changed, err)
				}
			})
		}
	}
}

func TestLifecycleRestoreExcludesBackupCreatedAfterDrainCheck(t *testing.T) {
	b := guardBackup("copy")
	b.Spec.Quiesce = false
	r, _ := guardFixture(t, b)
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "ns", UID: "restore-uid"}}
	if err := r.Create(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	rr := &RestoreReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	if claimed, err := rr.claimRestoreTarget(t.Context(), rs, &gs); err != nil || !claimed {
		t.Fatalf("claim restore: claimed=%t err=%v", claimed, err)
	}
	if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || claimed {
		t.Fatalf("unquiesced backup entered restore: claimed=%t err=%v", claimed, err)
	}
}

func TestLifecycleUnquiescedCleanupRetainsOwnerUntilWorkerDrains(t *testing.T) {
	b := guardBackup("copy")
	b.Spec.Quiesce = false
	r, q := guardFixture(t, b)
	if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "ns", Labels: map[string]string{backupUIDLabel: string(b.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
	if err := r.Status().Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if res, err := r.runUnquiesce(t.Context(), b); err != nil || res.RequeueAfter == 0 {
		t.Fatalf("cleanup released live worker: result=%+v err=%v", res, err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runUnquiesce(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" || q.unquiesced != 0 {
		t.Fatalf("unquiesced cleanup: owner=%q agent calls=%d", gs.Annotations[lifecycleOperationAnnotation], q.unquiesced)
	}
}

func TestLifecycleBackupFencePreservesReplicasAndTemplate(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "")
	ss := wipeStoppedSet(gs)
	one := int32(1)
	ss.Spec.Replicas = &one
	ss.Spec.Template.Spec.Containers = []corev1.Container{{Name: "game", Image: "before"}}
	b := guardBackup("copy")
	b.Spec.ServerRef.Name = gs.Name
	gs.Annotations[backupGuardAnnotation] = backupIdentityJSON(b.Name, b.UID)
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, b, false)})
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss, b).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	stale := gs.DeepCopy()
	stale.Annotations = nil
	stale.Spec.Suspend = true
	if err := r.reconcileStatefulSet(t.Context(), stale, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 0); err != nil {
		t.Fatal(err)
	}
	var current appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(ss), &current); err != nil {
		t.Fatal(err)
	}
	if *current.Spec.Replicas != 1 || current.Spec.Template.Spec.Containers[0].Image != "before" {
		t.Fatal("stale reconcile stopped or rolled protected source")
	}
	if replicas, delay, err := r.desiredReplicas(t.Context(), stale, &gameplanev1alpha1.GameTemplate{}, idleAsleep); err != nil || replicas != 1 || delay == 0 {
		t.Fatalf("stop/idle bypassed live owner: replicas=%d delay=%s err=%v", replicas, delay, err)
	}
}

func TestLifecycleConcurrentClaimsUseResourceVersion(t *testing.T) {
	b := guardBackup("copy")
	b.Spec.Quiesce = false
	r, _ := guardFixture(t, b)
	intercepted := false
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if gs, ok := obj.(*gameplanev1alpha1.GameServer); ok && !intercepted && gs.Annotations[lifecycleOperationAnnotation] != "" {
				intercepted = true
				var current gameplanev1alpha1.GameServer
				if err := c.Get(ctx, client.ObjectKeyFromObject(gs), &current); err != nil {
					return err
				}
				if current.Annotations == nil {
					current.Annotations = map[string]string{}
				}
				current.Annotations[WipeRequestedAnnotation] = "winner"
				current.Annotations[wipeGuardAnnotation] = "winner"
				returnAfter := c.Update(ctx, &current)
				if returnAfter != nil {
					return returnAfter
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	if claimed, err := r.claimBackupTarget(t.Context(), b); claimed || !apierrors.IsConflict(err) {
		t.Fatalf("lost CAS started backup: claimed=%t err=%v", claimed, err)
	}
	var jobs batchv1.JobList
	if err := r.List(t.Context(), &jobs); err != nil || len(jobs.Items) != 0 {
		t.Fatalf("losing claimant started workers: jobs=%d err=%v", len(jobs.Items), err)
	}
}

func TestLifecycleIdentityRejectsReplacementAndMalformedOwner(t *testing.T) {
	for _, owner := range []string{"broken", lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Kind: "Backup", Name: "other", UID: types.UID("other"), TargetUID: "replaced"}})} {
		t.Run(owner, func(t *testing.T) {
			b := guardBackup("copy")
			r, _ := guardFixture(t, b)
			var gs gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
				t.Fatal(err)
			}
			gs.Annotations = map[string]string{lifecycleOperationAnnotation: owner}
			if err := r.Update(t.Context(), &gs); err != nil {
				t.Fatal(err)
			}
			if claimed, err := r.claimBackupTarget(t.Context(), b); claimed || err == nil {
				t.Fatalf("invalid owner admitted backup: claimed=%t err=%v", claimed, err)
			}
		})
	}
}

func TestLifecycleOrphanBackupRecoveryFencesRestoreUntilDrain(t *testing.T) {
	b := guardBackup("copy")
	b.Spec.Quiesce = false
	r, _ := guardFixture(t, b)
	if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "ns", Labels: map[string]string{backupUIDLabel: string(b.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	forceDeleteGuardBackup(t, r, b.Name)
	rs := lifecycleRestore("restore", "restore-uid")
	if err := r.Create(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns", UID: "server-uid"}}
	rr := &RestoreReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	if claimed, err := rr.claimRestoreTarget(t.Context(), rs, gs); err != nil || claimed {
		t.Fatalf("restore admitted before orphan drained: %t %v", claimed, err)
	}
	gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	if replicas, _, err := gr.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake); err != nil || replicas != 1 {
		t.Fatalf("recovery stopped source while copy worker alive: replicas=%d err=%v", replicas, err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if claimed, err := rr.claimRestoreTarget(t.Context(), rs, gs); err != nil || !claimed {
		t.Fatalf("restore did not recover after drain: %t %v", claimed, err)
	}
}

func TestLifecycleIdleTransitionConflictsWithNewBackupOwner(t *testing.T) {
	b := guardBackup("copy")
	r, _ := guardFixture(t, b)
	var stale gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &stale); err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
	if err := gr.applyIdleTransition(t.Context(), &stale, idleOutcome{sleep: true}, time.Now()); !apierrors.IsConflict(err) {
		t.Fatalf("stale idle transition bypassed ownership CAS: %v", err)
	}
	var current gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(&stale), &current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[IdleAsleepSinceAnnotation] != "" {
		t.Fatal("backup acquisition allowed stale sleep")
	}
}

func TestLifecycleBackupFenceConflictsWithStaleWorkloadWrite(t *testing.T) {
	b := guardBackup("copy")
	r, _ := guardFixture(t, b)
	var stale appsv1.StatefulSet
	key := client.ObjectKey{Namespace: "ns", Name: "server"}
	if err := r.Get(t.Context(), key, &stale); err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
		t.Fatalf("claim: %t %v", claimed, err)
	}
	gs, err := r.ownedBackupTarget(t.Context(), b)
	if err != nil || gs == nil {
		t.Fatalf("owned target: %v", err)
	}
	if ready, err := fenceBackupWorkload(t.Context(), r.Client, r.Client, gs); err != nil || !ready {
		t.Fatalf("fence: %t %v", ready, err)
	}
	zero := int32(0)
	stale.Spec.Replicas = &zero
	stale.Spec.Template.Spec.Containers = []corev1.Container{{Name: "game", Image: "new-revision"}}
	if err := r.Update(t.Context(), &stale); !apierrors.IsConflict(err) {
		t.Fatalf("stale workload write defeated fence: %v", err)
	}
}

func TestLifecycleCSIInitialStartupExcludesBackupAndRollout(t *testing.T) {
	r, gs, rs := restoreLifecycleFixture(t)
	gs.Spec.Suspend = false
	gs.Annotations = map[string]string{restoreGuardAnnotation: restoreIdentityJSON(rs.Name, rs.UID)}
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	want := restoreLifecycleIdentity(gs, rs)
	want.InitialStartup = true
	if ready, err := claimLifecycleOperation(t.Context(), r.Client, r.apiReader(), gs, want, nil); err != nil || !ready {
		t.Fatalf("claim initial startup: %t %v", ready, err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	ss.Spec.Template.Spec.Containers = []corev1.Container{{Name: "game", Image: "snapshot-version"}}
	if err := r.Update(t.Context(), &ss); err != nil {
		t.Fatal(err)
	}
	gr := &GameServerReconciler{Client: r.Client, APIReader: r.apiReader(), Scheme: r.Scheme}
	replicas, _, err := gr.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake)
	if err != nil || replicas != 1 {
		t.Fatalf("CSI initial startup blocked first consumer: replicas=%d err=%v", replicas, err)
	}
	if err := gr.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, replicas); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	if *ss.Spec.Replicas != 1 || ss.Spec.Template.Spec.Containers[0].Image != "snapshot-version" {
		t.Fatal("CSI restore either blocked startup or adopted a different template")
	}
	b := guardBackup("new-copy")
	if err := r.Create(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	br := &BackupReconciler{Client: r.Client, APIReader: r.apiReader(), Scheme: r.Scheme}
	if claimed, err := br.claimBackupTarget(t.Context(), b); err != nil || claimed {
		t.Fatalf("backup entered CSI restore startup: %t %v", claimed, err)
	}
}

func TestLifecycleIndependentServersRemainConcurrent(t *testing.T) {
	first, second := guardBackup("first"), guardBackup("second")
	second.Spec.ServerRef.Name = "other"
	r, _ := guardFixture(t, first, second)
	other := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns", UID: "other-uid"}}
	if err := r.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*gameplanev1alpha1.Backup{first, second} {
		if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
			t.Fatalf("independent target %s blocked: %t %v", b.Spec.ServerRef.Name, claimed, err)
		}
	}
}

type lifecycleStopper struct{ calls int }

func (s *lifecycleStopper) Enabled() bool { return true }
func (s *lifecycleStopper) Stop(context.Context, string, string) error {
	s.calls++
	return nil
}

func TestLifecycleBackupDefersRestartAndStopRPC(t *testing.T) {
	for _, quiesce := range []bool{false, true} {
		t.Run(fmt.Sprintf("quiesce=%t", quiesce), func(t *testing.T) {
			b := guardBackup("copy")
			b.Spec.Quiesce = quiesce
			r, _ := guardFixture(t, b)
			if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
				t.Fatalf("claim: %t %v", claimed, err)
			}
			var gs gameplanev1alpha1.GameServer
			key := client.ObjectKey{Namespace: "ns", Name: "server"}
			if err := r.Get(t.Context(), key, &gs); err != nil {
				t.Fatal(err)
			}
			gs.Annotations[RestartRequestedAnnotation] = "restart-after-copy"
			gs.Spec.Suspend = true
			if err := r.Update(t.Context(), &gs); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), key, &ss); err != nil {
				t.Fatal(err)
			}
			ss.Status.Replicas, ss.Status.ReadyReplicas = 1, 1
			if err := r.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			stopper := &lifecycleStopper{}
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme, AgentClient: stopper}
			tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{RCON: &gameplanev1alpha1.RCONSpec{Protocol: "source"}, Capabilities: &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}}}}
			if replicas, poll, err := gr.desiredReplicas(t.Context(), &gs, tmpl, idleAsleep); err != nil || replicas != 1 || poll == 0 {
				t.Fatalf("restart changed protected source: replicas=%d poll=%s err=%v", replicas, poll, err)
			}
			if err := r.Get(t.Context(), key, &gs); err != nil {
				t.Fatal(err)
			}
			if stopper.calls != 0 || gs.Annotations[RestartCompletedAnnotation] != "" || gs.Annotations[stopRequestedAtAnnotation] != "" {
				t.Fatal("restart issued or acknowledged a stop during the copy")
			}
		})
	}
}
