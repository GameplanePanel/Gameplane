package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// rconConfigFixture is a template whose configSchema exports the same env var
// as rcon.passwordEnv (FS25 ADMIN_PASSWORD, L4D2/Squad/The Isle RCON_PASSWORD).
func rconConfigFixture() (*gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	gs, tmpl := rconForeignFixture()
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "ADMIN", EnvName: "PW", Type: "password", Default: "template-default"},
	}
	return gs, tmpl
}

func rconSecretPassword(t *testing.T, cl client.Client) corev1.Secret {
	t.Helper()
	var got corev1.Secret
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "srv-rcon"}, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReconcileRCONSecret_UsesUserConfigPassword(t *testing.T) {
	gs, tmpl := rconConfigFixture()
	gs.Spec.Config = map[string]string{"ADMIN": "typed-by-user"}
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := rconSecretPassword(t, cl)
	if string(got.Data["password"]) != "typed-by-user" {
		t.Fatalf("password = %q, want the user's config value", got.Data["password"])
	}
	if got.Annotations[rconPasswordSourceAnnotation] != rconPasswordSourceConfig {
		t.Fatal("config-sourced password not marked")
	}

	// Changing the field updates the stored password.
	gs.Spec.Config["ADMIN"] = "changed"
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := rconSecretPassword(t, cl); string(got.Data["password"]) != "changed" {
		t.Fatalf("password = %q after change, want %q", got.Data["password"], "changed")
	}

	// Clearing the field rotates to a fresh generated password.
	gs.Spec.Config["ADMIN"] = ""
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got = rconSecretPassword(t, cl)
	pw := string(got.Data["password"])
	if pw == "" || pw == "changed" || pw == "template-default" {
		t.Fatalf("password = %q after clearing, want a fresh generated one", pw)
	}
	if _, ok := got.Annotations[rconPasswordSourceAnnotation]; ok {
		t.Fatal("source annotation kept after clearing the field")
	}
}

func TestReconcileRCONSecret_IgnoresTemplateDefaultPassword(t *testing.T) {
	gs, tmpl := rconConfigFixture()
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &GameServerReconciler{Client: cl, Scheme: s}
	if err := r.reconcileRCONSecret(context.Background(), gs, tmpl); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := rconSecretPassword(t, cl)
	if pw := string(got.Data["password"]); pw == "" || pw == "template-default" {
		t.Fatalf("password = %q, want a generated one", pw)
	}
}

func TestRCONConfigPassword(t *testing.T) {
	gs, tmpl := rconConfigFixture()
	gs.Spec.Config = map[string]string{"ADMIN": "x"}
	if got := rconConfigPassword(gs, tmpl); got != "x" {
		t.Fatalf("envName match = %q, want x", got)
	}
	tmpl.Spec.ConfigSchema[0].Target = "file"
	if got := rconConfigPassword(gs, tmpl); got != "" {
		t.Fatalf("file-target field = %q, want empty", got)
	}
	tmpl.Spec.ConfigSchema[0] = gameplanev1alpha1.ConfigField{Name: "PW", Type: "password"}
	gs.Spec.Config = map[string]string{"PW": "y"}
	if got := rconConfigPassword(gs, tmpl); got != "y" {
		t.Fatalf("name match = %q, want y", got)
	}
	tmpl.Spec.RCON = nil
	if got := rconConfigPassword(gs, tmpl); got != "" {
		t.Fatalf("no rcon = %q, want empty", got)
	}
}
