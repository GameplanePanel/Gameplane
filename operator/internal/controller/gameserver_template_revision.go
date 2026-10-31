package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	effectiveTemplateAnnotation = "gameplane.local/effective-template-revision"
	templateRevisionAnnotation  = "gameplane.local/template-revision"
	templateSnapshotLabel       = "gameplane.local/template-snapshot"
	templateSnapshotKey         = "template.json"
	baselineTemplateAnnotation  = "gameplane.local/template-baseline"
	baselineWorkloadAnnotation  = "gameplane.local/template-baseline-workload"
	baselineInputsAnnotation    = "gameplane.local/template-baseline-inputs"
)

// Hash and persist content, not resourceVersion: status/metadata-only updates
// must not restart games. Name and the complete spec are all rendering inputs.
func templateSnapshot(tmpl *gameplanev1alpha1.GameTemplate) ([]byte, string, error) {
	content := gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name}, Spec: *tmpl.Spec.DeepCopy()}
	data, err := json.Marshal(content)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 900*1024 {
		return nil, "", fmt.Errorf("template snapshot exceeds 900 KiB")
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func validTemplateRevision(revision string) bool {
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == revision
}

func templateSnapshotName(gs *gameplanev1alpha1.GameServer, revision string) string {
	identity := sha256.Sum256([]byte(string(gs.UID) + "/" + gs.Namespace + "/" + gs.Name))
	return fmt.Sprintf("gpt-%x-%s", identity[:8], revision[:32])
}

func loadTemplateSnapshot(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer, revision string) (*gameplanev1alpha1.GameTemplate, error) {
	if !validTemplateRevision(revision) {
		return nil, fmt.Errorf("invalid effective template revision")
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: templateSnapshotName(gs, revision)}, &cm); err != nil {
		return nil, fmt.Errorf("read pinned template snapshot: %w", err)
	}
	if !metav1.IsControlledBy(&cm, gs) || cm.Immutable == nil || !*cm.Immutable || !cm.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("template snapshot is foreign, mutable or deleting")
	}
	var tmpl gameplanev1alpha1.GameTemplate
	if err := json.Unmarshal([]byte(cm.Data[templateSnapshotKey]), &tmpl); err != nil {
		return nil, fmt.Errorf("decode template snapshot: %w", err)
	}
	_, digest, err := templateSnapshot(&tmpl)
	if err != nil {
		return nil, err
	}
	if digest != revision || tmpl.Name != gs.Spec.TemplateRef.Name {
		return nil, fmt.Errorf("template snapshot content does not match pinned revision")
	}
	return &tmpl, nil
}

// Existing pre-upgrade operations may not have a pin yet. Once a pin exists,
// absence/corruption must fail closed rather than silently reading latest.
func effectiveGameTemplate(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (*gameplanev1alpha1.GameTemplate, error) {
	if revision := gs.Annotations[effectiveTemplateAnnotation]; revision != "" {
		return loadTemplateSnapshot(ctx, reader, gs, revision)
	}
	var tmpl gameplanev1alpha1.GameTemplate
	err := reader.Get(ctx, client.ObjectKey{Name: gs.Spec.TemplateRef.Name}, &tmpl)
	return &tmpl, err
}

func (r *GameServerReconciler) saveTemplateSnapshot(ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) (string, error) {
	data, revision, err := templateSnapshot(tmpl)
	if err != nil {
		return "", err
	}
	immutable := true
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: templateSnapshotName(gs, revision), Namespace: gs.Namespace, Labels: map[string]string{templateSnapshotLabel: string(gs.UID)}}, Immutable: &immutable, Data: map[string]string{templateSnapshotKey: string(data)}}
	if err := controllerutil.SetControllerReference(gs, cm, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	_, err = loadTemplateSnapshot(ctx, r.wipeReader(), gs, revision)
	return revision, err
}

// reconcileTemplateRevision runs before every template-dependent calculation.
// A true second result asks the caller to re-read admission state next pass.
func (r *GameServerReconciler) reconcileTemplateRevision(ctx context.Context, gs *gameplanev1alpha1.GameServer) (*gameplanev1alpha1.GameTemplate, bool, error) {
	var installed gameplanev1alpha1.GameTemplate
	err := r.wipeReader().Get(ctx, client.ObjectKey{Name: gs.Spec.TemplateRef.Name}, &installed)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	desired := ""
	if err == nil {
		_, desired, err = templateSnapshot(&installed)
		if err != nil {
			return nil, false, err
		}
	}
	pin := gs.Annotations[effectiveTemplateAnnotation]
	var effective *gameplanev1alpha1.GameTemplate
	if pin != "" {
		effective, err = loadTemplateSnapshot(ctx, r.wipeReader(), gs, pin)
		if err != nil {
			return nil, false, err
		}
	} else if desired != "" {
		effective = &installed
	}
	op, held, err := lifecycleOwner(gs)
	if err != nil {
		return nil, false, err
	}
	if pin == "" && held && op.Kind == "Rollout" {
		effective, err = loadTemplateSnapshot(ctx, r.wipeReader(), gs, op.Token)
		if err != nil {
			return nil, false, err
		}
	}
	if effective == nil {
		return nil, false, fmt.Errorf("GameTemplate %q not found", gs.Spec.TemplateRef.Name)
	}
	if err := r.reportTemplateRevision(ctx, gs, desired, op, held); err != nil {
		return nil, false, err
	}
	if pin == "" && gs.Annotations[restoreTemplateAnnotation] != "" {
		// The Restore must copy its durable source snapshot before even a
		// stopped StatefulSet is rendered, otherwise InitialStartup could
		// preserve a workload derived from the newest catalog template.
		return effective, true, nil
	}
	if held && op.Kind == "Rollout" {
		ready, err := recoverLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, op, r.OperationQuiescer)
		if err != nil || !ready {
			return effective, true, err
		}
		if op.Baseline {
			return effective, true, r.initializeTemplateBaseline(ctx, gs, op)
		}
		if op.Token != pin {
			// The admitted target survives a subsequent catalog update.
			target, err := loadTemplateSnapshot(ctx, r.wipeReader(), gs, op.Token)
			if err != nil {
				return nil, false, err
			}
			if _, err := materializeConfig(gs, target); err != nil {
				return nil, false, err
			}
			if _, err := resolveVersion(gs, target); err != nil {
				return nil, false, err
			}
			if r.RestrictedWorkloads {
				if err := validateRestrictedTemplate(target); err != nil {
					return nil, false, err
				}
			}
			drained, err := r.templateWorkloadDrained(ctx, gs)
			if err != nil || !drained {
				return effective, false, err
			}
			gs.Annotations[effectiveTemplateAnnotation] = op.Token
			delete(gs.Annotations, stopRequestedAtAnnotation)
			return effective, true, r.Update(ctx, gs)
		}
		observed, err := r.templateWorkloadAdopted(ctx, gs, pin)
		if err != nil || !observed {
			return effective, false, err
		}
		// Clean obsolete content before releasing, including when Automatic
		// already has another catalog revision queued for the next pass.
		if err := r.pruneTemplateSnapshots(ctx, gs); err != nil {
			return nil, false, err
		}
		if err := clearLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, op.lifecycleIdentity); err != nil {
			return nil, false, err
		}
		return effective, true, r.Update(ctx, gs)
	}
	// Existing owners continue rendering the old pin; initial startup of a
	// restore may proceed even before the first post-upgrade pin is recorded.
	if held {
		return effective, false, nil
	}
	if desired != "" && desired != pin {
		policy := gs.Spec.TemplateRollout
		admit := pin == "" || policy == nil || policy.Policy == "" || policy.Policy == "Automatic" || policy.RequestedRevision == desired
		if admit {
			if _, err := materializeConfig(gs, &installed); err != nil {
				return nil, false, err
			}
			if _, err := resolveVersion(gs, &installed); err != nil {
				return nil, false, err
			}
			if r.RestrictedWorkloads {
				if err := validateRestrictedTemplate(&installed); err != nil {
					return nil, false, err
				}
			}
			// Do not replace an unrelated restart or already-issued stop.
			pending, err := backupLifecycleTransitionPending(ctx, r.wipeReader(), gs)
			if err != nil || pending {
				return effective, false, err
			}
			revision, err := r.saveTemplateSnapshot(ctx, gs, &installed)
			if err != nil {
				return nil, false, err
			}
			want := lifecycleIdentity{Kind: "Rollout", Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Generation: gs.Generation, Token: revision, Baseline: pin == ""}
			claimed, err := claimLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, want, r.OperationQuiescer)
			return effective, claimed, err
		}
	}
	return effective, false, r.pruneTemplateSnapshots(ctx, gs)
}

// Baseline captures render inputs rather than comparing reconstructed PodSpecs:
// API defaults/admission fields must not turn feature installation into a fleet
// rollout. These fields deliberately exclude power intent, service-only config,
// capture opt-in, scheduling backups and the rollout policy itself.
func (r *GameServerReconciler) baselineRenderInputs(gs *gameplanev1alpha1.GameServer, revision string) (string, error) {
	spec := gs.Spec.DeepCopy()
	spec.Suspend = false
	spec.StopGracePeriodSeconds, spec.TemplateRollout, spec.BackupPolicy, spec.Idle, spec.Capture = nil, nil, nil, nil, nil
	if spec.Networking.Expose == "Hostport" {
		spec.Networking = gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"}
	} else {
		spec.Networking = gameplanev1alpha1.GameServerNetworking{}
	}
	data, err := json.Marshal(struct {
		Spec                                                 *gameplanev1alpha1.GameServerSpec
		Revision, AgentImage, AgentLogLevel, ConfigInitImage string
		AgentImagePullPolicy                                 string
		RestrictedWorkloads                                  bool
		AgentTokenExpirationSeconds                          int64
		AgentResources                                       *corev1.ResourceRequirements
	}{spec, revision, r.AgentImage, r.AgentLogLevel, r.ConfigInitImage, r.AgentImagePullPolicy, r.RestrictedWorkloads, r.AgentTokenExpirationSeconds, r.AgentResources})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (r *GameServerReconciler) initializeTemplateBaseline(ctx context.Context, gs *gameplanev1alpha1.GameServer, op lifecycleOperation) error {
	if _, err := loadTemplateSnapshot(ctx, r.wipeReader(), gs, op.Token); err != nil {
		return err
	}
	var ss appsv1.StatefulSet
	err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil {
		if !metav1.IsControlledBy(&ss, gs) {
			return fmt.Errorf("baseline workload belongs to another server")
		}
		// A real spec edit after admission must render normally. Recording
		// its new inputs against the old workload would silently swallow it.
		if gs.Generation == op.Generation {
			inputs, err := r.baselineRenderInputs(gs, op.Token)
			if err != nil {
				return err
			}
			gs.Annotations[baselineTemplateAnnotation] = op.Token
			gs.Annotations[baselineWorkloadAnnotation] = string(ss.UID)
			gs.Annotations[baselineInputsAnnotation] = inputs
		}
	}
	gs.Annotations[effectiveTemplateAnnotation] = op.Token
	if err := clearLifecycleOperation(ctx, r.Client, r.wipeReader(), gs, op.lifecycleIdentity); err != nil {
		return err
	}
	return r.Update(ctx, gs)
}

func (r *GameServerReconciler) preserveBaselinePodTemplate(ctx context.Context, gs *gameplanev1alpha1.GameServer, ss *appsv1.StatefulSet) (bool, error) {
	pin := gs.Annotations[effectiveTemplateAnnotation]
	if pin == "" || gs.Annotations[baselineTemplateAnnotation] != pin || string(ss.UID) != gs.Annotations[baselineWorkloadAnnotation] || ss.Spec.Template.Annotations[templateRevisionAnnotation] != "" {
		return false, nil
	}
	inputs, err := r.baselineRenderInputs(gs, pin)
	if err != nil || inputs != gs.Annotations[baselineInputsAnnotation] {
		return false, err
	}
	// Provenance initialization must not suppress a real security upgrade.
	// This also covers installing credential isolation and revisions together.
	if ss.Spec.Template.Spec.AutomountServiceAccountToken == nil || *ss.Spec.Template.Spec.AutomountServiceAccountToken || r.RestrictedWorkloads != (ss.Spec.Template.Annotations[workloadSecurityLabel] == "restricted") {
		return false, nil
	}
	projected := false
	for _, volume := range ss.Spec.Template.Spec.Volumes {
		if volume.Name == agentTokenVolumeName && volume.Projected != nil {
			projected = true
		}
	}
	if !projected {
		return false, nil
	}
	// Stamp while already drained, before resume; never stamp an existing
	// unannotated Pod or use its name as evidence of a known running revision.
	if ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 && ss.Status.Replicas == 0 && ss.Status.ObservedGeneration >= ss.Generation {
		var pod corev1.Pod
		err := r.wipeReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func (r *GameServerReconciler) reportTemplateRevision(ctx context.Context, gs *gameplanev1alpha1.GameServer, desired string, op lifecycleOperation, held bool) error {
	running, err := observedTemplateRevision(ctx, r.wipeReader(), gs)
	if err != nil {
		return err
	}
	base := gs.DeepCopy()
	gs.Status.TemplateRevision = &gameplanev1alpha1.TemplateRevisionStatus{Desired: desired, Effective: gs.Annotations[effectiveTemplateAnnotation], Running: running}
	switch {
	case held && op.Kind == "Rollout":
		setTemplateCondition(gs, metav1.ConditionTrue, "AdoptingRevision", "The admitted template revision is being adopted.")
	case desired == "":
		setTemplateCondition(gs, metav1.ConditionUnknown, "TemplateUnavailable", "The installed template is unavailable; the effective snapshot is retained.")
	case desired == gs.Annotations[effectiveTemplateAnnotation]:
		setTemplateCondition(gs, metav1.ConditionFalse, "RevisionCurrent", "The installed template revision is effective.")
	case held:
		setTemplateCondition(gs, metav1.ConditionTrue, "OperationInProgress", "Template adoption waits for the active lifecycle operation to finish.")
	default:
		setTemplateCondition(gs, metav1.ConditionTrue, "RevisionAvailable", "An installed template revision is waiting for admission.")
	}
	if reflect.DeepEqual(base.Status, gs.Status) {
		return nil
	}
	return r.Status().Patch(ctx, gs, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func setTemplateCondition(gs *gameplanev1alpha1.GameServer, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&gs.Status.Conditions, metav1.Condition{Type: "TemplateUpdatePending", Status: status, Reason: reason, Message: message, ObservedGeneration: gs.Generation})
}

func ownedTemplatePod(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (*corev1.Pod, error) {
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return nil, nil
	}
	var pod corev1.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&pod, &ss) || !pod.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return &pod, nil
}

func observedTemplateRevision(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (string, error) {
	pod, err := ownedTemplatePod(ctx, reader, gs)
	if err != nil || pod == nil {
		return "", err
	}
	revision := pod.Annotations[templateRevisionAnnotation]
	if !validTemplateRevision(revision) {
		return "", nil
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == "game" && container.State.Running != nil {
			return revision, nil
		}
	}
	return "", nil
}

func (r *GameServerReconciler) templateWorkloadDrained(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	var ss appsv1.StatefulSet
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("template rollout workload belongs to another server")
	}
	if ss.Annotations[lifecycleOperationAnnotation] != gs.Annotations[lifecycleOperationAnnotation] || ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	var pods corev1.PodList
	if err := r.wipeReader().List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == gs.Name+"-0" || metav1.IsControlledBy(pod, &ss) || (liveRestorePod(pod) && podMountsRestoreTarget(pod, gs.Name)) {
			return false, nil
		}
	}
	return true, nil
}

func (r *GameServerReconciler) templateWorkloadAdopted(ctx context.Context, gs *gameplanev1alpha1.GameServer, revision string) (bool, error) {
	var ss appsv1.StatefulSet
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&ss, gs) || ss.Spec.Template.Annotations[templateRevisionAnnotation] != revision || ss.Status.ObservedGeneration < ss.Generation {
		return false, nil
	}
	if ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 {
		return r.templateWorkloadDrained(ctx, gs)
	}
	pod, err := ownedTemplatePod(ctx, r.wipeReader(), gs)
	return pod != nil && pod.Annotations[templateRevisionAnnotation] == revision, err
}

func (r *GameServerReconciler) pruneTemplateSnapshots(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	var snapshots corev1.ConfigMapList
	if err := r.wipeReader().List(ctx, &snapshots, client.InNamespace(gs.Namespace), client.MatchingLabels{templateSnapshotLabel: string(gs.UID)}); err != nil {
		return err
	}
	pin := gs.Annotations[effectiveTemplateAnnotation]
	for i := range snapshots.Items {
		cm := &snapshots.Items[i]
		if !metav1.IsControlledBy(cm, gs) || (validTemplateRevision(pin) && cm.Name == templateSnapshotName(gs, pin)) {
			continue
		}
		if err := r.Delete(ctx, cm, client.Preconditions{UID: &cm.UID, ResourceVersion: &cm.ResourceVersion}); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}
