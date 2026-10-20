package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestSetPhase_ReadyConditionCarriesObservedGeneration proves setPhase
// stamps ObservedGeneration on the Ready condition it builds, matching
// computeConditions and every mark* helper. Without it, the dashboard
// (and any consumer keying off ObservedGeneration to detect a stale
// condition) would see a Ready condition that looks perpetually unobserved.
func TestSetPhase_ReadyConditionCarriesObservedGeneration(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "srv"
	gs.Namespace = "ns"
	gs.Generation = 7

	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(gs).
		WithStatusSubresource(&gameplanev1alpha1.GameServer{}).
		Build()
	r := &GameServerReconciler{Client: cl, APIReader: cl}

	if err := r.setPhase(context.Background(), gs, "boom"); err != nil {
		t.Fatalf("setPhase: %v", err)
	}

	got := &gameplanev1alpha1.GameServer{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "srv"}, got); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("Ready condition missing")
	}
	if ready.ObservedGeneration != 7 {
		t.Errorf("ObservedGeneration = %d, want 7", ready.ObservedGeneration)
	}
}
