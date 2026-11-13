package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	gp "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestRecoverObsoleteFailedPod(t *testing.T) {
	type fixture struct {
		gs  *gp.GameServer
		ss  *appsv1.StatefulSet
		pod *corev1.Pod
	}
	for _, tc := range []struct {
		name   string
		change func(fixture)
		evict  bool
	}{
		{"image pull backoff", func(fixture) {}, true},
		{"repeated crash", func(f fixture) {
			s := &f.pod.Status.ContainerStatuses[0]
			s.State.Waiting.Reason = "CrashLoopBackOff"
			s.RestartCount = crashLoopFailureThreshold
		}, true},
		{"still starting", func(f fixture) { f.pod.Status.ContainerStatuses[0].State.Waiting.Reason = "ContainerCreating" }, false},
		{"current revision", func(f fixture) { f.pod.Labels[appsv1.StatefulSetRevisionLabel] = "new" }, false},
		{"missing revision", func(f fixture) { f.pod.Labels = nil }, false},
		{"ready", func(f fixture) {
			f.pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		}, false},
		{"unrelated pod", func(f fixture) { f.pod.OwnerReferences[0].UID = "foreign" }, false},
		{"unrelated set", func(f fixture) { f.ss.OwnerReferences[0].UID = "other-server" }, false},
		{"set not observed", func(f fixture) { f.ss.Status.ObservedGeneration-- }, false},
		{"scaled down", func(f fixture) { f.ss.Spec.Replicas = ptr.To(int32(0)) }, false},
		{"partition", func(f fixture) {
			f.ss.Spec.UpdateStrategy.RollingUpdate = &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(1))}
		}, false},
		{"on delete", func(f fixture) { f.ss.Spec.UpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType }, false},
		{"suspended", func(f fixture) { f.gs.Spec.Suspend = true }, false},
		{"backup", func(f fixture) { f.gs.Annotations[backupGuardAnnotation] = backupIdentityJSON("b", "backup") }, false},
		{"restore", func(f fixture) { f.gs.Annotations[restoreGuardAnnotation] = restoreIdentityJSON("r", "restore") }, false},
		{"wipe", func(f fixture) { f.gs.Annotations[WipeRequestedAnnotation] = "1" }, false},
		{"stop", func(f fixture) { f.gs.Annotations[stopRequestedAtAnnotation] = "now" }, false},
		{"restart", func(f fixture) { f.gs.Annotations[RestartRequestedAnnotation] = "1" }, false},
		{"hostport fence", func(f fixture) { f.ss.Annotations = map[string]string{hostportStopFenceAnnotation: "1"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs, ss, pod := failedPodFixture()
			tc.change(fixture{gs, ss, pod})
			evictions := 0
			c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(gs, ss, pod).WithInterceptorFuncs(interceptor.Funcs{
				SubResourceCreate: func(ctx context.Context, c client.Client, sub string, obj client.Object, body client.Object, _ ...client.SubResourceCreateOption) error {
					evictions++
					competing := lifecycleIdentity{Kind: "Backup", Name: "snapshot", UID: "backup-uid", TargetUID: gs.UID}
					claimed, err := claimLifecycleOperation(ctx, c, c, gs.DeepCopy(), competing, nil)
					if err != nil || claimed {
						t.Fatalf("backup claimed target during eviction: claimed=%v err=%v", claimed, err)
					}
					e := body.(*policyv1.Eviction)
					if sub != "eviction" || obj.GetUID() != pod.UID || e.Name != pod.Name || e.Namespace != pod.Namespace || e.DeleteOptions == nil || e.DeleteOptions.Preconditions == nil || ptr.Deref(e.DeleteOptions.Preconditions.UID, "") != pod.UID || ptr.Deref(e.DeleteOptions.Preconditions.ResourceVersion, "") != "19" {
						t.Fatalf("unfenced eviction: %+v", e)
					}
					return apierrors.NewTooManyRequests("disruption budget", 1)
				},
			}).Build()
			r := &GameServerReconciler{Client: c}
			retry, err := r.recoverObsoleteFailedPod(context.Background(), gs)
			if err != nil || (retry > 0) != tc.evict || (evictions == 1) != tc.evict {
				t.Fatalf("retry %v, evictions %d, error %v", retry, evictions, err)
			}
		})
	}
}

func failedPodFixture() (*gp.GameServer, *appsv1.StatefulSet, *corev1.Pod) {
	gs := wipeGameServer(false, "")
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "set", Generation: 2,
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gp.GroupVersion.WithKind("GameServer"))}},
		Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1)), UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType}},
		Status: appsv1.StatefulSetStatus{UpdateRevision: "new", ObservedGeneration: 2}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "old-pod", ResourceVersion: "19", Labels: map[string]string{appsv1.StatefulSetRevisionLabel: "old"}, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: gameContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}
	return gs, ss, pod
}

func TestBackupWinsEvictionAdmissionRace(t *testing.T) {
	gs, ss, pod := failedPodFixture()
	c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(gs, ss, pod).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			op, _, _ := lifecycleOwner(obj.(*gp.GameServer))
			if op.Kind != podRecoveryKind {
				t.Fatal("unexpected owner update")
			}
			// Win on the shared GameServer while the recovery claimant is
			// holding the immediately preceding resourceVersion.
			want := lifecycleIdentity{Kind: "Backup", Name: "scheduled", UID: "copy", TargetUID: gs.UID}
			ok, err := claimLifecycleOperation(ctx, c, c, gs.DeepCopy(), want, nil)
			if err != nil || !ok {
				t.Fatalf("backup admission: %t %v", ok, err)
			}
			return c.Update(ctx, obj, opts...)
		},
		SubResourceCreate: func(context.Context, client.Client, string, client.Object, client.Object, ...client.SubResourceCreateOption) error {
			t.Fatal("lost claim reached eviction")
			return nil
		},
	}).Build()
	r := GameServerReconciler{Client: c}
	if _, err := r.recoverObsoleteFailedPod(t.Context(), gs); !apierrors.IsConflict(err) {
		t.Fatalf("losing candidate: %v", err)
	}
}

func TestRefusedEvictionReconcilesBackupScheduleAndStatus(t *testing.T) {
	gs, ss, pod := failedPodFixture()
	gs.Spec.BackupPolicy = &gp.InlineBackupPolicy{
		Schedule: "0 */6 * * *",
		RepoRef:  gp.SecretKeySelector{Name: "repo", Key: "url"},
	}
	r := lifecycleGameServerFixture(t, gs, ss, pod)
	evictions := 0
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		SubResourceCreate: func(ctx context.Context, c client.Client, sub string, obj client.Object, body client.Object, opts ...client.SubResourceCreateOption) error {
			if sub == "eviction" {
				evictions++
				return apierrors.NewTooManyRequests("disruption budget", 1)
			}
			return c.SubResource(sub).Create(ctx, obj, body, opts...)
		},
	})

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}
	result, err := r.Reconcile(t.Context(), req)
	if err != nil || result.RequeueAfter != evictionRetry || evictions != 1 {
		t.Fatalf("refused eviction: result=%+v attempts=%d err=%v", result, evictions, err)
	}
	var schedule gp.BackupSchedule
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-auto"}, &schedule); err != nil {
		t.Fatalf("backup schedule missing after eviction refusal: %v", err)
	}
	current := wipeCurrent(t, r.Client, gs)
	if current.Status.Phase != gp.GameServerPhaseFailed {
		t.Fatalf("phase after eviction refusal = %q, want Failed", current.Status.Phase)
	}
}

func TestAcceptedEvictionKeepsOrdinaryReconciliationActive(t *testing.T) {
	for _, intent := range []string{"running", "suspend", "restart", "wipe"} {
		t.Run(intent, func(t *testing.T) {
			gs, ss, pod := failedPodFixture()
			gs.Generation = 2
			gs.Spec.BackupPolicy = &gp.InlineBackupPolicy{
				Schedule: "0 */6 * * *",
				RepoRef:  gp.SecretKeySelector{Name: "repo", Key: "url"},
			}
			pod.Finalizers = []string{"test.gameplane.local/drain"}
			r := lifecycleGameServerFixture(t, gs, ss, pod)
			evictions := 0
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				SubResourceCreate: func(ctx context.Context, c client.Client, sub string, obj client.Object, body client.Object, opts ...client.SubResourceCreateOption) error {
					if sub == "eviction" {
						evictions++
						return c.Delete(ctx, obj)
					}
					return c.SubResource(sub).Create(ctx, obj, body, opts...)
				},
			})
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}
			if _, err := r.Reconcile(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			current := wipeCurrent(t, r.Client, gs)
			owner, held, err := lifecycleOwner(current)
			if err != nil || !held || owner.Kind != podRecoveryKind || evictions != 1 {
				t.Fatalf("eviction admission: owner=%+v held=%t attempts=%d err=%v", owner, held, evictions, err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
				t.Fatal(err)
			}
			protectedSpec := ss.Spec.DeepCopy()
			// The accepted eviction is still draining. Settings unrelated to
			// its workload must reconcile while new power/template intent waits.
			current.Generation++
			current.Spec.BackupPolicy.Suspend = true
			current.Spec.Image = "example.invalid/game:next"
			switch intent {
			case "suspend":
				current.Spec.Suspend = true
			case "restart":
				current.Annotations[RestartRequestedAnnotation] = "later-restart"
			case "wipe":
				current.Annotations[WipeRequestedAnnotation] = "later-wipe"
			}
			if err := r.Update(t.Context(), current); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if result, err := r.Reconcile(t.Context(), req); err != nil || result.RequeueAfter <= 0 {
					t.Fatalf("reconcile during eviction: result=%+v err=%v", result, err)
				}
			}
			var schedule gp.BackupSchedule
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-auto"}, &schedule); err != nil {
				t.Fatal(err)
			}
			if !schedule.Spec.Suspend {
				t.Fatal("backup schedule change stalled during accepted eviction")
			}
			current = wipeCurrent(t, r.Client, gs)
			if current.Status.ObservedGeneration != current.Generation {
				t.Fatalf("status stalled during eviction: observed=%d generation=%d", current.Status.ObservedGeneration, current.Generation)
			}
			liveOwner, held, err := lifecycleOwner(current)
			if err != nil || !held || !sameLifecycleOwner(liveOwner.lifecycleIdentity, owner.lifecycleIdentity) || evictions != 1 {
				t.Fatalf("eviction lost exclusion or repeated: owner=%+v held=%t attempts=%d err=%v", liveOwner, held, evictions, err)
			}
			if current.Annotations[RestartCompletedAnnotation] != "" || current.Annotations[WipeCompletedAnnotation] != "" || current.Annotations[stopRequestedAtAnnotation] != "" {
				t.Fatal("power transition advanced before the evicted UID drained")
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ss.Spec, *protectedSpec) {
				t.Fatal("accepted eviction did not preserve the complete workload spec")
			}
			requireNoWipeJob(t, r, gs)
		})
	}
}

func TestEvictionOwnerRecoversThroughReconcile(t *testing.T) {
	for _, tc := range []string{"accepted", "pdb", "changed intent", "uncertain response"} {
		t.Run(tc, func(t *testing.T) {
			gs, ss, pod := failedPodFixture()
			gs.Generation = 2
			want := lifecycleIdentity{Kind: podRecoveryKind, Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Generation: 2, Token: string(pod.UID)}
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: want})
			if tc == "changed intent" {
				gs.Generation++
				gs.Spec.Suspend = true
			}
			pod.Finalizers = []string{"test.gameplane.local/drain"}
			evictions := 0
			c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(gs, ss, pod).WithStatusSubresource(gs).WithInterceptorFuncs(interceptor.Funcs{
				SubResourceCreate: func(ctx context.Context, c client.Client, _ string, obj client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
					evictions++
					if tc == "pdb" {
						return apierrors.NewTooManyRequests("blocked", 1)
					}
					if err := c.Delete(ctx, obj); err != nil {
						return err
					}
					if tc == "uncertain response" {
						return errors.New("eviction response lost")
					}
					return nil
				},
			}).Build()
			r := GameServerReconciler{Client: c}
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}
			// A new reconciler has no in-memory state from admission. No
			// template is installed, so recovery must run before validation.
			result, err := r.Reconcile(t.Context(), req)
			if (err != nil) != (tc == "uncertain response") {
				t.Fatalf("first pass: %v", err)
			}
			if tc == "accepted" && result.RequeueAfter != evictionRetry {
				t.Fatalf("missing template suppressed eviction cleanup polling: %+v", result)
			}
			if tc == "changed intent" && evictions != 0 {
				t.Fatal("evicted after suspend")
			}
			if tc == "pdb" && evictions != 1 {
				t.Fatal("missing eviction attempt")
			}
			if tc == "accepted" || tc == "uncertain response" {
				if evictions != 1 {
					t.Fatalf("evictions: %d", evictions)
				}
				r = GameServerReconciler{Client: c}
				if _, err = r.Reconcile(t.Context(), req); err != nil {
					t.Fatal(err)
				}
				var live gp.GameServer
				if err = c.Get(t.Context(), req.NamespacedName, &live); err != nil {
					t.Fatal(err)
				}
				op, held, err := lifecycleOwner(&live)
				if !held || err != nil || !sameLifecycleOwner(op.lifecycleIdentity, want) || evictions != 1 {
					t.Fatalf("lost owner during grace: %+v %d %v", op, evictions, err)
				}
				if err = c.Get(t.Context(), client.ObjectKeyFromObject(pod), pod); err != nil {
					t.Fatal(err)
				}
				pod.Finalizers = nil
				if err = c.Update(t.Context(), pod); err != nil {
					t.Fatal(err)
				}
				if _, err = r.Reconcile(t.Context(), req); err != nil {
					t.Fatal(err)
				}
			}
			var live gp.GameServer
			if err := c.Get(t.Context(), req.NamespacedName, &live); err != nil {
				t.Fatal(err)
			}
			if _, held, err := lifecycleOwner(&live); err != nil || held {
				t.Fatalf("owner retained after release: %v", err)
			}
			if tc == "pdb" {
				if wait, err := r.recoverObsoleteFailedPod(t.Context(), &live); err != nil || wait <= 0 || evictions != 1 {
					t.Fatalf("immediate retry: %s %d %v", wait, evictions, err)
				}
			}
		})
	}
}
