package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func wipeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gameplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("gameplane scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("apps scheme: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("batch scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core scheme: %v", err)
	}
	return s
}

func wipeGameServer(suspend bool, req string) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	gs.Namespace = "ns"
	gs.UID = "game-server"
	gs.Annotations = map[string]string{WipeRequestedAnnotation: req}
	gs.Spec.Suspend = suspend
	gs.Spec.TemplateRef.Name = "mc"
	return gs
}

func TestReconcileWipe_CreatesJobWhenSuspended(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 0
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	var job batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &job); err != nil {
		t.Fatalf("expected wipe job: %v", err)
	}
	if job.Labels[wipeTokenLabel] != "tok1" {
		t.Errorf("token label = %q, want tok1", job.Labels[wipeTokenLabel])
	}
	if got := job.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName; got != "alpha-data" {
		t.Errorf("data claim = %q, want alpha-data", got)
	}
}

func TestReconcileWipe_AcquiresGuardBeforeWorkloadExists(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "tok1")
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)
	current := wipeCurrent(t, cl, gs)
	if current.Annotations[wipeGuardAnnotation] != "tok1" || !current.Spec.Suspend {
		t.Fatal("pending request did not fence direct start")
	}
	var job batchv1.Job
	err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no wipe job, got err=%v", err)
	}
}

func TestReconcileWipe_AcksWhenJobSucceeded(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 0
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alpha-wipe",
			Namespace: "ns",
			Labels:    map[string]string{wipeTokenLabel: "tok1"},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss, job).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	// The request is acked on the GameServer.
	var got gameplanev1alpha1.GameServer
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha", Namespace: "ns"}, &got); err != nil {
		t.Fatalf("get gs: %v", err)
	}
	if got.Annotations[WipeCompletedAnnotation] != "tok1" {
		t.Errorf("completed annotation = %q, want tok1", got.Annotations[WipeCompletedAnnotation])
	}
	// After a successful wipe, suspend must be false to restart the server.
	if got.Spec.Suspend {
		t.Errorf("suspend after wipe = true, want false")
	}
	// The finished Job is cleaned up.
	var leftover batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &leftover); !apierrors.IsNotFound(err) {
		t.Errorf("expected wipe job deleted, got err=%v", err)
	}
}

func TestReconcileWipe_RestartsServerAfterWipe(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 0
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alpha-wipe",
			Namespace: "ns",
			Labels:    map[string]string{wipeTokenLabel: "tok1"},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss, job).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	// After a successful wipe, suspend must be false to restart the server.
	var got gameplanev1alpha1.GameServer
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha", Namespace: "ns"}, &got); err != nil {
		t.Fatalf("get gs: %v", err)
	}
	if got.Spec.Suspend {
		t.Errorf("suspend after wipe = true, want false")
	}
	if got.Annotations[WipeCompletedAnnotation] != "tok1" {
		t.Errorf("completed annotation = %q, want tok1", got.Annotations[WipeCompletedAnnotation])
	}
}

func TestReconcileWipe_WaitsForPodToBeGone(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	// StatefulSet exists with replicas > 0 (pod still draining).
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 1
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	// No wipe job should be created while the pod is still running.
	var job batchv1.Job
	err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no wipe job while pod draining, got err=%v", err)
	}
}

func TestReconcileWipe_WaitsForPodObjectToBeGone(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	// StatefulSet exists with replicas == 0 but pod still terminating.
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 0
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alpha-0",
			Namespace: "ns",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "test", Image: "test"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss, pod).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	// No wipe job should be created while the pod object exists, even with replicas == 0.
	var job batchv1.Job
	err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &job)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected no wipe job while pod object exists, got err=%v", err)
	}
}

func TestReconcileWipe_CreatesJobWhenPodGone(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "tok1")
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "mc"

	// StatefulSet exists with replicas == 0 (pod is gone).
	ss := wipeStoppedSet(gs)
	ss.Status.Replicas = 0
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, ss).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	wipePasses(t, r, gs, 4)

	// Wipe job should be created once the pod is gone.
	var job batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "alpha-wipe", Namespace: "ns"}, &job); err != nil {
		t.Fatalf("expected wipe job when pod gone: %v", err)
	}
	if job.Labels[wipeTokenLabel] != "tok1" {
		t.Errorf("token label = %q, want tok1", job.Labels[wipeTokenLabel])
	}
}
