//go:build envtest

package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// withRestoreUploadReconciler wires RestoreReconciler with the upload settings
// and seeds the agent CA that signs receiver certificates.
func withRestoreUploadReconciler(t *testing.T, ns, stagingClass string) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&RestoreReconciler{
			Client:                    mgr.GetClient(),
			APIReader:                 mgr.GetAPIReader(),
			Scheme:                    mgr.GetScheme(),
			UploadStagingStorageClass: stagingClass,
			AgentImage:                "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:         "agent-ca",
			AgentCASecretNamespace:    ns,
		}).SetupWithManager(mgr)
	}
}

// bootstrapUploadFixtures creates the template, target server and its data claim.
func bootstrapUploadFixtures(t *testing.T, ns string) {
	t.Helper()
	ctx := context.Background()
	if err := k8sClient.Create(ctx, buildGameTemplate("minecraft")); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	if err := k8sClient.Create(ctx, buildGameServer(ns, "smp", "minecraft")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "smp-data", Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("1Gi"),
			}},
		},
	}
	if err := k8sClient.Create(ctx, pvc); err != nil {
		t.Fatalf("create data claim: %v", err)
	}
}

// buildUploadRestore returns an upload Restore against the "smp" server.
func buildUploadRestore(ns, name, confirmedTemplate string) *gameplanev1alpha1.Restore {
	return &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.RestoreSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "smp"},
			Upload: &gameplanev1alpha1.RestoreUpload{
				FileName:          "backup.tar.gz",
				Format:            "tar.gz",
				DeclaredBytes:     1024,
				ConfirmedTemplate: confirmedTemplate,
			},
		},
	}
}

// workerSummary builds a receiver or copy termination message.
func workerSummary(t *testing.T, kind, template string, volumes ...string) string {
	t.Helper()
	s := map[string]any{"kind": kind, "unpackedBytes": 10, "volumes": volumes, "zipPermissionsReset": false, "error": ""}
	if kind == "gameplane" {
		s["manifest"] = map[string]any{
			"formatVersion": 1,
			"server":        map[string]string{"name": "smp"},
			"template":      map[string]string{"name": template},
			"gameVersion":   "1.21.0",
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	return string(data)
}

// finishWorkerPod records a terminal phase and termination message on a worker Pod.
func finishWorkerPod(t *testing.T, ns, name string, phase corev1.PodPhase, message string) {
	t.Helper()
	ctx := context.Background()
	var pod corev1.Pod
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", name, err)
	}
	pod.Status.Phase = phase
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name: "worker",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			ExitCode: 0, Message: message,
		}},
	}}
	if err := k8sClient.Status().Update(ctx, &pod); err != nil {
		t.Fatalf("finish pod %s: %v", name, err)
	}
}

// requireNoStagingClaim asserts the Restore's staging claim is gone.
func requireNoStagingClaim(t *testing.T, ns, rsName string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: rsName + "-staging"}, &pvc)
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		// envtest runs no controller-manager, so the pvc-protection finalizer is
		// never removed: a claim the operator deleted stays with a deletion
		// timestamp. That timestamp proves the delete was issued.
		if err == nil && !pvc.DeletionTimestamp.IsZero() {
			return true, ""
		}
		return false, "staging claim still present"
	})
}

// requireServerNotFenced asserts the target was never suspended or bound.
func requireServerNotFenced(t *testing.T, ns string) {
	t.Helper()
	gs := getGameServer(t, ns, "smp")
	if gs.Spec.Suspend {
		t.Fatal("target server was suspended")
	}
	if gs.Annotations[restoreGuardAnnotation] != "" {
		t.Fatal("target server carries a restore guard")
	}
}

// TestRestoreUpload_NoStagingStorageClassFailsBeforeTouchingServer checks the
// Failed message names the missing configuration and nothing is created.
func TestRestoreUpload_NoStagingStorageClassFailsBeforeTouchingServer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreUploadReconciler(t, ns, ""))
	bootstrapUploadFixtures(t, ns)
	if err := k8sClient.Create(context.Background(), buildUploadRestore(ns, "rs-up", "")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-up")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if !strings.Contains(r.Status.Message, "NoStagingStorageClass") {
			return false, "message: " + r.Status.Message
		}
		return true, ""
	})
	requireServerNotFenced(t, ns)
	var pvc corev1.PersistentVolumeClaim
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "rs-up-staging"}, &pvc); !apierrors.IsNotFound(err) {
		t.Fatalf("staging claim created without a storage class: %v", err)
	}
}

// TestRestoreUpload_WrongGameRefusedServerUntouched checks FR-013: a
// gameplane archive for another template fails in Receiving, before any fence.
func TestRestoreUpload_WrongGameRefusedServerUntouched(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreUploadReconciler(t, ns, "standard"))
	bootstrapUploadFixtures(t, ns)
	if err := k8sClient.Create(context.Background(), buildUploadRestore(ns, "rs-up", "")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-up-receive")
		return err == nil, "waiting for receiver pod"
	})
	finishWorkerPod(t, ns, "rs-up-receive", corev1.PodSucceeded, workerSummary(t, "gameplane", "terraria", "primary"))

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-up")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message != "archive is for terraria, this server runs minecraft" {
			return false, "message: " + r.Status.Message
		}
		return true, ""
	})
	requireServerNotFenced(t, ns)
	requireNoStagingClaim(t, ns, "rs-up")
}

// TestRestoreUpload_PlainArchiveNeedsMatchingTemplate checks the confirmed
// template must match the target for plain archives.
func TestRestoreUpload_PlainArchiveNeedsMatchingTemplate(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreUploadReconciler(t, ns, "standard"))
	bootstrapUploadFixtures(t, ns)
	if err := k8sClient.Create(context.Background(), buildUploadRestore(ns, "rs-plain", "terraria")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-plain-receive")
		return err == nil, "waiting for receiver pod"
	})
	finishWorkerPod(t, ns, "rs-plain-receive", corev1.PodSucceeded, workerSummary(t, "plain", "", "data"))

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-plain")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message != "plain archive is for terraria, this server runs minecraft" {
			return false, "message: " + r.Status.Message
		}
		return true, ""
	})
	requireServerNotFenced(t, ns)
	requireNoStagingClaim(t, ns, "rs-plain")
}

// TestRestoreUpload_ConfirmationTimeoutFails checks an unconfirmed archive
// fails once its confirmation window closes, leaving the server alone.
func TestRestoreUpload_ConfirmationTimeoutFails(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreUploadReconciler(t, ns, "standard"))
	bootstrapUploadFixtures(t, ns)
	if err := k8sClient.Create(context.Background(), buildUploadRestore(ns, "rs-timeout", "")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-timeout-receive")
		return err == nil, "waiting for receiver pod"
	})
	finishWorkerPod(t, ns, "rs-timeout-receive", corev1.PodSucceeded, workerSummary(t, "gameplane", "minecraft", "primary"))
	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-timeout")
		return r.Status.Phase == gameplanev1alpha1.RestorePhaseAwaitingConfirmation, describeRestoreStatus(r)
	})

	// Age the receive record past the window instead of waiting 30 minutes.
	r := getRestore(t, ns, "rs-timeout")
	old := metav1.NewTime(time.Now().Add(-31 * time.Minute))
	r.Status.Upload.ReceivedAt = &old
	if err := k8sClient.Status().Update(context.Background(), r); err != nil {
		t.Fatalf("age receive record: %v", err)
	}

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-timeout")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message != "confirmation timed out" {
			return false, "message: " + r.Status.Message
		}
		return true, ""
	})
	requireServerNotFenced(t, ns)
	requireNoStagingClaim(t, ns, "rs-timeout")
}

// TestRestoreUpload_CopyFailureLeavesServerStopped checks a copy that fails
// partway reports the partial state and does not resume the server.
func TestRestoreUpload_CopyFailureLeavesServerStopped(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreUploadReconciler(t, ns, "standard"))
	bootstrapUploadFixtures(t, ns)
	ctx := context.Background()
	if err := k8sClient.Create(ctx, buildUploadRestore(ns, "rs-copy", "")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-copy-receive")
		return err == nil, "waiting for receiver pod"
	})
	finishWorkerPod(t, ns, "rs-copy-receive", corev1.PodSucceeded, workerSummary(t, "gameplane", "minecraft", "primary"))
	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-copy")
		return r.Status.Phase == gameplanev1alpha1.RestorePhaseAwaitingConfirmation, describeRestoreStatus(r)
	})

	r := getRestore(t, ns, "rs-copy")
	r.Spec.Upload.Confirmed = true
	if err := k8sClient.Update(ctx, r); err != nil {
		t.Fatalf("confirm restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		return getGameServer(t, ns, "smp").Spec.Suspend, "waiting for spec.suspend"
	})
	markGameServerPhase(t, ns, "smp", gameplanev1alpha1.GameServerPhaseSuspended)
	createStoppedRestoreStatefulSet(t, ns, "smp")

	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-copy-copy")
		return err == nil, "waiting for copy pod"
	})
	finishWorkerPod(t, ns, "rs-copy-copy", corev1.PodFailed, `{"error":"disk full","copiedVolumes":[]}`)

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-copy")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if !strings.HasPrefix(r.Status.Message, "copy failed partway: data on smp may be partly replaced; the server stays stopped") ||
			!strings.Contains(r.Status.Message, "disk full") {
			return false, "message: " + r.Status.Message
		}
		return true, ""
	})
	if gs := getGameServer(t, ns, "smp"); !gs.Spec.Suspend {
		t.Fatal("server was resumed after a failed copy")
	}
	requireNoStagingClaim(t, ns, "rs-copy")
	eventually(t, func() (bool, string) {
		_, err := getPodIfPresent(ns, "rs-copy-copy")
		return apierrors.IsNotFound(err), "copy pod still present"
	})
}

// getPodIfPresent returns the named Pod, or the lookup error.
func getPodIfPresent(ns, name string) (*corev1.Pod, error) {
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &pod); err != nil {
		return nil, err
	}
	return &pod, nil
}
