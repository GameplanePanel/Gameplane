package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNodeCaptureWorkerBoundAndNoCredentials(t *testing.T) {
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap-test", UID: "capture-uid"}}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "server-uid"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "11111111-2222-3333-4444-555555555555"}, Spec: corev1.PodSpec{NodeName: "target-node"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "game", ContainerID: "containerd://" + strings.Repeat("a", 64), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	spec, err := nodeWorkerPodSpec(nc, gs, pod, "image", "pvc", "tcp port 25565", 10, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !spec.HostPID || spec.HostNetwork || spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken || spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("unsafe worker isolation: %+v", spec)
	}
	if len(spec.Volumes) != 1 || spec.Volumes[0].PersistentVolumeClaim == nil {
		t.Fatalf("worker acquired host paths or credentials: %+v", spec.Volumes)
	}
	if len(spec.Containers) != 1 || *spec.Containers[0].SecurityContext.RunAsUser != 0 || *spec.Containers[0].SecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("unexpected worker privileges: %+v", spec.Containers)
	}
	node := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields[0]
	if node.Key != "metadata.name" || len(node.Values) != 1 || node.Values[0] != "target-node" {
		t.Fatalf("worker not bound to exact node: %+v", node)
	}
	pod.Spec.HostNetwork = true
	if _, err := nodeWorkerPodSpec(nc, gs, pod, "image", "pvc", "tcp port 25565", 10, 1024); err == nil {
		t.Fatal("host network target accepted")
	}
	pod.Spec.HostNetwork = false
	pod.Status.ContainerStatuses[0].ContainerID = "docker://" + strings.Repeat("a", 64)
	if _, err := nodeWorkerPodSpec(nc, gs, pod, "image", "pvc", "tcp port 25565", 10, 1024); err == nil {
		t.Fatal("unsupported runtime accepted")
	}
}

func nodeReconcilerFixture(t *testing.T, objects ...client.Object) *NetworkCaptureReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, batchv1.AddToScheme, gameplanev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&gameplanev1alpha1.NetworkCapture{}, &gameplanev1alpha1.GameServer{}).WithObjects(objects...).Build()
	return &NetworkCaptureReconciler{Client: c, APIReader: c, Scheme: scheme, CaptureEnabled: true, RestrictedWorkloads: true, DiagnosticsNamespace: "diagnostics"}
}

type staleCaptureListClient struct{ client.Client }

func (c staleCaptureListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if captures, ok := list.(*gameplanev1alpha1.NetworkCaptureList); ok {
		captures.Items = nil
		return nil
	}
	return c.Client.List(ctx, list, opts...)
}

func TestNodeCaptureExclusivityUsesLiveList(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "server"}, Spec: gameplanev1alpha1.GameServerSpec{Capture: &gameplanev1alpha1.CaptureConfiguration{Enabled: true, NodeDiagnostic: true}}}
	owner := metav1.OwnerReference{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: gs.Name, UID: gs.UID, Controller: ptrTo(true)}
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "newer", Namespace: "games", UID: "newer", CreationTimestamp: metav1.NewTime(time.Now()), OwnerReferences: []metav1.OwnerReference{owner}}}
	nc.Spec.ServerRef.Name = gs.Name
	older := nc.DeepCopy()
	older.Name = "older"
	older.UID = "older"
	older.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "statefulset", OwnerReferences: []metav1.OwnerReference{owner}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "game-0", Namespace: "games", UID: "pod", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: ss.Name, UID: ss.UID, Controller: ptrTo(true)}}}, Spec: corev1.PodSpec{NodeName: "node"}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "game", ContainerID: "containerd://" + strings.Repeat("a", 64), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "diagnostics", Labels: map[string]string{"pod-security.kubernetes.io/enforce": "privileged"}}}
	r := nodeReconcilerFixture(t, gs, nc, older, ss, pod, ns)
	r.Client = staleCaptureListClient{r.Client}
	if _, err := r.reconcileNodeCapture(t.Context(), nc); err != nil {
		t.Fatal(err)
	}
	var live gameplanev1alpha1.NetworkCapture
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(nc), &live); err != nil {
		t.Fatal(err)
	}
	if live.Status.Phase != gameplanev1alpha1.CapturePhaseFailed || !strings.Contains(live.Status.Message, "another capture") {
		t.Fatalf("stale cache bypassed exclusivity: %+v", live.Status)
	}
	if live.Annotations[nodeCaptureNamespaceAnnotation] != "" {
		t.Fatal("new worker bound despite older request")
	}
}

func TestDeletingNodeCaptureReleasesOnlyItsOwnLock(t *testing.T) {
	for _, test := range []struct {
		name, active, serverUID string
		clear                   bool
	}{{"matching", "cap", "server", true}, {"other-capture", "other", "server", false}, {"replaced-server", "cap", "replacement", false}} {
		t.Run(test.name, func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: types.UID(test.serverUID)}, Status: gameplanev1alpha1.GameServerStatus{Capture: &gameplanev1alpha1.CaptureStatus{ActiveCapture: ptrTo(test.active)}}}
			nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", Namespace: "games", UID: "capture", Finalizers: []string{nodeCaptureFinalizer}, DeletionTimestamp: ptrTo(metav1.Now()), Annotations: map[string]string{nodeCaptureNamespaceAnnotation: "diagnostics"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "game", UID: "server", Controller: ptrTo(true)}}}}
			nc.Spec.ServerRef.Name = gs.Name
			r := nodeReconcilerFixture(t, gs, nc)
			if _, err := r.reconcileNodeCapture(t.Context(), nc); err != nil {
				t.Fatal(err)
			}
			var live gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &live); err != nil {
				t.Fatal(err)
			}
			if (live.Status.Capture.ActiveCapture == nil) != test.clear {
				t.Fatalf("wrong lock release: %+v", live.Status.Capture)
			}
		})
	}
}

func TestNodeWorkerCopiesTargetTolerations(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: "pod"}, Spec: corev1.PodSpec{NodeName: "tainted-node", Tolerations: []corev1.Toleration{{Key: "game-nodes", Operator: corev1.TolerationOpEqual, Value: "dedicated", Effect: corev1.TaintEffectNoSchedule}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "game", ContainerID: "containerd://" + strings.Repeat("a", 64), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	spec, err := nodeWorkerPodSpec(&gameplanev1alpha1.NetworkCapture{}, &gameplanev1alpha1.GameServer{}, pod, "image", "pvc", "tcp", 10, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Tolerations, pod.Spec.Tolerations) {
		t.Fatal("worker lost target tolerations")
	}
}

func TestNodeCaptureRefusesWithoutServerOptIn(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Capture: &gameplanev1alpha1.CaptureConfiguration{Enabled: true}}}
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", Namespace: "games", UID: "capture-uid"}}
	nc.Spec.ServerRef.Name = gs.Name
	r := nodeReconcilerFixture(t, gs, nc)
	if _, err := r.reconcileNodeCapture(t.Context(), nc); err != nil {
		t.Fatal(err)
	}
	var live gameplanev1alpha1.NetworkCapture
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(nc), &live); err != nil {
		t.Fatal(err)
	}
	if live.Status.Phase != gameplanev1alpha1.CapturePhaseFailed || !strings.Contains(live.Status.Message, "administrator") {
		t.Fatalf("opt-in refusal missing: %+v", live.Status)
	}
	var jobs batchv1.JobList
	if err := r.List(t.Context(), &jobs); err != nil || len(jobs.Items) != 0 {
		t.Fatalf("unauthorized worker: %v %+v", err, jobs)
	}
}

func TestNodeCaptureDrainsOrphanWorkersBeforeCleanup(t *testing.T) {
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", Namespace: "games", UID: "capture-uid", Annotations: map[string]string{nodeCaptureNamespaceAnnotation: "diagnostics"}}}
	labels := map[string]string{nodeCaptureUIDLabel: string(nc.UID), nodeCaptureComponentLabel: "worker"}
	anchor := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: nodeCaptureName(nc), Namespace: "diagnostics", UID: "anchor", Labels: labels}}
	worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan-worker", Namespace: "diagnostics", UID: "worker", Labels: labels}}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other-worker", Namespace: "diagnostics", UID: "other", Labels: map[string]string{nodeCaptureUIDLabel: "other", nodeCaptureComponentLabel: "worker"}}}
	r := nodeReconcilerFixture(t, nc, anchor, worker, other)
	ctx := context.Background()
	if clean, err := r.cleanupNodeCapture(ctx, nc); err != nil || clean {
		t.Fatalf("cleanup freed lock before observed worker deletion: %t %v", clean, err)
	}
	var live corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKeyFromObject(anchor), &live); err != nil {
		t.Fatal("anchor deleted before draining worker")
	}
	if clean, err := r.cleanupNodeCapture(ctx, nc); err != nil || clean {
		t.Fatalf("cleanup did not wait for anchor GC: %t %v", clean, err)
	}
	if clean, err := r.cleanupNodeCapture(ctx, nc); err != nil || !clean {
		t.Fatalf("cleanup failed to finish: %t %v", clean, err)
	}
	var p corev1.Pod
	if err := r.Get(ctx, client.ObjectKeyFromObject(other), &p); err != nil {
		t.Fatal("deleted unrelated worker")
	}
}

func TestNodeCaptureDoesNotDeleteNamesakeJob(t *testing.T) {
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", UID: "capture-uid", Annotations: map[string]string{nodeCaptureNamespaceAnnotation: "diagnostics"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: nodeCaptureName(nc) + "-worker", Namespace: "diagnostics", UID: "foreign"}}
	r := nodeReconcilerFixture(t, job)
	if _, err := r.deleteNodeWorkers(t.Context(), nc); err == nil {
		t.Fatal("accepted foreign job")
	}
	var live batchv1.Job
	if err := r.Get(t.Context(), types.NamespacedName{Namespace: job.Namespace, Name: job.Name}, &live); apierrors.IsNotFound(err) {
		t.Fatal("deleted foreign job")
	}
}

func TestNodeOptInPreservesLegacyCaptureLifecycle(t *testing.T) {
	r := nodeReconcilerFixture(t)
	for _, phase := range []gameplanev1alpha1.CapturePhase{gameplanev1alpha1.CapturePhasePending, gameplanev1alpha1.CapturePhaseRunning, gameplanev1alpha1.CapturePhaseCompleted} {
		nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{capturePodUIDAnnotation: "legacy-pod"}}, Status: gameplanev1alpha1.NetworkCaptureStatus{Phase: phase}}
		if node, err := r.useNodeCapture(t.Context(), nc); err != nil || node {
			t.Fatalf("legacy phase %s rerouted: %t %v", phase, node, err)
		}
		nc.Annotations[nodeCaptureNamespaceAnnotation] = "diagnostics"
		if node, err := r.useNodeCapture(t.Context(), nc); err != nil || !node {
			t.Fatalf("bound node phase %s lost lifecycle: %t %v", phase, node, err)
		}
	}
}
