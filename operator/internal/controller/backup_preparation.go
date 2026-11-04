package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	backupPreparationAnnotation    = "backup.gameplane.local/preparation"
	backupCleanupOutcomeAnnotation = "backup.gameplane.local/cleanup-outcome"
)

var errBackupSourceChanged = errors.New("backup source changed after preparation")

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
	if !b.Spec.Quiesce {
		return true, nil
	}
	p, err := parseBackupPreparation(b)
	if err != nil {
		return false, err
	}
	if p == nil && !agentQuiesceAvailable(r.AgentClient) {
		return true, nil
	}
	// Pre-upgrade attempts have no source identity. Do not invent a new one
	// after their RPC; preserve their existing cleanup contract.
	if p == nil && b.Annotations[annoQuiesceAttempted] != "" {
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
			return false, nil
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
			return false, nil
		}
		candidate.Outcome, candidate.PodUID = "live", pod.UID
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
	copyJobs := map[types.UID]bool{}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Name == b.Name && metav1.IsControlledBy(job, b) {
			copyJobs[job.UID] = true
			continue
		}
		if ((job.Status.Succeeded == 0 && !jobPermanentlyFailed(job)) || job.Status.Active > 0 || !job.DeletionTimestamp.IsZero()) && podMountsRestoreTarget(&corev1.Pod{Spec: job.Spec.Template.Spec}, gs.Name) {
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
		if metav1.IsControlledBy(pod, ss) || metav1.IsControlledBy(pod, gs) || pod.Name == gs.Name+"-0" || podMountsRestoreTarget(pod, gs.Name) {
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
