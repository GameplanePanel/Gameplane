package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strconv"
	"time"

	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const captureFormatAnnotation = "backup.gameplane.local/capture-format"
const restoreFormatAnnotation = "restore.gameplane.local/capture-format"
const restoreDeadlineAnnotation = "restore.gameplane.local/copy-deadline"
const restoreAuthorizedAnnotation = "restore.gameplane.local/authorized-worker"

func recoveryCaptureMode(src *v1.Backup) (bool, error) {
	m := src.Status.CaptureManifest
	all := src.Spec.VolumePolicy == "all-data" || src.Annotations[captureFormatAnnotation] == "all-data-v2" || (m != nil && (m.FormatVersion != 0 || m.VolumePolicy == "all-data"))
	if all {
		if src.Spec.Strategy == "volume-snapshot" {
			return false, fmt.Errorf("CSI cannot restore an all-data archive")
		}
		if err := validateRecoveryManifest(m); err != nil {
			return false, err
		}
		if m.TemplateName == "" || m.TemplateRevision == "" || m.ServerUID == "" {
			return false, fmt.Errorf("recovery manifest lacks provenance")
		}
		return true, nil
	}
	if m != nil {
		for _, v := range m.Volumes {
			if v.SnapshotRoot != "" || v.LogicalName != "" || (v.Included && v.CoverageReason != "PrimaryDataClaim") {
				return false, fmt.Errorf("contradictory legacy manifest")
			}
		}
	}
	return false, nil
}

func (r *RestoreReconciler) preflightRecovery(ctx context.Context, rs *v1.Restore, src *v1.Backup, gs *v1.GameServer) (bool, error) {
	all, err := recoveryCaptureMode(src)
	if err != nil {
		return false, err
	}
	if rs.Annotations[restoreFormatAnnotation] == "all-data-v2" && !all {
		return false, fmt.Errorf("source recovery manifest was removed or downgraded")
	}
	if !all {
		if rs.Status.RecoveryPlan != nil {
			return false, fmt.Errorf("recovery plan cannot downgrade to legacy")
		}
		return false, nil
	}
	if src.Status.SnapshotID != rs.Status.SnapshotID || src.Status.Phase != v1.BackupPhaseSucceeded {
		return false, fmt.Errorf("source snapshot changed or is invalid")
	}
	current, err := recoveryLayout(ctx, r.apiReader(), gs)
	if err != nil {
		return false, err
	}
	saved := src.Status.CaptureManifest
	if current.TemplateName != saved.TemplateName || current.TemplateRevision != saved.TemplateRevision || current.GameVersion != saved.GameVersion {
		return false, fmt.Errorf("target template or version differs from capture provenance")
	}
	sourceByLogical := map[string]v1.BackupVolumeCoverage{}
	for _, v := range saved.Volumes {
		sourceByLogical[v.LogicalName] = v
	}
	if len(current.Volumes) != len(saved.Volumes) {
		return false, fmt.Errorf("target recovery volume set differs")
	}
	for _, target := range current.Volumes {
		original, ok := sourceByLogical[target.LogicalName]
		if !ok || target.Included != original.Included || target.Classification != original.Classification || !reflect.DeepEqual(target.MountPaths, original.MountPaths) || target.SnapshotRoot != original.SnapshotRoot {
			return false, fmt.Errorf("target logical layout differs for %s", target.LogicalName)
		}
		if target.Included && saved.ServerUID == string(gs.UID) && (target.ClaimName != original.ClaimName || target.ClaimUID != original.ClaimUID) {
			return false, fmt.Errorf("original claim %s was replaced", target.LogicalName)
		}
	}
	// Excluded rebuildable claims may be created or reconstructed during recovery;
	// they have no writable target mapping and must not invalidate included roots.
	for i := range current.Volumes {
		if !current.Volumes[i].Included {
			current.Volumes[i].ClaimUID = ""
		}
	}
	candidate := &v1.RestoreRecoveryPlan{AdmittedSuspend: gs.Spec.Suspend, AdmittedIdle: gs.Annotations[IdleAsleepSinceAnnotation], AdmittedGeneration: gs.Generation, BackupUID: string(src.UID), ManifestDigest: recoveryDigest(saved), TargetUID: string(gs.UID), TemplateRevision: current.TemplateRevision, GameVersion: current.GameVersion, Volumes: current.Volumes}
	if rs.Status.RecoveryPlan != nil {
		candidate.AdmittedSuspend = rs.Status.RecoveryPlan.AdmittedSuspend
		candidate.AdmittedIdle = rs.Status.RecoveryPlan.AdmittedIdle
		candidate.AdmittedGeneration = rs.Status.RecoveryPlan.AdmittedGeneration
		if !reflect.DeepEqual(candidate, rs.Status.RecoveryPlan) {
			return false, fmt.Errorf("pinned recovery mapping or source manifest changed")
		}
		return false, nil
	}
	rs.Status.RecoveryPlan = candidate
	return true, r.Status().Update(ctx, rs)
}

// A ready Pod proves the claims are mounted. PVC protection prevents deletion
// and name reuse until all Pods mounting that claim are gone. UID revalidation
// after readiness closes the pre-scheduling replacement gap.
func validateMountedRecoveryClaims(ctx context.Context, reader client.Reader, namespace string, volumes []v1.BackupVolumeCoverage) error {
	for _, v := range volumes {
		if !v.Included {
			continue
		}
		var pvc corev1.PersistentVolumeClaim
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: v.ClaimName}, &pvc); err != nil {
			return err
		}
		protected := false
		for _, finalizer := range pvc.Finalizers {
			protected = protected || finalizer == "kubernetes.io/pvc-protection"
		}
		if string(pvc.UID) != v.ClaimUID || !pvc.DeletionTimestamp.IsZero() || pvc.Status.Phase != corev1.ClaimBound || pvc.Spec.VolumeName == "" || !protected {
			return fmt.Errorf("mounted claim %s is replaced, unbound, deleting or lacks PVC protection", v.ClaimName)
		}
	}
	return nil
}
func restoreStartName(rs *v1.Restore) string {
	return fmt.Sprintf("restore-start-%x", sha256.Sum256([]byte(rs.Name+"/"+string(rs.UID))))[:61]
}
func (r *RestoreReconciler) restoreRecoveryGate(ctx context.Context, rs *v1.Restore) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: restoreStartName(rs), Namespace: rs.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if !cm.CreationTimestamp.IsZero() && !metav1.IsControlledBy(cm, rs) {
			return fmt.Errorf("foreign restore gate")
		}
		if cm.Data == nil {
			cm.Data = map[string]string{backupStartTokenKey: ""}
		}
		return controllerutil.SetControllerReference(rs, cm, r.Scheme)
	})
	return cm, err
}
func (r *RestoreReconciler) revokeRecoveryRestore(ctx context.Context, rs *v1.Restore) error {
	var cm corev1.ConfigMap
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: rs.Namespace, Name: restoreStartName(rs)}, &cm); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&cm, rs) || cm.Data[backupStartTokenKey] == "" {
		return nil
	}
	cm.Data[backupStartTokenKey] = ""
	return r.Update(ctx, &cm)
}
func (r *RestoreReconciler) prepareRecoveryRestorePod(ctx context.Context, rs *v1.Restore, spec *corev1.PodSpec) error {
	if rs.Status.RecoveryPlan == nil {
		return nil
	}
	if rs.Annotations[restoreDeadlineAnnotation] == "" {
		_, seconds := resolveJobLimits(r.JobBackoffLimit, r.JobActiveDeadlineSeconds)
		if rs.Annotations == nil {
			rs.Annotations = map[string]string{}
		}
		rs.Annotations[restoreDeadlineAnnotation] = time.Now().Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339Nano)
		if err := r.Update(ctx, rs); err != nil {
			return err
		}
	}
	deadline, err := time.Parse(time.RFC3339Nano, rs.Annotations[restoreDeadlineAnnotation])
	if err != nil {
		return err
	}
	if _, err := r.restoreRecoveryGate(ctx, rs); err != nil {
		return err
	}
	worker := &spec.Containers[0]
	worker.Command = []string{"/bin/sh", "-c"}
	worker.Args = append([]string{resticWorkerScript, "restore-worker"}, worker.Args...)
	worker.Env = append(worker.Env, corev1.EnvVar{Name: "BACKUP_UID", Value: string(rs.UID)}, corev1.EnvVar{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}}, corev1.EnvVar{Name: "SOURCE_ID", Value: rs.Status.RecoveryPlan.ManifestDigest}, corev1.EnvVar{Name: "PREPARATION_EXPIRES", Value: strconv.FormatInt(deadline.Unix(), 10)})
	worker.VolumeMounts = append(worker.VolumeMounts, corev1.VolumeMount{Name: "start", MountPath: "/start", ReadOnly: true})
	worker.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-c", "test -f /tmp/prepared"}}}, PeriodSeconds: 2, FailureThreshold: 1}
	spec.Volumes = append(spec.Volumes, corev1.Volume{Name: "start", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: restoreStartName(rs)}}}})
	noToken := false
	spec.AutomountServiceAccountToken = &noToken
	return nil
}
func (r *RestoreReconciler) authorizeRecoveryRestore(ctx context.Context, rs *v1.Restore, src *v1.Backup, gs *v1.GameServer, job *batchv1.Job) (bool, error) {
	if rs.Status.RecoveryPlan == nil {
		return true, nil
	}
	if err := r.revokeRecoveryRestore(ctx, rs); err != nil {
		return false, err
	}
	if _, err := r.preflightRecovery(ctx, rs, src, gs); err != nil {
		return false, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, rs.Annotations[restoreDeadlineAnnotation])
	if err != nil {
		return false, err
	}
	if job.Status.Succeeded > 0 {
		if rs.Annotations[restoreAuthorizedAnnotation] == "" {
			return false, fmt.Errorf("restore completed without authorization")
		}
		return true, nil
	}
	if jobPermanentlyFailed(job) {
		return false, r.restoreJobFailure(ctx, job)
	}
	if !time.Now().Before(deadline) {
		return false, fmt.Errorf("restore copy deadline expired")
	}
	worker, err := (&BackupReconciler{Client: r.Client, APIReader: r.apiReader()}).preparedBackupWorker(ctx, &v1.Backup{ObjectMeta: metav1.ObjectMeta{Namespace: rs.Namespace}}, job)
	if err != nil || worker == nil {
		return false, err
	}
	if err := validateMountedRecoveryClaims(ctx, r.apiReader(), rs.Namespace, rs.Status.RecoveryPlan.Volumes); err != nil {
		return false, err
	}
	// Recheck other writers, excluding only this exact Restore's worker.
	if err := r.recoveryRestoreWritersDrained(ctx, rs, gs); err != nil {
		return false, err
	}
	if _, err := r.preflightRecovery(ctx, rs, src, gs); err != nil {
		return false, err
	}
	rs.Annotations[restoreAuthorizedAnnotation] = string(worker.UID)
	if err := r.Update(ctx, rs); err != nil {
		return false, err
	}
	cm, err := r.restoreRecoveryGate(ctx, rs)
	if err != nil {
		return false, err
	}
	cm.Data[backupStartTokenKey] = fmt.Sprintf("v1 %s %s %s %d\n", rs.UID, worker.UID, rs.Status.RecoveryPlan.ManifestDigest, deadline.Unix())
	return true, r.Update(ctx, cm)
}
func (r *RestoreReconciler) recoveryRestoreWritersDrained(ctx context.Context, rs *v1.Restore, gs *v1.GameServer) error {
	var pods corev1.PodList
	var jobs batchv1.JobList
	if err := r.apiReader().List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return err
	}
	if err := r.apiReader().List(ctx, &jobs, client.InNamespace(gs.Namespace)); err != nil {
		return err
	}
	own := map[string]bool{}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if metav1.IsControlledBy(job, rs) {
			own[string(job.UID)] = true
			continue
		}
		if (job.Status.Succeeded == 0 && !jobPermanentlyFailed(job)) || job.Status.Active > 0 || !job.DeletionTimestamp.IsZero() {
			if podMountsRecoveryClaims(&corev1.Pod{Spec: job.Spec.Template.Spec}, rs.Status.RecoveryPlan.Volumes) {
				return fmt.Errorf("another Job still uses recovery claims")
			}
		}
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner != nil && own[string(owner.UID)] {
			continue
		}
		if liveRestorePod(pod) && podMountsRecoveryClaims(pod, rs.Status.RecoveryPlan.Volumes) {
			return fmt.Errorf("another Pod still uses recovery claims")
		}
	}
	return nil
}
