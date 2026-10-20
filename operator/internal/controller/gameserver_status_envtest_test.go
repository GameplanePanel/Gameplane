//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_PVCProvisioningFailureDetection_NonexistentStorageClass — a
// GameServer referencing a nonexistent StorageClass ends up with the Ready
// condition False and reason PVCProvisioningFailed, with a message naming
// the missing class.
func TestGameServer_PVCProvisioningFailureDetection_NonexistentStorageClass(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create a GameServer that specifies a nonexistent StorageClass.
	gs := buildGameServer(ns, "test-missing-sc", tmpl.Name)
	scName := "nonexistent-fast-nvme"
	gs.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{
		MountPath:        "/data",
		StorageClassName: &scName,
		Size:             resource.MustParse("20Gi"),
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for the reconciler to detect the PVC provisioning failure. A
	// missing StorageClass is recoverable (an admin can create it and the PVC
	// binds with no action on the GameServer), so the phase stays Pending (not
	// escalated to Failed) while the Ready condition carries the specific,
	// actionable error.
	eventually(t, func() (bool, string) {
		cur := getGameServer(t, ns, "test-missing-sc")
		// Find the Ready condition and check its reason.
		for _, cond := range cur.Status.Conditions {
			if cond.Type == "Ready" {
				if cond.Status != metav1.ConditionFalse {
					return false, "Ready.Status=" + string(cond.Status) + ", want False"
				}
				if cond.Reason != GameServerConditionReasonPVCProvisioningFailed {
					return false, "Ready.Reason=" + cond.Reason + ", want " + GameServerConditionReasonPVCProvisioningFailed
				}
				// The message should mention the missing StorageClass.
				if !strings.Contains(cond.Message, "nonexistent-fast-nvme") {
					return false, "Ready.Message should mention the StorageClass, got: " + cond.Message
				}
				return true, ""
			}
		}
		return false, "Ready condition not found"
	})

	// Verify that status.phase stayed non-terminal (Pending or Starting, not Failed).
	// A PVC provisioning failure is recoverable and must not escalate to Failed.
	cur := getGameServer(t, ns, "test-missing-sc")
	if cur.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed {
		t.Errorf("phase=%s, want a non-terminal phase (Pending or Starting); a PVC provisioning failure is recoverable and must not escalate to Failed",
			cur.Status.Phase)
	}
	if cur.Status.Phase != gameplanev1alpha1.GameServerPhasePending && cur.Status.Phase != gameplanev1alpha1.GameServerPhaseStarting {
		t.Errorf("phase=%s, want Pending or Starting; a PVC provisioning failure is recoverable and must not escalate to Failed",
			cur.Status.Phase)
	}
}

// TestGameServer_PVCProvisioningFailureDetection_SuccessfulProvisioning — a
// GameServer whose PVC provisions normally (no nonexistent StorageClass)
// does NOT get the PVCProvisioningFailed condition. This is the negative
// control: the reconciler should proceed with normal startup instead.
func TestGameServer_PVCProvisioningFailureDetection_SuccessfulProvisioning(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create a GameServer without specifying a StorageClass (uses cluster default).
	gs := buildGameServer(ns, "test-valid-sc", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for the PVC to be created; there should be no provisioning failure.
	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "test-valid-sc-data"}, &pvc); err != nil {
			return false, "data pvc: " + err.Error()
		}
		return true, ""
	})

	// Fetch the GameServer and verify that the Ready condition does NOT have
	// the PVCProvisioningFailed reason. The phase should be Pending or Starting,
	// not Failed.
	cur := getGameServer(t, ns, "test-valid-sc")

	// The phase should NOT be Failed since there's no provisioning error.
	if cur.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed {
		t.Fatalf("phase should not be Failed for successful provisioning, but got: %s", cur.Status.Phase)
	}

	// Verify the Ready condition does not use PVCProvisioningFailed.
	for _, cond := range cur.Status.Conditions {
		if cond.Type == "Ready" && cond.Reason == GameServerConditionReasonPVCProvisioningFailed {
			t.Fatalf("unexpected PVCProvisioningFailed condition on successful provisioning: %s", cond.Message)
		}
	}
}
