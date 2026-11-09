package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHostportBackstopStopsOnceListenerReady(t *testing.T) {
	t.Parallel()
	for _, asleep := range []bool{false, true} {
		for _, status := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown} {
			plan := sentinelPlan{wantSentinel: asleep, hostportCondition: &metav1.Condition{Status: status}}
			if got := sentinelBackstopPending(plan); got != (status != metav1.ConditionTrue) {
				t.Fatalf("asleep=%v status=%s backstop=%v", asleep, status, got)
			}
		}
	}
	if !sentinelBackstopPending(sentinelPlan{wantSentinel: true}) || sentinelBackstopPending(sentinelPlan{}) {
		t.Fatal("service-backed sentinel backstop changed")
	}
}

func TestHostportConditionPreservesExistingReadyFailure(t *testing.T) {
	t.Parallel()
	hostport := &metav1.Condition{Type: hostportWakeCondition, Status: metav1.ConditionFalse, Reason: "ListenerPending", Message: "Waiting for a Ready listener"}
	for _, existing := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionUnknown} {
		conditions := []metav1.Condition{{Type: "Ready", Status: existing, Reason: "CrashLoopBackOff", Message: "game keeps crashing"}}
		got := applyHostportCondition(conditions, hostport)
		ready := meta.FindStatusCondition(got, "Ready")
		if existing == metav1.ConditionFalse {
			if ready.Reason != "CrashLoopBackOff" || ready.Message != "game keeps crashing" {
				t.Fatalf("lost startup failure: %+v", ready)
			}
		} else if ready.Reason != hostport.Reason || ready.Status != metav1.ConditionFalse {
			t.Fatalf("endpoint failure did not block readiness: %+v", ready)
		}
		if meta.FindStatusCondition(got, hostportWakeCondition) == nil {
			t.Fatal("hostport diagnostic missing")
		}
	}
	if meta.FindStatusCondition(applyHostportCondition(nil, hostport), "Ready") == nil {
		t.Fatal("missing Ready condition was not added")
	}
}
