package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestLifecycleBackupAdmissionRequiresObservedStopAndDrainedWriters(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		want bool
	}{
		{"workload missing", false},
		{"foreign workload", false},
		{"replicas unspecified", false},
		{"desired replica remains", false},
		{"observed replica remains", false},
		{"generation unobserved", false},
		{"game pod running", false},
		{"PVC writer running", false},
		{"terminal pod still terminating", false},
		{"restart pending", false},
		{"terminal pod drained", true},
		{"unrelated pod running", true},
		{"stop complete", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			gs := wipeGameServer(true, "")
			gs.Annotations[stopRequestedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
			ss := wipeStoppedSet(gs)
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			var pods []client.Object
			switch scenario.name {
			case "workload missing":
				ss = nil
			case "foreign workload":
				ss.OwnerReferences = nil
			case "replicas unspecified":
				ss.Spec.Replicas = nil
			case "desired replica remains":
				*ss.Spec.Replicas = 1
			case "observed replica remains":
				ss.Status.Replicas = 1
			case "generation unobserved":
				ss.Status.ObservedGeneration = ss.Generation - 1
			case "game pod running":
				pods = append(pods, pod)
			case "PVC writer running":
				pod.Name = "external-writer"
				pod.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-data"}}}}
				pods = append(pods, pod)
			case "terminal pod still terminating":
				pod.Status.Phase = corev1.PodSucceeded
				pod.Finalizers = []string{"test/hold"}
				now := metav1.Now()
				pod.DeletionTimestamp = &now
				pods = append(pods, pod)
			case "restart pending":
				gs.Annotations[RestartRequestedAnnotation] = "restart"
			case "terminal pod drained":
				pod.Status.Phase = corev1.PodSucceeded
				pods = append(pods, pod)
			case "unrelated pod running":
				pod.Name = "unrelated"
				pods = append(pods, pod)
			}
			b := guardBackup("new-copy")
			b.Spec.ServerRef.Name = gs.Name
			objects := append([]client.Object{gs, b}, pods...)
			if ss != nil {
				objects = append(objects, ss)
			}
			c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(objects...).Build()
			want := backupLifecycleIdentity(gs, b, false)
			claimed, err := claimLifecycleOperation(t.Context(), c, c, gs, want, nil)
			if err != nil || claimed != scenario.want {
				t.Fatalf("backup admission=%t, want %t: %v", claimed, scenario.want, err)
			}
			current := wipeCurrent(t, c, gs)
			op, held, err := lifecycleOwner(current)
			if err != nil || held != scenario.want || (held && !sameLifecycleOwner(op.lifecycleIdentity, want)) {
				t.Fatalf("durable admission disagrees with stop proof: held=%t owner=%+v err=%v", held, op, err)
			}
			if !current.Spec.Suspend || current.Annotations[stopRequestedAtAnnotation] == "" {
				t.Fatal("backup admission changed the caller's stop intent")
			}
		})
	}
}

func TestLifecycleStopProofFailsClosedWhenSafetyReadsFail(t *testing.T) {
	t.Parallel()
	for _, failRead := range []string{"workload", "pods"} {
		t.Run(failRead, func(t *testing.T) {
			t.Parallel()
			gs := wipeGameServer(true, "")
			gs.Annotations[stopRequestedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
			cause := errors.New("API unavailable")
			c := fake.NewClientBuilder().WithScheme(wipeScheme(t)).WithObjects(gs, wipeStoppedSet(gs)).Build()
			reader := interceptor.NewClient(c, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, workload := obj.(*appsv1.StatefulSet); workload && failRead == "workload" {
						return cause
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, pods := list.(*corev1.PodList); pods && failRead == "pods" {
						return cause
					}
					return c.List(ctx, list, opts...)
				},
			})
			pending, err := backupLifecycleTransitionPending(t.Context(), reader, gs)
			if !pending || !errors.Is(err, cause) {
				t.Fatalf("failed %s read allowed copy admission: pending=%t err=%v", failRead, pending, err)
			}
		})
	}
}
