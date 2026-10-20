package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func restartScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gameplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("gameplane scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core scheme: %v", err)
	}
	return s
}

func restartGameServer(req, done string) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	gs.Namespace = "ns"
	gs.Annotations = map[string]string{}
	if req != "" {
		gs.Annotations[RestartRequestedAnnotation] = req
	}
	if done != "" {
		gs.Annotations[RestartCompletedAnnotation] = done
	}
	gs.Spec.TemplateRef.Name = "mc"
	return gs
}

// restartStatefulSet builds a StatefulSet whose Status.Replicas is the "pods
// created" count restartPhase reads to decide whether the pod is gone.
func restartStatefulSet(replicas int32) *appsv1.StatefulSet {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
	}
	ss.Status.Replicas = replicas
	return ss
}

func TestRestartPhase_NoneWhenNoToken(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("", "")
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	got, err := r.restartPhase(context.Background(), gs)
	if err != nil {
		t.Fatalf("restartPhase: %v", err)
	}
	if got != restartNone {
		t.Errorf("phase = %d, want restartNone", got)
	}
}

func TestRestartPhase_NoneWhenAcked(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("tok1", "tok1")
	// A StatefulSet with a live pod must not matter once the token is acked.
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(gs, restartStatefulSet(1)).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	got, err := r.restartPhase(context.Background(), gs)
	if err != nil {
		t.Fatalf("restartPhase: %v", err)
	}
	if got != restartNone {
		t.Errorf("phase = %d, want restartNone", got)
	}
}

func TestRestartPhase_DrainingWhilePodPresent(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("tok1", "")
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(gs, restartStatefulSet(1)).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	got, err := r.restartPhase(context.Background(), gs)
	if err != nil {
		t.Fatalf("restartPhase: %v", err)
	}
	if got != restartDraining {
		t.Errorf("phase = %d, want restartDraining", got)
	}
}

func TestRestartPhase_CompleteWhenStatefulSetDrained(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("tok1", "")
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(gs, restartStatefulSet(0)).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	got, err := r.restartPhase(context.Background(), gs)
	if err != nil {
		t.Fatalf("restartPhase: %v", err)
	}
	if got != restartComplete {
		t.Errorf("phase = %d, want restartComplete", got)
	}
}

func TestRestartPhase_CompleteWhenStatefulSetMissing(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("tok1", "")
	// No StatefulSet object at all — a server that was never started.
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	got, err := r.restartPhase(context.Background(), gs)
	if err != nil {
		t.Fatalf("restartPhase: %v", err)
	}
	if got != restartComplete {
		t.Errorf("phase = %d, want restartComplete", got)
	}
}

func TestAckRestart_EchoesTokenAndClearsGraceClock(t *testing.T) {
	s := restartScheme(t)
	gs := restartGameServer("tok1", "")
	gs.Annotations[stopRequestedAtAnnotation] = "2026-01-01T00:00:00Z"
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}

	if err := r.ackRestart(context.Background(), gs); err != nil {
		t.Fatalf("ackRestart: %v", err)
	}

	got := &gameplanev1alpha1.GameServer{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(gs), got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Annotations[RestartCompletedAnnotation] != "tok1" {
		t.Errorf("completed = %q, want tok1", got.Annotations[RestartCompletedAnnotation])
	}
	if _, ok := got.Annotations[stopRequestedAtAnnotation]; ok {
		t.Errorf("stop-requested annotation should be cleared, got %v", got.Annotations)
	}
	// A now-acked token reports no pending restart.
	if phase, err := r.restartPhase(context.Background(), got); err != nil || phase != restartNone {
		t.Errorf("post-ack phase = %d, err = %v; want restartNone", phase, err)
	}
}
