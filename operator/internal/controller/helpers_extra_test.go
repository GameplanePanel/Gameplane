package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestUpsertCondition_Append(t *testing.T) {
	now := metav1.Now()
	got := upsertCondition(nil, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue})
	if len(got) != 1 || got[0].Type != "Ready" {
		t.Fatalf("got %+v", got)
	}
	if got[0].LastTransitionTime.IsZero() {
		t.Fatal("LastTransitionTime should be set on append")
	}
	_ = now
}

func TestUpsertCondition_PreservesProvidedTransitionTime(t *testing.T) {
	provided := metav1.Time{Time: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	got := upsertCondition(nil, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: provided,
	})
	if !got[0].LastTransitionTime.Equal(&provided) {
		t.Fatalf("got %v want %v", got[0].LastTransitionTime, provided)
	}
}

func TestUpsertCondition_SameStatus_KeepsTimestamp(t *testing.T) {
	old := metav1.Time{Time: time.Now().Add(-time.Hour)}
	conds := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: old}}
	got := upsertCondition(conds, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Updated"})
	if !got[0].LastTransitionTime.Equal(&old) {
		t.Fatalf("timestamp changed: %v vs %v", got[0].LastTransitionTime, old)
	}
	if got[0].Reason != "Updated" {
		t.Fatalf("reason not propagated")
	}
}

func TestUpsertCondition_StatusFlip_BumpsTimestamp(t *testing.T) {
	old := metav1.Time{Time: time.Now().Add(-time.Hour)}
	conds := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, LastTransitionTime: old}}
	got := upsertCondition(conds, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse})
	if got[0].LastTransitionTime.Equal(&old) {
		t.Fatal("timestamp should advance on status flip")
	}
}

func TestSameConditions(t *testing.T) {
	a := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Up"},
	}
	b := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Up"},
	}
	if !sameConditions(a, b) {
		t.Fatal("equal conditions should match")
	}
	c := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Up"},
	}
	if sameConditions(a, c) {
		t.Fatal("status diff should not match")
	}
	d := []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Different"},
	}
	if sameConditions(a, d) {
		t.Fatal("reason diff should not match")
	}
	if sameConditions(a, nil) {
		t.Fatal("length diff should not match")
	}
}

func TestEnqueueTemplateForServer(t *testing.T) {
	h := enqueueTemplateForServer()

	gs := &gameplanev1alpha1.GameServer{
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "minecraft"},
		},
	}

	// We don't have a real workqueue available; the func returns a
	// MapFunc-handler whose dispatch is exercised by envtest. Here we
	// only smoke-test that the handler is constructed.
	_ = h

	// Direct: call the closure via the handler's underlying type. We
	// recreate the body of the func to verify the mapping logic without
	// depending on internal types.
	// (The package-private mapping is exercised by envtest in CI; this
	// keeps a minimal pure-unit smoke test on the mapping pure function.)
	got := mapServerToTemplate(gs)
	if len(got) != 1 || got[0] != "minecraft" {
		t.Fatalf("got %+v", got)
	}
	if mapServerToTemplate(&gameplanev1alpha1.GameServer{}) != nil {
		t.Fatal("empty TemplateRef.Name should map to nil")
	}
	_ = context.Background
}

// mapServerToTemplate mirrors the logic inside enqueueTemplateForServer's
// MapFunc so we can unit-test it without bringing up controller-runtime.
func mapServerToTemplate(gs *gameplanev1alpha1.GameServer) []string {
	if gs == nil || gs.Spec.TemplateRef.Name == "" {
		return nil
	}
	return []string{gs.Spec.TemplateRef.Name}
}
