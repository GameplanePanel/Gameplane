package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func rconForeignFixture() (*gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "srv", Namespace: "ns", UID: "gs-uid"}}
	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
		RCON: &gameplanev1alpha1.RCONSpec{Protocol: "source", PasswordEnv: "PW"},
	}}
	return gs, tmpl
}

func TestReconcileRCONSecret_RefusesUnownedSecret(t *testing.T) {
	gs, tmpl := rconForeignFixture()
	planted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srv-rcon", Namespace: "ns"},
		Data:       map[string][]byte{"password": []byte("attacker")},
	}
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(planted).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}
	err := r.reconcileRCONSecret(context.Background(), gs, tmpl)
	if !errors.Is(err, errRCONSecretNotOwned) {
		t.Fatalf("err = %v, want errRCONSecretNotOwned", err)
	}
	var got corev1.Secret
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "srv-rcon"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.OwnerReferences) != 0 || string(got.Data["password"]) != "attacker" {
		t.Fatal("foreign Secret was modified")
	}
}

func TestReconcileRCONSecret_ReusesOwnedSecret(t *testing.T) {
	gs, tmpl := rconForeignFixture()
	s := scrapeScheme(t)
	owned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srv-rcon", Namespace: "ns"},
		Data:       map[string][]byte{"password": []byte("kept")},
	}
	if err := controllerutil.SetControllerReference(gs, owned, s); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(owned).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var got corev1.Secret
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "srv-rcon"}, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Data["password"]) != "kept" {
		t.Fatal("owned password was rotated")
	}
}

// A template that references its own Secret named <gs>-rcon keeps using it:
// the operator neither refuses, adopts nor rewrites it.
func TestReconcileRCONSecret_LeavesReferencedSecretWithGeneratedName(t *testing.T) {
	gs, tmpl := rconForeignFixture()
	tmpl.Spec.RCON.PasswordSecretRef = &gameplanev1alpha1.SecretKeySelector{Name: "srv-rcon", Key: "pw"}
	mine := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "srv-rcon", Namespace: "ns"},
		Data:       map[string][]byte{"pw": []byte("chosen")},
	}
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mine).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var got corev1.Secret
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "srv-rcon"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.OwnerReferences) != 0 || string(got.Data["pw"]) != "chosen" || len(got.Data["password"]) != 0 {
		t.Fatal("referenced Secret was modified")
	}
}
