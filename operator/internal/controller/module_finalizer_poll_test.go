package controller

import (
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestModuleFinalizerPollRetainsTemplateUntilUnreferenced(t *testing.T) {
	t.Parallel()
	scheme := scrapeScheme(t)
	mod := &gameplanev1alpha1.Module{ObjectMeta: metav1.ObjectMeta{Name: "module", UID: "module-uid", Finalizers: []string{gameplanev1alpha1.ModuleFinalizer}}}
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: mod.Name}}
	if err := controllerutil.SetControllerReference(mod, tmpl, scheme); err != nil {
		t.Fatal(err)
	}
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns"},
		Spec:       gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mod, tmpl, gs).
		WithStatusSubresource(&gameplanev1alpha1.Module{}).Build()
	r := &ModuleReconciler{Client: c, Scheme: scheme}
	if err := c.Delete(t.Context(), mod); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(mod), mod); err != nil {
		t.Fatal(err)
	}
	result, err := r.finalize(t.Context(), mod)
	if err != nil || result != (ctrl.Result{RequeueAfter: 5 * time.Second}) {
		t.Fatalf("in-use finalizer must poll: result=%+v err=%v", result, err)
	}
	if !controllerutil.ContainsFinalizer(mod, gameplanev1alpha1.ModuleFinalizer) {
		t.Fatal("finalizer released while template is referenced")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(tmpl), tmpl); err != nil {
		t.Fatalf("referenced template removed: %v", err)
	}
	if err := c.Delete(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	result, err = r.finalize(t.Context(), mod)
	if err != nil || !result.IsZero() {
		t.Fatalf("unreferenced module must finish: result=%+v err=%v", result, err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(tmpl), tmpl); !apierrors.IsNotFound(err) {
		t.Fatalf("owned template survived finalization: %v", err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(mod), mod); !apierrors.IsNotFound(err) {
		t.Fatalf("Module finalizer was not released: %v", err)
	}
}
