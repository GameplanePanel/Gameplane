package controller

import (
	"reflect"
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBackupManifestPinsCoverageAndClassification(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	_ = gameplanev1alpha1.AddToScheme(scheme)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "game-uid"}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "template"}}}
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template"}, Spec: gameplanev1alpha1.GameTemplateSpec{Storage: gameplanev1alpha1.GameStorageSpec{
		Extra: []gameplanev1alpha1.ExtraVolumeSpec{{Name: "install", MountPath: "/serverfiles", BackupRole: "rebuildable"}},
	}}}
	b := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "copy", Namespace: "games", UID: "backup-uid", Annotations: map[string]string{annoQuiesceAttempted: "unsupported"}}, Spec: gameplanev1alpha1.BackupSpec{ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gs.Name}, Quiesce: true}}
	owner := *metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{owner}},
		Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "game", VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/world"}, {Name: "extra-install", MountPath: "/serverfiles"}, {Name: "mods", MountPath: "/world/mods"}}}},
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "game-data"}}},
				{Name: "extra-install", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "game-extra-install"}}},
				{Name: "mods", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "game-mods-active"}}},
			},
		}}},
	}
	objects := []client.Object{gs, tmpl, b, ss}
	for _, name := range []string{"game-data", "game-extra-install", "game-mods-active", "game-mods-old"} {
		objects = append(objects, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, UID: types.UID("uid-" + name), OwnerReferences: []metav1.OwnerReference{owner}}})
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(b).WithObjects(objects...).Build()
	r := &BackupReconciler{Client: c, APIReader: c, Scheme: scheme}
	if err := r.recordBackupManifest(t.Context(), b, gs); err != nil {
		t.Fatal(err)
	}
	manifest := b.Status.CaptureManifest
	if manifest == nil || manifest.State != "Recorded" || manifest.QuiesceOutcome != "unsupported" || len(manifest.Volumes) != 4 {
		t.Fatalf("incomplete manifest: %+v", manifest)
	}
	byName := map[string]gameplanev1alpha1.BackupVolumeCoverage{}
	for _, volume := range manifest.Volumes {
		byName[volume.ClaimName] = volume
	}
	if !byName["game-data"].Included || byName["game-data"].ClaimUID != "uid-game-data" || !reflect.DeepEqual(byName["game-data"].MountPaths, []string{"/world"}) {
		t.Fatalf("primary coverage: %+v", byName["game-data"])
	}
	if byName["game-extra-install"].Included || byName["game-extra-install"].Classification != "rebuildable" || byName["game-mods-active"].Included || byName["game-mods-old"].Included {
		t.Fatalf("extra or nested mod volume incorrectly covered: %+v", manifest.Volumes)
	}
	before := manifest.DeepCopy()
	tmpl.Spec.Storage.Extra[0].BackupRole = "data"
	if err := c.Update(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	if err := r.recordBackupManifest(t.Context(), b, gs); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, b.Status.CaptureManifest) {
		t.Fatal("template change rewrote capture manifest")
	}
}

func TestBackupManifestUsesEffectiveTemplateClassification(t *testing.T) {
	r, gs, installed := revisionFixture(t)
	installed.Spec.Storage.Extra = []gameplanev1alpha1.ExtraVolumeSpec{{Name: "world", MountPath: "/world-extra", BackupRole: "data"}}
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	pin, err := r.saveTemplateSnapshot(t.Context(), gs, installed)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = pin
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	installed.Spec.Storage.Extra[0].BackupRole = "rebuildable"
	if err := r.Update(t.Context(), installed); err != nil {
		t.Fatal(err)
	}
	b := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "capture", Namespace: gs.Namespace}}
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: "game", VolumeMounts: []corev1.VolumeMount{{Name: "world", MountPath: "/world-extra"}}}},
		Volumes:    []corev1.Volume{{Name: "world", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "game-extra-world"}}}},
	}}}}
	for _, obj := range []client.Object{b, ss} {
		if err := r.Create(t.Context(), obj); err != nil {
			t.Fatal(err)
		}
	}
	backups := &BackupReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme}
	if err := backups.recordBackupManifest(t.Context(), b, gs); err != nil {
		t.Fatal(err)
	}
	for _, volume := range b.Status.CaptureManifest.Volumes {
		if volume.ClaimName == "game-extra-world" && volume.Classification == "data" {
			return
		}
	}
	t.Fatal("pending catalog revision changed effective volume classification")
}

func TestBackupManifestDoesNotInventHistoryForExistingWorker(t *testing.T) {
	for _, strategy := range []string{"restic-snapshot", "volume-snapshot"} {
		t.Run(strategy, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = batchv1.AddToScheme(scheme)
			_ = snapshotv1.AddToScheme(scheme)
			_ = gameplanev1alpha1.AddToScheme(scheme)
			b := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "games"}, Spec: gameplanev1alpha1.BackupSpec{Strategy: strategy}}
			var worker client.Object = &batchv1.Job{ObjectMeta: b.ObjectMeta}
			if strategy == "volume-snapshot" {
				worker = &snapshotv1.VolumeSnapshot{ObjectMeta: b.ObjectMeta}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(b).WithObjects(b, worker).Build()
			r := &BackupReconciler{Client: c, APIReader: c, Scheme: scheme}
			if err := r.recordBackupManifest(t.Context(), b, &gameplanev1alpha1.GameServer{}); err != nil {
				t.Fatal(err)
			}
			if b.Status.CaptureManifest.State != "LegacyUnknown" || len(b.Status.CaptureManifest.Volumes) != 0 {
				t.Fatalf("invented history: %+v", b.Status.CaptureManifest)
			}
		})
	}
}

func TestBackupConsistencyDescribesAchievedResult(t *testing.T) {
	for _, tc := range []struct {
		strategy, outcome, reason string
		status                    metav1.ConditionStatus
	}{
		{"restic-snapshot", "confirmed", "Quiesced", metav1.ConditionTrue},
		{"restic-snapshot", "already-stopped", "StoppedSource", metav1.ConditionTrue},
		{"restic-snapshot", "unsupported", "UnsupportedRawCopy", metav1.ConditionFalse},
		{"restic-snapshot", "not-requested", "RawCopy", metav1.ConditionFalse},
		{"restic-snapshot", "agent-unavailable", "AgentUnavailableRawCopy", metav1.ConditionFalse},
		{"volume-snapshot", "unsupported", "CrashConsistentSnapshot", metav1.ConditionFalse},
		{"volume-snapshot", "confirmed", "Quiesced", metav1.ConditionTrue},
		{"volume-snapshot", "already-stopped", "StoppedSource", metav1.ConditionTrue},
	} {
		t.Run(tc.strategy+"/"+tc.outcome, func(t *testing.T) {
			b := &gameplanev1alpha1.Backup{Status: gameplanev1alpha1.BackupStatus{Phase: gameplanev1alpha1.BackupPhaseSucceeded, SnapshotID: "snapshot-id",
				CaptureManifest: &gameplanev1alpha1.BackupCaptureManifest{State: "Recorded", Strategy: tc.strategy, QuiesceOutcome: tc.outcome}}}
			condition := backupConsistencyCondition(b)
			if condition.Reason != tc.reason || condition.Status != tc.status {
				t.Fatalf("got %+v", condition)
			}
			b.Status.SnapshotID = ""
			if condition := backupConsistencyCondition(b); condition.Reason != "CapturePending" || condition.Status != metav1.ConditionUnknown {
				t.Fatalf("copy without restorable identity claims achieved consistency: %+v", condition)
			}
			b.Status.SnapshotID = "snapshot-id"
			b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
			if condition := backupConsistencyCondition(b); condition.Status != metav1.ConditionUnknown {
				t.Fatalf("unfinished copy claims achieved consistency: %+v", condition)
			}
			b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
			if condition := backupConsistencyCondition(b); condition.Reason != "CaptureFailed" || condition.Status != metav1.ConditionFalse {
				t.Fatalf("failed copy claims usable result: %+v", condition)
			}
		})
	}
	b := &gameplanev1alpha1.Backup{Status: gameplanev1alpha1.BackupStatus{Phase: gameplanev1alpha1.BackupPhaseSucceeded}}
	if condition := backupConsistencyCondition(b); condition.Status != metav1.ConditionUnknown || condition.Reason != "LegacyMetadataUnknown" {
		t.Fatalf("legacy backup guarantee invented: %+v", condition)
	}
}
