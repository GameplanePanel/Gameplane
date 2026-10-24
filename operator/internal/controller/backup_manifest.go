package controller

import (
	"context"
	"sort"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Called after lifecycle ownership and the workload fence, before a copy is
// authorized. Requeues and later template edits never rewrite this manifest.
func (r *BackupReconciler) recordBackupManifest(ctx context.Context, b *gameplanev1alpha1.Backup, gs *gameplanev1alpha1.GameServer) error {
	if b.Status.CaptureManifest != nil {
		return nil
	}
	reader := r.backupAPIReader()
	var worker client.Object = &batchv1.Job{}
	if b.Spec.Strategy == "volume-snapshot" {
		worker = &snapshotv1.VolumeSnapshot{}
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(b), worker); err == nil {
		// Current prepared workers exist before capture but cannot read data
		// without authorization. Its durable annotation is written before the
		// projected token and never cleared, including during cancellation.
		// All other existing workers retain unknown historical coverage.
		waiting := false
		if job, ok := worker.(*batchv1.Job); ok && metav1.IsControlledBy(job, b) && job.Annotations[backupWorkerProtocolAnnotation] == "v1" &&
			b.Annotations[backupPreparationDeadlineAnnotation] != "" && b.Annotations[backupAuthorizedWorkerAnnotation] == "" &&
			job.Status.Succeeded == 0 && !jobPermanentlyFailed(job) {
			var gate corev1.ConfigMap
			if err := reader.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: backupStartConfigMapName(b)}, &gate); err == nil {
				waiting = metav1.IsControlledBy(&gate, b) && gate.Data[backupStartTokenKey] == ""
			} else if !apierrors.IsNotFound(err) {
				return err
			}
		}
		if !waiting {
			b.Status.CaptureManifest = &gameplanev1alpha1.BackupCaptureManifest{State: "LegacyUnknown"}
			return r.Status().Update(ctx, b)
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	var ss appsv1.StatefulSet
	err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	var layout corev1.PodSpec
	source := "Unavailable"
	if err == nil && metav1.IsControlledBy(&ss, gs) {
		layout, source = ss.Spec.Template.Spec, "StatefulSet"
		var pod corev1.Pod
		err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err == nil && metav1.IsControlledBy(&pod, &ss) {
			layout, source = pod.Spec, "Pod"
		}
	}
	tmpl, err := effectiveGameTemplate(ctx, reader, gs)
	if err != nil && (gs.Annotations[effectiveTemplateAnnotation] != "" || !apierrors.IsNotFound(err)) {
		return err
	}
	primary := gs.Name + "-data"
	volumes := map[string]*gameplanev1alpha1.BackupVolumeCoverage{
		primary: {ClaimName: primary, Included: true, Classification: "data", CoverageReason: "PrimaryDataClaim"},
	}
	podNames := map[string]string{}
	for _, volume := range layout.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		name := volume.PersistentVolumeClaim.ClaimName
		podNames[volume.Name] = name
		if volumes[name] == nil {
			volumes[name] = &gameplanev1alpha1.BackupVolumeCoverage{ClaimName: name, Classification: "unclassified", CoverageReason: "NotCovered"}
		}
	}
	for _, container := range layout.Containers {
		if container.Name != "game" {
			continue
		}
		for _, mount := range container.VolumeMounts {
			if volume := volumes[podNames[mount.Name]]; volume != nil {
				volume.MountPaths = append(volume.MountPaths, mount.MountPath)
			}
		}
	}
	var claims corev1.PersistentVolumeClaimList
	if err := reader.List(ctx, &claims, client.InNamespace(gs.Namespace)); err != nil {
		return err
	}
	for _, claim := range claims.Items {
		volume := volumes[claim.Name]
		if volume == nil && metav1.IsControlledBy(&claim, gs) {
			volume = &gameplanev1alpha1.BackupVolumeCoverage{ClaimName: claim.Name, Classification: "unclassified", CoverageReason: "RetainedUnmountedClaim"}
			volumes[claim.Name] = volume
		}
		if volume != nil {
			volume.ClaimUID = string(claim.UID)
		}
	}
	for _, extra := range effectiveExtraVolumes(gs, tmpl) {
		volume := volumes[extraPVCName(gs, extra.Name)]
		if volume == nil {
			continue
		}
		// A newer declaration for a different mount must not classify the
		// frozen workload's volume as safely rebuildable.
		for _, mount := range volume.MountPaths {
			if mount == extra.MountPath && extra.BackupRole != "" {
				volume.Classification = extra.BackupRole
			}
		}
	}
	now := metav1.Now()
	manifest := &gameplanev1alpha1.BackupCaptureManifest{State: "Recorded", RecordedAt: &now,
		ServerUID: string(gs.UID), Strategy: b.Spec.Strategy, LayoutSource: source, QuiesceOutcome: backupQuiesceOutcome(b)}
	if manifest.Strategy == "" {
		manifest.Strategy = "restic-snapshot"
	}
	for _, volume := range volumes {
		sort.Strings(volume.MountPaths)
		manifest.Volumes = append(manifest.Volumes, *volume)
	}
	sort.Slice(manifest.Volumes, func(i, j int) bool { return manifest.Volumes[i].ClaimName < manifest.Volumes[j].ClaimName })
	b.Status.CaptureManifest = manifest
	return r.Status().Update(ctx, b)
}

func backupQuiesceOutcome(b *gameplanev1alpha1.Backup) string {
	if preparation, err := parseBackupPreparation(b); err == nil && preparation != nil && preparation.Outcome == "already-stopped" {
		return "already-stopped"
	}
	switch b.Annotations[annoQuiesceAttempted] {
	case "true":
		return "confirmed"
	case "unsupported":
		return "unsupported"
	case "pending":
		return "unknown"
	default:
		if !b.Spec.Quiesce {
			return "not-requested"
		}
		return "agent-unavailable"
	}
}

// Consistency=True means the copy completed after confirmed quiesce or from an
// already-stopped source whose writers had drained before capture admission.
// False is an explicit weaker guarantee, not a synonym for copy failure.
func backupConsistencyCondition(b *gameplanev1alpha1.Backup) metav1.Condition {
	condition := metav1.Condition{Type: "Consistency", ObservedGeneration: b.Generation, Status: metav1.ConditionUnknown}
	switch {
	case b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed:
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, "CaptureFailed", "The capture failed; no usable consistency guarantee was achieved."
	case b.Status.CaptureManifest == nil || b.Status.CaptureManifest.State != "Recorded":
		condition.Reason, condition.Message = "LegacyMetadataUnknown", "Capture-time metadata is unavailable. Do not infer consistency or volume coverage from the current template."
	case b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || b.Status.SnapshotID == "":
		condition.Reason, condition.Message = "CapturePending", "The capture has not completed; its achieved consistency is not yet known."
	case b.Status.CaptureManifest.QuiesceOutcome == "confirmed":
		condition.Status, condition.Reason, condition.Message = metav1.ConditionTrue, "Quiesced", "The primary volume copy completed after the agent confirmed quiesce. This depends on the module's quiesce commands; other volumes remain outside this backup."
	case b.Status.CaptureManifest.QuiesceOutcome == "already-stopped":
		condition.Status, condition.Reason, condition.Message = metav1.ConditionTrue, "StoppedSource", "The primary volume copy completed from a source that was already stopped and drained before capture. Other volumes remain outside this backup."
	case b.Status.CaptureManifest.Strategy == "volume-snapshot":
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, "CrashConsistentSnapshot", "The primary CSI snapshot is crash-consistent; application quiesce was not confirmed ("+b.Status.CaptureManifest.QuiesceOutcome+")."
	case b.Status.CaptureManifest.QuiesceOutcome == "unsupported":
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, "UnsupportedRawCopy", "The game does not support quiesce. The primary volume was copied while it could still change."
	case b.Status.CaptureManifest.QuiesceOutcome == "agent-unavailable":
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, "AgentUnavailableRawCopy", "No agent quiesce capability was configured. The primary volume was copied without pausing game writes."
	default:
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, "RawCopy", "The primary volume was copied without confirmed application quiesce."
	}
	return condition
}

func (r *BackupReconciler) syncBackupConsistency(ctx context.Context, b *gameplanev1alpha1.Backup) error {
	conditions := upsertCondition(b.Status.Conditions, backupConsistencyCondition(b))
	if sameConditions(b.Status.Conditions, conditions) {
		return nil
	}
	b.Status.Conditions = conditions
	return r.Status().Update(ctx, b)
}
