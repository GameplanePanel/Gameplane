package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// WipeRequestedAnnotation carries the token of a requested data wipe
	// (set by the API). WipeCompletedAnnotation echoes it back once the
	// wipe Job has succeeded, so the same request never re-runs.
	WipeRequestedAnnotation = "gameplane.local/wipe-data-requested"
	WipeCompletedAnnotation = "gameplane.local/wipe-data-completed"
	wipeTokenLabel          = "gameplane.local/wipe-token"

	// wipeScript empties the directory given as $1 and verifies it is empty
	// afterwards; see the comment at its use in createWipeJob for why the
	// verification step is required.
	wipeScript = `find "$1" -mindepth 1 -delete
left=$(ls -A "$1") || exit 1
if [ -n "$left" ]; then
  echo "data wipe incomplete; entries remain under $1:" >&2
  printf '%s\n' "$left" >&2
  exit 1
fi`
)

// reconcileWipe holds exclusive access from guard acquisition through worker
// drain. Reconciles for a GameServer key are serialized; restore/API writers
// arbitrate through the checked GameServer guard update.
func (r *GameServerReconciler) reconcileWipe(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	changed, err := r.ensureWipeGuard(ctx, gs)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if gs.Annotations[restoreGuardAnnotation] != "" {
		return nil
	}
	req := gs.Annotations[WipeRequestedAnnotation]
	guard := gs.Annotations[wipeGuardAnnotation]
	jobName := gs.Name + "-wipe"
	state, err := wipeWorkers(ctx, r.wipeReader(), gs)
	if err != nil {
		return err
	}
	// Foreign Jobs can never prove completion or authorize cleanup.
	var job batchv1.Job
	err = r.wipeReader().Get(ctx, types.NamespacedName{Name: jobName, Namespace: gs.Namespace}, &job)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	exists := err == nil
	if exists && !metav1.IsControlledBy(&job, gs) {
		if pendingWipe(gs) {
			return fmt.Errorf("wipe Job %s/%s is not controlled by this GameServer", gs.Namespace, jobName)
		}
		exists = false
	}
	if changed {
		return nil
	}
	if guard == "" {
		return r.deleteWipeJob(ctx, gs, jobName)
	}
	if !pendingWipe(gs) || req != guard || (exists && job.Labels[wipeTokenLabel] != guard) {
		// Foreground deletion cannot hide independently inventoried pods.
		for _, old := range state.jobs {
			if old.DeletionTimestamp.IsZero() {
				if err := r.deleteWipeJob(ctx, gs, old.Name); err != nil {
					return err
				}
			}
		}
		if len(state.jobs) != 0 || state.livePods {
			return nil
		}
		return r.releaseWipeGuard(ctx, gs, guard, false)
	}
	if gs.Annotations[wipeSuccessAnnotation] == guard {
		// Success is durable before Job deletion, and acknowledgment waits
		// for foreground cleanup, including terminal pods now terminating.
		if exists && job.DeletionTimestamp.IsZero() {
			return r.deleteWipeJob(ctx, gs, jobName)
		}
		if len(state.jobs) != 0 || state.livePods {
			return nil
		}
		return r.ackWipe(ctx, gs, guard)
	}
	if exists && !job.DeletionTimestamp.IsZero() {
		return nil
	}
	if exists {
		if job.Status.Succeeded > 0 {
			return r.recordWipeSuccess(ctx, gs, guard)
		}
		if jobPermanentlyFailed(&job) {
			// Retain guard, failed Job and logs even after a direct start request.
			if !gs.Spec.Suspend {
				gs.Spec.Suspend = true
				if err := r.Update(ctx, gs); err != nil {
					return err
				}
			}
			return r.setWipeFailed(ctx, gs)
		}
		return nil
	}
	if state.busy {
		return nil
	}
	stopped, err := r.wipeTargetStopped(ctx, gs)
	if err != nil || !stopped {
		return err
	}
	// Revalidate immediately before launch. Another GameServer reconcile for
	// this key cannot concurrently release the guard during this launch.
	var live gameplanev1alpha1.GameServer
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if live.UID != gs.UID || live.Annotations[wipeGuardAnnotation] != guard || live.Annotations[WipeRequestedAnnotation] != guard || !pendingWipe(&live) || live.Annotations[restoreGuardAnnotation] != "" {
		return nil
	}
	return r.createWipeJob(ctx, &live, tmpl, jobName, guard)
}

func (r *GameServerReconciler) createWipeJob(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	name, token string,
) error {
	mountPath := effectiveMountPath(tmpl)
	uid := int64(65532) // same non-root UID the game + backup jobs use
	nonRoot := true
	noPrivEsc := false
	backoff := int32(2)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: gs.Namespace,
			Labels: map[string]string{
				wipeTokenLabel:                 token,
				wipeUIDLabel:                   string(gs.UID),
				"app.kubernetes.io/managed-by": "gameplane",
				"app.kubernetes.io/name":       gs.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{wipeTokenLabel: token, wipeUIDLabel: string(gs.UID)}},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &nonRoot,
						RunAsUser:      &uid,
						RunAsGroup:     &uid,
						FSGroup:        &uid,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name: "wipe",
						// Same shell image as the config-init container, so an
						// air-gapped install only has to mirror one.
						Image:   configInitImageOrDefault(r.ConfigInitImage),
						Command: []string{"/bin/sh", "-c"},
						// Remove all contents (including dotfiles) but keep the
						// mount point itself. `find -delete` doesn't error on an
						// empty directory (unlike the glob patterns this replaced,
						// which errored on "no match" and had to swallow that with
						// `2>/dev/null; true` — which also swallowed a real EACCES
						// from a subdirectory this uid can't write into).
						//
						// find's exit status alone is NOT trusted: BusyBox find
						// (the default image) prints a failed unlink/rmdir from
						// -delete to stderr but still exits 0. So the script then
						// checks the volume is actually empty and exits non-zero
						// (failing the Job) if anything is left, or if the listing
						// itself fails. The mount path is passed as $1 rather than
						// spliced into the script.
						Args:         []string{wipeScript, "wipe", mountPath},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: mountPath}},
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &nonRoot,
							RunAsUser:                &uid,
							AllowPrivilegeEscalation: &noPrivEsc,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: gs.Name + "-data",
							},
						},
					}},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(gs, job, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *GameServerReconciler) ackWipe(ctx context.Context, gs *gameplanev1alpha1.GameServer, token string) error {
	if err := r.releaseWipeGuard(ctx, gs, token, true); err != nil {
		return err
	}
	if gs.Annotations[WipeCompletedAnnotation] != token {
		return nil
	}
	if meta.FindStatusCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionDataWipe) == nil {
		return nil
	}
	base := gs.DeepCopy()
	gs.Status.Conditions = removeCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionDataWipe)
	if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("clear DataWipe condition: %w", err)
	}
	return nil
}

func (r *GameServerReconciler) deleteWipeJob(ctx context.Context, gs *gameplanev1alpha1.GameServer, name string) error {
	var job batchv1.Job
	if err := r.wipeReader().Get(ctx, types.NamespacedName{Name: name, Namespace: gs.Namespace}, &job); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&job, gs) {
		return nil
	}
	policy := metav1.DeletePropagationForeground
	uid := job.UID
	return client.IgnoreNotFound(r.Delete(ctx, &job, &client.DeleteOptions{PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid}}))
}

// jobPermanentlyFailed reports whether job has given up (batch/v1 sets
// JobConditionFailed True once BackoffLimit is exhausted).
func jobPermanentlyFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// setWipeFailed upserts the DataWipe=False/JobFailed condition on the
// GameServer so a wipe that didn't actually empty the volume is visible
// instead of silently acked (F-054).
func (r *GameServerReconciler) setWipeFailed(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	base := gs.DeepCopy()
	gs.Status.Conditions = upsertCondition(gs.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.GameServerConditionDataWipe,
		Status:             metav1.ConditionFalse,
		Reason:             "JobFailed",
		Message:            "data wipe job did not complete; the volume may not be fully cleared",
		ObservedGeneration: gs.Generation,
	})
	return r.Status().Patch(ctx, gs, client.MergeFrom(base))
}
