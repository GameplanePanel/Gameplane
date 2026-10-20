package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func restoreLifecycleFixture(t *testing.T, extras ...client.Object) (*RestoreReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.Restore) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, batchv1.AddToScheme, gameplanev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns", UID: "server-uid"},
		Spec:       gameplanev1alpha1.GameServerSpec{Suspend: true},
		Status:     gameplanev1alpha1.GameServerStatus{Phase: gameplanev1alpha1.GameServerPhaseSuspended},
	}
	rs := lifecycleRestore("first", "restore-first")
	backup := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "ns"},
		Spec:       gameplanev1alpha1.BackupSpec{RepoRef: &gameplanev1alpha1.SecretKeySelector{Name: "repo"}},
		Status:     gameplanev1alpha1.BackupStatus{Phase: gameplanev1alpha1.BackupPhaseSucceeded, SnapshotID: "snapshot"},
	}
	objects := append([]client.Object{gs, rs, backup}, extras...)
	// Model the workload controller's durable, observed scale-to-zero fence.
	for _, obj := range append([]client.Object(nil), objects...) {
		server, ok := obj.(*gameplanev1alpha1.GameServer)
		if !ok {
			continue
		}
		found := false
		for _, candidate := range extras {
			if ss, ok := candidate.(*appsv1.StatefulSet); ok && ss.Name == server.Name {
				found = true
			}
		}
		if !found {
			zero := int32(0)
			objects = append(objects, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: server.Name, Namespace: server.Namespace, UID: types.UID(server.Name + "-ss"), OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: server.Name, UID: server.UID, Controller: ownerBoolPtr(true)}}}, Spec: appsv1.StatefulSetSpec{Replicas: &zero}})
		}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}, &gameplanev1alpha1.GameServer{}, &batchv1.Job{}, &appsv1.StatefulSet{}).Build()
	return &RestoreReconciler{Client: cl, APIReader: cl, Scheme: scheme}, gs, rs
}

func lifecycleRestore(name string, uid types.UID) *gameplanev1alpha1.Restore {
	return &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: uid},
		Spec:       gameplanev1alpha1.RestoreSpec{ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "server"}, BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "backup"}},
		Status:     gameplanev1alpha1.RestoreStatus{Phase: gameplanev1alpha1.RestorePhaseSuspending, SnapshotID: "snapshot"},
	}
}

func lifecyclePasses(t *testing.T, r *RestoreReconciler, name string, count int) {
	t.Helper()
	for range count {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
}

func lifecycleServer(t *testing.T, r *RestoreReconciler) *gameplanev1alpha1.GameServer {
	t.Helper()
	gs := &gameplanev1alpha1.GameServer{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "server"}, gs); err != nil {
		t.Fatal(err)
	}
	return gs
}

func lifecycleJobs(t *testing.T, r *RestoreReconciler) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs, client.InNamespace("ns")); err != nil {
		t.Fatal(err)
	}
	return jobs.Items
}

func TestRestoreWaitsForObservedScaleDownAndPodTermination(t *testing.T) {
	for _, state := range []string{"missing-statefulset", "desired-one", "observed-one", "unobserved-generation", "terminating-pod", "orphan-game-pod", "pending-writer-job"} {
		t.Run(state, func(t *testing.T) {
			zero := int32(0)
			ss := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns", UID: "ss-uid", Generation: 2, OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: "server", UID: "server-uid", Controller: ownerBoolPtr(true)}}},
				Spec:       appsv1.StatefulSetSpec{Replicas: &zero},
				Status:     appsv1.StatefulSetStatus{ObservedGeneration: 2},
			}
			var extras []client.Object
			switch state {
			case "desired-one":
				one := int32(1)
				ss.Spec.Replicas = &one
			case "observed-one":
				ss.Status.Replicas = 1 // ReadyReplicas is zero: phase can already report Suspended.
			case "unobserved-generation":
				ss.Status.ObservedGeneration = 1
			case "pending-writer-job":
				extras = append(extras, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "server-wipe", Namespace: "ns"}, Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "server-data"}}}}}}}})
			case "terminating-pod", "orphan-game-pod":
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: "ns", UID: "pod-uid", OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: ss.Name, UID: ss.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if state == "terminating-pod" {
					now := metav1.Now()
					pod.DeletionTimestamp = &now
					pod.Finalizers = []string{"test/hold"}
				}
				extras = append(extras, pod)
			}
			if state != "orphan-game-pod" {
				extras = append(extras, ss)
			}
			r, _, _ := restoreLifecycleFixture(t, extras...)
			if state == "missing-statefulset" || state == "orphan-game-pod" {
				if err := r.Delete(context.Background(), ss); err != nil {
					t.Fatal(err)
				}
			}
			lifecyclePasses(t, r, "first", 8)
			for _, job := range lifecycleJobs(t, r) {
				if job.Name == "restore-first" {
					t.Fatal("destructive restore Job created before workload fully stopped")
				}
			}
			var rs gameplanev1alpha1.Restore
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Phase != gameplanev1alpha1.RestorePhaseSuspending {
				t.Fatalf("phase=%s, want Suspending", rs.Status.Phase)
			}
		})
	}
}

func TestRestoreWipeIgnoresStalePendingRequest(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprintf("guarded-%t", guarded), func(t *testing.T) {
			r, _, _ := restoreLifecycleFixture(t)
			gs := lifecycleServer(t, r)
			gs.Annotations = map[string]string{WipeRequestedAnnotation: "wipe", WipeCompletedAnnotation: "wipe"}
			if guarded {
				gs.Annotations[restoreGuardAnnotation] = restoreIdentityJSON("first", "restore-first")
			}
			if err := r.Update(context.Background(), gs); err != nil {
				t.Fatal(err)
			}
			stale := gs.DeepCopy()
			delete(stale.Annotations, WipeCompletedAnnotation)
			delete(stale.Annotations, restoreGuardAnnotation)
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
			if err := gr.reconcileWipe(context.Background(), stale, &gameplanev1alpha1.GameTemplate{}); err != nil {
				t.Fatal(err)
			}
			if len(lifecycleJobs(t, r)) != 0 {
				t.Fatal("stale wipe request created a destructive Job")
			}
		})
	}
}

func TestRestoresSerializeAndDoNotResumeWhileJobPodIsLive(t *testing.T) {
	r, _, _ := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	lifecyclePasses(t, r, "first", 8)
	lifecyclePasses(t, r, "second", 8)
	jobs := lifecycleJobs(t, r)
	if len(jobs) != 1 || jobs[0].Name != "restore-first" {
		t.Fatalf("overlapping restore Jobs: %v", jobs)
	}
	job := &jobs[0]
	job.UID = "job-first"
	if err := r.Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "restore-worker", Namespace: "ns", Labels: job.Spec.Template.Labels, OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 4)
	lifecyclePasses(t, r, "second", 4)
	if !lifecycleServer(t, r).Spec.Suspend || len(lifecycleJobs(t, r)) != 1 {
		t.Fatal("live restore worker lost exclusive access")
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 6)
	if lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("successful restore did not resume its target")
	}
	lifecyclePasses(t, r, "second", 8)
	if len(lifecycleJobs(t, r)) != 2 || !lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("waiting restore did not acquire exclusive access after completion")
	}
}

func TestRestoreCompletionStatusConflictKeepsGuard(t *testing.T) {
	r, _, _ := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	lifecyclePasses(t, r, "first", 8)
	job := lifecycleJobs(t, r)[0]
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if rs, ok := obj.(*gameplanev1alpha1.Restore); ok && rs.Status.Phase == gameplanev1alpha1.RestorePhaseSucceeded {
			return apierrors.NewConflict(gameplanev1alpha1.GroupVersion.WithResource("restores").GroupResource(), rs.Name, fmt.Errorf("simulated interrupted status write"))
		}
		return c.SubResource(subResource).Update(ctx, obj, opts...)
	}})
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "first"}}); !apierrors.IsConflict(err) {
		t.Fatalf("expected completion write conflict, got %v", err)
	}
	if !lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("target resumed before completion was persisted")
	}
	lifecyclePasses(t, r, "second", 3)
	if len(lifecycleJobs(t, r)) != 1 {
		t.Fatal("status conflict lost exclusive access")
	}
	r.Client = r.APIReader.(client.Client)
	lifecyclePasses(t, r, "first", 5)
	if lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("restarted reconciler failed to finish committed Job")
	}
}

func TestRestoreLegacyJobIsCanceledBeforeGuardRelease(t *testing.T) {
	r, _, rs := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseRunning
	if err := r.Status().Update(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "restore-first", Namespace: "ns", UID: "legacy-job", OwnerReferences: []metav1.OwnerReference{{Kind: "Restore", Name: rs.Name, UID: rs.UID, Controller: ownerBoolPtr(true)}}}}
	if err := r.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	// Legacy workers lack the new UID label. Their Job owner must still
	// keep the guard while foreground cancellation drains the pod.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy-worker", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// The fake client has no garbage collector: hold the Job during its
	// foreground deletion, as Kubernetes does until its pods are gone.
	job.Finalizers = []string{"foregroundDeletion"}
	if err := r.Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 5)
	lifecyclePasses(t, r, "second", 5)
	gs := lifecycleServer(t, r)
	if !gs.Spec.Suspend || gs.Annotations[restoreGuardAnnotation] != restoreIdentityJSON("first", "restore-first") {
		t.Fatal("legacy worker lost the guard during cancellation")
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	if rs.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
		t.Fatal("unbound legacy Job was accepted as a safe restore")
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(job), job); err != nil {
		t.Fatal(err)
	}
	job.Finalizers = nil
	if err := r.Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 5)
	lifecyclePasses(t, r, "second", 8)
	jobs := lifecycleJobs(t, r)
	if len(jobs) != 1 || jobs[0].Name != "restore-second" {
		t.Fatal("waiting restore could not proceed after legacy cancellation")
	}
}

func TestRestoreDeletionDrainsWorkersBeforeReleasingGuard(t *testing.T) {
	r, _, rs := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	lifecyclePasses(t, r, "first", 8)
	jobs := lifecycleJobs(t, r)
	job := jobs[0]
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "ns", Labels: job.Spec.Template.Labels, OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	if len(rs.Finalizers) == 0 {
		t.Fatal("restore has no cleanup finalizer")
	}
	if err := r.Delete(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 4)
	lifecyclePasses(t, r, "second", 4)
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatalf("restore deleted while its pod was live: %v", err)
	}
	if !lifecycleServer(t, r).Spec.Suspend || len(lifecycleJobs(t, r)) != 0 {
		t.Fatal("deletion failed to cancel Job or released guard too soon")
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 4)
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); !apierrors.IsNotFound(err) {
		t.Fatalf("finalizer not removed after workers stopped: %v", err)
	}
	if !lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("deleted restore resumed potentially partial data")
	}
	lifecyclePasses(t, r, "second", 8)
	if len(lifecycleJobs(t, r)) != 1 {
		t.Fatal("waiting restore stayed blocked after cleanup")
	}
}

func TestRestoreTargetRecreationCannotInheritRestore(t *testing.T) {
	r, gs, _ := restoreLifecycleFixture(t)
	lifecyclePasses(t, r, "first", 8)
	if err := r.Delete(context.Background(), gs); err != nil {
		t.Fatal(err)
	}
	gs.ResourceVersion = ""
	gs.UID = "replacement-uid"
	gs.Spec.Suspend = false
	gs.Annotations = nil
	if err := r.Create(context.Background(), gs); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 8)
	if lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("old restore modified recreated target")
	}
	var rs gameplanev1alpha1.Restore
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &rs); err != nil {
		t.Fatal(err)
	}
	if rs.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
		t.Fatalf("recreated target did not fail old restore: %s", rs.Status.Phase)
	}
}

func TestRestoreAcquisitionUsesOptimisticConcurrency(t *testing.T) {
	r, _, _ := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	// The first pass persists identity/finalization before any target write.
	lifecyclePasses(t, r, "first", 1)
	lifecyclePasses(t, r, "second", 1)
	var barrier sync.WaitGroup
	barrier.Add(2)
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if _, ok := obj.(*gameplanev1alpha1.GameServer); ok {
			barrier.Done()
			barrier.Wait()
		}
		return c.Update(ctx, obj, opts...)
	}})
	var workers sync.WaitGroup
	var results [2]error
	for i, name := range []string{"first", "second"} {
		workers.Go(func() {
			_, results[i] = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}})
		})
	}
	workers.Wait()
	conflicts := 0
	for _, err := range results {
		if apierrors.IsConflict(err) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("competing claims must produce one conflict, got %v", results)
	}
	// Drop the barrier and prove only the winning owner creates a Job.
	r.Client = r.APIReader.(client.Client)
	lifecyclePasses(t, r, "first", 6)
	lifecyclePasses(t, r, "second", 6)
	if len(lifecycleJobs(t, r)) != 1 {
		t.Fatal("optimistic claim allowed overlapping Jobs")
	}
}

func TestRestoreFailureRetainsSuspensionAndReleasesGuard(t *testing.T) {
	r, _, _ := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	lifecyclePasses(t, r, "first", 8)
	jobs := lifecycleJobs(t, r)
	jobs[0].Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
	if err := r.Status().Update(context.Background(), &jobs[0]); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 6)
	if !lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("failed restore resumed target")
	}
	lifecyclePasses(t, r, "second", 8)
	if len(lifecycleJobs(t, r)) != 2 {
		t.Fatal("terminal failure leaked target lock")
	}
}

func TestRestoreGuardOverridesResumeRequest(t *testing.T) {
	r, _, _ := restoreLifecycleFixture(t)
	lifecyclePasses(t, r, "first", 8)
	gs := lifecycleServer(t, r)
	gs.Spec.Suspend = false
	if err := r.Update(context.Background(), gs); err != nil {
		t.Fatal(err)
	}
	gr := &GameServerReconciler{Client: r.Client, Scheme: r.Scheme}
	replicas, _, err := gr.desiredReplicas(context.Background(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake)
	if err != nil || replicas != 0 {
		t.Fatalf("restore guard allowed resume: replicas=%d error=%v", replicas, err)
	}
}

func TestRestoreGuardFencesStaleStatefulSetScaleUp(t *testing.T) {
	zero := int32(0)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns", UID: "ss-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: "server", UID: "server-uid", Controller: ownerBoolPtr(true)}}}, Spec: appsv1.StatefulSetSpec{Replicas: &zero}}
	r, staleGS, _ := restoreLifecycleFixture(t, ss)
	lifecyclePasses(t, r, "first", 8)
	// This snapshot deliberately predates acquisition of the target guard.
	staleGS.Spec.Suspend = false
	gr := &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
	if err := gr.reconcileStatefulSet(context.Background(), staleGS, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var after appsv1.StatefulSet
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(ss), &after); err != nil {
		t.Fatal(err)
	}
	if after.Spec.Replicas == nil || *after.Spec.Replicas != 0 {
		t.Fatal("stale GameServer reconciliation scaled up during restore")
	}
	job := lifecycleJobs(t, r)[0]
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 4)
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(ss), &after); err != nil {
		t.Fatal(err)
	}
	if after.Annotations[restoreGuardAnnotation] != "" {
		t.Fatal("completion leaked StatefulSet guard")
	}
}

func TestRestoreStaleOwnerRecoveryWaitsForOrphanWorker(t *testing.T) {
	r, _, rs := restoreLifecycleFixture(t, lifecycleRestore("second", "restore-second"))
	lifecyclePasses(t, r, "first", 8)
	job := lifecycleJobs(t, r)[0]
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan-worker", Namespace: "ns", Labels: job.Spec.Template.Labels, OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	rs.Finalizers = nil // Simulate a force-deleted owner, then recreate its name.
	if err := r.Update(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	replacement := lifecycleRestore("first", "replacement-restore")
	if err := r.Create(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "second", 5)
	if len(lifecycleJobs(t, r)) != 0 {
		t.Fatal("orphan Job was not canceled before takeover")
	}
	if !lifecycleServer(t, r).Spec.Suspend {
		t.Fatal("orphan worker lost suspension guard")
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "second", 8)
	jobs := lifecycleJobs(t, r)
	if len(jobs) != 1 || jobs[0].Name != "restore-second" {
		t.Fatalf("stale lock was not reclaimed after orphan drained: %v", jobs)
	}
}

func TestRestoreDifferentTargetsDoNotBlockEachOther(t *testing.T) {
	second := lifecycleRestore("second", "restore-second")
	second.Spec.ServerRef.Name = "other"
	other := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns", UID: "other-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Suspend: true}, Status: gameplanev1alpha1.GameServerStatus{Phase: gameplanev1alpha1.GameServerPhaseSuspended}}
	r, _, _ := restoreLifecycleFixture(t, second, other)
	lifecyclePasses(t, r, "first", 8)
	lifecyclePasses(t, r, "second", 8)
	if got := len(lifecycleJobs(t, r)); got != 2 {
		t.Fatalf("independent targets blocked: %d Jobs", got)
	}
}

func TestRestoreJobUsesConfiguredLimits(t *testing.T) {
	for _, backoff := range []int32{0, 5} {
		t.Run(fmt.Sprintf("backoff-%d", backoff), func(t *testing.T) {
			r, _, _ := restoreLifecycleFixture(t)
			r.JobBackoffLimit = &backoff
			r.JobActiveDeadlineSeconds = 3600
			lifecyclePasses(t, r, "first", 8)
			jobs := lifecycleJobs(t, r)
			if len(jobs) != 1 {
				t.Fatalf("got %d Jobs, want one", len(jobs))
			}
			job := jobs[0]
			if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != backoff {
				t.Fatalf("Job did not preserve configured backoff %d: %v", backoff, job.Spec.BackoffLimit)
			}
			if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 3600 {
				t.Fatalf("Job did not preserve configured deadline: %v", job.Spec.ActiveDeadlineSeconds)
			}
		})
	}
}
