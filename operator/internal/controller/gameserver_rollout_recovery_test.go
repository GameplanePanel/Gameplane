package controller

import (
	"context"
	gp "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"testing"
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
		{"image pull backoff", func(f fixture) {}, true},
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
		{"backup", func(f fixture) { f.gs.Annotations[backupGuardAnnotation] = "held" }, false},
		{"restore", func(f fixture) { f.gs.Annotations[restoreGuardAnnotation] = "held" }, false},
		{"wipe", func(f fixture) { f.gs.Annotations[WipeRequestedAnnotation] = "1" }, false},
		{"stop", func(f fixture) { f.gs.Annotations[stopRequestedAtAnnotation] = "now" }, false},
		{"restart", func(f fixture) { f.gs.Annotations[RestartRequestedAnnotation] = "1" }, false},
		{"hostport fence", func(f fixture) { f.ss.Annotations = map[string]string{hostportStopFenceAnnotation: "1"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs := wipeGameServer(false, "")
			ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "set", Generation: 2,
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gp.GroupVersion.WithKind("GameServer"))}},
				Spec:   appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1)), UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType}},
				Status: appsv1.StatefulSetStatus{UpdateRevision: "new", ObservedGeneration: 2}}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "old-pod", ResourceVersion: "19", Labels: map[string]string{appsv1.StatefulSetRevisionLabel: "old"}, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: gameContainerName, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}
			tc.change(fixture{gs, ss, pod})
			evictions := 0
			c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(gs, ss, pod).WithInterceptorFuncs(interceptor.Funcs{
				SubResourceCreate: func(ctx context.Context, c client.Client, sub string, obj client.Object, body client.Object, opts ...client.SubResourceCreateOption) error {
					evictions++
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
