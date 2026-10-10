package controller

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/ValgulNecron/gameplane/operator/api/v1alpha1"
)

func wipeStoppedSet(gs *gameplanev1alpha1.GameServer) *appsv1.StatefulSet {
	zero := int32(0)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload", Generation: 2,
			Annotations:     map[string]string{wipeGuardAnnotation: gs.Annotations[wipeGuardAnnotation]},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: gs.Name, UID: gs.UID, Controller: ownerBoolPtr(true)}}},
		Spec:   appsv1.StatefulSetSpec{Replicas: &zero},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 2},
	}
}

func wipePasses(t *testing.T, r *GameServerReconciler, gs *gameplanev1alpha1.GameServer, count int) {
	t.Helper()
	for range count {
		if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}); err != nil {
			t.Fatal(err)
		}
	}
}

func wipeCurrent(t *testing.T, c client.Client, gs *gameplanev1alpha1.GameServer) *gameplanev1alpha1.GameServer {
	t.Helper()
	var current gameplanev1alpha1.GameServer
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &current); err != nil {
		t.Fatal(err)
	}
	return &current
}

func TestWipePendingStartAcquiresGuardBeforeLaunching(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "current")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs)).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 1)
	current := wipeCurrent(t, c, gs)
	if current.Annotations[wipeGuardAnnotation] != "current" || !current.Spec.Suspend {
		t.Fatal("direct start bypassed durable wipe acquisition")
	}
	var job batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); !apierrors.IsNotFound(err) {
		t.Fatal("worker launched in guard acquisition pass")
	}
	wipePasses(t, r, gs, 2)
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); err != nil {
		t.Fatal(err)
	}
	if job.Spec.Template.Labels[wipeUIDLabel] != string(gs.UID) || job.Spec.Template.Labels[wipeTokenLabel] != "current" {
		t.Fatal("worker pod lacks durable identity labels")
	}
}

func TestWipeCancellationAndReplacementWaitForOrphanWorkers(t *testing.T) {
	for _, request := range []string{"", "next"} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("request=%q/legacy=%t", request, legacy), func(t *testing.T) {
				s := wipeScheme(t)
				gs := wipeGameServer(false, request)
				gs.Annotations[wipeGuardAnnotation] = "old"
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "worker", Labels: map[string]string{wipeTokenLabel: "old"}}}
				if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
					t.Fatal(err)
				}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-pod", Namespace: "ns", Finalizers: []string{"test/drain"},
					Labels:          map[string]string{wipeUIDLabel: string(gs.UID), wipeTokenLabel: "old"},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: ownerBoolPtr(true)}}},
					Spec:   corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "alpha-data"}}}}},
					Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
				if legacy {
					pod.Labels = nil
				}
				foreground := false
				c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs), job, pod).WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*batchv1.Job); ok {
							o := &client.DeleteOptions{}
							o.ApplyOptions(opts)
							foreground = o.PropagationPolicy != nil && *o.PropagationPolicy == metav1.DeletePropagationForeground && o.Preconditions != nil && o.Preconditions.UID != nil && *o.Preconditions.UID == job.UID
						}
						return c.Delete(ctx, obj, opts...)
					},
				}).Build()
				r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
				if err := c.Delete(t.Context(), pod); err != nil {
					t.Fatal(err)
				}
				wipePasses(t, r, gs, 3)
				current := wipeCurrent(t, c, gs)
				if !foreground || current.Annotations[wipeGuardAnnotation] != "old" || current.Annotations[WipeCompletedAnnotation] != "" {
					t.Fatal("old worker was released or acknowledged before termination")
				}
				replicas, poll, err := r.desiredReplicas(t.Context(), current, &gameplanev1alpha1.GameTemplate{}, idleAwake)
				if err != nil || replicas != 0 || poll == 0 {
					t.Fatalf("start during drain: replicas=%d poll=%s err=%v", replicas, poll, err)
				}
				var remaining corev1.Pod
				if err := c.Get(t.Context(), client.ObjectKeyFromObject(pod), &remaining); err != nil {
					t.Fatal(err)
				}
				remaining.Finalizers = nil
				if err := c.Update(t.Context(), &remaining); err != nil {
					t.Fatal(err)
				}
				wipePasses(t, r, gs, 1)
				current = wipeCurrent(t, c, gs)
				if current.Annotations[wipeGuardAnnotation] != request || current.Annotations[WipeCompletedAnnotation] != "" {
					t.Fatal("drained worker did not release/hand off safely")
				}
				if request == "" && current.Spec.Suspend {
					t.Fatal("cancellation overwrote current start intent")
				}
			})
		}
	}
}

func TestWipeSuccessWaitsForWorkerAndChecksCurrentRequest(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "old")
	gs.Annotations[wipeGuardAnnotation] = "old"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "worker", Labels: map[string]string{wipeTokenLabel: "old"}}, Status: batchv1.JobStatus{Succeeded: 1}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-pod", Namespace: "ns", Labels: map[string]string{wipeUIDLabel: string(gs.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs), job, pod).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 1)
	if wipeCurrent(t, c, gs).Annotations[WipeCompletedAnnotation] != "" {
		t.Fatal("success acked while worker live")
	}
	if err := c.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	wipePasses(t, r, gs, 1) // Delete the successful Job after durable proof.
	r.Client = interceptor.NewClient(c, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if update, ok := obj.(*gameplanev1alpha1.GameServer); ok && update.Annotations[WipeCompletedAnnotation] == "old" {
				var current gameplanev1alpha1.GameServer
				if err := c.Get(ctx, client.ObjectKeyFromObject(gs), &current); err != nil {
					return err
				}
				current.Annotations[WipeRequestedAnnotation] = "next"
				if err := c.Update(ctx, &current); err != nil {
					return err
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}); !apierrors.IsConflict(err) {
		t.Fatalf("ack race error=%v, want conflict", err)
	}
	current := wipeCurrent(t, c, gs)
	if current.Annotations[WipeRequestedAnnotation] != "next" || current.Annotations[WipeCompletedAnnotation] != "" || current.Annotations[wipeGuardAnnotation] != "old" {
		t.Fatal("old success overwrote replacement request")
	}
}

func TestWipeRecoversGuardBeforeLegacyWorkerCanStartGame(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "")
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "worker", Labels: map[string]string{wipeTokenLabel: "old"}}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	if changed, err := r.ensureWipeGuard(t.Context(), gs); err != nil || !changed {
		t.Fatalf("legacy writer exclusion recovery: changed=%t err=%v", changed, err)
	}
	replicas, poll, err := r.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake)
	if err != nil || replicas != 0 || poll == 0 {
		t.Fatalf("legacy writer allowed start: replicas=%d poll=%s err=%v", replicas, poll, err)
	}
	if wipeCurrent(t, c, gs).Annotations[wipeGuardAnnotation] != "old" {
		t.Fatal("legacy writer exclusion was not durable")
	}
}

func TestWipeStatefulSetHonorsLiveGuardAgainstStaleStart(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			s := wipeScheme(t)
			stale := wipeGameServer(false, "")
			live := stale.DeepCopy()
			live.Annotations[wipeGuardAnnotation] = "wipe"
			objects := []client.Object{live}
			if existing {
				objects = append(objects, wipeStoppedSet(stale))
			}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
			if err := r.reconcileStatefulSet(t.Context(), stale, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(stale), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Annotations[wipeGuardAnnotation] != "wipe" {
				t.Fatal("stale workload callback restarted guarded server")
			}
		})
	}
}

func TestRestoreCannotClaimCanceledWipeWithGuardOrOrphan(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprintf("guarded=%t", guarded), func(t *testing.T) {
			r, _, rs := restoreLifecycleFixture(t)
			gs := lifecycleServer(t, r)
			if gs.Annotations == nil {
				gs.Annotations = map[string]string{}
			}
			if guarded {
				gs.Annotations[wipeGuardAnnotation] = "canceled"
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if !guarded {
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan-wipe", Namespace: gs.Namespace, Labels: map[string]string{wipeUIDLabel: string(gs.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if err := r.Create(t.Context(), pod); err != nil {
					t.Fatal(err)
				}
			}
			lifecyclePasses(t, r, "first", 4)
			if lifecycleServer(t, r).Annotations[restoreGuardAnnotation] != "" {
				t.Fatal("restore claimed canceled wipe before drain")
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Message == "" {
				t.Fatal("wipe exclusion wait was not reported")
			}
		})
	}
}

func TestWipeLegacyPodSurvivesSameNameJobReplacement(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign=%t", foreign), func(t *testing.T) {
			s := wipeScheme(t)
			gs := wipeGameServer(false, "")
			gs.Annotations[wipeGuardAnnotation] = "old"
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "replacement", Labels: map[string]string{wipeTokenLabel: "replacement"}}}
			if !foreign {
				if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
					t.Fatal(err)
				}
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy-worker", Namespace: "ns",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: "old-job", Controller: ownerBoolPtr(true)}}},
				Spec:   corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "alpha-data"}}}}},
				Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job, pod).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
			wipePasses(t, r, gs, 3)
			if wipeCurrent(t, c, gs).Annotations[wipeGuardAnnotation] != "old" {
				t.Fatal("replacement hid surviving legacy worker")
			}
			var remaining batchv1.Job
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &remaining); foreign && (err != nil || remaining.UID != job.UID) {
				t.Fatal("foreign replacement Job was removed")
			}
		})
	}
}

func TestWipeFailureRetainsGuardSuspensionAndLogs(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "failed")
	gs.Annotations[wipeGuardAnnotation] = "failed"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "failed-worker", Labels: map[string]string{wipeTokenLabel: "failed"}},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job).WithStatusSubresource(gs).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 2)
	current := wipeCurrent(t, c, gs)
	if !current.Spec.Suspend || current.Annotations[wipeGuardAnnotation] != "failed" || current.Annotations[WipeCompletedAnnotation] != "" {
		t.Fatal("failed wipe was acknowledged or resumed")
	}
	conditionFound := false
	for _, condition := range current.Status.Conditions {
		if condition.Type == gameplanev1alpha1.GameServerConditionDataWipe && condition.Status == metav1.ConditionFalse && condition.Reason == "JobFailed" {
			conditionFound = true
		}
	}
	if !conditionFound {
		t.Fatal("failed wipe did not report DataWipe failure")
	}
	var retained batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &retained); err != nil || retained.UID != job.UID {
		t.Fatal("failed Job/logs were removed")
	}
}

func TestWipeStatefulSetFenceConflictsWithInFlightStaleUpdate(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "")
	ss := wipeStoppedSet(gs)
	intercepted := false
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if workload, ok := obj.(*appsv1.StatefulSet); ok && workload.Spec.Replicas != nil && *workload.Spec.Replicas == 1 && !intercepted {
				intercepted = true
				var current gameplanev1alpha1.GameServer
				if err := c.Get(ctx, client.ObjectKeyFromObject(gs), &current); err != nil {
					return err
				}
				current.Annotations[WipeRequestedAnnotation] = "wipe"
				current.Annotations[wipeGuardAnnotation] = "wipe"
				current.Spec.Suspend = true
				if err := c.Update(ctx, &current); err != nil {
					return err
				}
				var fenced appsv1.StatefulSet
				if err := c.Get(ctx, client.ObjectKeyFromObject(ss), &fenced); err != nil {
					return err
				}
				fenced.Annotations[wipeGuardAnnotation] = "wipe"
				if err := c.Update(ctx, &fenced); err != nil {
					return err
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	}).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); !apierrors.IsConflict(err) {
		t.Fatalf("stale StatefulSet write err=%v, want conflict", err)
	}
	var remaining appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(ss), &remaining); err != nil {
		t.Fatal(err)
	}
	if remaining.Annotations[wipeGuardAnnotation] != "wipe" || *remaining.Spec.Replicas != 0 {
		t.Fatal("stale update defeated wipe fence")
	}
}

func TestWipeInterruptedReleaseDoesNotLeaveWorkloadStoppedForever(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "finished")
	gs.Annotations[WipeCompletedAnnotation] = "finished"
	ss := wipeStoppedSet(gs)
	ss.Annotations[wipeGuardAnnotation] = "finished"
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var current appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(ss), &current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[wipeGuardAnnotation] != "" || *current.Spec.Replicas != 1 {
		t.Fatal("interrupted release marker prevented successful resume")
	}
}

func TestWipeSuccessForegroundDrainSurvivesRestartBeforeAck(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "success")
	gs.Annotations[wipeGuardAnnotation] = "success"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "worker", Finalizers: []string{"test/drain"}, Labels: map[string]string{wipeTokenLabel: "success"}}, Status: batchv1.JobStatus{Succeeded: 1}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-pod", Namespace: "ns", Finalizers: []string{"test/drain"}, Labels: map[string]string{wipeUIDLabel: string(gs.UID), wipeTokenLabel: "success"}}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs), job, pod).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 2) // Persist success, then foreground-delete its Job.
	current := wipeCurrent(t, c, gs)
	if current.Annotations[wipeSuccessAnnotation] != "success" || current.Annotations[WipeCompletedAnnotation] != "" || !current.Spec.Suspend {
		t.Fatal("success was acknowledged before foreground drain")
	}
	if err := c.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	var deletingJob batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &deletingJob); err != nil {
		t.Fatal(err)
	}
	if deletingJob.DeletionTimestamp.IsZero() {
		t.Fatal("foreground Job was not retained during drain")
	}
	deletingJob.Finalizers = nil
	if err := c.Update(t.Context(), &deletingJob); err != nil {
		t.Fatal(err)
	}
	// Restart the reconciler with the Job now absent and a terminating terminal
	// pod present. Durable success must prevent a replacement destructive Job.
	r = &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 2)
	if wipeCurrent(t, c, gs).Annotations[WipeCompletedAnnotation] != "" {
		t.Fatal("terminating success worker was acknowledged")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &deletingJob); !apierrors.IsNotFound(err) {
		t.Fatal("success proof was lost and worker was recreated")
	}
	var deletingPod corev1.Pod
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(pod), &deletingPod); err != nil {
		t.Fatal(err)
	}
	deletingPod.Finalizers = nil
	if err := c.Update(t.Context(), &deletingPod); err != nil {
		t.Fatal(err)
	}
	wipePasses(t, r, gs, 1)
	current = wipeCurrent(t, c, gs)
	if current.Spec.Suspend || current.Annotations[WipeCompletedAnnotation] != "success" || current.Annotations[wipeGuardAnnotation] != "" || current.Annotations[wipeSuccessAnnotation] != "" {
		t.Fatal("drained successful wipe did not acknowledge and resume atomically")
	}
}

func TestWipeAcknowledgedForegroundCleanupPreservesResumeIntent(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "done")
	gs.Annotations[WipeCompletedAnnotation] = "done"
	deleting := metav1.Now()
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alpha-wipe", Namespace: "ns", UID: "worker", DeletionTimestamp: &deleting, Finalizers: []string{"test/drain"}, Labels: map[string]string{wipeTokenLabel: "done"}}, Status: batchv1.JobStatus{Succeeded: 1}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-pod", Namespace: "ns", DeletionTimestamp: &deleting, Finalizers: []string{"test/drain"}, Labels: map[string]string{wipeUIDLabel: string(gs.UID), wipeTokenLabel: "done"}}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job, pod).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	wipePasses(t, r, gs, 2)
	current := wipeCurrent(t, c, gs)
	replicas, _, err := r.desiredReplicas(t.Context(), current, &gameplanev1alpha1.GameTemplate{}, idleAwake)
	if err != nil || replicas != 0 || current.Annotations[wipeGuardAnnotation] != "done" || current.Spec.Suspend {
		t.Fatal("acknowledged cleanup lost resume intent or allowed early start")
	}
	var drainingJob batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &drainingJob); err != nil {
		t.Fatal(err)
	}
	drainingJob.Finalizers = nil
	if err := c.Update(t.Context(), &drainingJob); err != nil {
		t.Fatal(err)
	}
	var drainingPod corev1.Pod
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(pod), &drainingPod); err != nil {
		t.Fatal(err)
	}
	drainingPod.Finalizers = nil
	if err := c.Update(t.Context(), &drainingPod); err != nil {
		t.Fatal(err)
	}
	wipePasses(t, r, gs, 1)
	current = wipeCurrent(t, c, gs)
	if current.Spec.Suspend || current.Annotations[wipeGuardAnnotation] != "" || current.Annotations[WipeCompletedAnnotation] != "done" {
		t.Fatal("foreground cleanup stranded acknowledged success suspended")
	}
}

func TestWipeAcknowledgedLegacyOrphanPreservesPowerIntent(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		t.Run(fmt.Sprintf("suspended=%t", suspended), func(t *testing.T) {
			s := wipeScheme(t)
			gs := wipeGameServer(suspended, "done")
			gs.Annotations[WipeCompletedAnnotation] = "done"
			deleting := metav1.Now()
			// The old implementation did not label PodTemplate workers. Its
			// Job is already gone, so neither server UID nor token is available.
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legacy-worker", Namespace: gs.Namespace, DeletionTimestamp: &deleting, Finalizers: []string{"test/drain"},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: gs.Name + "-wipe", UID: "gone-job", Controller: ownerBoolPtr(true)}}},
				Spec:   corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-data"}}}}},
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs), pod).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
			wipePasses(t, r, gs, 2)
			current := wipeCurrent(t, c, gs)
			replicas, poll, err := r.desiredReplicas(t.Context(), current, &gameplanev1alpha1.GameTemplate{}, idleAwake)
			if err != nil || replicas != 0 || poll == 0 || current.Annotations[wipeGuardAnnotation] != wipeOrphanGuard || current.Spec.Suspend != suspended {
				t.Fatalf("legacy orphan drain lost power intent or exclusion: replicas=%d poll=%s suspended=%t guard=%q err=%v", replicas, poll, current.Spec.Suspend, current.Annotations[wipeGuardAnnotation], err)
			}
			// Even a stale start callback must keep the live orphan fence.
			if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Annotations[wipeGuardAnnotation] != wipeOrphanGuard {
				t.Fatal("legacy orphan allowed premature workload resume")
			}
			var remaining corev1.Pod
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(pod), &remaining); err != nil {
				t.Fatal(err)
			}
			remaining.Finalizers = nil
			if err := c.Update(t.Context(), &remaining); err != nil {
				t.Fatal(err)
			}
			wipePasses(t, r, gs, 1)
			current = wipeCurrent(t, c, gs)
			if current.Spec.Suspend != suspended || current.Annotations[wipeGuardAnnotation] != "" || current.Annotations[WipeCompletedAnnotation] != "done" {
				t.Fatal("drained legacy orphan changed power intent or retained its guard")
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Annotations[wipeGuardAnnotation] != "" {
				t.Fatal("drained legacy orphan retained workload guard")
			}
			var job batchv1.Job
			if err := c.Get(t.Context(), client.ObjectKey{Name: gs.Name + "-wipe", Namespace: gs.Namespace}, &job); !apierrors.IsNotFound(err) {
				t.Fatal("acknowledged legacy cleanup recreated wipe worker")
			}
		})
	}
}
