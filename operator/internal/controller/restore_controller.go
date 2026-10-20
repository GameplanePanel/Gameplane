package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// RestoreReconciler drives a Restore through suspend → restic-restore Job
// → resume. The target GameServer is paused for the duration of the Job
// to serialize I/O against its data PVC.
type RestoreReconciler struct {
	client.Client
	// APIReader bypasses the informer cache for target/guard ownership,
	// workload and worker drainage, and reference-copy ownership checks.
	// Wired from mgr.GetAPIReader() in cmd/main.go, mirroring
	// GameServerReconciler.APIReader. May be nil
	// (e.g. in unit tests that construct a RestoreReconciler directly);
	// apiReader() falls back to Client in that case.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	// ResticImage is the image for the restic restore Job. Set from an
	// operator flag so air-gapped installs can point it at a private
	// registry mirror. Empty falls back to DefaultResticImage.
	ResticImage string
	// JobBackoffLimit / JobActiveDeadlineSeconds bound the restic restore Job.
	// Set from operator flags; nil / zero fall back to
	// DefaultBackupJobBackoffLimit / DefaultBackupJobActiveDeadlineSeconds.
	JobBackoffLimit          *int32
	JobActiveDeadlineSeconds int64
}

// apiReader returns the uncached reader for live-consistency checks,
// falling back to the cached Client when APIReader is unset.
func (r *RestoreReconciler) apiReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Writes to Restore status/finalizers, GameServers and StatefulSets are scoped
// to managed namespaces by role_namespace.yaml and the Helm chart's Role.
// These generated ClusterRole markers deliberately grant reads only.
// +kubebuilder:rbac:groups=gameplane.local,resources=restores,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=gameservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=backups,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch

func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rs gameplanev1alpha1.Restore
	if err := r.apiReader().Get(ctx, req.NamespacedName, &rs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !rs.DeletionTimestamp.IsZero() || rs.Status.Phase == gameplanev1alpha1.RestorePhaseSucceeded ||
		rs.Status.Phase == gameplanev1alpha1.RestorePhaseFailed {
		return r.cleanupRestore(ctx, &rs)
	}

	// Pin the snapshotID at first observation so retention can't pull
	// the rug out from under us mid-restore.
	if rs.Status.SnapshotID == "" {
		var src gameplanev1alpha1.Backup
		if err := r.Get(ctx, types.NamespacedName{Name: rs.Spec.BackupRef.Name, Namespace: rs.Namespace}, &src); err != nil {
			if apierrors.IsNotFound(err) {
				return r.fail(ctx, &rs, fmt.Sprintf("source backup %q not found", rs.Spec.BackupRef.Name))
			}
			return ctrl.Result{}, err
		}
		if src.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
			// Terminal: a Failed backup will never grow a snapshotID, so
			// waiting would leave the Restore Pending forever.
			return r.fail(ctx, &rs, fmt.Sprintf(
				"source backup %q failed and has no usable snapshot: %s",
				rs.Spec.BackupRef.Name, src.Status.Message))
		}
		if src.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || src.Status.SnapshotID == "" {
			// Source backup is not ready yet. Stay Pending.
			if rs.Status.Phase != gameplanev1alpha1.RestorePhasePending {
				rs.Status.Phase = gameplanev1alpha1.RestorePhasePending
				rs.Status.ObservedGeneration = rs.Generation
				if err := r.Status().Update(ctx, &rs); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		rs.Status.SnapshotID = src.Status.SnapshotID
		if src.Spec.Strategy == "volume-snapshot" {
			// Volume-snapshot restores stand up a brand-new server rather
			// than suspending and overwriting an existing one, so they skip
			// the Suspending phase entirely.
			rs.Status.Phase = gameplanev1alpha1.RestorePhaseRunning
		} else {
			rs.Status.Phase = gameplanev1alpha1.RestorePhaseSuspending
		}
		rs.Status.ObservedGeneration = rs.Generation
		if err := r.Status().Update(ctx, &rs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Re-resolve the source Backup so we know its strategy on every pass
	// (the pin block above only runs once). Volume-snapshot restores never
	// touch an existing server — they provision a new one seeded from the
	// CSI snapshot — so they branch off here, before the suspend/Job flow.
	var src gameplanev1alpha1.Backup
	if err := r.Get(ctx, types.NamespacedName{Name: rs.Spec.BackupRef.Name, Namespace: rs.Namespace}, &src); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, &rs, fmt.Sprintf("source backup %q disappeared", rs.Spec.BackupRef.Name))
		}
		return ctrl.Result{}, err
	}
	if src.Spec.Strategy == "volume-snapshot" && rs.Annotations[restoreTargetAnnotation] == "" {
		return r.reconcileVolumeSnapshotRestore(ctx, &rs, &src)
	}

	var gs gameplanev1alpha1.GameServer
	gsKey := types.NamespacedName{Name: rs.Spec.ServerRef.Name, Namespace: rs.Namespace}
	if err := r.apiReader().Get(ctx, gsKey, &gs); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, &rs, fmt.Sprintf("target server %q not found", rs.Spec.ServerRef.Name))
		}
		return ctrl.Result{}, err
	}
	if !gs.DeletionTimestamp.IsZero() {
		return r.fail(ctx, &rs, "target server is being deleted")
	}
	changed, err := r.bindRestoreTarget(ctx, &rs, &gs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if changed {
		return ctrl.Result{Requeue: true}, nil
	}
	target, err := parseRestoreIdentity(rs.Annotations[restoreTargetAnnotation])
	if err != nil {
		return ctrl.Result{}, err
	}
	if target.Name != gs.Name || target.UID != gs.UID {
		return r.fail(ctx, &rs, "target server changed identity during restore")
	}
	claimed, err := r.claimRestoreTarget(ctx, &rs, &gs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !claimed {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if rs.Status.Message != "" {
		rs.Status.Message = ""
		if err := r.Status().Update(ctx, &rs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// A missing template is not fatal: it only refines the FSGroup, and
	// buildBackupPodSecurityContext falls back to 65532 for a zero template.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: gs.Spec.TemplateRef.Name}, &tmpl); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if rs.Status.Phase == gameplanev1alpha1.RestorePhaseSuspending {
		if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseSuspended &&
			gs.Status.Phase != gameplanev1alpha1.GameServerPhaseStopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		stopped, progressed, err := r.restoreTargetStopped(ctx, &gs)
		if err != nil {
			return ctrl.Result{}, err
		}
		if progressed {
			return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
		}
		if !stopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		rs.Status.Phase = gameplanev1alpha1.RestorePhaseRunning
		if err := r.Status().Update(ctx, &rs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// src (the source Backup, with RepoRef for the Job env) was resolved
	// above before the volume-snapshot branch.
	backoff, deadline := resolveJobLimits(r.JobBackoffLimit, r.JobActiveDeadlineSeconds)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "restore-" + rs.Name, Namespace: rs.Namespace}}
	err = r.apiReader().Get(ctx, client.ObjectKeyFromObject(job), job)
	if apierrors.IsNotFound(err) {
		// Recheck after transitioning to Running, including recovery from a
		// crash between status persistence and Job creation.
		stopped, progressed, err := r.restoreTargetStopped(ctx, &gs)
		if err != nil {
			return ctrl.Result{}, err
		}
		if progressed {
			return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
		}
		if !stopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		if src.Spec.RepoRef == nil {
			return r.fail(ctx, &rs, "source backup has no restic repository")
		}
		job.Spec.BackoffLimit = &backoff
		job.Spec.ActiveDeadlineSeconds = &deadline
		job.Annotations = map[string]string{restoreTargetAnnotation: rs.Annotations[restoreTargetAnnotation]}
		job.Spec.Template.Labels = map[string]string{backupRestoreJobLabel: backupRestoreJobValue, restoreUIDLabel: string(rs.UID)}
		job.Spec.Template.Spec = r.buildRestorePodSpec(&rs, &src, &tmpl)
		job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
		if err := controllerutil.SetControllerReference(&rs, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, job); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	} else if !metav1.IsControlledBy(job, &rs) || job.Annotations[restoreTargetAnnotation] != rs.Annotations[restoreTargetAnnotation] {
		// Legacy Jobs have no pinned target identity or durable workload
		// fence. Cancel and drain owned workers rather than accept them.
		return r.fail(ctx, &rs, "restore Job does not belong to this Restore and target")
	}

	switch {
	case job.Status.Succeeded > 0:
		busy, err := r.restoreWorkersLive(ctx, rs.Namespace, restoreIdentity{Name: rs.Name, UID: rs.UID}, false)
		if err != nil {
			return ctrl.Result{}, err
		}
		if busy {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		now := metav1.Now()
		rs.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
		if rs.Status.StartTime == nil && job.Status.StartTime != nil {
			rs.Status.StartTime = job.Status.StartTime
		}
		if rs.Status.CompletionTime == nil {
			if job.Status.CompletionTime != nil {
				rs.Status.CompletionTime = job.Status.CompletionTime
			} else {
				rs.Status.CompletionTime = &now
			}
		}
		rs.Status.Conditions = upsertCondition(rs.Status.Conditions, metav1.Condition{
			Type:               "Completed",
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			ObservedGeneration: rs.Generation,
		})
		if err := r.Status().Update(ctx, &rs); err != nil {
			return ctrl.Result{}, err
		}
		return r.cleanupRestore(ctx, &rs)

	case jobPermanentlyFailed(job):
		// Leave the server suspended; surface the failure.
		return r.fail(ctx, &rs, "restore job reported Failed")

	default:
		if rs.Status.StartTime == nil && job.Status.StartTime != nil {
			rs.Status.StartTime = job.Status.StartTime
			if err := r.Status().Update(ctx, &rs); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
}

func (r *RestoreReconciler) fail(ctx context.Context, rs *gameplanev1alpha1.Restore, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseFailed
	rs.Status.Message = msg
	if rs.Status.CompletionTime == nil {
		rs.Status.CompletionTime = &now
	}
	rs.Status.Conditions = upsertCondition(rs.Status.Conditions, metav1.Condition{
		Type:               "Completed",
		Status:             metav1.ConditionFalse,
		Reason:             "Failed",
		Message:            msg,
		ObservedGeneration: rs.Generation,
	})
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return r.cleanupRestore(ctx, rs)
}

func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.Restore{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// buildRestorePodSpec mirrors backup_controller.buildBackupPodSpec but
// runs `restic restore <id>` and mounts the data PVC read-write.
func (r *RestoreReconciler) buildRestorePodSpec(
	rs *gameplanev1alpha1.Restore, src *gameplanev1alpha1.Backup, tmpl *gameplanev1alpha1.GameTemplate,
) corev1.PodSpec {
	nonRoot := true
	roRootFS := true
	noPrivEsc := false
	uid := int64(65532)
	return corev1.PodSpec{
		SecurityContext: buildBackupPodSecurityContext(tmpl),
		Containers: []corev1.Container{{
			Name:  "restic",
			Image: resticImageOrDefault(r.ResticImage),
			// --target / restores each snapshot entry at its original
			// absolute path. The companion backup runs `restic backup
			// /data`, so the snapshot tree is rooted at /data/...; with
			// --target /data the path doubles to /data/data/marker.txt.
			//
			// --delete removes files under the restored paths that aren't
			// present in the snapshot. Without it, files created after the
			// snapshot (e.g. newer save files) survive the restore and the
			// server ends up loading data the snapshot never contained
			// (F-049) — the volume must match the snapshot exactly.
			// restic refuses to combine --delete with --target unless
			// --include or --exclude is also given ("this ensures that
			// you cannot accidentally delete the whole system"), since
			// otherwise the deletion walk isn't scoped and could touch
			// anything under --target. --include /data pins that scope
			// to the same subtree the companion backup captures, so the
			// restore both lands and prunes only inside /data.
			Args: []string{"restore", rs.Status.SnapshotID, "--target", "/", "--delete", "--include", "/data", "--retry-lock", "10m"},
			Env: []corev1.EnvVar{
				{Name: "RESTIC_REPOSITORY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: src.Spec.RepoRef.Name},
						Key:                  "repo",
					},
				}},
				{Name: "RESTIC_PASSWORD", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: src.Spec.RepoRef.Name},
						Key:                  "password",
					},
				}},
				{Name: "XDG_CACHE_HOME", Value: "/tmp/restic-cache"},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/data"},
				{Name: "cache", MountPath: "/tmp"},
			},
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             &nonRoot,
				RunAsUser:                &uid,
				ReadOnlyRootFilesystem:   &roRootFS,
				AllowPrivilegeEscalation: &noPrivEsc,
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		}},
		Volumes: []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: rs.Spec.ServerRef.Name + "-data",
					},
				},
			},
			{
				Name:         "cache",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		},
	}
}
