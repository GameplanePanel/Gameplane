package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// filesStartTestServer returns a GameServer whose file request is fresh.
func filesStartTestServer() *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{
		Name: "gs", Namespace: "ns",
		Annotations: map[string]string{
			FilesRequestedAtAnnotation: time.Now().UTC().Format(time.RFC3339),
		},
	}}
}

// TestReconcileFilesPodNotRecreatedWhileStartPending covers the start race:
// holdStartForFilesPod zeroes replicas while the file-only pod drains. With a
// start still wanted, reconcileFilesPod must not create a replacement pod.
func TestReconcileFilesPodNotRecreatedWhileStartPending(t *testing.T) {
	s := testScheme(t)
	gs := filesStartTestServer()
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s, FilesIdleTimeout: 10 * time.Minute}
	key := types.NamespacedName{Namespace: "ns", Name: filesPodName(gs)}

	if _, err := r.reconcileFilesPod(context.Background(), gs,
		&gameplanev1alpha1.GameTemplate{}, &gameplanev1alpha1.GameVersion{}, 0, true); err != nil {
		t.Fatalf("reconcileFilesPod (start pending): %v", err)
	}
	var pod corev1.Pod
	if err := cl.Get(context.Background(), key, &pod); !apierrors.IsNotFound(err) {
		t.Fatalf("files pod created while start pending: get err = %v", err)
	}
}

// TestReconcileFilesPodCreatedWhenIdle is the control for the test above: with
// no start wanted and a fresh file request, the file-only pod is created.
func TestReconcileFilesPodCreatedWhenIdle(t *testing.T) {
	s := testScheme(t)
	gs := filesStartTestServer()
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s, FilesIdleTimeout: 10 * time.Minute}
	key := types.NamespacedName{Namespace: "ns", Name: filesPodName(gs)}

	if _, err := r.reconcileFilesPod(context.Background(), gs,
		&gameplanev1alpha1.GameTemplate{}, &gameplanev1alpha1.GameVersion{}, 0, false); err != nil {
		t.Fatalf("reconcileFilesPod (idle): %v", err)
	}
	var pod corev1.Pod
	if err := cl.Get(context.Background(), key, &pod); err != nil {
		t.Fatalf("files pod not created while idle: %v", err)
	}
}
