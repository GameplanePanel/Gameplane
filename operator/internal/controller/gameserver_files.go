package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// File-only pod (spec 040). While a server is stopped, the API asks for file
// access by stamping FilesRequestedAtAnnotation. The operator then runs a
// short-lived pod, <gs>-files, that carries the same data and mod volumes and
// the agent with --files-only. It never starts the game, never writes wake or
// idle state and is removed after FilesIdleTimeout without a file operation.
const (
	// FilesRequestedAtAnnotation holds an RFC 3339 timestamp written by the
	// API on every file operation against a stopped server.
	FilesRequestedAtAnnotation = "gameplane.local/files-requested-at"
	// DefaultFilesIdleTimeout is how long a file-only pod outlives its last
	// file operation when the operator sets no idle timeout.
	DefaultFilesIdleTimeout = 10 * time.Minute

	filesPodNameSuffix  = "-files"
	filesPodGoneRequeue = 2 * time.Second
)

// filesPodName is the name of the file-only pod for gs.
func filesPodName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + filesPodNameSuffix
}

// filesPodLabels are the only labels on a file-only pod. They deliberately
// omit the game labels so no game Service, PodDisruptionBudget, NetworkPolicy
// or wake path selects it; only the <gs>-agent Service selects it, through
// reconcileAgentService.
func filesPodLabels(gs *gameplanev1alpha1.GameServer) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "gameplane-files",
		"app.kubernetes.io/instance": gs.Name,
	}
}

// withAgentTransferFlags appends the optional per-agent flags (spec 040).
func withAgentTransferFlags(c corev1.Container, maxUploadBytes int64, filesOnly bool) corev1.Container {
	if maxUploadBytes > 0 {
		c.Args = append(c.Args, "--max-upload-bytes="+strconv.FormatInt(maxUploadBytes, 10))
	}
	if filesOnly {
		c.Args = append(c.Args, "--files-only")
	}
	return c
}

// filesTimeout is the configured idle timeout, or the default when unset.
func (r *GameServerReconciler) filesTimeout() time.Duration {
	if r.FilesIdleTimeout <= 0 {
		return DefaultFilesIdleTimeout
	}
	return r.FilesIdleTimeout
}

// filesRequestFresh reports whether a file operation was requested within
// timeout of now, and how long that window has left. An unparseable stamp,
// or one further in the future than timeout, is never fresh.
func filesRequestFresh(gs *gameplanev1alpha1.GameServer, now time.Time, timeout time.Duration) (bool, time.Duration) {
	raw := gs.Annotations[FilesRequestedAtAnnotation]
	if raw == "" {
		return false, 0
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil || at.After(now.Add(timeout)) {
		return false, 0
	}
	age := max(now.Sub(at), 0)
	if age >= timeout {
		return false, 0
	}
	return true, timeout - age
}

// getFilesPod returns the file-only pod for gs, or nil when there is none. A
// pod with that name that gs does not control is an error: it is never
// adopted or deleted.
func (r *GameServerReconciler) getFilesPod(ctx context.Context, gs *gameplanev1alpha1.GameServer) (*corev1.Pod, error) {
	var pod corev1.Pod
	key := types.NamespacedName{Namespace: gs.Namespace, Name: filesPodName(gs)}
	if err := r.Get(ctx, key, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get files pod: %w", err)
	}
	if !metav1.IsControlledBy(&pod, gs) {
		return nil, fmt.Errorf("pod %s exists and is not controlled by this GameServer", key.Name)
	}
	return &pod, nil
}

// filesPodServing reports whether the file-only pod exists and is not
// terminating, which is when the <gs>-agent Service routes to it.
func (r *GameServerReconciler) filesPodServing(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	pod, err := r.getFilesPod(ctx, gs)
	if err != nil {
		return false, err
	}
	return pod != nil && pod.DeletionTimestamp == nil, nil
}

// filesSafeToMount reports whether no backup, restore, wipe or rollout owner
// may be using the server's volumes, so a file-only pod can mount them.
func filesSafeToMount(gs *gameplanev1alpha1.GameServer) bool {
	if gs.Annotations[restoreGuardAnnotation] != "" || gs.Annotations[wipeGuardAnnotation] != "" || pendingWipe(gs) {
		return false
	}
	_, held, err := lifecycleOwner(gs)
	return !held && err == nil
}

// buildFilesPod renders the file-only pod. It runs the agent with --files-only
// and mounts only the data volume, the agent TLS Secret and the active mod
// volume. The RCON password and mod-portal credentials are left out, and so
// is the service-account token, since no heartbeat runs in this pod.
func (r *GameServerReconciler) buildFilesPod(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, ver *gameplanev1alpha1.GameVersion,
) (*corev1.Pod, error) {
	c := withAgentTransferFlags(
		buildAgentContainer(gs, tmpl, ver, r.AgentImage, r.AgentLogLevel, r.AgentImagePullPolicy, r.AgentResources),
		r.AgentMaxUploadBytes, true)
	keep := map[string]bool{"data": true, "agent-tls": true}
	if m := modVolumeMount(tmpl, ver); m != nil {
		keep[m.Name] = true
	}
	var mounts []corev1.VolumeMount
	for _, m := range c.VolumeMounts {
		if keep[m.Name] {
			mounts = append(mounts, m)
		}
	}
	c.VolumeMounts = mounts

	volumes := []corev1.Volume{
		{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: gs.Name + "-data"},
			},
		},
		{
			Name: "agent-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: agentTLSSecretName(gs)},
			},
		},
	}
	if v := modVolume(gs, tmpl, ver); v != nil {
		volumes = append(volumes, *v)
	}

	sec := gamePodSecurityContext(tmpl)
	if sec == nil {
		sec = &corev1.PodSecurityContext{}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      filesPodName(gs),
			Namespace: gs.Namespace,
			Labels:    filesPodLabels(gs),
		},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{c},
			Volumes:                       volumes,
			AutomountServiceAccountToken:  ptrTo(false),
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: ptrTo(int64(5)),
			SecurityContext:               sec,
		},
	}
	pod.Spec.NodeSelector, pod.Spec.Tolerations = r.GameWorkloadPolicy.placement(gs, tmpl)
	if r.RestrictedWorkloads {
		sec.RunAsNonRoot = ptrTo(true)
		sec.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
		pod.Annotations = map[string]string{workloadSecurityLabel: "restricted"}
	}
	if err := controllerutil.SetControllerReference(gs, pod, r.Scheme); err != nil {
		return nil, fmt.Errorf("set files pod owner: %w", err)
	}
	return pod, nil
}

// reconcileFilesPod keeps the file-only pod in step with the server. It exists
// only while the server has no replicas, a file operation is still inside the
// idle timeout, and no lifecycle owner holds the volumes. It is created only
// after the game pod is gone, because the data volume is ReadWriteOnce. It
// returns how long until the idle timeout could expire, or zero.
//
// startWanted is the replica count before holdStartForFilesPod ran. When a
// start is wanted, replicas may already be zero only because the hold is
// waiting for the files pod to drain, so no file-only pod is created then.
func (r *GameServerReconciler) reconcileFilesPod(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	ver *gameplanev1alpha1.GameVersion, replicas int32, startWanted bool,
) (time.Duration, error) {
	pod, err := r.getFilesPod(ctx, gs)
	if err != nil {
		return 0, err
	}
	fresh, remaining := filesRequestFresh(gs, time.Now(), r.filesTimeout())
	if replicas != 0 || startWanted || !fresh || !filesSafeToMount(gs) {
		stale := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: filesPodName(gs), Namespace: gs.Namespace}}
		if err := r.deleteIfControlledBy(ctx, gs, stale); err != nil {
			return 0, fmt.Errorf("delete files pod: %w", err)
		}
		if err := r.reconcileAgentService(ctx, gs); err != nil {
			return 0, err
		}
		return 0, nil
	}

	if pod != nil {
		// Still terminating: the volume must not be shared until the pod is
		// gone, so wait rather than create a replacement.
		if pod.DeletionTimestamp != nil {
			return filesPodGoneRequeue, nil
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			if err := r.deleteIfControlledBy(ctx, gs, pod); err != nil {
				return 0, fmt.Errorf("delete finished files pod: %w", err)
			}
			return filesPodGoneRequeue, nil
		}
	} else {
		// The game pod must be fully gone before a second pod mounts the
		// ReadWriteOnce data volume (FR-013c). Read it live, not from cache.
		var game corev1.Pod
		err := r.wipeReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &game)
		if err == nil {
			return filesPodGoneRequeue, nil
		}
		if !apierrors.IsNotFound(err) {
			return 0, fmt.Errorf("get game pod: %w", err)
		}
		fp, err := r.buildFilesPod(gs, tmpl, ver)
		if err != nil {
			return 0, err
		}
		if err := r.Create(ctx, fp); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return time.Second, nil
			}
			return 0, fmt.Errorf("create files pod: %w", err)
		}
	}
	if err := r.reconcileAgentService(ctx, gs); err != nil {
		return 0, err
	}
	// Nothing else wakes the reconciler when the idle window closes, so
	// requeue just past its end. A new file operation rewrites the stamp.
	return remaining + time.Second, nil
}

// holdStartForFilesPod keeps a scale-up waiting until the file-only pod is gone
// (FR-013c). The caller passes the replicas it computed, so the hold is a
// no-op unless a file-only pod exists.
func (r *GameServerReconciler) holdStartForFilesPod(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, replicas int32, requeue time.Duration,
) (int32, time.Duration, error) {
	if replicas <= 0 {
		return replicas, requeue, nil
	}
	// A file-only pod can exist only after a file operation stamped the
	// server, so servers that never used files pay no extra read.
	if gs.Annotations[FilesRequestedAtAnnotation] == "" {
		return replicas, requeue, nil
	}
	// Read live: a pod created moments ago may not be in the cache yet, and
	// starting the game beside it would share the ReadWriteOnce volume.
	var pod *corev1.Pod
	var live corev1.Pod
	key := types.NamespacedName{Namespace: gs.Namespace, Name: filesPodName(gs)}
	switch err := r.wipeReader().Get(ctx, key, &live); {
	case err == nil:
		if !metav1.IsControlledBy(&live, gs) {
			return 0, 0, fmt.Errorf("pod %s exists and is not controlled by this GameServer", key.Name)
		}
		pod = &live
	case !apierrors.IsNotFound(err):
		return 0, 0, fmt.Errorf("get files pod: %w", err)
	}
	if pod == nil {
		return replicas, requeue, nil
	}
	if err := r.deleteIfControlledBy(ctx, gs, pod); err != nil {
		return 0, 0, fmt.Errorf("delete files pod before start: %w", err)
	}
	if err := r.reconcileAgentService(ctx, gs); err != nil {
		return 0, 0, err
	}
	live = corev1.Pod{}
	switch err := r.wipeReader().Get(ctx, key, &live); {
	case apierrors.IsNotFound(err):
		return replicas, requeue, nil
	case err != nil:
		return 0, 0, fmt.Errorf("get files pod: %w", err)
	}
	// The pod object still exists (terminating), so the volume is still in
	// use. A StatefulSet that is already running is never scaled to zero here.
	var ss appsv1.StatefulSet
	err := r.wipeReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name}, &ss)
	if err != nil && !apierrors.IsNotFound(err) {
		return 0, 0, fmt.Errorf("get StatefulSet: %w", err)
	}
	if err == nil && ss.Spec.Replicas != nil && *ss.Spec.Replicas > 0 {
		return replicas, requeue, nil
	}
	return 0, filesPodGoneRequeue, nil
}
