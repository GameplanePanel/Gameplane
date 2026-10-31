package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	backupWorkerSourceAnnotation     = "backup.gameplane.local/worker-source"
	backupAuthorizedWorkerAnnotation = "backup.gameplane.local/authorized-worker"
	backupStartTokenKey              = "start"
	backupWorkerProtocolAnnotation   = "backup.gameplane.local/worker-protocol"
)

// The waiting worker never reads source files until the operator authorizes
// this exact Backup/pod/source generation. Repository preparation takes a
// shared restic lock; later lock contention is bounded by the copy timeout.
// Values and tags are arguments/environment, never interpolated shell code.
const resticWorkerScript = `set -eu
for utility in timeout date cat touch sleep restic; do command -v "$utility" >/dev/null; done
timeout -s TERM -k 5 1 /bin/sh -c ':'
test -d /data && test -r /data && test -x /data
restic snapshots --json --retry-lock 10m >/dev/null
touch /tmp/prepared
while :; do
  now=$(date +%s)
  if [ -r /start/start ]; then
    token=$(cat /start/start)
    if [ -n "$token" ]; then
      read -r version backup pod source expires extra <<EOF
$token
EOF
      if [ "$version" = v1 ] && [ "$backup" = "$BACKUP_UID" ] && [ "$pod" = "$POD_UID" ] && [ "$source" = "$SOURCE_ID" ] && [ -z "$extra" ]; then
        case "$expires" in ''|*[!0-9]*) exit 1;; esac
        remaining=$((expires - now))
        [ "$remaining" -gt 0 ] || exit 1
        exec timeout -s TERM -k 5 "$remaining" restic "$@"
      fi
    fi
  fi
  [ "$now" -lt "$PREPARATION_EXPIRES" ] || exit 1
  sleep 1
done
`

type backupWorkerSource struct {
	PodUID      types.UID           `json:"podUID,omitempty"`
	NodeName    string              `json:"nodeName,omitempty"`
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

func backupStartConfigMapName(b *gameplanev1alpha1.Backup) string {
	// Backup names may be a full DNS subdomain; a fixed hash avoids suffix
	// overflow and keeps same-name replacement authorization separate.
	return fmt.Sprintf("backup-start-%x", sha256.Sum256([]byte(b.Name+"/"+string(b.UID))))[:61]
}

func backupWorkerSourceID(b *gameplanev1alpha1.Backup) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.Annotations[backupTargetAnnotation]+b.Annotations[backupPreparationAnnotation]+b.Annotations[backupWorkerSourceAnnotation])))
}

// Raw copies retain their no-agent behavior while pinning any observed game
// pod for placement and rejecting a replacement before authorization. The
// stronger stopped/live proof remains #716's preparation for protected copies.
// A nil source means the initial workload has not reached a stable node yet.
func (r *BackupReconciler) prepareBackupWorkerSource(ctx context.Context, b *gameplanev1alpha1.Backup) (*backupWorkerSource, error) {
	var source backupWorkerSource
	var pod corev1.Pod
	err := r.backupAPIReader().Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: b.Spec.ServerRef.Name + "-0"}, &pod)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	var ss appsv1.StatefulSet
	if err := r.backupAPIReader().Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: b.Spec.ServerRef.Name}, &ss); err != nil {
		return nil, err
	}
	source.Tolerations = ss.Spec.Template.Spec.Tolerations
	if err == nil {
		if !metav1.IsControlledBy(&pod, &ss) || !pod.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("%w: source pod is not an active owned workload", errBackupSourceChanged)
		}
		source.PodUID, source.NodeName, source.Tolerations = pod.UID, pod.Spec.NodeName, pod.Spec.Tolerations
	}
	if stored := b.Annotations[backupWorkerSourceAnnotation]; stored != "" {
		var previous backupWorkerSource
		if err := json.Unmarshal([]byte(stored), &previous); err != nil {
			return nil, fmt.Errorf("decode worker source: %w", err)
		}
		if previous.PodUID != source.PodUID || previous.NodeName != source.NodeName {
			return nil, fmt.Errorf("%w: worker source relocated or replaced", errBackupSourceChanged)
		}
		return &previous, nil
	}
	// The StatefulSet controller and scheduler may still be establishing the
	// first pod. Do not freeze an absent UID or empty node into the worker's
	// immutable source ID and then mistake normal startup for replacement.
	// An intentionally stopped workload remains eligible without any pod.
	if (err == nil && pod.Spec.NodeName == "") || (apierrors.IsNotFound(err) && (ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0)) {
		return nil, nil
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	patchBackupAnnotations(b, map[string]string{backupWorkerSourceAnnotation: string(encoded)})
	return &source, r.Update(ctx, b)
}

func (r *BackupReconciler) buildPreparedBackupPodSpec(b *gameplanev1alpha1.Backup, tmpl *gameplanev1alpha1.GameTemplate, source *backupWorkerSource, deadline time.Time) corev1.PodSpec {
	spec := r.buildBackupPodSpec(b, tmpl)
	automount := false
	spec.AutomountServiceAccountToken = &automount
	spec.Tolerations = source.Tolerations
	if source.NodeName != "" {
		term := corev1.NodeSelectorTerm{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{source.NodeName}}}}
		affinity := &corev1.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{Weight: 100, Preference: term}}}
		if r.HardSameNode {
			affinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{term}}
		}
		spec.Affinity = &corev1.Affinity{NodeAffinity: affinity}
	}
	spec.Volumes = append(spec.Volumes, corev1.Volume{Name: "start", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: backupStartConfigMapName(b)}}}})
	worker := &spec.Containers[0]
	worker.Command = []string{"/bin/sh", "-c"}
	worker.Args = append([]string{resticWorkerScript, "backup-worker"}, worker.Args...)
	worker.Env = append(worker.Env, corev1.EnvVar{Name: "BACKUP_UID", Value: string(b.UID)},
		corev1.EnvVar{Name: "POD_UID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.uid"}}},
		corev1.EnvVar{Name: "SOURCE_ID", Value: backupWorkerSourceID(b)},
		corev1.EnvVar{Name: "PREPARATION_EXPIRES", Value: strconv.FormatInt(deadline.Unix(), 10)})
	worker.VolumeMounts = append(worker.VolumeMounts, corev1.VolumeMount{Name: "start", MountPath: "/start", ReadOnly: true})
	worker.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-c", "test -f /tmp/prepared"}}}, PeriodSeconds: 2, FailureThreshold: 1}
	return spec
}

func (r *BackupReconciler) preparedBackupWorker(ctx context.Context, b *gameplanev1alpha1.Backup, job *batchv1.Job) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := r.backupAPIReader().List(ctx, &pods, client.InNamespace(b.Namespace)); err != nil {
		return nil, err
	}
	var candidate *corev1.Pod
	live := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if job.UID == "" || !metav1.IsControlledBy(pod, job) || !liveRestorePod(pod) {
			continue
		}
		live++
		if pod.UID == "" || !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodRunning || pod.Spec.NodeName == "" {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				candidate = pod
			}
		}
	}
	if live != 1 {
		return nil, nil
	}
	return candidate, nil
}

func (r *BackupReconciler) reconcilePreparedRestic(ctx context.Context, b *gameplanev1alpha1.Backup) (ctrl.Result, error) {
	// Existing Jobs use their original execution protocol. Never replace a
	// running pre-upgrade Job with a gated worker or claim it was prepared.
	var job batchv1.Job
	err := r.backupAPIReader().Get(ctx, client.ObjectKeyFromObject(b), &job)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if err == nil && !metav1.IsControlledBy(&job, b) {
		return r.fail(ctx, b, "copy Job is not owned by this Backup")
	}
	if err == nil && job.Annotations[backupWorkerProtocolAnnotation] != "v1" {
		if res, err := r.prepareBackupQuiesce(ctx, b); err != nil || res.RequeueAfter > 0 {
			return res, err
		}
		return r.mirrorJobStatus(ctx, b, &job)
	}
	// Queueing behind another lifecycle owner is not worker preparation and
	// must not consume its budget before this Backup can start preparing.
	if b.Annotations[backupPreparationDeadlineAnnotation] == "" {
		claimed, err := r.claimBackupTarget(ctx, b)
		if err != nil {
			if errors.Is(err, errLifecycleGenerationChanged) {
				return r.abortResticCopy(ctx, b, err.Error())
			}
			return ctrl.Result{}, err
		}
		if !claimed {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}
	preparation, err := r.backupDeadline(ctx, b, backupPreparationDeadlineAnnotation, backupTimeout(r.PreparationTimeout, DefaultBackupPreparationTimeout))
	if err != nil {
		return r.abortResticCopy(ctx, b, err.Error())
	}
	if b.Annotations[backupAuthorizedWorkerAnnotation] == "" && time.Now().After(preparation) {
		return r.abortResticCopy(ctx, b, "worker preparation deadline exceeded before copy authorization")
	}
	if value := b.Annotations[backupQuiesceDeadlineAnnotation]; value != "" {
		deadline, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return r.abortResticCopy(ctx, b, "invalid persisted quiesce deadline")
		}
		finishedInTime := job.Status.Succeeded > 0 && job.Status.CompletionTime != nil && !job.Status.CompletionTime.After(deadline)
		if time.Now().After(deadline) && !finishedInTime {
			return r.abortResticCopy(ctx, b, "maximum quiesce duration exceeded; copy invalidated")
		}
	}
	if res, err := r.prepareBackupWorkload(ctx, b); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	// Admission refreshes the target. Read its effective content only after
	// owning the workload fence, so a rollout completed before admission
	// cannot leave the worker using an earlier filesystem layout.
	bound, err := r.ownedBackupTarget(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if bound == nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	tmpl, err := effectiveGameTemplate(ctx, r.backupAPIReader(), bound)
	if err != nil && (bound.Annotations[effectiveTemplateAnnotation] != "" || !apierrors.IsNotFound(err)) {
		return ctrl.Result{}, err
	}
	source, err := r.prepareBackupWorkerSource(ctx, b)
	if err != nil {
		return r.abortResticCopy(ctx, b, err.Error())
	}
	if source == nil {
		if err := r.setBackupProgress(ctx, b, "WorkerPreparing", "waiting for the source workload to be created and scheduled"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	gate := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: backupStartConfigMapName(b), Namespace: b.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, gate, func() error {
		if !gate.CreationTimestamp.IsZero() && !metav1.IsControlledBy(gate, b) {
			return fmt.Errorf("copy authorization ConfigMap is not owned by this Backup")
		}
		if gate.Data == nil {
			gate.Data = map[string]string{backupStartTokenKey: ""}
		}
		return controllerutil.SetControllerReference(b, gate, r.Scheme)
	}); err != nil {
		return ctrl.Result{}, err
	}
	if job.Name == "" {
		backoff, deadline := resolveJobLimits(r.JobBackoffLimit, r.JobActiveDeadlineSeconds)
		job = batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace, Annotations: map[string]string{backupWorkerProtocolAnnotation: "v1"}}, Spec: batchv1.JobSpec{BackoffLimit: &backoff, ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{backupRestoreJobLabel: backupRestoreJobValue, backupUIDLabel: string(b.UID)}}, Spec: r.buildPreparedBackupPodSpec(b, tmpl, source, preparation)}}}
		job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
		if err := controllerutil.SetControllerReference(b, &job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}
	}
	if jobPermanentlyFailed(&job) {
		return r.abortResticCopy(ctx, b, "backup job reported Failed")
	}
	if job.Status.Succeeded > 0 {
		if b.Annotations[backupAuthorizedWorkerAnnotation] == "" {
			return r.abortResticCopy(ctx, b, "backup worker completed without copy authorization")
		}
		return r.mirrorJobStatus(ctx, b, &job)
	}
	worker, err := r.preparedBackupWorker(ctx, b, &job)
	if err != nil {
		return ctrl.Result{}, err
	}
	if worker == nil {
		if err := r.setBackupProgress(ctx, b, "WorkerPreparing", "waiting for a scheduled worker with mounted source and validated repository"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	// Raw and already-stopped copies have no save-off window. Their tokens use
	// the full Job deadline, preserving long-copy support independently.
	_, copySeconds := resolveJobLimits(r.JobBackoffLimit, r.JobActiveDeadlineSeconds)
	if job.Spec.ActiveDeadlineSeconds != nil && *job.Spec.ActiveDeadlineSeconds > 0 {
		copySeconds = *job.Spec.ActiveDeadlineSeconds
	}
	const maxDurationSeconds = int64((1<<63 - 1) / time.Second)
	if copySeconds > maxDurationSeconds {
		copySeconds = maxDurationSeconds
	}
	copyBudget := time.Duration(copySeconds) * time.Second
	protected := b.Spec.Quiesce && agentQuiesceAvailable(r.AgentClient)
	if prepared, err := parseBackupPreparation(b); err != nil {
		return ctrl.Result{}, err
	} else if prepared != nil && prepared.Outcome == "already-stopped" {
		protected = false
	}
	deadlineKey := backupQuiesceDeadlineAnnotation
	if protected {
		copyBudget = backupTimeout(r.QuiesceTimeout, DefaultBackupQuiesceTimeout)
	}
	deadline, err := r.backupDeadline(ctx, b, deadlineKey, copyBudget)
	if err != nil {
		return ctrl.Result{}, err
	}
	if res, err := r.prepareBackupQuiesce(ctx, b); err != nil || res.RequeueAfter > 0 {
		return res, err
	}
	if _, err := r.prepareBackupWorkerSource(ctx, b); err != nil {
		return r.abortResticCopy(ctx, b, err.Error())
	}
	// Re-read readiness after the RPC. A replacement worker cannot inherit
	// the first pod's authorization, including during a Job retry.
	current, err := r.preparedBackupWorker(ctx, b, &job)
	if err != nil {
		return ctrl.Result{}, err
	}
	if current == nil || current.UID != worker.UID {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if !time.Now().Before(deadline) {
		return r.abortResticCopy(ctx, b, "maximum quiesce duration exceeded before start authorization")
	}
	if b.Annotations[backupAuthorizedWorkerAnnotation] != string(worker.UID) {
		patchBackupAnnotations(b, map[string]string{backupAuthorizedWorkerAnnotation: string(worker.UID)})
		if err := r.Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	token := fmt.Sprintf("v1 %s %s %s %d\n", b.UID, worker.UID, backupWorkerSourceID(b), deadline.Unix())
	if gate.Data[backupStartTokenKey] != token {
		gate.Data[backupStartTokenKey] = token
		if err := r.Update(ctx, gate); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.setBackupProgress(ctx, b, "WorkerAuthorized", "prepared worker authorized; copy deadline "+deadline.UTC().Format(time.RFC3339)); err != nil {
		return ctrl.Result{}, err
	}
	return r.mirrorJobStatus(ctx, b, &job)
}
