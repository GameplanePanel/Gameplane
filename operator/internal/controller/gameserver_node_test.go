package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestReconcileNodePlacement_SetsAndClears(t *testing.T) {
	s := wipeScheme(t) // gameplane + batch + core

	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"
	gs.Namespace = "ns"
	gs.Spec.TemplateRef.Name = "mc"

	pod := &corev1.Pod{}
	pod.Name = "alpha-0"
	pod.Namespace = "ns"
	pod.Spec.NodeName = "node-7"

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, pod).Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl, Scheme: s}
	ctx := context.Background()
	key := types.NamespacedName{Name: "alpha", Namespace: "ns"}

	// Pod scheduled → annotation set to the node it landed on.
	if err := r.reconcileNodePlacement(ctx, gs); err != nil {
		t.Fatalf("reconcileNodePlacement (set): %v", err)
	}
	var got gameplanev1alpha1.GameServer
	if err := cl.Get(ctx, key, &got); err != nil {
		t.Fatalf("get gs: %v", err)
	}
	if got.Annotations[nodeAnnotation] != "node-7" {
		t.Fatalf("node annotation = %q, want node-7", got.Annotations[nodeAnnotation])
	}

	// Idempotent: re-running with the same placement is a no-op (no patch, no
	// self-triggered reconcile loop).
	if err := r.reconcileNodePlacement(ctx, &got); err != nil {
		t.Fatalf("reconcileNodePlacement (noop): %v", err)
	}

	// Pod gone (server stopped) → annotation cleared, not left stale.
	if err := cl.Delete(ctx, pod); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	if err := r.reconcileNodePlacement(ctx, &got); err != nil {
		t.Fatalf("reconcileNodePlacement (clear): %v", err)
	}
	var cleared gameplanev1alpha1.GameServer
	if err := cl.Get(ctx, key, &cleared); err != nil {
		t.Fatalf("get gs: %v", err)
	}
	if v, ok := cleared.Annotations[nodeAnnotation]; ok {
		t.Fatalf("node annotation still present after pod removed: %q", v)
	}
}
