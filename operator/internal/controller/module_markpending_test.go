package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestMarkPending_ClearsStalePullingCondition proves markPending clears a
// leftover Pulling=True condition, mirroring markFailed. This is reachable
// when a pulling reconcile is interrupted (markPullingTransition already ran
// and set Pulling=True) and the catalog then drops the module/version before
// the next reconcile — which lands in markPending instead. Without this,
// Phase=Pending could coexist with a Pulling condition stuck True forever.
func TestMarkPending_ClearsStalePullingCondition(t *testing.T) {
	mod := &gameplanev1alpha1.Module{}
	mod.Name = "mc"
	mod.Generation = 3
	mod.Status.Phase = gameplanev1alpha1.ModulePhasePulling
	mod.Status.Conditions = []metav1.Condition{{
		Type:               gameplanev1alpha1.ModuleConditionPulling,
		Status:             metav1.ConditionTrue,
		Reason:             "Pulling",
		ObservedGeneration: 2,
	}}

	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(mod).
		WithStatusSubresource(&gameplanev1alpha1.Module{}).
		Build()
	r := &ModuleReconciler{Client: cl}

	if _, err := r.markPending(context.Background(), mod, "WaitingForCatalog",
		errors.New("source has not yet indexed module")); err != nil {
		t.Fatalf("markPending: %v", err)
	}

	got := &gameplanev1alpha1.Module{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "mc"}, got); err != nil {
		t.Fatalf("get module: %v", err)
	}
	if got.Status.Phase != gameplanev1alpha1.ModulePhasePending {
		t.Errorf("Phase = %s, want Pending", got.Status.Phase)
	}
	pulling := meta.FindStatusCondition(got.Status.Conditions, gameplanev1alpha1.ModuleConditionPulling)
	if pulling == nil {
		t.Fatal("Pulling condition missing after markPending")
	}
	if pulling.Status != metav1.ConditionFalse {
		t.Errorf("Pulling = %s, want False", pulling.Status)
	}
}
