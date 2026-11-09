package controller

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This record lives on the arbitration object, including during orphan transfer.
// Admitted is persisted atomically with ownership; Stopping precedes any stop RPC.
type stoppedBackup struct {
	Stage          string                    `json:"stage"`
	WasRunning     bool                      `json:"wasRunning"`
	Generation     int64                     `json:"generation"`
	Suspend        bool                      `json:"suspend"`
	Idle           string                    `json:"idle,omitempty"`
	Restart        string                    `json:"restart,omitempty"`
	Wake           string                    `json:"wake,omitempty"`
	WorkloadUID    types.UID                 `json:"workloadUID,omitempty"`
	OriginalPodUID types.UID                 `json:"originalPodUID,omitempty"`
	Deadline       string                    `json:"deadline,omitempty"`
	Claims         []v1.BackupVolumeCoverage `json:"claims,omitempty"`
}

func newStoppedBackup(gs *v1.GameServer) *stoppedBackup {
	return &stoppedBackup{Stage: "Admitted", WasRunning: !gs.Spec.Suspend && gs.Annotations[IdleAsleepSinceAnnotation] == "", Generation: gs.Generation, Suspend: gs.Spec.Suspend, Idle: gs.Annotations[IdleAsleepSinceAnnotation], Restart: gs.Annotations[RestartRequestedAnnotation], Wake: gs.Annotations[IdleWakeRequestedAnnotation]}
}
func stoppedBackupMayResume(gs *v1.GameServer, s *stoppedBackup) bool {
	return s != nil && s.WasRunning && !gs.Spec.Suspend && gs.Annotations[IdleAsleepSinceAnnotation] == "" && gs.Generation == s.Generation && gs.Annotations[RestartRequestedAnnotation] == s.Restart && gs.Annotations[IdleWakeRequestedAnnotation] == s.Wake
}

func (r *BackupReconciler) prepareStoppedBackup(ctx context.Context, b *v1.Backup, gs *v1.GameServer) (bool, error) {
	op, held, err := lifecycleOwner(gs)
	if err != nil {
		return false, err
	}
	if !held || op.StoppedBackup == nil {
		return false, fmt.Errorf("stopped backup lacks durable owner provenance")
	}
	s := op.StoppedBackup
	var ss appsv1.StatefulSet
	if err := r.backupAPIReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return false, err
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("%w: stopped workload owner", errBackupSourceChanged)
	}
	if s.Stage == "Admitted" {
		// Select coverage before the first stop; failures cannot interrupt a running game.
		if b.Spec.VolumePolicy == "all-data" {
			m, err := recoveryLayout(ctx, r.backupAPIReader(), gs)
			if err != nil {
				return false, fmt.Errorf("%w: %w", errBackupSourceChanged, err)
			}
			s.Claims = m.Volumes
		} else {
			s.Claims = []v1.BackupVolumeCoverage{{ClaimName: gs.Name + "-data", Included: true}}
		}
		var pod corev1.Pod
		err := r.backupAPIReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		if err == nil {
			if !metav1.IsControlledBy(&pod, &ss) {
				return false, fmt.Errorf("%w: original pod owner", errBackupSourceChanged)
			}
			s.OriginalPodUID = pod.UID
		}
		s.WorkloadUID = ss.UID
		s.Deadline = b.Annotations[backupPreparationDeadlineAnnotation]
		s.Stage = "Stopping"
		gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
		return false, r.Update(ctx, gs)
	}
	if s.WorkloadUID != ss.UID {
		return false, fmt.Errorf("%w: stopped workload replaced", errBackupSourceChanged)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	drained, err := r.backupSourceDrained(ctx, b, gs, &ss)
	if err != nil || !drained {
		return false, err
	}
	if s.Stage != "Drained" {
		s.Stage = "Drained"
		gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
		if err := r.Update(ctx, gs); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Cleanup never changes spec or acknowledges a user's pending restart/wake.
func stoppedBackupCleanup(ctx context.Context, c client.Client, reader client.Reader, gs *v1.GameServer, s *stoppedBackup) (bool, error) {
	if s == nil || s.Stage == "Admitted" {
		return true, nil
	}
	if s.WorkloadUID == "" || s.Deadline == "" || (s.Stage != "Stopping" && s.Stage != "Drained") {
		return false, fmt.Errorf("stopped backup provenance is incomplete; recovery intervention required")
	}
	if _, err := time.Parse(time.RFC3339Nano, s.Deadline); err != nil {
		return false, fmt.Errorf("invalid stopped backup deadline: %w", err)
	}
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if ss.UID != s.WorkloadUID || !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("stopped backup workload replaced; recovery intervention required")
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	gone, err := backupQuiesceProcessGone(ctx, reader, gs.Namespace, gs.Name, s.OriginalPodUID)
	if err != nil || !gone {
		return false, err
	}
	fake := &v1.Backup{ObjectMeta: metav1.ObjectMeta{Namespace: gs.Namespace}, Status: v1.BackupStatus{CaptureManifest: &v1.BackupCaptureManifest{Volumes: s.Claims}}}
	return (&BackupReconciler{Client: c, APIReader: reader}).backupSourceDrained(ctx, fake, gs, &ss)
}

func recoveryIntervention(ctx context.Context, c client.Client, gs *v1.GameServer, message string) error {
	gs.Status.Conditions = upsertCondition(gs.Status.Conditions, metav1.Condition{Type: "RecoveryIntervention", Status: metav1.ConditionTrue, Reason: "ProvenanceUnavailable", Message: message, ObservedGeneration: gs.Generation})
	return c.Status().Update(ctx, gs)
}

// Autonomous orphan cleanup runs even if nobody requests a successor operation.
func (r *GameServerReconciler) recoverStoppedBackupOwner(ctx context.Context, gs *v1.GameServer) (bool, error) {
	op, held, err := lifecycleOwner(gs)
	if err != nil {
		return true, recoveryIntervention(ctx, r.Client, gs, err.Error())
	}
	if !held || op.Kind != "Backup" || op.StoppedBackup == nil {
		return false, nil
	}
	exists, err := lifecycleOwnerExists(ctx, r.wipeReader(), gs.Namespace, op.lifecycleIdentity)
	if err != nil || exists {
		return false, err
	}
	backup := &BackupReconciler{Client: r.Client, APIReader: r.wipeReader(), Scheme: r.Scheme}
	busy, err := backup.backupWorkersLive(ctx, gs.Namespace, backupIdentity{Name: op.Name, UID: op.UID}, true)
	if err != nil || busy {
		return false, err
	}
	ready, err := recoverLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, op, r.OperationQuiescer)
	if err != nil || !ready {
		return false, err
	}
	ready, err = stoppedBackupCleanup(ctx, r.Client, r.wipeReader(), gs, op.StoppedBackup)
	if err != nil {
		return true, recoveryIntervention(ctx, r.Client, gs, err.Error())
	}
	if !ready {
		return false, nil
	}
	if err := clearLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, op.lifecycleIdentity); err != nil {
		return false, err
	}
	delete(gs.Annotations, backupGuardAnnotation)
	delete(gs.Annotations, stopRequestedAtAnnotation)
	return true, r.Update(ctx, gs)
}

// Recovery keeps the oldest outstanding graceful stop ahead of a successor.
// Admitted carries no stop side effect; Drained is handled by the zero fence.
func lifecycleStoppingBackup(op lifecycleOperation) *stoppedBackup {
	for _, id := range append(append([]lifecycleIdentity{}, op.Recovery...), op.lifecycleIdentity) {
		if id.StoppedBackup != nil && id.StoppedBackup.Stage == "Stopping" {
			return id.StoppedBackup
		}
	}
	return nil
}
func (r *GameServerReconciler) continueBackupStop(ctx context.Context, gs *v1.GameServer, tmpl *v1.GameTemplate, debt *stoppedBackup) (int32, time.Duration, error) {
	deadline, err := time.Parse(time.RFC3339Nano, debt.Deadline)
	if err != nil {
		return 0, 5 * time.Second, fmt.Errorf("invalid persisted backup stop deadline: %w", err)
	}
	if !time.Now().Before(deadline) {
		return 0, 5 * time.Second, nil
	}
	// A replacement process must not receive the old process's graceful RPC.
	var pod corev1.Pod
	err = r.wipeReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
	if apierrors.IsNotFound(err) {
		return 0, 5 * time.Second, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if debt.OriginalPodUID == "" || pod.UID != debt.OriginalPodUID {
		return 0, 5 * time.Second, nil
	}
	replicas, poll, err := r.softStop(ctx, gs, tmpl)
	if poll == 0 || poll > time.Until(deadline) {
		poll = time.Until(deadline)
	}
	return replicas, poll, err
}
