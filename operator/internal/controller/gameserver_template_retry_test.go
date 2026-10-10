package controller

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestGameServer_MissingTemplateKeepsBoundedRetry(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "waiting", Namespace: "games"}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "missing"}}}
	scheme := testScheme(t)
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs).WithStatusSubresource(gs).Build()
	r := &GameServerReconciler{Client: c}
	for range 2 {
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
		if err != nil {
			t.Fatal(err)
		}
		if result.RequeueAfter < time.Second || result.RequeueAfter > time.Minute {
			t.Fatalf("retry delay = %v", result.RequeueAfter)
		}
	}
}

func TestTemplateChangeSelectsReferencingServers(t *testing.T) {
	first := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "games"}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "changed"}}}
	other := first.DeepCopy()
	other.Name, other.Spec.TemplateRef.Name = "two", "unrelated"
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(first, other).Build()
	r := &GameServerReconciler{Client: c}
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "changed"}}
	requests := r.mapTemplateToGameServers(t.Context(), tmpl)
	if len(requests) != 1 || requests[0].NamespacedName != client.ObjectKeyFromObject(first) {
		t.Fatalf("template change selected %v", requests)
	}
}
