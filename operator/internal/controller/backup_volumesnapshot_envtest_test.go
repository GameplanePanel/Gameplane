//go:build envtest

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestBackup_VolumeSnapshotSucceeds drives the full volume-snapshot backup
// path: the reconciler creates a VolumeSnapshot of the data PVC, the (faked)
// CSI driver reports readyToUse, and the Backup records the snapshot
// identity + size. No restic Job is ever created.
func TestBackup_VolumeSnapshotSucceeds(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-vs", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	// A VolumeSnapshot of <gs>-data, owned by the Backup, appears and the
	// Backup goes Running while it provisions.
	eventually(t, func() (bool, string) {
		vs, ok := getVolumeSnapshot(t, ns, "smp-vs")
		if !ok {
			return false, "volumesnapshot not yet created"
		}
		if vs.Spec.Source.PersistentVolumeClaimName == nil ||
			*vs.Spec.Source.PersistentVolumeClaimName != "smp-data" {
			return false, "snapshot source PVC wrong"
		}
		if len(vs.OwnerReferences) == 0 || vs.OwnerReferences[0].Kind != "Backup" {
			return false, "snapshot not owned by Backup"
		}
		got := getBackup(t, ns, "smp-vs")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseRunning {
			return false, describeBackupStatus(got)
		}
		return true, ""
	})

	// Simulate the CSI driver completing the snapshot.
	markVolumeSnapshotReady(t, ns, "smp-vs", "snapcontent-smp-vs", "5Gi")

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-vs")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
			return false, describeBackupStatus(got)
		}
		if got.Status.SnapshotID != "smp-vs" {
			return false, "snapshotID = " + got.Status.SnapshotID
		}
		if got.Status.VolumeSnapshotContentName != "snapcontent-smp-vs" {
			return false, "contentName = " + got.Status.VolumeSnapshotContentName
		}
		if got.Status.Size == nil {
			return false, "size not recorded"
		}
		return true, ""
	})

	// The volume-snapshot path must never create a restic Job.
	consistently(t, 750*time.Millisecond, func() (bool, string) {
		if _, ok := getJob(t, ns, "smp-vs"); ok {
			return false, "restic Job created for a volume-snapshot backup"
		}
		return true, ""
	})
}

// TestBackup_VolumeSnapshotErrorFails verifies a CSI snapshot error flips
// the Backup to Failed with the driver's message surfaced.
func TestBackup_VolumeSnapshotErrorFails(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withBackupReconciler())
	seedGameServer(t, ns, "smp")

	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-vs", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	eventually(t, func() (bool, string) {
		if _, ok := getVolumeSnapshot(t, ns, "smp-vs"); !ok {
			return false, "snapshot not created"
		}
		return true, ""
	})

	markVolumeSnapshotError(t, ns, "smp-vs", "csi driver exploded")

	eventually(t, func() (bool, string) {
		got := getBackup(t, ns, "smp-vs")
		if got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
			return false, describeBackupStatus(got)
		}
		if !strings.Contains(got.Status.Message, "csi driver exploded") {
			return false, "message = " + got.Status.Message
		}
		return true, ""
	})
}

// TestRestore_VolumeSnapshotProvisionsNewServer verifies a Restore of a
// volume-snapshot Backup stands up a NEW GameServer (copying the original's
// spec, with storage.dataSource → snapshot) and never touches the original.
func TestRestore_VolumeSnapshotProvisionsNewServer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	// Original server — its spec is the template for the restored copy.
	orig := buildGameServer(ns, "smp", "mc-template")
	orig.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{Size: resource.MustParse("12Gi")}
	if err := k8sClient.Create(context.Background(), orig); err != nil {
		t.Fatalf("create original gs: %v", err)
	}

	// A succeeded volume-snapshot Backup of the original.
	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-bk", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceededVolumeSnapshot(t, ns, "smp-bk", "smp-bk", "snapcontent-x")

	// Restore into a NEW server name.
	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs", "smp-bk", "smp-restored")); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-restored"}, &gs); err != nil {
			return false, "restored server not yet created: " + err.Error()
		}
		if gs.Spec.TemplateRef.Name != "mc-template" {
			return false, "spec not copied from original"
		}
		if gs.Spec.Storage == nil || gs.Spec.Storage.DataSource == nil {
			return false, "dataSource not set on restored server"
		}
		if gs.Spec.Storage.DataSource.Name != "smp-bk" {
			return false, "dataSource name = " + gs.Spec.Storage.DataSource.Name
		}
		return true, ""
	})

	// The original must be untouched (never suspended).
	if getGameServer(t, ns, "smp").Spec.Suspend {
		t.Fatalf("original server was suspended by a volume-snapshot restore")
	}

	// Once the restored server reports Running, the Restore succeeds.
	markGameServerPhase(t, ns, "smp-restored", gameplanev1alpha1.GameServerPhaseRunning)
	eventually(t, func() (bool, string) {
		got := getRestore(t, ns, "rs")
		if got.Status.Phase != gameplanev1alpha1.RestorePhaseSucceeded {
			return false, describeRestoreStatus(got)
		}
		return true, ""
	})
}

// TestRestore_VolumeSnapshotRejectsExistingTarget verifies a volume-snapshot
// restore refuses to clobber an existing server: the target name must be
// free (these restores create a new server, never overwrite one).
func TestRestore_VolumeSnapshotRejectsExistingTarget(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", "mc-template")); err != nil {
		t.Fatalf("create original: %v", err)
	}
	// A pre-existing server occupying the target name.
	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp-restored", "mc-template")); err != nil {
		t.Fatalf("create existing: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-bk", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	markBackupSucceededVolumeSnapshot(t, ns, "smp-bk", "smp-bk", "snapcontent-x")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs", "smp-bk", "smp-restored")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	eventually(t, func() (bool, string) {
		got := getRestore(t, ns, "rs")
		if got.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(got)
		}
		if !strings.Contains(got.Status.Message, "already exists") {
			return false, "message = " + got.Status.Message
		}
		return true, ""
	})
}

// TestGameServer_DataSourceSeedsPVC verifies reconcilePVC stamps the data
// PVC's dataSource with the referenced CSI VolumeSnapshot when the server
// sets storage.dataSource — the mechanism a volume-snapshot restore relies
// on.
func TestGameServer_DataSourceSeedsPVC(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{
		DataSource: &gameplanev1alpha1.GameDataSource{Kind: "VolumeSnapshot", Name: "snap-1"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, err.Error()
		}
		ds := pvc.Spec.DataSource
		if ds == nil {
			return false, "PVC dataSource nil"
		}
		if ds.Kind != "VolumeSnapshot" || ds.Name != "snap-1" {
			return false, fmt.Sprintf("dataSource = %+v", ds)
		}
		if ds.APIGroup == nil || *ds.APIGroup != "snapshot.storage.k8s.io" {
			return false, "dataSource APIGroup wrong"
		}
		return true, ""
	})
}
