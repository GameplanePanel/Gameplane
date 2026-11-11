package controller

import (
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestColdWakeCapacitySurvivesOperatorRestart(t *testing.T) {
	r, first, _ := revisionFixture(t)
	ctx := t.Context()
	r.WakeStartLimit, r.WakeStartNamespace = 1, "panel"
	if err := r.Create(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "gameplane-operator", Namespace: r.WakeStartNamespace, UID: "operator",
	}}); err != nil {
		t.Fatal(err)
	}
	first.Spec.Idle = &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true}
	first.Annotations = map[string]string{IdleAsleepSinceAnnotation: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), IdleWakeRequestedAnnotation: "first-wake"}
	if err := r.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first.DeepCopy()
	second.ObjectMeta = metav1.ObjectMeta{Namespace: "other-tenant", Name: first.Name, UID: "second",
		Annotations: map[string]string{IdleAsleepSinceAnnotation: first.Annotations[IdleAsleepSinceAnnotation], IdleWakeRequestedAnnotation: "second-wake"}}
	if err := r.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	if admitted, err := r.reserveWakeStart(ctx, first); err != nil || !admitted {
		t.Fatalf("reserve before operator restart: admitted=%t err=%v", admitted, err)
	}
	// Process dies between the reservation and the annotation patch. The
	// replacement reconciler starts without any process-local wake state.
	restarted := &GameServerReconciler{Client: r.Client, Scheme: r.Scheme, APIReader: r.APIReader, WakeStartLimit: 1, WakeStartNamespace: "panel"}
	state, idle, retry, _, err := restarted.reconcileIdle(ctx, second)
	if err != nil || state != idleAsleep || !idle.Asleep || !strings.Contains(idle.Reason, "queued") || retry <= 0 || second.Annotations[IdleWakeCompletedAnnotation] != "" {
		t.Fatalf("second wake skipped the queue or lost its token: state=%d idle=%+v retry=%v err=%v", state, idle, retry, err)
	}
	if _, _, _, _, err := restarted.reconcileIdle(ctx, first); err != nil || first.Annotations[IdleWakeCompletedAnnotation] != "first-wake" {
		t.Fatalf("resume first reservation: %v %+v", err, first.Annotations)
	}
	if ok, err := restarted.reserveWakeStart(ctx, second); err != nil || ok {
		t.Fatalf("first start still has no StatefulSet: admitted=%t err=%v", ok, err)
	}
	one := int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: first.Name, Namespace: first.Namespace, OwnerReferences: []metav1.OwnerReference{
		*metav1.NewControllerRef(first, gameplanev1alpha1.GroupVersion.WithKind("GameServer")),
	}}, Spec: appsv1.StatefulSetSpec{Replicas: &one}}
	if err := r.Create(ctx, ss); err != nil {
		t.Fatal(err)
	}
	if ok, err := restarted.reserveWakeStart(ctx, second); err != nil || ok {
		t.Fatalf("first pod still starting: admitted=%t err=%v", ok, err)
	}
	ss.Status.ObservedGeneration, ss.Status.ReadyReplicas = ss.Generation, 1
	if err := r.Status().Update(ctx, ss); err != nil {
		t.Fatal(err)
	}
	state, _, _, _, err = restarted.reconcileIdle(ctx, second)
	if err != nil || state != idleAwake || second.Annotations[IdleWakeCompletedAnnotation] != "second-wake" {
		t.Fatalf("queued wake was not admitted after readiness: state=%d err=%v annotations=%v", state, err, second.Annotations)
	}
	var ledger corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{Namespace: "panel", Name: wakeStartsName}, &ledger); err != nil || len(ledger.Data) != 1 || ledger.Data["second"] == "" {
		t.Fatalf("unexpected persisted reservations: %v %v", ledger.Data, err)
	}
}
