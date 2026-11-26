//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// withBackupExportReconciler wires BackupExportReconciler into the test
// manager and seeds the agent CA Secret it signs worker certificates with.
// It lives in this file so the shared envtest helpers need no edit.
func withBackupExportReconciler(t *testing.T, ns string) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&BackupExportReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
			WriterVersion:          "test",
		}).SetupWithManager(mgr)
	}
}

func buildBackupExport(ns, name, backupName, backupUID, format string) *gameplanev1alpha1.BackupExport {
	return &gameplanev1alpha1.BackupExport{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.BackupExportSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: backupName},
			BackupUID: backupUID,
			Format:    format,
		},
	}
}

func getBackupExport(t *testing.T, ns, name string) *gameplanev1alpha1.BackupExport {
	t.Helper()
	var e gameplanev1alpha1.BackupExport
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &e); err != nil {
		t.Fatalf("get backupexport %s/%s: %v", ns, name, err)
	}
	return &e
}

// TestBackupExport_CreatesWorkerForSucceededRestic verifies a finished restic
// Backup yields a worker Pod, Service and TLS Secret, all owned by the export,
// with the repository and archive environment the worker needs.
func TestBackupExport_CreatesWorkerForSucceededRestic(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupExportReconciler(t, ns))

	ctx := context.Background()
	if err := k8sClient.Create(ctx, buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create repo secret: %v", err)
	}
	if err := k8sClient.Create(ctx, buildBackup(ns, "bk", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceeded(t, ns, "bk", "snap-1", "1Mi")
	b := getBackup(t, ns, "bk")

	if err := k8sClient.Create(ctx, buildBackupExport(ns, "bk-export", "bk", string(b.UID), "tar.gz")); err != nil {
		t.Fatalf("create backupexport: %v", err)
	}
	export := getBackupExport(t, ns, "bk-export")

	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bk-export"}, &pod); err != nil {
			return false, "pod not yet created: " + err.Error()
		}
		if !metav1IsControlledBy(&pod, export) {
			return false, "pod not controlled by the export"
		}
		env := map[string]corev1.EnvVar{}
		for _, e := range pod.Spec.Containers[0].Env {
			env[e.Name] = e
		}
		repo, ok := env["RESTIC_REPOSITORY"]
		if !ok || repo.ValueFrom == nil || repo.ValueFrom.SecretKeyRef == nil ||
			repo.ValueFrom.SecretKeyRef.Name != "repo" || repo.ValueFrom.SecretKeyRef.Key != "repo" {
			return false, "RESTIC_REPOSITORY not sourced from repo secret"
		}
		if got := env["ARCHIVE_SNAPSHOT_ID"].Value; got != "snap-1" {
			return false, "ARCHIVE_SNAPSHOT_ID = " + got
		}
		if got := env["ARCHIVE_FORMAT"].Value; got != "tar.gz" {
			return false, "ARCHIVE_FORMAT = " + got
		}
		if !strings.Contains(env["ARCHIVE_MANIFEST"].Value, `"resticSnapshotID":"snap-1"`) {
			return false, "ARCHIVE_MANIFEST missing snapshot id"
		}
		if pod.Spec.ActiveDeadlineSeconds == nil || *pod.Spec.ActiveDeadlineSeconds != 3600 {
			return false, "activeDeadlineSeconds not the default TTL"
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bk-export"}, &svc); err != nil {
			return false, "service not yet created: " + err.Error()
		}
		if !metav1IsControlledBy(&svc, export) {
			return false, "service not controlled by the export"
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bk-export"}, &sec); err != nil {
			return false, "TLS secret not yet created: " + err.Error()
		}
		if !metav1IsControlledBy(&sec, export) {
			return false, "TLS secret not controlled by the export"
		}
		if len(sec.Data["ca.crt"]) == 0 || len(sec.Data[corev1.TLSCertKey]) == 0 || len(sec.Data[corev1.TLSPrivateKeyKey]) == 0 {
			return false, "TLS secret missing cert, key or ca.crt"
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		got := getBackupExport(t, ns, "bk-export")
		if got.Status.ServiceName != "bk-export" || got.Status.SnapshotID != "snap-1" {
			return false, "status not observed yet"
		}
		if got.Status.Phase != gameplanev1alpha1.BackupExportPhasePending {
			return false, "phase = " + string(got.Status.Phase)
		}
		return true, ""
	})
}

// TestBackupExport_VolumeSnapshotFails verifies a CSI snapshot Backup cannot
// be exported.
func TestBackupExport_VolumeSnapshotFails(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupExportReconciler(t, ns))

	ctx := context.Background()
	if err := k8sClient.Create(ctx, buildVolumeSnapshotBackup(ns, "vs", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceededVolumeSnapshot(t, ns, "vs", "vs", "snapcontent-vs")
	b := getBackup(t, ns, "vs")

	if err := k8sClient.Create(ctx, buildBackupExport(ns, "vs-export", "vs", string(b.UID), "zip")); err != nil {
		t.Fatalf("create backupexport: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackupExport(t, ns, "vs-export")
		if got.Status.Phase != gameplanev1alpha1.BackupExportPhaseFailed {
			return false, "phase = " + string(got.Status.Phase)
		}
		if got.Status.Message != "snapshot backups cannot be downloaded" {
			return false, "message = " + got.Status.Message
		}
		return true, ""
	})
}

// TestBackupExport_ReplacedBackupFails verifies an export pinned to a previous
// Backup UID is refused and creates no worker.
func TestBackupExport_ReplacedBackupFails(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupExportReconciler(t, ns))

	ctx := context.Background()
	if err := k8sClient.Create(ctx, buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create repo secret: %v", err)
	}
	if err := k8sClient.Create(ctx, buildBackup(ns, "bk2", "smp", "repo")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceeded(t, ns, "bk2", "snap-2", "1Mi")

	if err := k8sClient.Create(ctx, buildBackupExport(ns, "bk2-export", "bk2", "stale-uid", "tar.gz")); err != nil {
		t.Fatalf("create backupexport: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getBackupExport(t, ns, "bk2-export")
		if got.Status.Phase != gameplanev1alpha1.BackupExportPhaseFailed {
			return false, "phase = " + string(got.Status.Phase)
		}
		if got.Status.Message != "backup was replaced" {
			return false, "message = " + got.Status.Message
		}
		return true, ""
	})

	consistently(t, 500*time.Millisecond, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bk2-export"}, &pod); err == nil {
			return false, "worker Pod created for a replaced backup"
		}
		return true, ""
	})
}

// metav1IsControlledBy reports whether obj's controller reference points at owner.
func metav1IsControlledBy(obj metav1.Object, owner metav1.Object) bool {
	ref := metav1.GetControllerOf(obj)
	return ref != nil && ref.UID == owner.GetUID() && ref.Kind == "BackupExport"
}
