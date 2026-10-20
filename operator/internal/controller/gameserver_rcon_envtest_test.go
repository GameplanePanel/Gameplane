//go:build envtest

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestReconcileRCONSecret_ExternalRef — ensures NO <gs>-rcon Secret is
// created/preserved when template uses external PasswordSecretRef.
func TestReconcileRCONSecret_ExternalRef(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	// Create a template with external PasswordSecretRef
	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{
		Protocol: "source",
		PasswordSecretRef: &gameplanev1alpha1.SecretKeySelector{
			Name: "external-rcon-secret",
			Key:  "password",
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create external secret to ensure it exists
	extSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "external-rcon-secret", Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte("external-pw")},
	}
	if err := k8sClient.Create(context.Background(), extSecret); err != nil {
		t.Fatalf("create external secret: %v", err)
	}
	deleteCleanup(t, extSecret)

	// Create GameServer with external ref template
	gs := buildGameServer(ns, "external-test", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for reconciliation to complete, then verify no <gs>-rcon Secret exists
	eventually(t, func() (bool, string) {
		var straySecret corev1.Secret
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "external-test-rcon"}, &straySecret)
		if err == nil {
			return false, "stray <gs>-rcon Secret should not exist for external ref template"
		}
		if !apierrors.IsNotFound(err) {
			return false, "unexpected error: " + err.Error()
		}
		return true, ""
	})
}

// TestReconcileRCONSecret_PasswordFile — ensures NO <gs>-rcon Secret is
// created/preserved when template uses password file mode.
func TestReconcileRCONSecret_PasswordFile(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	// Create a template with PasswordFile
	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{
		Protocol:     "source",
		PasswordFile: "config/rconpassword",
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create GameServer with file-mode template
	gs := buildGameServer(ns, "filmode-test", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for reconciliation to complete, then verify no <gs>-rcon Secret exists
	eventually(t, func() (bool, string) {
		var straySecret corev1.Secret
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "filmode-test-rcon"}, &straySecret)
		if err == nil {
			return false, "stray <gs>-rcon Secret should not exist for file-mode template"
		}
		if !apierrors.IsNotFound(err) {
			return false, "unexpected error: " + err.Error()
		}
		return true, ""
	})
}

// TestReconcileRCONSecret_Generated — ensures the <gs>-rcon Secret IS
// created and preserved when template declares RCON with neither
// PasswordSecretRef nor PasswordFile.
func TestReconcileRCONSecret_Generated(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	// Create a template with default (generated) RCON mode
	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{
		Protocol:    "source",
		PasswordEnv: "RCON_PASSWORD",
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create GameServer with generated RCON template
	gs := buildGameServer(ns, "genmode-test", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for <gs>-rcon Secret to be created
	eventually(t, func() (bool, string) {
		var rconSecret corev1.Secret
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "genmode-test-rcon"}, &rconSecret)
		if err != nil {
			return false, "genmode-test-rcon Secret should exist: " + err.Error()
		}
		if _, ok := rconSecret.Data["password"]; !ok {
			return false, "genmode-test-rcon Secret missing 'password' key"
		}
		return true, ""
	})
}
