package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	backupPreparationAnnotation           = "backup.gameplane.local/preparation"
	backupCleanupOutcomeAnnotation        = "backup.gameplane.local/cleanup-outcome"
	backupSourceStartupDeadlineAnnotation = "backup.gameplane.local/source-startup-deadline"
)

var errBackupSourceChanged = errors.New("backup source changed after preparation")

var errBackupSourceStartupTimeout = errors.New("backup source startup grace period expired")

// Allow ordinary startup/restart races without holding the workload fence
// indefinitely when an initial live source never becomes Running.
const backupSourceStartupGracePeriod = 2 * time.Minute

// Call only after lifecycle ownership. Prepared workers share their existing
// preparation budget; CSI and older Jobs persist a separate startup deadline.
// Queue time and operator restarts never spend or reset an admitted budget.
func (r *BackupReconciler) waitForBackupSource(ctx context.Context, b *gameplanev1alpha1.Backup, reason string) (bool, error) {
	key := backupSourceStartupDeadlineAnnotation
	if b.Annotations[backupPreparationDeadlineAnnotation] != "" {
		key = backupPreparationDeadlineAnnotation
	}
	stored := b.Annotations[key] != ""
	deadline, err := r.backupDeadline(ctx, b, key, backupSourceStartupGracePeriod)
	if err != nil {
		if stored {
			return false, fmt.Errorf("%w: cannot read source startup deadline: %w", errBackupSourceStartupTimeout, err)
		}
		return false, err // A failed first persistence is retryable, not expiry.
	}
	if time.Now().Before(deadline) {
		return false, nil
	}
	return false, fmt.Errorf("%w at %s: %s", errBackupSourceStartupTimeout, deadline.UTC().Format(time.RFC3339), reason)
}

type podAgentQuiescer interface {
	CheckPod(context.Context, string, string, string) error
	QuiescePod(context.Context, string, string, string) error
	UnquiescePod(context.Context, string, string, string) error
}

func agentQuiesceAvailable(q AgentQuiescer) bool {
	if q == nil {
		return false
	}
	if configured, ok := q.(interface{ QuiesceEnabled() bool }); ok {
		return configured.QuiesceEnabled()
	}
	return true
}

func callBackupQuiesce(ctx context.Context, q AgentQuiescer, namespace, server string, podUID types.UID, undo bool) error {
	if undo && podUID == "" {
		return fmt.Errorf("cannot recover legacy quiesce without proof that the original process is gone")
	}
	if !agentQuiesceAvailable(q) {
		return fmt.Errorf("agent quiesce client is not configured")
	}
	if podUID != "" {
		bound, ok := q.(podAgentQuiescer)
		if !ok {
			return fmt.Errorf("agent client does not support pod-bound quiesce")
		}
		if undo {
			return bound.UnquiescePod(ctx, namespace, server, string(podUID))
		}
		return bound.QuiescePod(ctx, namespace, server, string(podUID))
	}
	return q.Quiesce(ctx, namespace, server)
}

// Source identity is immutable for one copy. RPC outcome remains in the
// quiesce annotations, so a stopped source is never described as quiesced.
type backupPreparation struct {
	Outcome            string    `json:"outcome"`
	TargetUID          types.UID `json:"targetUID"`
	WorkloadUID        types.UID `json:"workloadUID"`
	WorkloadGeneration int64     `json:"workloadGeneration"`
	PodUID             types.UID `json:"podUID,omitempty"`
	GameContainerID    string    `json:"gameContainerID,omitempty"`
	GameRestartCount   *int32    `json:"gameRestartCount,omitempty"`
}

func parseBackupPreparation(b *gameplanev1alpha1.Backup) (*backupPreparation, error) {
	value := b.Annotations[backupPreparationAnnotation]
	if value == "" {
		return nil, nil
	}
	var p backupPreparation
	if err := json.Unmarshal([]byte(value), &p); err != nil {
		return nil, fmt.Errorf("decode backup preparation: %w", err)
	}
	if p.TargetUID == "" || p.WorkloadUID == "" || (p.Outcome != "live" && p.Outcome != "already-stopped") || (p.Outcome == "live" && p.PodUID == "") || (p.Outcome == "already-stopped" && p.PodUID != "") {
		return nil, fmt.Errorf("invalid backup preparation identity")
	}
	return &p, nil
}

// Call only after the shared owner and StatefulSet fence are held. Readiness
// and desired suspend/idle state are not evidence that writers have exited.
func (r *BackupReconciler) prepareBackupSource(ctx context.Context, b *gameplanev1alpha1.Backup, gs *gameplanev1alpha1.GameServer) (bool, error) {
	if !b.Spec.Quiesce && b.Spec.PowerPolicy != "stop-copy-restart" {
		return true, nil
	}
	p, err := parseBackupPreparation(b)
	if err != nil {
		return false, err
	}
	// Old live preparations cannot prove which game incarnation was paused.
	// Keep them parseable for cleanup, but never backfill identity after a
	// possible save-off or copy. A pre-upgrade worker is observed separately.
	if p != nil && p.Outcome == "live" && (p.GameContainerID == "" || p.GameRestartCount == nil) {
		return false, fmt.Errorf("%w: live preparation lacks game container identity", errBackupSourceChanged)
	}
	if p == nil && b.Annotations[annoQuiesceAttempted] != "" && b.Annotations[annoQuiesceAttempted] != "unsupported" {
		return false, fmt.Errorf("%w: quiesce attempt lacks source preparation", errBackupSourceChanged)
	}
	if p == nil && !agentQuiesceAvailable(r.AgentClient) && b.Spec.PowerPolicy != "stop-copy-restart" {
		return true, nil
	}
	// An unsupported attempt did not pause the game and retains raw-copy
	// behavior without inventing a protected source identity.
	if p == nil && b.Annotations[annoQuiesceAttempted] == "unsupported" {
		return true, nil
	}
	var ss appsv1.StatefulSet
	if err := r.backupAPIReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		if apierrors.IsNotFound(err) && p != nil {
			return false, fmt.Errorf("%w: workload absent", errBackupSourceChanged)
		}
		return false, client.IgnoreNotFound(err)
	}
	if p != nil && (p.TargetUID != gs.UID || p.WorkloadUID != ss.UID || p.WorkloadGeneration != ss.Generation) {
		return false, fmt.Errorf("%w: workload identity", errBackupSourceChanged)
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("backup source workload is not owned by the bound GameServer")
	}
	if ss.Spec.Replicas == nil || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	if p != nil && ((p.Outcome == "already-stopped") != (*ss.Spec.Replicas == 0)) {
		return false, fmt.Errorf("%w: desired replicas", errBackupSourceChanged)
	}
	var candidate backupPreparation
	candidate.TargetUID, candidate.WorkloadUID, candidate.WorkloadGeneration = gs.UID, ss.UID, ss.Generation
	if *ss.Spec.Replicas == 0 {
		if ss.Status.Replicas != 0 {
			if p != nil {
				return false, fmt.Errorf("%w: stopped workload has replicas", errBackupSourceChanged)
			}
			return false, nil
		}
		drained, err := r.backupSourceDrained(ctx, b, gs, &ss)
		if err != nil || !drained {
			if err == nil && p != nil {
				return false, fmt.Errorf("%w: stopped source acquired a writer", errBackupSourceChanged)
			}
			return false, err
		}
		candidate.Outcome = "already-stopped"
	} else {
		var pod corev1.Pod
		err := r.backupAPIReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
		if apierrors.IsNotFound(err) {
			if p != nil {
				return false, fmt.Errorf("%w: original pod absent", errBackupSourceChanged)
			}
			return r.waitForBackupSource(ctx, b, "source pod absent")
		}
		if err != nil {
			return false, err
		}
		if p != nil && (p.PodUID != pod.UID || !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodRunning) {
			return false, fmt.Errorf("%w: original process no longer running", errBackupSourceChanged)
		}
		if !metav1.IsControlledBy(&pod, &ss) || pod.UID == "" {
			return false, fmt.Errorf("backup source pod is not owned by the bound workload")
		}
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodRunning {
			return r.waitForBackupSource(ctx, b, "source pod is not Running or is terminating")
		}
		var game *corev1.ContainerStatus
		for i := range pod.Status.ContainerStatuses {
			if pod.Status.ContainerStatuses[i].Name == "game" {
				game = &pod.Status.ContainerStatuses[i]
				break
			}
		}
		if game == nil || game.ContainerID == "" || game.State.Running == nil {
			if p != nil {
				return false, fmt.Errorf("%w: original game container is not observably running", errBackupSourceChanged)
			}
			return r.waitForBackupSource(ctx, b, "game container identity or running state is unavailable")
		}
		if p != nil && (p.GameContainerID != game.ContainerID || *p.GameRestartCount != game.RestartCount) {
			return false, fmt.Errorf("%w: game container restarted", errBackupSourceChanged)
		}
		candidate.Outcome, candidate.PodUID = "live", pod.UID
		candidate.GameContainerID, candidate.GameRestartCount = game.ContainerID, &game.RestartCount
	}
	if p != nil {
		if p.Outcome != candidate.Outcome || p.PodUID != candidate.PodUID {
			return false, fmt.Errorf("%w: process identity", errBackupSourceChanged)
		}
		return true, nil
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return false, err
	}
	patchBackupAnnotations(b, map[string]string{backupPreparationAnnotation: string(encoded)})
	return true, r.Update(ctx, b)
}

// Current-copy workers are readers and are already protected by the owner;
// ignore only those, so requeues can observe their completion. Any other PVC
// user or unscheduled writer Job prevents stopped-source admission.
func (r *BackupReconciler) backupSourceDrained(ctx context.Context, b *gameplanev1alpha1.Backup, gs *gameplanev1alpha1.GameServer, ss *appsv1.StatefulSet) (bool, error) {
	var jobs batchv1.JobList
	if err := r.backupAPIReader().List(ctx, &jobs, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	var selected []gameplanev1alpha1.BackupVolumeCoverage
	if b.Status.CaptureManifest != nil {
		selected = b.Status.CaptureManifest.Volumes
	}
	if b.Status.CaptureManifest == nil {
		if op, _, err := lifecycleOwner(gs); err == nil && op.Kind == "Backup" && op.UID == b.UID && op.StoppedBackup != nil {
			selected = op.StoppedBackup.Claims
		}
	}
	mounts := func(pod *corev1.Pod) bool {
		return podMountsRestoreTarget(pod, gs.Name) || podMountsRecoveryClaims(pod, selected)
	}
	copyJobs := map[types.UID]bool{}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Name == b.Name && metav1.IsControlledBy(job, b) {
			copyJobs[job.UID] = true
			continue
		}
		if ((job.Status.Succeeded == 0 && !jobPermanentlyFailed(job)) || job.Status.Active > 0 || !job.DeletionTimestamp.IsZero()) && mounts(&corev1.Pod{Spec: job.Spec.Template.Spec}) {
			return false, nil
		}
	}
	var pods corev1.PodList
	if err := r.backupAPIReader().List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !liveRestorePod(pod) {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if owner != nil && owner.Kind == "Job" && copyJobs[owner.UID] {
			continue
		}
		if metav1.IsControlledBy(pod, ss) || metav1.IsControlledBy(pod, gs) || pod.Name == gs.Name+"-0" || mounts(pod) {
			return false, nil
		}
	}
	return true, nil
}

// An absent/replaced original pod cannot retain save-off state. A timeout,
// unready pod, or terminating pod with the same UID is never proof of exit.
// Empty UID is legacy state: only absence or an observed terminal pod suffices.
func backupQuiesceProcessGone(ctx context.Context, reader client.Reader, namespace, server string, uid types.UID) (bool, error) {
	var pod corev1.Pod
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: server + "-0"}, &pod)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if uid != "" && pod.UID != uid {
		return true, nil
	}
	return pod.DeletionTimestamp.IsZero() && (pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed), nil
}
