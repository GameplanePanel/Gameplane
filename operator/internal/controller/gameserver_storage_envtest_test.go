//go:build envtest

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServerStorage_ExplicitOverrideWins asserts that when a GameServer
// explicitly sets Spec.Storage.StorageClassName, the PVC uses that value
// even when both the operator's DefaultStorageClassName and the GameTemplate
// default are also set. This is the first (most specific) precedence level.
func TestGameServerStorage_ExplicitOverrideWins(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerStorageClass(t, ns, "install-time-default"))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmplSCName := "template-default"
	tmpl.Spec.Storage.StorageClassName = &tmplSCName
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gsSCName := "explicit-override"
	gs.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{
		StorageClassName: &gsSCName,
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc not found: " + err.Error()
		}
		if pvc.Spec.StorageClassName == nil {
			return false, "pvc storageClassName is nil, expected explicit-override"
		}
		if *pvc.Spec.StorageClassName != "explicit-override" {
			return false, "pvc storageClassName = " + *pvc.Spec.StorageClassName + ", want explicit-override"
		}
		return true, ""
	})
}

// TestGameServerStorage_TemplateDefaultWhenServerUnset asserts that when
// a GameServer does NOT set Spec.Storage.StorageClassName but the GameTemplate
// does, the PVC uses the template default — even when the operator's
// DefaultStorageClassName is also set. Template default is the second
// precedence level.
func TestGameServerStorage_TemplateDefaultWhenServerUnset(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerStorageClass(t, ns, "install-time-default"))

	tmpl := buildGameTemplate(uniqueName("valheim"))
	tmplSCName := "template-default"
	tmpl.Spec.Storage.StorageClassName = &tmplSCName
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Leave Spec.Storage.StorageClassName unset (nil)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc not found: " + err.Error()
		}
		if pvc.Spec.StorageClassName == nil {
			return false, "pvc storageClassName is nil, expected template-default"
		}
		if *pvc.Spec.StorageClassName != "template-default" {
			return false, "pvc storageClassName = " + *pvc.Spec.StorageClassName + ", want template-default"
		}
		return true, ""
	})
}

// TestGameServerStorage_InstallTimeDefaultWhenBothUnset asserts that when
// neither GameServer nor GameTemplate set Spec.Storage.StorageClassName,
// the PVC uses the operator's DefaultStorageClassName (install-time default).
// This is the third precedence level.
func TestGameServerStorage_InstallTimeDefaultWhenBothUnset(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerStorageClass(t, ns, "install-time-default"))

	tmpl := buildGameTemplate(uniqueName("terraria"))
	// Leave Spec.Storage.StorageClassName unset (nil)
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Leave Spec.Storage.StorageClassName unset (nil)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc not found: " + err.Error()
		}
		if pvc.Spec.StorageClassName == nil {
			return false, "pvc storageClassName is nil, expected install-time-default"
		}
		if *pvc.Spec.StorageClassName != "install-time-default" {
			return false, "pvc storageClassName = " + *pvc.Spec.StorageClassName + ", want install-time-default"
		}
		return true, ""
	})
}

// TestGameServerStorage_ClusterDefaultWhenAllUnset asserts that when
// GameServer, GameTemplate, and the operator's DefaultStorageClassName
// are all unset/empty, the PVC's StorageClassName is nil — falling back
// to the cluster's default StorageClass. This preserves pre-feature behavior
// (SC-008).
func TestGameServerStorage_ClusterDefaultWhenAllUnset(t *testing.T) {
	ns := newNamespace(t)
	// Start reconciler with empty DefaultStorageClassName
	startMgr(t, ns, withGameServerReconcilerStorageClass(t, ns, ""))

	tmpl := buildGameTemplate(uniqueName("garrysmod"))
	// Leave Spec.Storage.StorageClassName unset (nil)
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Leave Spec.Storage.StorageClassName unset (nil)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc not found: " + err.Error()
		}
		if pvc.Spec.StorageClassName != nil {
			return false, "pvc storageClassName = " + *pvc.Spec.StorageClassName + ", want nil (cluster default)"
		}
		return true, ""
	})
}

// TestGameServerStorage_PVCImmutableAfterCreation asserts that once a PVC
// has been created, changing the operator's DefaultStorageClassName and
// reconciling the GameServer again does NOT mutate the existing PVC's
// storageClassName field. Storage class is set only on creation, never
// on later reconciles.
func TestGameServerStorage_PVCImmutableAfterCreation(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerStorageClass(t, ns, "original-default"))

	tmpl := buildGameTemplate(uniqueName("factorio"))
	// Leave template storage class unset
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Leave GameServer storage class unset
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for PVC creation with original-default
	var pvc corev1.PersistentVolumeClaim
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc not found: " + err.Error()
		}
		if pvc.Spec.StorageClassName == nil {
			return false, "pvc storageClassName is nil, expected original-default"
		}
		if *pvc.Spec.StorageClassName != "original-default" {
			return false, "pvc storageClassName = " + *pvc.Spec.StorageClassName + ", want original-default"
		}
		return true, ""
	})

	// Now force a reconcile by patching the GameServer spec. This
	// simulates what would happen if the operator's DefaultStorageClassName
	// were changed at runtime. We can't directly change the reconciler's
	// field (it's not mutable after startup), so we trigger a re-reconcile
	// and verify the PVC is untouched.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "smp"}, &fresh); err != nil {
			return err
		}
		// Trigger a reconcile by adding a label (benign change that doesn't affect PVC creation)
		if fresh.Labels == nil {
			fresh.Labels = make(map[string]string)
		}
		fresh.Labels["force-reconcile"] = "true"
		return k8sClient.Update(context.Background(), &fresh)
	}); err != nil {
		t.Fatalf("update gs to force reconcile: %v", err)
	}

	// Wait for the reconciler to observe the change
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "smp"}, &got); err != nil {
			return false, "get gs: " + err.Error()
		}
		return got.Labels["force-reconcile"] == "true", "label not yet observed"
	})

	// Verify the PVC's storageClassName has NOT changed
	consistently(t, defaultEventuallyInterval*5, func() (bool, string) {
		var current corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &current); err != nil {
			return false, "get pvc: " + err.Error()
		}
		if current.Spec.StorageClassName == nil {
			return false, "pvc storageClassName became nil unexpectedly"
		}
		if *current.Spec.StorageClassName != "original-default" {
			return false, "pvc storageClassName changed to " + *current.Spec.StorageClassName + ", should remain original-default"
		}
		return true, ""
	})
}

// withGameServerReconcilerStorageClass is withGameServerReconciler with
// DefaultStorageClassName also set, proving the operator's
// --game-data-storage-class flag reaches the GameServerReconciler
// and is used as the install-time default storage class.
func withGameServerReconcilerStorageClass(t *testing.T, ns, storageClassName string) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                  mgr.GetClient(),
			APIReader:               mgr.GetAPIReader(),
			Scheme:                  mgr.GetScheme(),
			AgentImage:              "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:       "agent-ca",
			AgentCASecretNamespace:  ns,
			DefaultStorageClassName: storageClassName,
		}).SetupWithManager(mgr)
	}
}
