package controller

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const nodeCaptureNamespaceAnnotation = "gameplane.local/capture-node-namespace"
const nodeCaptureNodeAnnotation = "gameplane.local/capture-node"
const nodeCaptureContainerAnnotation = "gameplane.local/capture-container-id"
const nodeCaptureFinishedAnnotation = "gameplane.local/capture-worker-finished"
const nodeCaptureFinalizer = "gameplane.local/node-capture-cleanup"
const nodeCaptureUIDLabel = "gameplane.local/capture-uid"
const nodeCaptureComponentLabel = "gameplane.local/capture-component"

func nodeCaptureName(nc *gameplanev1alpha1.NetworkCapture) string { return "capture-" + string(nc.UID) }
func (r *NetworkCaptureReconciler) nodeReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *NetworkCaptureReconciler) useNodeCapture(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture) (bool, error) {
	if nc.Annotations[nodeCaptureNamespaceAnnotation] != "" || controllerutil.ContainsFinalizer(nc, nodeCaptureFinalizer) {
		return true, nil
	}
	// A profile/opt-in change must not reinterpret an already-started legacy
	// capture as a new node worker or abandon its stop/retention protocol.
	if nc.Annotations[capturePodUIDAnnotation] != "" || nc.Status.Phase == gameplanev1alpha1.CapturePhaseRunning || isTerminalCapturePhase(nc.Status.Phase) {
		return false, nil
	}
	if r.RestrictedWorkloads {
		return true, nil
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: nc.Namespace, Name: nc.Spec.ServerRef.Name}, &gs); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if gs.Spec.Capture != nil && gs.Spec.Capture.NodeDiagnostic {
		return true, nil
	}
	var pod corev1.Pod
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: nc.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return pod.Annotations[workloadSecurityLabel] == "restricted", nil
}

func nodeGameContainerID(pod *corev1.Pod) (string, error) {
	if pod.Spec.NodeName == "" || pod.UID == "" || pod.Spec.HostNetwork || pod.DeletionTimestamp != nil {
		return "", errors.New("node capture requires a live scheduled Pod with its own network namespace")
	}
	for _, c := range pod.Status.ContainerStatuses {
		if c.Name == "game" && c.State.Running != nil {
			id := strings.TrimPrefix(c.ContainerID, "containerd://")
			decoded, err := hex.DecodeString(id)
			if err != nil || len(decoded) != 32 || !strings.HasPrefix(c.ContainerID, "containerd://") {
				return "", errors.New("node capture currently supports Linux containerd targets only")
			}
			return c.ContainerID, nil
		}
	}
	return "", errors.New("game container is not running")
}

func nodeAffinity(name string) *corev1.Affinity {
	return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{name}}}}}}}}
}

func nodeWorkerPodSpec(nc *gameplanev1alpha1.NetworkCapture, gs *gameplanev1alpha1.GameServer, pod *corev1.Pod, image, pvc, filter string, duration, size int64) (corev1.PodSpec, error) {
	id, err := nodeGameContainerID(pod)
	if err != nil {
		return corev1.PodSpec{}, err
	}
	if duration < 1 || duration > 3600 || size < 1 || size > 900*1024*1024 || filter == "" {
		return corev1.PodSpec{}, errors.New("node capture requires a filter, duration <= 3600s and size <= 900Mi")
	}
	raw, err := json.Marshal(map[string]any{"serverUID": string(gs.UID), "captureUID": string(nc.UID), "captureID": nc.Name, "podUID": string(pod.UID), "containerID": id, "node": pod.Spec.NodeName, "filter": filter, "durationSeconds": duration, "maxSizeBytes": size})
	if err != nil {
		return corev1.PodSpec{}, err
	}
	return corev1.PodSpec{
		HostPID: true, RestartPolicy: corev1.RestartPolicyNever, AutomountServiceAccountToken: ptrTo(false), Affinity: nodeAffinity(pod.Spec.NodeName), NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
		Tolerations:                   append([]corev1.Toleration(nil), pod.Spec.Tolerations...),
		TerminationGracePeriodSeconds: ptrTo(int64(10)),
		SecurityContext:               &corev1.PodSecurityContext{RunAsUser: ptrTo(int64(0)), RunAsGroup: ptrTo(int64(65532)), FSGroup: ptrTo(int64(65532))},
		Volumes:                       []corev1.Volume{{Name: "captures", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc}}}},
		Containers: []corev1.Container{{Name: "capture", Image: image, Command: []string{"/capture-tool"}, Args: []string{"--node-worker"}, Env: []corev1.EnvVar{{Name: "NODE_CAPTURE_CONFIG", Value: string(raw)}}, VolumeMounts: []corev1.VolumeMount{{Name: "captures", MountPath: "/tmp/captures"}},
			Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("256Mi")}},
			SecurityContext: &corev1.SecurityContext{RunAsUser: ptrTo(int64(0)), ReadOnlyRootFilesystem: ptrTo(true), AllowPrivilegeEscalation: ptrTo(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}, Add: []corev1.Capability{"SYS_ADMIN", "SYS_PTRACE", "NET_RAW"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}},
		}},
	}, nil
}

// Each capture gets a same-namespace anchor. Cross-namespace ownerReferences
// cannot link diagnostics to the NetworkCapture; its finalizer deletes this
// anchor in the recorded namespace and waits for foreground GC to finish.
func (r *NetworkCaptureReconciler) createNodeObject(ctx context.Context, obj client.Object, anchor *corev1.ConfigMap, nc *gameplanev1alpha1.NetworkCapture) error {
	obj.SetNamespace(anchor.Namespace)
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[nodeCaptureUIDLabel] = string(nc.UID)
	obj.SetLabels(labels)
	if err := controllerutil.SetControllerReference(anchor, obj, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, obj); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		live, ok := obj.DeepCopyObject().(client.Object)
		if !ok {
			return errors.New("diagnostic resource is not a Kubernetes object")
		}
		if err := r.nodeReader().Get(ctx, client.ObjectKeyFromObject(obj), live); err != nil {
			return err
		}
		owner := metav1.GetControllerOf(live)
		if owner == nil || owner.UID != anchor.UID || live.GetLabels()[nodeCaptureUIDLabel] != string(nc.UID) {
			return errors.New("diagnostic resource identity collision")
		}
	}
	return nil
}

func (r *NetworkCaptureReconciler) ensureNodeArtifacts(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture, gs *gameplanev1alpha1.GameServer, pod *corev1.Pod) (*corev1.ConfigMap, error) {
	ns, name := nc.Annotations[nodeCaptureNamespaceAnnotation], nodeCaptureName(nc)
	anchor := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{nodeCaptureUIDLabel: string(nc.UID)}}}
	if err := r.Create(ctx, anchor); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if err := r.nodeReader().Get(ctx, client.ObjectKeyFromObject(anchor), anchor); err != nil {
			return nil, err
		}
		if anchor.Labels[nodeCaptureUIDLabel] != string(nc.UID) {
			return nil, errors.New("diagnostic anchor identity collision")
		}
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}}}}
	if r.DiagnosticsStorageClass != "" {
		pvc.Spec.StorageClassName = &r.DiagnosticsStorageClass
	}
	if err := r.createNodeObject(ctx, pvc, anchor, nc); err != nil {
		return nil, err
	}
	if err := r.ensureNodeCertificate(ctx, anchor, nc); err != nil {
		return nil, err
	}
	image := r.CaptureSidecarImage
	if image == "" {
		image = DefaultCaptureSidecarImage
	}
	files := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-files", Labels: map[string]string{nodeCaptureComponentLabel: "artifacts"}}, Spec: corev1.PodSpec{
		AutomountServiceAccountToken: ptrTo(false), Affinity: nodeAffinity(pod.Spec.NodeName),
		Tolerations:     append([]corev1.Toleration(nil), pod.Spec.Tolerations...),
		SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptrTo(true), RunAsUser: ptrTo(int64(65532)), RunAsGroup: ptrTo(int64(65532)), FSGroup: ptrTo(int64(65532)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
		Volumes:         []corev1.Volume{{Name: "captures", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name}}}, {Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: name + "-tls"}}}},
		Containers: []corev1.Container{{Name: "artifacts", Image: image, Command: []string{"/capture-tool"}, Args: []string{"--artifacts-only"}, Env: []corev1.EnvVar{{Name: "GAMEPLANE_SERVER_UID", Value: string(gs.UID)}}, Ports: []corev1.ContainerPort{{Name: "capture", ContainerPort: 9091}}, VolumeMounts: []corev1.VolumeMount{{Name: "captures", MountPath: "/tmp/captures"}, {Name: "tls", MountPath: "/etc/tls", ReadOnly: true}},
			Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("32Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("128Mi")}},
			SecurityContext: &corev1.SecurityContext{RunAsNonRoot: ptrTo(true), ReadOnlyRootFilesystem: ptrTo(true), AllowPrivilegeEscalation: ptrTo(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
		}},
	}}
	if err := r.createNodeObject(ctx, files, anchor, nc); err != nil {
		return nil, err
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name + "-agent"}, Spec: corev1.ServiceSpec{Selector: map[string]string{nodeCaptureUIDLabel: string(nc.UID), nodeCaptureComponentLabel: "artifacts"}, Ports: []corev1.ServicePort{{Name: "capture", Port: 9091, TargetPort: intstr.FromInt32(9091)}}}}
	if err := r.createNodeObject(ctx, svc, anchor, nc); err != nil {
		return nil, err
	}
	return anchor, nil
}

func (r *NetworkCaptureReconciler) ensureNodeCertificate(ctx context.Context, anchor *corev1.ConfigMap, nc *gameplanev1alpha1.NetworkCapture) error {
	var ca corev1.Secret
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: r.AgentCASecretNamespace, Name: r.AgentCASecretName}, &ca); err != nil {
		return err
	}
	fingerprint := caCertFingerprint(ca.Data["ca.crt"])
	key := types.NamespacedName{Namespace: anchor.Namespace, Name: anchor.Name + "-tls"}
	var existing corev1.Secret
	err := r.nodeReader().Get(ctx, key, &existing)
	if err == nil {
		owner := metav1.GetControllerOf(&existing)
		if owner == nil || owner.UID != anchor.UID {
			return errors.New("diagnostic certificate identity collision")
		}
		if existing.Annotations["gameplane.local/ca-fingerprint"] == fingerprint {
			return nil
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	cert, keyCA, err := parseAgentCA(ca.Data["ca.crt"], ca.Data["ca.key"])
	if err != nil {
		return err
	}
	fake := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: anchor.Name, Namespace: anchor.Namespace}}
	crt, private, err := signAgentServerCert(cert, keyCA, fake)
	if err != nil {
		return err
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Annotations: map[string]string{"gameplane.local/ca-fingerprint": fingerprint}}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{"tls.crt": crt, "tls.key": private, "ca.crt": ca.Data["ca.crt"]}}
	if existing.UID != "" {
		existing.Data = sec.Data
		existing.Annotations = sec.Annotations
		return r.Update(ctx, &existing)
	}
	return r.createNodeObject(ctx, sec, anchor, nc)
}

func (r *NetworkCaptureReconciler) deleteNodeWorkers(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture) (bool, error) {
	ns := nc.Annotations[nodeCaptureNamespaceAnnotation]
	if ns == "" {
		return true, nil
	}
	var job batchv1.Job
	err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: ns, Name: nodeCaptureName(nc) + "-worker"}, &job)
	if err == nil {
		if job.Labels[nodeCaptureUIDLabel] != string(nc.UID) {
			return false, errors.New("diagnostic worker identity collision")
		}
		if job.DeletionTimestamp == nil {
			if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationForeground), client.Preconditions{UID: &job.UID}); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}
	var pods corev1.PodList
	if err := r.nodeReader().List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{nodeCaptureUIDLabel: string(nc.UID), nodeCaptureComponentLabel: "worker"}); err != nil {
		return false, err
	}
	// Independently drain orphan worker Pods too, including a Job deleted by
	// another actor with orphan propagation. Never free a capture lock early.
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp == nil {
			if err := r.Delete(ctx, p, client.Preconditions{UID: &p.UID}); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
		}
	}
	return apierrors.IsNotFound(err) && len(pods.Items) == 0, nil
}

func (r *NetworkCaptureReconciler) cleanupNodeCapture(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture) (bool, error) {
	drained, err := r.deleteNodeWorkers(ctx, nc)
	if err != nil || !drained {
		return false, err
	}
	ns := nc.Annotations[nodeCaptureNamespaceAnnotation]
	if ns == "" {
		return true, nil
	}
	var anchor corev1.ConfigMap
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: ns, Name: nodeCaptureName(nc)}, &anchor); err != nil {
		return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
	}
	if anchor.Labels[nodeCaptureUIDLabel] != string(nc.UID) {
		return false, errors.New("diagnostic anchor identity collision")
	}
	if anchor.DeletionTimestamp == nil {
		if err := r.Delete(ctx, &anchor, client.PropagationPolicy(metav1.DeletePropagationForeground), client.Preconditions{UID: &anchor.UID}); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}

func (r *NetworkCaptureReconciler) nodeFail(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture, message string) (ctrl.Result, error) {
	drained, err := r.deleteNodeWorkers(ctx, nc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !drained {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return r.fail(ctx, nc, message)
}

func (r *NetworkCaptureReconciler) reconcileNodeCapture(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture) (ctrl.Result, error) {
	if nc.DeletionTimestamp != nil {
		clean, err := r.cleanupNodeCapture(ctx, nc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !clean {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		if err := r.releaseNodeCaptureLock(ctx, nc, ptrTo(metav1.Now())); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(nc, nodeCaptureFinalizer)
		return ctrl.Result{}, r.Update(ctx, nc)
	}
	if isTerminalCapturePhase(nc.Status.Phase) {
		drained, err := r.deleteNodeWorkers(ctx, nc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !drained {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		if nc.Status.CompletionTime == nil {
			nc.Status.CompletionTime = ptrTo(metav1.Now())
			return ctrl.Result{RequeueAfter: retentionPollInterval}, r.Status().Update(ctx, nc)
		}
		ttl := effectiveRetentionSeconds(nc.Spec.TTLSecondsAfterFinished, r.CaptureDefaultRetentionSeconds, r.CaptureMaxRetentionSeconds)
		if time.Now().Before(nc.Status.CompletionTime.Add(time.Duration(ttl) * time.Second)) {
			// Retained artifacts need certificate rotation even after the target
			// game Pod has gone away. No target or worker is recreated here.
			if ns := nc.Annotations[nodeCaptureNamespaceAnnotation]; ns != "" {
				var anchor corev1.ConfigMap
				if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: ns, Name: nodeCaptureName(nc)}, &anchor); err != nil {
					if !apierrors.IsNotFound(err) {
						return ctrl.Result{}, err
					}
				} else if anchor.Labels[nodeCaptureUIDLabel] != string(nc.UID) {
					return ctrl.Result{}, errors.New("diagnostic anchor identity collision")
				} else if err := r.ensureNodeCertificate(ctx, &anchor, nc); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{RequeueAfter: retentionPollInterval}, nil
		}
		return ctrl.Result{}, r.Delete(ctx, nc, client.Preconditions{UID: &nc.UID})
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: nc.Namespace, Name: nc.Spec.ServerRef.Name}, &gs); err != nil {
		if apierrors.IsNotFound(err) {
			return r.nodeFail(ctx, nc, "target GameServer no longer exists")
		}
		return ctrl.Result{}, err
	}
	if owner := metav1.GetControllerOf(nc); owner != nil && owner.UID != gs.UID {
		return r.nodeFail(ctx, nc, "target GameServer identity changed")
	}
	if err := r.ensureOwnerReference(ctx, nc, &gs); err != nil {
		return ctrl.Result{}, err
	}
	if gs.DeletionTimestamp != nil {
		return r.nodeFail(ctx, nc, "target GameServer is terminating")
	}
	if !r.CaptureEnabled || r.DiagnosticsNamespace == "" || gs.Spec.Capture == nil || !gs.Spec.Capture.Enabled || !gs.Spec.Capture.NodeDiagnostic {
		return r.nodeFail(ctx, nc, "node diagnostics require cluster deployment opt-in and administrator spec.capture.nodeDiagnostic opt-in on this server")
	}
	if _, stop := nc.Annotations[stopRequestedAnnotation]; stop {
		drained, err := r.deleteNodeWorkers(ctx, nc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !drained {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return r.finishNodeCapture(ctx, nc, true)
	}
	if nc.Annotations[nodeCaptureFinishedAnnotation] != "" {
		drained, err := r.deleteNodeWorkers(ctx, nc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !drained {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return r.finishNodeCapture(ctx, nc, false)
	}
	var pod corev1.Pod
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return r.nodeFail(ctx, nc, "target game Pod no longer exists")
		}
		return ctrl.Result{}, err
	}
	var ss appsv1.StatefulSet
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name}, &ss); err != nil {
		return ctrl.Result{}, err
	}
	ssOwner, podOwner := metav1.GetControllerOf(&ss), metav1.GetControllerOf(&pod)
	if ssOwner == nil || ssOwner.UID != gs.UID || podOwner == nil || podOwner.UID != ss.UID {
		return r.nodeFail(ctx, nc, "target Pod is not controlled by this GameServer")
	}
	containerID, err := nodeGameContainerID(&pod)
	if err != nil {
		return r.nodeFail(ctx, nc, err.Error())
	}
	if bound := nc.Annotations[capturePodUIDAnnotation]; bound != "" && (bound != string(pod.UID) || nc.Annotations[nodeCaptureNodeAnnotation] != pod.Spec.NodeName || nc.Annotations[nodeCaptureContainerAnnotation] != containerID) {
		return r.nodeFail(ctx, nc, "target Pod, node or container identity changed")
	}
	if nc.Annotations[nodeCaptureNamespaceAnnotation] == "" {
		if r.DiagnosticsNamespace == gs.Namespace {
			return r.nodeFail(ctx, nc, "node diagnostics namespace must be separate from the games namespace")
		}
		var namespace corev1.Namespace
		if err := r.nodeReader().Get(ctx, types.NamespacedName{Name: r.DiagnosticsNamespace}, &namespace); err != nil {
			return ctrl.Result{}, err
		}
		if namespace.Labels["pod-security.kubernetes.io/enforce"] != "privileged" {
			return r.nodeFail(ctx, nc, "diagnostics namespace must explicitly permit privileged node diagnostics")
		}
		var captures gameplanev1alpha1.NetworkCaptureList
		if err := r.nodeReader().List(ctx, &captures, client.InNamespace(nc.Namespace)); err != nil {
			return ctrl.Result{}, err
		}
		for i := range captures.Items {
			o := &captures.Items[i]
			if o.UID != nc.UID && o.Spec.ServerRef.Name == gs.Name && !isTerminalCapturePhase(o.Status.Phase) && (o.CreationTimestamp.Before(&nc.CreationTimestamp) || (o.CreationTimestamp.Equal(&nc.CreationTimestamp) && o.Name < nc.Name)) {
				return r.nodeFail(ctx, nc, "another capture is already in progress on this GameServer")
			}
		}
		if nc.Annotations == nil {
			nc.Annotations = map[string]string{}
		}
		nc.Annotations[nodeCaptureNamespaceAnnotation] = r.DiagnosticsNamespace
		nc.Annotations[capturePodUIDAnnotation] = string(pod.UID)
		nc.Annotations[nodeCaptureNodeAnnotation] = pod.Spec.NodeName
		nc.Annotations[nodeCaptureContainerAnnotation] = containerID
		controllerutil.AddFinalizer(nc, nodeCaptureFinalizer)
		if err := r.Update(ctx, nc); err != nil {
			return ctrl.Result{}, err
		}
	}
	filter, failMessage, err := r.captureFilter(ctx, nc, &gs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if failMessage != "" {
		return r.nodeFail(ctx, nc, failMessage)
	}
	duration, size := r.CaptureDefaultMaxDurationSeconds, r.CaptureDefaultMaxSizeBytes
	if nc.Spec.MaxDuration != nil {
		duration = int64(nc.Spec.MaxDuration.Seconds())
	}
	if nc.Spec.MaxSize != nil {
		size = nc.Spec.MaxSize.Value()
	}
	image := r.CaptureSidecarImage
	if image == "" {
		image = DefaultCaptureSidecarImage
	}
	spec, err := nodeWorkerPodSpec(nc, &gs, &pod, image, nodeCaptureName(nc), *filter, duration, size)
	if err != nil {
		return r.nodeFail(ctx, nc, err.Error())
	}
	anchor, err := r.ensureNodeArtifacts(ctx, nc, &gs, &pod)
	if err != nil {
		return ctrl.Result{}, err
	}
	var job batchv1.Job
	jobKey := types.NamespacedName{Namespace: anchor.Namespace, Name: anchor.Name + "-worker"}
	err = r.nodeReader().Get(ctx, jobKey, &job)
	if apierrors.IsNotFound(err) {
		if nc.Status.Phase == gameplanev1alpha1.CapturePhaseRunning {
			return r.nodeFail(ctx, nc, "node diagnostic worker disappeared")
		}
		job = batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobKey.Name}, Spec: batchv1.JobSpec{BackoffLimit: ptrTo(int32(0)), ActiveDeadlineSeconds: ptrTo(duration + 30), Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{nodeCaptureUIDLabel: string(nc.UID), nodeCaptureComponentLabel: "worker"}}, Spec: spec}}}
		if err := r.createNodeObject(ctx, &job, anchor, nc); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}
	if job.Labels[nodeCaptureUIDLabel] != string(nc.UID) {
		return ctrl.Result{}, errors.New("diagnostic worker identity collision")
	}
	if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
		if job.Status.Failed > 0 {
			return r.nodeFail(ctx, nc, "node diagnostic worker failed; verify Linux containerd cgroups, node capabilities and storage")
		}
		nc.Annotations[nodeCaptureFinishedAnnotation] = time.Now().UTC().Format(time.RFC3339)
		if err := r.Update(ctx, nc); err != nil {
			return ctrl.Result{}, err
		}
		drained, err := r.deleteNodeWorkers(ctx, nc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !drained {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return r.finishNodeCapture(ctx, nc, false)
	}
	if nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
		nc.Status.Phase = gameplanev1alpha1.CapturePhaseRunning
		nc.Status.StartTime = ptrTo(metav1.Now())
		nc.Status.Message = "node diagnostic capture running"
		if err := r.patchGameServerActiveCapture(ctx, &gs, nc, nil); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Status().Update(ctx, nc); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *NetworkCaptureReconciler) finishNodeCapture(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture, stopped bool) (ctrl.Result, error) {
	if nc.Annotations[nodeCaptureNamespaceAnnotation] == "" {
		return r.fail(ctx, nc, "capture stopped before the node diagnostic worker started")
	}
	phase, packets, bytes, message, err := r.SidecarClient.GetCaptureStatus(ctx, nc.Annotations[nodeCaptureNamespaceAnnotation], nodeCaptureName(nc), nc.Name, captureOwnerUID(nc.OwnerReferences, nc.Spec.ServerRef.Name), string(nc.UID))
	if err != nil {
		if stopped {
			return r.fail(ctx, nc, "capture stopped before a completed artifact was available")
		}
		finished, parseErr := time.Parse(time.RFC3339, nc.Annotations[nodeCaptureFinishedAnnotation])
		if parseErr != nil || time.Since(finished) > 2*time.Minute {
			return r.fail(ctx, nc, "node diagnostic worker finished without an available artifact")
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	nc.Status.Phase = gameplanev1alpha1.CapturePhaseCompleted
	if phase != "completed" {
		nc.Status.Phase = gameplanev1alpha1.CapturePhaseFailed
	}
	nc.Status.PacketsWritten = packets
	nc.Status.BytesWritten = resource.NewQuantity(bytes, resource.BinarySI)
	nc.Status.Message = message
	nc.Status.CompletionTime = ptrTo(metav1.Now())
	if err := r.releaseNodeCaptureLock(ctx, nc, nc.Status.CompletionTime); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retentionPollInterval}, r.Status().Update(ctx, nc)
}

// A direct CR deletion can arrive before terminal status. Release only after
// worker drain and only for this capture/server incarnation; optimistic locking
// prevents a concurrent successor from losing its newly acquired pointer.
func (r *NetworkCaptureReconciler) releaseNodeCaptureLock(ctx context.Context, nc *gameplanev1alpha1.NetworkCapture, terminalTime *metav1.Time) error {
	var live gameplanev1alpha1.NetworkCapture
	if err := r.nodeReader().Get(ctx, client.ObjectKeyFromObject(nc), &live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if live.UID != nc.UID {
		return nil
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.nodeReader().Get(ctx, types.NamespacedName{Namespace: nc.Namespace, Name: nc.Spec.ServerRef.Name}, &gs); err != nil {
		return client.IgnoreNotFound(err)
	}
	if captureOwnerUID(nc.OwnerReferences, nc.Spec.ServerRef.Name) != string(gs.UID) || gs.Status.Capture == nil || gs.Status.Capture.ActiveCapture == nil || *gs.Status.Capture.ActiveCapture != nc.Name {
		return nil
	}
	base := gs.DeepCopy()
	gs.Status.Capture.ActiveCapture = nil
	gs.Status.Capture.LastCaptureTime = terminalTime
	return r.Status().Patch(ctx, &gs, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
