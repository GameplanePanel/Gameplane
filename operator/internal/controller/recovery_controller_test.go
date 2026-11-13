package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func recoveryFixture(t *testing.T) (client.Client, *v1.GameServer, *v1.Backup, *v1.Restore) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	gs := &v1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "game-uid", Generation: 3, Annotations: map[string]string{}}, Spec: v1.GameServerSpec{TemplateRef: v1.GameTemplateRef{Name: "template"}, Version: "v1"}}
	tmpl := &v1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: "template"}, Spec: v1.GameTemplateSpec{Storage: v1.GameStorageSpec{MountPath: "/data", Extra: []v1.ExtraVolumeSpec{{Name: "world", MountPath: "/world", BackupRole: "data"}, {Name: "cache", MountPath: "/cache", BackupRole: "rebuildable"}}}, Versions: []v1.GameVersion{{ID: "v1", Loader: "paper", Image: "busybox"}}, Capabilities: &v1.CapabilitiesSpec{Mods: &v1.ModsSpec{Loaders: map[string]v1.ModLoaderSpec{"paper": {Path: "mods"}}}}}}
	one := int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload", Generation: 1, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, v1.GroupVersion.WithKind("GameServer"))}}, Spec: appsv1.StatefulSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game"}}}}}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 1}}
	objects := []client.Object{gs, tmpl, ss}
	for i, pair := range []struct{ name, mount string }{{"data", "/data"}, {"extra-world", "/world"}, {"extra-cache", "/cache"}, {"mods-v1", "/data/mods"}} {
		name := gs.Name + "-" + pair.name
		ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes, corev1.Volume{Name: pair.name, VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name}}})
		ss.Spec.Template.Spec.Containers[0].VolumeMounts = append(ss.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: pair.name, MountPath: pair.mount})
		objects = append(objects, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, UID: types.UID(name + "-uid"), Finalizers: []string{"kubernetes.io/pvc-protection"}}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: []string{"pv0", "pv1", "pv2", "pv3"}[i]}, Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound}})
	}
	b := &v1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: gs.Namespace, UID: "backup-uid", Generation: 1, Annotations: map[string]string{captureFormatAnnotation: "all-data-v2"}}, Spec: v1.BackupSpec{ServerRef: v1.LocalObjectRef{Name: gs.Name}, RepoRef: &v1.SecretKeySelector{Name: "repo"}, VolumePolicy: "all-data", PowerPolicy: "stop-copy-restart"}, Status: v1.BackupStatus{Phase: v1.BackupPhaseSucceeded, SnapshotID: "snapshot"}}
	rs := &v1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: gs.Namespace, UID: "restore-uid", Annotations: map[string]string{restoreFormatAnnotation: "all-data-v2"}}, Spec: v1.RestoreSpec{ServerRef: v1.LocalObjectRef{Name: gs.Name}, BackupRef: v1.LocalObjectRef{Name: b.Name}}, Status: v1.RestoreStatus{SnapshotID: "snapshot", Phase: v1.RestorePhaseSuspending}}
	objects = append(objects, b, rs)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(gs, b, rs).WithObjects(objects...).Build()
	m, err := recoveryLayout(t.Context(), c, gs)
	if err != nil {
		t.Fatal(err)
	}
	b.Status.CaptureManifest = m
	if err := c.Status().Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	return c, gs, b, rs
}

func TestRecoveryLayoutIncludesDataAndSeparateMods(t *testing.T) {
	c, gs, b, _ := recoveryFixture(t)
	included, excluded := 0, 0
	for _, v := range b.Status.CaptureManifest.Volumes {
		if v.Included {
			included++
		} else {
			excluded++
			if v.RecoveryPrerequisite == "" {
				t.Fatal("missing rebuild prerequisite")
			}
		}
	}
	if included != 3 || excluded != 1 {
		t.Fatalf("coverage: %d/%d", included, excluded)
	}
	var tmpl v1.GameTemplate
	if err := c.Get(t.Context(), client.ObjectKey{Name: "template"}, &tmpl); err != nil {
		t.Fatal(err)
	}
	tmpl.Spec.Storage.Extra[0].BackupRole = ""
	if err := c.Update(t.Context(), &tmpl); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryLayout(t.Context(), c, gs); err == nil {
		t.Fatal("unclassified required claim admitted")
	}
}

func TestRecoveryPreflightFailsBeforeSuspension(t *testing.T) {
	for _, mutation := range []string{"missing", "replaced", "path", "traversal", "duplicate", "downgrade"} {
		t.Run(mutation, func(t *testing.T) {
			c, gs, b, rs := recoveryFixture(t)
			switch mutation {
			case "missing", "replaced":
				var pvc corev1.PersistentVolumeClaim
				key := client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-extra-world"}
				if err := c.Get(t.Context(), key, &pvc); err != nil {
					t.Fatal(err)
				}
				pvc.Finalizers = nil
				if err := c.Update(t.Context(), &pvc); err != nil {
					t.Fatal(err)
				}
				if err := c.Delete(t.Context(), &pvc); err != nil {
					t.Fatal(err)
				}
				if mutation == "replaced" {
					pvc.ResourceVersion = ""
					pvc.UID = "replacement"
					if err := c.Create(t.Context(), &pvc); err != nil {
						t.Fatal(err)
					}
				}
			case "path":
				var ss appsv1.StatefulSet
				if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
					t.Fatal(err)
				}
				ss.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath = "/redirect"
				if err := c.Update(t.Context(), &ss); err != nil {
					t.Fatal(err)
				}
			case "traversal":
				b.Status.CaptureManifest.Volumes[0].SnapshotRoot = "/volumes/../data"
			case "duplicate":
				b.Status.CaptureManifest.Volumes = append(b.Status.CaptureManifest.Volumes, b.Status.CaptureManifest.Volumes[0])
			case "downgrade":
				b.Status.CaptureManifest = nil
			}
			if err := c.Status().Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rs)}); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Spec.Suspend || gs.Annotations[restoreGuardAnnotation] != "" {
				t.Fatal("invalid restore mutated target power/ownership")
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Phase != v1.RestorePhaseFailed {
				t.Fatalf("expected visible failure: %+v", rs.Status)
			}
			var jobs batchv1.JobList
			if err := c.List(t.Context(), &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 0 {
				t.Fatal("invalid restore created destructive job")
			}
		})
	}
}
func TestRecoveryPlanPinsTargetAgainstReplacement(t *testing.T) {
	c, gs, b, rs := recoveryFixture(t)
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if changed, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil || !changed {
		t.Fatalf("preflight: %v %v", changed, err)
	}
	before := rs.Status.RecoveryPlan.DeepCopy()
	var pvc corev1.PersistentVolumeClaim
	key := client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-mods-v1"}
	if err := c.Get(t.Context(), key, &pvc); err != nil {
		t.Fatal(err)
	}
	pvc.UID = "replacement"
	if err := c.Update(t.Context(), &pvc); err != nil {
		t.Fatal(err)
	}
	if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err == nil {
		t.Fatal("replacement admitted")
	}
	if before.Volumes[0].ClaimUID != rs.Status.RecoveryPlan.Volumes[0].ClaimUID {
		t.Fatal("mapping rewritten")
	}
}
func TestRecoveryMountedClaimsRequireProtection(t *testing.T) {
	c, gs, b, _ := recoveryFixture(t)
	if err := validateMountedRecoveryClaims(t.Context(), c, gs.Namespace, b.Status.CaptureManifest.Volumes); err != nil {
		t.Fatal(err)
	}
	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-data"}, &pvc); err != nil {
		t.Fatal(err)
	}
	pvc.Finalizers = nil
	if err := c.Update(t.Context(), &pvc); err != nil {
		t.Fatal(err)
	}
	if err := validateMountedRecoveryClaims(t.Context(), c, gs.Namespace, b.Status.CaptureManifest.Volumes); err == nil {
		t.Fatal("unprotected mounted claim admitted")
	}
}
func TestRecoveryAntiDowngradeAndConsistency(t *testing.T) {
	_, _, b, _ := recoveryFixture(t)
	b.Status.CaptureManifest.QuiesceOutcome = "confirmed"
	if backupConsistencyCondition(b).Status != metav1.ConditionFalse {
		t.Fatal("live all-data overclaimed atomic consistency")
	}
	b.Status.CaptureManifest.QuiesceOutcome = "already-stopped"
	if backupConsistencyCondition(b).Status != metav1.ConditionTrue {
		t.Fatal("drained all-data not recognized")
	}
	for _, state := range []string{"LegacyUnknown", "Unknown"} {
		candidate := b.DeepCopy()
		candidate.Status.CaptureManifest.State = state
		if _, err := recoveryCaptureMode(candidate); err == nil {
			t.Fatal("unknown manifest downgraded")
		}
	}
	legacy := &v1.Backup{}
	if all, err := recoveryCaptureMode(legacy); err != nil || all {
		t.Fatal("legacy primary restore rejected")
	}
}
func TestStoppedOwnerSurvivesRestartAndForceDeletion(t *testing.T) {
	for _, stage := range []string{"Admitted", "Stopping", "Drained"} {
		t.Run(stage, func(t *testing.T) {
			c, gs, b, _ := recoveryFixture(t)
			debt := newStoppedBackup(gs)
			debt.Stage = stage
			debt.WorkloadUID = "workload"
			debt.Deadline = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
			debt.Claims = b.Status.CaptureManifest.Volumes
			op := lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, b, false)}
			op.StoppedBackup = debt
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			gs.Annotations[backupGuardAnnotation] = backupIdentityJSON(b.Name, b.UID)
			gs.Spec.Suspend = true // A later user stop must survive recovery.
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := c.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if changed, err := r.recoverStoppedBackupOwner(t.Context(), gs); err != nil || !changed {
				t.Fatalf("orphan recovery: %v %v", changed, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] != "" || !gs.Spec.Suspend {
				t.Fatal("orphan recovery lost cleanup or user suspend")
			}
		})
	}
}
func TestStoppedOwnerRejectsAmbiguousWorkload(t *testing.T) {
	c, gs, b, _ := recoveryFixture(t)
	debt := newStoppedBackup(gs)
	debt.Stage = "Drained"
	debt.WorkloadUID = "replacement"
	debt.Deadline = time.Now().Format(time.RFC3339Nano)
	debt.Claims = b.Status.CaptureManifest.Volumes
	if ready, err := stoppedBackupCleanup(t.Context(), c, c, gs, debt); ready || err == nil || !strings.Contains(err.Error(), "intervention") {
		t.Fatalf("ambiguous provenance: %v %v", ready, err)
	}
}

func TestRecoveryInterventionClearsAfterStoppedBackupCleanup(t *testing.T) {
	for _, orphan := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "orphan"}[orphan], func(t *testing.T) {
			c, gs, b, _ := recoveryFixture(t)
			b.Annotations[backupTargetAnnotation] = backupIdentityJSON(gs.Name, gs.UID)
			if err := c.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			op := lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, b, false)}
			op.StoppedBackup.Stage = "Drained"
			op.StoppedBackup.WorkloadUID = "unverified-workload"
			op.StoppedBackup.Deadline = time.Now().Format(time.RFC3339Nano)
			op.StoppedBackup.Claims = b.Status.CaptureManifest.Volumes
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			gs.Annotations[backupGuardAnnotation] = backupIdentityJSON(b.Name, b.UID)
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			gs.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}}
			if err := c.Status().Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			gr := &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			br := &BackupReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			cleanup := func(wantError string) {
				t.Helper()
				var err error
				if orphan {
					_, err = gr.recoverStoppedBackupOwner(t.Context(), gs)
					wantError = "" // Orphan intervention is reported through status.
				} else {
					err = br.releaseBackupTarget(t.Context(), b)
				}
				if wantError == "" && err != nil {
					t.Fatal(err)
				}
				if wantError != "" && (err == nil || !strings.Contains(err.Error(), wantError)) {
					t.Fatalf("cleanup error = %v, want %q", err, wantError)
				}
				if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
					t.Fatal(err)
				}
			}
			if orphan {
				if err := c.Delete(t.Context(), b); err != nil {
					t.Fatal(err)
				}
			}
			cleanup("intervention")
			if !meta.IsStatusConditionTrue(gs.Status.Conditions, "RecoveryIntervention") || gs.Annotations[lifecycleOperationAnnotation] == "" {
				t.Fatal("unverified workload did not retain intervention and ownership")
			}
			op.StoppedBackup.WorkloadUID = "workload"
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := c.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			writer := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "remaining-writer", Namespace: gs.Namespace}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "world", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-extra-world"}}}}}}
			if err := c.Create(t.Context(), writer); err != nil {
				t.Fatal(err)
			}
			cleanup("waiting for stopped source and workers to drain")
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || changed {
				t.Fatalf("intervention changed before writer drainage: %v %v", changed, err)
			}
			if !meta.IsStatusConditionTrue(gs.Status.Conditions, "RecoveryIntervention") || gs.Annotations[lifecycleOperationAnnotation] == "" {
				t.Fatal("live writer did not retain intervention and ownership")
			}
			if err := c.Delete(t.Context(), writer); err != nil {
				t.Fatal(err)
			}
			cleanup("")
			if gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[backupGuardAnnotation] != "" {
				t.Fatal("completed cleanup retained ownership")
			}
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || !changed {
				t.Fatalf("clear completed intervention: %v %v", changed, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if meta.FindStatusCondition(gs.Status.Conditions, "RecoveryIntervention") != nil || !meta.IsStatusConditionTrue(gs.Status.Conditions, "Ready") {
				t.Fatal("completed cleanup retained intervention or removed unrelated status")
			}
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || changed {
				t.Fatalf("clean recovery did not settle: %v %v", changed, err)
			}
		})
	}
}

func TestStoppedPredecessorClaimsSurviveSuccessorAdmission(t *testing.T) {
	c, gs, b, _ := recoveryFixture(t)
	debt := newStoppedBackup(gs)
	debt.Stage = "Drained"
	debt.WorkloadUID = "workload"
	debt.Deadline = time.Now().Add(time.Minute).Format(time.RFC3339Nano)
	debt.Claims = b.Status.CaptureManifest.Volumes
	predecessor := backupLifecycleIdentity(gs, b, false)
	predecessor.StoppedBackup = debt
	successor := b.DeepCopy()
	successor.Name = "successor"
	successor.UID = "successor-uid"
	op := lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, successor, false), Recovery: []lifecycleIdentity{predecessor}}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
	if err := c.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	ss.Spec.Replicas = &zero
	if err := c.Update(t.Context(), &ss); err != nil {
		t.Fatal(err)
	}
	writer := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "extra-writer", Namespace: gs.Namespace}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "extra", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-extra-world"}}}}}}
	if err := c.Create(t.Context(), writer); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if ready, err := stoppedBackupCleanup(t.Context(), c, c, gs, debt); err != nil || ready {
			t.Fatalf("predecessor writer ignored: %v %v", ready, err)
		}
		op.Recovery = append(op.Recovery, op.lifecycleIdentity)
		op.UID = types.UID("another-successor")
		gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
		if err := c.Update(t.Context(), gs); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Delete(t.Context(), writer); err != nil {
		t.Fatal(err)
	}
	if ready, err := stoppedBackupCleanup(t.Context(), c, c, gs, debt); err != nil || !ready {
		t.Fatalf("drained predecessor retained: %v %v", ready, err)
	}
}
func TestStoppedPredecessorStageControlsGracefulRecovery(t *testing.T) {
	for _, stage := range []string{"Admitted", "Stopping", "Drained"} {
		op := lifecycleOperation{Recovery: []lifecycleIdentity{{Kind: "Backup", StoppedBackup: &stoppedBackup{Stage: stage}}}}
		if (lifecycleStoppingBackup(op) != nil) != (stage == "Stopping") {
			t.Fatalf("stage %s lost graceful debt", stage)
		}
		if lifecycleRecoveryNeedsStop(op) != (stage == "Drained") {
			t.Fatalf("stage %s incorrectly forced zero", stage)
		}
	}
}
func TestStaleRunningReplicaDecisionCannotRestartStoppedCapture(t *testing.T) {
	for _, stage := range []string{"Stopping", "Drained"} {
		t.Run(stage, func(t *testing.T) {
			c, gs, b, _ := recoveryFixture(t)
			stale := gs.DeepCopy()
			op := lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, b, false)}
			op.StoppedBackup.Stage = stage
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := c.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			tmpl, err := effectiveGameTemplate(t.Context(), c, gs)
			if err != nil {
				t.Fatal(err)
			}
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if err := r.reconcileStatefulSet(t.Context(), stale, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
				t.Fatal("stale reconcile restarted captured workload")
			}
		})
	}
}
func TestRecoveryRestorePreservesPowerDuringAdmissionAndSuccess(t *testing.T) {
	for _, mode := range []string{"running", "suspended", "idle"} {
		t.Run(mode, func(t *testing.T) {
			c, gs, b, rs := recoveryFixture(t)
			gs.Spec.Suspend = mode == "suspended"
			if mode == "idle" {
				gs.Annotations[IdleAsleepSinceAnnotation] = "2026-01-01T00:00:00Z"
			}
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil {
				t.Fatal(err)
			}
			if _, err := r.bindRestoreTarget(t.Context(), rs, gs); err != nil {
				t.Fatal(err)
			}
			if claimed, err := r.claimRestoreTarget(t.Context(), rs, gs); err != nil || !claimed {
				t.Fatalf("claim: %v %v", claimed, err)
			}
			if gs.Spec.Suspend != (mode == "suspended") {
				t.Fatal("admission overwrote user power intent")
			}
			if mode == "idle" && gs.Annotations[IdleAsleepSinceAnnotation] == "" {
				t.Fatal("admission cleared idle intent")
			}
			rs.Status.Phase = v1.RestorePhaseSucceeded
			if err := c.Status().Update(t.Context(), rs); err != nil {
				t.Fatal(err)
			}
			if _, err := r.cleanupRestore(t.Context(), rs); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Spec.Suspend != (mode == "suspended") {
				t.Fatal("successful recovery overwrote power intent")
			}
			if mode == "idle" && gs.Annotations[IdleAsleepSinceAnnotation] == "" {
				t.Fatal("successful recovery woke idle game")
			}
		})
	}
}
func TestRecoveryMissingRebuildableIsExplicitPrerequisite(t *testing.T) {
	c, gs, b, rs := recoveryFixture(t)
	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-extra-cache"}, &pvc); err != nil {
		t.Fatal(err)
	}
	pvc.Finalizers = nil
	if err := c.Update(t.Context(), &pvc); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(t.Context(), &pvc); err != nil {
		t.Fatal(err)
	}
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil {
		t.Fatal(err)
	}
	for _, volume := range rs.Status.RecoveryPlan.Volumes {
		if volume.LogicalName == "extra-cache" {
			if volume.Included || volume.ClaimUID != "" || volume.RecoveryPrerequisite == "" {
				t.Fatal("missing rebuildable silently treated as covered")
			}
			return
		}
	}
	t.Fatal("missing prerequisite from plan")
}
func TestFailedRecoveryWorkerDoesNotWaitForReadinessDeadline(t *testing.T) {
	c, gs, b, rs := recoveryFixture(t)
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil {
		t.Fatal(err)
	}
	rs.Annotations[restoreDeadlineAnnotation] = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	job := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}
	if ready, err := r.authorizeRecoveryRestore(t.Context(), rs, b, gs, job); ready || err == nil || !strings.Contains(err.Error(), "job reported Failed") {
		t.Fatalf("failed job hidden by readiness: %v %v", ready, err)
	}
}

func TestStoppedCopyFailureAndCancellationWaitForWorkers(t *testing.T) {
	for _, reason := range []string{"copy failed", "backup cancelled", "preparation deadline expired"} {
		t.Run(reason, func(t *testing.T) {
			c, gs, b, _ := recoveryFixture(t)
			b.Status.Phase = v1.BackupPhaseRunning
			b.Status.SnapshotID = ""
			if err := c.Status().Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			b.Annotations[backupTargetAnnotation] = backupIdentityJSON(gs.Name, gs.UID)
			b.Annotations[backupPreparationDeadlineAnnotation] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
			if err := c.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			debt := newStoppedBackup(gs)
			debt.Stage = "Drained"
			debt.WorkloadUID = "workload"
			debt.Deadline = b.Annotations[backupPreparationDeadlineAnnotation]
			debt.Claims = b.Status.CaptureManifest.Volumes
			op := lifecycleOperation{lifecycleIdentity: backupLifecycleIdentity(gs, b, false)}
			op.StoppedBackup = debt
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			gs.Annotations[backupGuardAnnotation] = backupIdentityJSON(b.Name, b.UID)
			gs.Spec.Suspend = true
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			zero := int32(0)
			ss.Spec.Replicas = &zero
			if err := c.Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "copy-worker", Namespace: gs.Namespace, Labels: map[string]string{backupUIDLabel: string(b.UID)}}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-data"}}}}}}
			if err := c.Create(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			r := &BackupReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if result, err := r.abortResticCopy(t.Context(), b, reason); err != nil || result.RequeueAfter == 0 {
				t.Fatalf("worker drain: %v %v", result, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] == "" {
				t.Fatal("failure released live reader")
			}
			if err := c.Delete(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			// A new reconciler instance proves the persisted abort and stop debt are sufficient.
			r = &BackupReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] != "" || !gs.Spec.Suspend {
				t.Fatal("failure cleanup lost release or newer suspend intent")
			}
		})
	}
}
func TestRecoveryAuthorizationRejectsPostPreparationClaimReplacement(t *testing.T) {
	c, gs, b, rs := recoveryFixture(t)
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil {
		t.Fatal(err)
	}
	rs.Annotations[restoreDeadlineAnnotation] = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if err := c.Update(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	gate, err := r.restoreRecoveryGate(t.Context(), rs)
	if err != nil {
		t.Fatal(err)
	}
	gate.Data[backupStartTokenKey] = "old authorization"
	if err := c.Update(t.Context(), gate); err != nil {
		t.Fatal(err)
	}
	var pvc corev1.PersistentVolumeClaim
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-extra-world"}, &pvc); err != nil {
		t.Fatal(err)
	}
	pvc.UID = "replacement"
	if err := c.Update(t.Context(), &pvc); err != nil {
		t.Fatal(err)
	}
	if ready, err := r.authorizeRecoveryRestore(t.Context(), rs, b, gs, &batchv1.Job{}); ready || err == nil {
		t.Fatal("post-preparation replacement authorized")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gate), gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("failed revalidation retained authorization")
	}
}

// The transport records real calls made by desiredReplicas/softStop. Failure
// must retain the same durable grace timestamp across new reconciler instances.
type recoveryStopper struct {
	calls int
	fail  bool
}

func (s *recoveryStopper) Enabled() bool { return true }
func (s *recoveryStopper) Stop(context.Context, string, string) error {
	s.calls++
	if s.fail {
		return errors.New("stop transport unavailable")
	}
	return nil
}

func TestStoppedBackupGracefulDebtAcrossReconcileAndForceDeletion(t *testing.T) {
	for _, deleteAt := range []string{"before-rpc", "after-rpc"} {
		t.Run(deleteAt, func(t *testing.T) {
			c, gs, b, _ := recoveryFixture(t)
			var tmpl v1.GameTemplate
			if err := c.Get(t.Context(), client.ObjectKey{Name: "template"}, &tmpl); err != nil {
				t.Fatal(err)
			}
			tmpl.Spec.RCON = &v1.RCONSpec{Protocol: "source", Port: 25575}
			tmpl.Spec.Capabilities.Lifecycle = &v1.LifecycleSpec{Stop: []string{"stop"}}
			if err := c.Update(t.Context(), &tmpl); err != nil {
				t.Fatal(err)
			}
			b.Status = v1.BackupStatus{}
			if err := c.Status().Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			b.Annotations = map[string]string{}
			if err := c.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: gs.Namespace}, Data: map[string][]byte{"repo": []byte("s3:https://example.invalid/repo"), "password": []byte("fixture")}}
			if err := c.Create(t.Context(), secret); err != nil {
				t.Fatal(err)
			}
			var ss appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			ss.Status.ReadyReplicas = 1
			ss.Status.Replicas = 1
			if err := c.Status().Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "original-pod", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}, Spec: ss.Spec.Template.Spec, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			pod.Spec.NodeName = "node"
			if err := c.Create(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			br := &BackupReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
			if _, err := br.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			op, held, err := lifecycleOwner(gs)
			if err != nil || !held || op.StoppedBackup == nil || op.StoppedBackup.Stage != "Stopping" {
				t.Fatalf("missing admitted stop debt: %+v %v", op, err)
			}
			deadline := op.StoppedBackup.Deadline
			if deadline == "" || op.StoppedBackup.OriginalPodUID != pod.UID || gs.Spec.Suspend {
				t.Fatal("admission lost deadline/process or mutated suspend")
			}
			stopper := &recoveryStopper{fail: true}
			gr := &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme(), AgentClient: stopper}
			if deleteAt == "after-rpc" {
				replicas, _, err := gr.desiredReplicas(t.Context(), gs, &tmpl, idleAwake)
				if err != nil || replicas != 1 || stopper.calls != 1 {
					t.Fatalf("initial graceful stop: %d %v calls=%d", replicas, err, stopper.calls)
				}
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(b), b); err != nil {
				t.Fatal(err)
			}
			b.Finalizers = nil
			if err := c.Update(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || changed {
				t.Fatalf("orphan released original process: %v %v", changed, err)
			}
			replicas, poll, err := gr.desiredReplicas(t.Context(), gs, &tmpl, idleAwake)
			if err != nil || replicas != 1 || poll <= 0 || stopper.calls != 1 {
				t.Fatalf("orphan skipped/replayed graceful stop: %d %s %v calls=%d", replicas, poll, err, stopper.calls)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			stamp := gs.Annotations[stopRequestedAtAnnotation]
			if stamp == "" {
				t.Fatal("stop attempt lacks durable timestamp")
			}
			// Controller restart must neither replay a failed RPC nor renew its deadline.
			gr = &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme(), AgentClient: stopper}
			if _, _, err := gr.desiredReplicas(t.Context(), gs, &tmpl, idleAwake); err != nil {
				t.Fatal(err)
			}
			if stopper.calls != 1 {
				t.Fatal("operator restart replayed stop RPC")
			}
			op, _, _ = lifecycleOwner(gs)
			if op.StoppedBackup.Deadline != deadline || gs.Annotations[stopRequestedAtAnnotation] != stamp {
				t.Fatal("operator restart renewed stop budget")
			}
			gs.Annotations[stopRequestedAtAnnotation] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
			gs.Spec.Suspend = true
			if err := c.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			replicas, _, err = gr.desiredReplicas(t.Context(), gs, &tmpl, idleAwake)
			if err != nil || replicas != 0 || stopper.calls != 1 {
				t.Fatalf("expired graceful backstop: %d %v", replicas, err)
			}
			if err := gr.reconcileStatefulSet(t.Context(), gs, &tmpl, nil, &materializedConfig{}, replicas); err != nil {
				t.Fatal(err)
			}
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || changed {
				t.Fatalf("timeout mistaken for process exit: %v %v", changed, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
				t.Fatal(err)
			}
			ss.Status.Replicas = 0
			ss.Status.ReadyReplicas = 0
			ss.Status.ObservedGeneration = ss.Generation
			if err := c.Status().Update(t.Context(), &ss); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			if changed, err := gr.recoverStoppedBackupOwner(t.Context(), gs); err != nil || !changed {
				t.Fatalf("drained orphan failed release: %v %v", changed, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if !gs.Spec.Suspend || gs.Annotations[lifecycleOperationAnnotation] != "" {
				t.Fatal("orphan release lost user stop intent")
			}
		})
	}
}

func TestStoppedBackupWorkerRetryCancellationAcrossReconcile(t *testing.T) {
	c, gs, b, _ := recoveryFixture(t)
	b.Status = v1.BackupStatus{}
	if err := c.Status().Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	b.Annotations = map[string]string{}
	if err := c.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: gs.Namespace}, Data: map[string][]byte{"repo": []byte("s3:https://example.invalid/repo"), "password": []byte("fixture")}}); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	original := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "original", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}}, Spec: ss.Spec.Template.Spec, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	original.Spec.NodeName = "node"
	if err := c.Create(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	reconcile := func() {
		t.Helper()
		r := &BackupReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
		if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	tmpl, err := effectiveGameTemplate(t.Context(), c, gs)
	if err != nil {
		t.Fatal(err)
	}
	gr := &GameServerReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	replicas, _, err := gr.desiredReplicas(t.Context(), gs, tmpl, idleAwake)
	if err != nil || replicas != 0 {
		t.Fatalf("stop decision: %d %v", replicas, err)
	}
	if err := gr.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, replicas); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	reconcile()
	var job batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(b), &job); err != nil {
		t.Fatal(err)
	}
	job.UID = "copy-job"
	if err := c.Update(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	readyPod := func(name string, ready bool) *corev1.Pod {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, UID: types.UID(name), Labels: map[string]string{backupUIDLabel: string(b.UID)}, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&job, batchv1.SchemeGroupVersion.WithKind("Job"))}}, Spec: corev1.PodSpec{NodeName: "node", Volumes: job.Spec.Template.Spec.Volumes}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}}}
	}
	first := readyPod("copy-one", true)
	if err := c.Create(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(b), b); err != nil {
		t.Fatal(err)
	}
	deadline := b.Annotations[backupQuiesceDeadlineAnnotation]
	var gate corev1.ConfigMap
	key := client.ObjectKey{Namespace: gs.Namespace, Name: backupStartConfigMapName(b)}
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	oldToken := gate.Data[backupStartTokenKey]
	if !strings.Contains(oldToken, "copy-one") {
		t.Fatal("first ready worker was not authorized")
	}
	first.Status.Phase = corev1.PodFailed
	if err := c.Status().Update(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	retry := readyPod("copy-two", false)
	retry.Finalizers = []string{"fixture/terminating"}
	if err := c.Create(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("unprepared retry inherited old token")
	}
	retry.Status.Conditions[0].Status = corev1.ConditionTrue
	if err := c.Status().Update(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gate.Data[backupStartTokenKey], "copy-two") || gate.Data[backupStartTokenKey] == oldToken {
		t.Fatal("retry authorization not Pod-bound")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(b), b); err != nil {
		t.Fatal(err)
	}
	if b.Annotations[backupQuiesceDeadlineAnnotation] != deadline {
		t.Fatal("retry reset copy deadline")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	gs.Spec.Suspend = true
	gs.Annotations[IdleAsleepSinceAnnotation] = "later-idle"
	gs.Annotations[RestartRequestedAnnotation] = "later-restart"
	gs.Annotations[IdleWakeRequestedAnnotation] = "later-wake"
	if err := c.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Delete(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] == "" {
		t.Fatal("cancel released terminating reader")
	}
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("cancel retained authorization")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(retry), retry); err != nil {
		t.Fatal(err)
	}
	retry.Finalizers = nil
	if err := c.Update(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" || !gs.Spec.Suspend || gs.Annotations[IdleAsleepSinceAnnotation] != "later-idle" || gs.Annotations[RestartRequestedAnnotation] != "later-restart" || gs.Annotations[IdleWakeRequestedAnnotation] != "later-wake" {
		t.Fatal("cancel cleanup lost newer power/admin intent")
	}
}

func TestRecoveryRestoreGateRetryAndCancelledWriter(t *testing.T) {
	c, gs, b, rs := recoveryFixture(t)
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if _, err := r.preflightRecovery(t.Context(), rs, b, gs); err != nil {
		t.Fatal(err)
	}
	if _, err := r.bindRestoreTarget(t.Context(), rs, gs); err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.claimRestoreTarget(t.Context(), rs, gs); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	tmpl, err := effectiveGameTemplate(t.Context(), c, gs)
	if err != nil {
		t.Fatal(err)
	}
	spec := r.buildRestorePodSpec(rs, b, tmpl)
	if err := r.prepareRecoveryRestorePod(t.Context(), rs, &spec); err != nil {
		t.Fatal(err)
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "restore-" + rs.Name, Namespace: gs.Namespace, UID: "restore-job", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rs, v1.GroupVersion.WithKind("Restore"))}}, Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: spec}}}
	if err := c.Create(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "restore-one", Namespace: gs.Namespace, UID: "restore-one", Labels: map[string]string{restoreUIDLabel: string(rs.UID)}, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(job, batchv1.SchemeGroupVersion.WithKind("Job"))}}, Spec: spec, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	pod.Spec.NodeName = "node"
	if err := c.Create(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if ready, err := r.authorizeRecoveryRestore(t.Context(), rs, b, gs, job); err != nil || !ready {
		t.Fatalf("authorization: %v %v", ready, err)
	}
	key := client.ObjectKey{Namespace: gs.Namespace, Name: restoreStartName(rs)}
	var gate corev1.ConfigMap
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	oldToken := gate.Data[backupStartTokenKey]
	deadline := rs.Annotations[restoreDeadlineAnnotation]
	pod.Status.Phase = corev1.PodFailed
	if err := c.Status().Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	second := pod.DeepCopy()
	second.ResourceVersion = ""
	second.Name = "restore-two"
	second.UID = "restore-two"
	second.Status.Phase = corev1.PodRunning
	second.Status.Conditions[0].Status = corev1.ConditionFalse
	if err := c.Create(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	r = &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	if ready, err := r.authorizeRecoveryRestore(t.Context(), rs, b, gs, job); err != nil || ready {
		t.Fatalf("unprepared retry: %v %v", ready, err)
	}
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("restore retry inherited prior authorization")
	}
	second.Status.Conditions[0].Status = corev1.ConditionTrue
	if err := c.Status().Update(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if ready, err := r.authorizeRecoveryRestore(t.Context(), rs, b, gs, job); err != nil || !ready {
		t.Fatalf("retry ready: %v %v", ready, err)
	}
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] == oldToken || !strings.Contains(gate.Data[backupStartTokenKey], "restore-two") || rs.Annotations[restoreDeadlineAnnotation] != deadline {
		t.Fatal("retry lost Pod binding or reset deadline")
	}
	if result, err := r.fail(t.Context(), rs, "cancelled during copy"); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("cancel failed to drain: %v %v", result, err)
	}
	if err := c.Get(t.Context(), key, &gate); err != nil {
		t.Fatal(err)
	}
	if gate.Data[backupStartTokenKey] != "" {
		t.Fatal("failed restore retained token")
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[restoreGuardAnnotation] == "" {
		t.Fatal("failed restore released live writer")
	}
	if err := c.Delete(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	ss.Spec.Replicas = &zero
	if err := c.Update(t.Context(), &ss); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := r.cleanupRestore(t.Context(), rs); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if !gs.Spec.Suspend || gs.Annotations[restoreGuardAnnotation] != "" {
		t.Fatal("failed restore did not stay suspended after worker drain")
	}
}

func TestRecoveryQuiesceOutcomeFinalizedOnlyBeforeFirstAuthorization(t *testing.T) {
	c, _, b, _ := recoveryFixture(t)
	b.Spec.PowerPolicy = "preserve"
	b.Spec.Quiesce = true
	b.Annotations[annoQuiesceAttempted] = "true"
	if err := c.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	before := b.Status.CaptureManifest.DeepCopy()
	r := &BackupReconciler{Client: c, APIReader: c}
	if err := r.finalizeBackupCaptureOutcome(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if b.Status.CaptureManifest.QuiesceOutcome != "legacy-unverified" {
		t.Fatal("capture retained pre-RPC quiesce outcome")
	}
	for i, v := range before.Volumes {
		if v.ClaimUID != b.Status.CaptureManifest.Volumes[i].ClaimUID || v.SnapshotRoot != b.Status.CaptureManifest.Volumes[i].SnapshotRoot {
			t.Fatal("quiesce finalization rewrote coverage")
		}
	}
	b.Annotations[backupAuthorizedWorkerAnnotation] = "worker"
	b.Annotations[annoQuiesceAttempted] = "unsupported"
	if err := c.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := r.finalizeBackupCaptureOutcome(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if b.Status.CaptureManifest.QuiesceOutcome != "legacy-unverified" {
		t.Fatal("post-authorization outcome rewrote capture history")
	}
	if backupConsistencyCondition(b).Status != metav1.ConditionFalse {
		t.Fatal("live multi-volume copy overclaimed consistency")
	}
}

func TestRestoreFailurePreservesExactWorkerDiagnosticsBeforeCleanup(t *testing.T) {
	c, _, _, rs := recoveryFixture(t)
	r := &RestoreReconciler{Client: c, APIReader: c, Scheme: c.Scheme()}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "restore-worker", Namespace: rs.Namespace, UID: "worker-job"}, Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "Job has reached its retry limit"}}}}
	controller := true
	for _, tc := range []struct{ name, owner, message string }{{"ours", "worker-job", "Fatal: repository https://user:secret@example.invalid failed"}, {"foreign", "replaced-job", "foreign diagnostic must not leak"}} {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: rs.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: types.UID(tc.owner), Controller: &controller}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "restic", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: tc.message}}}}}}
		if err := c.Create(t.Context(), pod); err != nil {
			t.Fatal(err)
		}
	}
	message := r.restoreJobFailure(t.Context(), job).Error()
	if !strings.Contains(message, "BackoffLimitExceeded") || !strings.Contains(message, "exited 1 (Error)") || strings.Contains(message, "secret") || strings.Contains(message, "repository") || strings.Contains(message, "foreign diagnostic") {
		t.Fatalf("incomplete or foreign failure evidence: %s", message)
	}
	if _, err := r.fail(t.Context(), rs, message); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}
	if rs.Status.Phase != v1.RestorePhaseFailed || rs.Status.Message != message {
		t.Fatalf("cleanup lost persisted worker failure: %+v", rs.Status)
	}
}
