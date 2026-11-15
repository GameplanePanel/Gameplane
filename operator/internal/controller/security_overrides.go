package controller

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// securityAppliedAnnotation is written on the StatefulSet pod template. It
	// carries the resolved values and sources the template was rendered with
	// (research R5, R7). It is compact JSON of a SecuritySettings.
	securityAppliedAnnotation = "gameplane.local/security-applied"

	// securityAppliedObservedAnnotation is written on the StatefulSet object. It
	// holds the values the data volume was last set up with (research R6). It
	// is the only source of status.security.applied and of the backup and
	// restore filesystem group.
	securityAppliedObservedAnnotation = "gameplane.local/security-applied-observed"

	// securityConfigInitContainerName is the config-init container name. Its
	// start counts as volume setup (research R6).
	securityConfigInitContainerName = "config-init"
)

// securityIDResolved is one resolved uid or gid and where it came from. A nil
// Value means no id is applied (the image default).
type securityIDResolved struct {
	Value  *int64
	Source gameplanev1alpha1.SecuritySource
}

// securityCheckResolved is the resolved volume ownership check policy and its
// source.
type securityCheckResolved struct {
	Value  gameplanev1alpha1.VolumeOwnershipCheck
	Source gameplanev1alpha1.SecuritySource
}

// resolvedGameSecurity is the output of resolveGameSecurity: the effective
// security settings for one GameServer against one GameTemplate.
type resolvedGameSecurity struct {
	RunAsUser            securityIDResolved
	RunAsGroup           securityIDResolved
	FSGroup              securityIDResolved
	VolumeOwnershipCheck securityCheckResolved
}

// resolveGameSecurity is the pure resolver (research R4, FR-003). Per id, in
// order: a server override with useImageDefault gives no value (source
// ImageDefaultOverride); a server override value gives that value (source
// Override); the template default gives its value (source ModuleDefault);
// otherwise no value (source ImageDefault). The ownership check resolves the
// same way, with the built-in default OnlyWhenWrong as the last layer (source
// BuiltInDefault). A nil template or server reads as no defaults or no
// overrides.
func resolveGameSecurity(tmpl *gameplanev1alpha1.GameTemplate, gs *gameplanev1alpha1.GameServer) resolvedGameSecurity {
	var module *gameplanev1alpha1.GameSecuritySpec
	if tmpl != nil {
		module = tmpl.Spec.Security
	}
	var override *gameplanev1alpha1.GameServerSecuritySpec
	if gs != nil {
		override = gs.Spec.Security
	}

	var runAsUser, runAsGroup, fsGroup *gameplanev1alpha1.SecurityIDOverride
	var check gameplanev1alpha1.VolumeOwnershipCheck
	if override != nil {
		runAsUser = override.RunAsUser
		runAsGroup = override.RunAsGroup
		fsGroup = override.FSGroup
		check = override.VolumeOwnershipCheck
	}

	var moduleUser, moduleGroup, moduleFS *int64
	var moduleCheck gameplanev1alpha1.VolumeOwnershipCheck
	if module != nil {
		moduleUser = module.RunAsUser
		moduleGroup = module.RunAsGroup
		moduleFS = module.FSGroup
		moduleCheck = module.VolumeOwnershipCheck
	}

	out := resolvedGameSecurity{
		RunAsUser:  resolveSecurityID(runAsUser, moduleUser),
		RunAsGroup: resolveSecurityID(runAsGroup, moduleGroup),
		FSGroup:    resolveSecurityID(fsGroup, moduleFS),
	}
	switch {
	case check != "":
		out.VolumeOwnershipCheck = securityCheckResolved{Value: check, Source: gameplanev1alpha1.SecuritySourceOverride}
	case moduleCheck != "":
		out.VolumeOwnershipCheck = securityCheckResolved{Value: moduleCheck, Source: gameplanev1alpha1.SecuritySourceModuleDefault}
	default:
		out.VolumeOwnershipCheck = securityCheckResolved{Value: gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault}
	}
	return out
}

// resolveSecurityID applies the per-id resolution order to one setting.
func resolveSecurityID(override *gameplanev1alpha1.SecurityIDOverride, module *int64) securityIDResolved {
	if override != nil {
		if override.UseImageDefault != nil && *override.UseImageDefault {
			return securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefaultOverride}
		}
		if override.Value != nil {
			return securityIDResolved{Value: copyInt64(*override.Value), Source: gameplanev1alpha1.SecuritySourceOverride}
		}
	}
	if module != nil {
		return securityIDResolved{Value: copyInt64(*module), Source: gameplanev1alpha1.SecuritySourceModuleDefault}
	}
	return securityIDResolved{Source: gameplanev1alpha1.SecuritySourceImageDefault}
}

func copyInt64(v int64) *int64 {
	return &v
}

// settings converts the resolved values to the status read model.
func (g resolvedGameSecurity) settings() gameplanev1alpha1.SecuritySettings {
	return gameplanev1alpha1.SecuritySettings{
		RunAsUser:            idStatus(g.RunAsUser),
		RunAsGroup:           idStatus(g.RunAsGroup),
		FSGroup:              idStatus(g.FSGroup),
		VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: g.VolumeOwnershipCheck.Value, Source: g.VolumeOwnershipCheck.Source},
	}
}

func idStatus(r securityIDResolved) gameplanev1alpha1.SecurityIDStatus {
	return gameplanev1alpha1.SecurityIDStatus{Value: r.Value, Source: r.Source}
}

// fsGroupChangePolicy maps the ownership check to the pod field
// fsGroupChangePolicy (research R8): EveryStart is Always, OnlyWhenWrong is
// OnRootMismatch. It returns nil when no filesystem group applies, because the
// policy has no effect then (FR-002a).
func (g resolvedGameSecurity) fsGroupChangePolicy() *corev1.PodFSGroupChangePolicy {
	if g.FSGroup.Value == nil {
		return nil
	}
	policy := corev1.FSGroupChangeOnRootMismatch
	if g.VolumeOwnershipCheck.Value == gameplanev1alpha1.VolumeOwnershipCheckEveryStart {
		policy = corev1.FSGroupChangeAlways
	}
	return &policy
}

// encodeSecurityAnnotation renders the resolved settings and sources as the
// compact JSON stored in an annotation.
func encodeSecurityAnnotation(s gameplanev1alpha1.SecuritySettings) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("encode security settings annotation: %w", err)
	}
	return string(raw), nil
}

// decodeSecurityAnnotation parses an annotation written by encodeSecurityAnnotation.
func decodeSecurityAnnotation(raw string) (*gameplanev1alpha1.SecuritySettings, error) {
	var s gameplanev1alpha1.SecuritySettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("decode security settings annotation: %w", err)
	}
	return &s, nil
}

// securityPending reports whether a start would change the applied values. It
// is true when applied exists and any value differs from desired. Sources do
// not count. The ownership check counts only when a filesystem group applies
// on either side, because it has no effect otherwise (FR-002a).
func securityPending(desired gameplanev1alpha1.SecuritySettings, applied *gameplanev1alpha1.SecuritySettings) bool {
	if applied == nil {
		return false
	}
	if !sameInt64Ptr(desired.RunAsUser.Value, applied.RunAsUser.Value) ||
		!sameInt64Ptr(desired.RunAsGroup.Value, applied.RunAsGroup.Value) ||
		!sameInt64Ptr(desired.FSGroup.Value, applied.FSGroup.Value) {
		return true
	}
	if desired.FSGroup.Value == nil && applied.FSGroup.Value == nil {
		return false
	}
	return desired.VolumeOwnershipCheck.Value != applied.VolumeOwnershipCheck.Value
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// podVolumesSetUp reports whether a pod has passed volume setup, which is
// where the kubelet applies the filesystem group and change policy (research
// R6). The signal is the PodReadyToStartContainers condition, or any started
// init or game container status on clusters without that condition.
func podVolumesSetUp(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReadyToStartContainers && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.Name == securityConfigInitContainerName && containerStatusStarted(cs) {
			return true
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == gameContainerName && containerStatusStarted(cs) {
			return true
		}
	}
	return false
}

func containerStatusStarted(cs corev1.ContainerStatus) bool {
	return (cs.Started != nil && *cs.Started) || cs.State.Running != nil || cs.State.Terminated != nil
}

// settingsFromPodSpec reconstructs the applied values from a pod spec. It is
// the fallback for StatefulSets created before this feature, which carry no
// security annotations. Non-empty values are labelled ModuleDefault and empty
// ids ImageDefault. An unset policy is Kubernetes' Always, which is EveryStart.
func settingsFromPodSpec(spec *corev1.PodSpec) gameplanev1alpha1.SecuritySettings {
	var runAsUser, runAsGroup *int64
	for i := range spec.Containers {
		if spec.Containers[i].Name != gameContainerName {
			continue
		}
		if sc := spec.Containers[i].SecurityContext; sc != nil {
			runAsUser = sc.RunAsUser
			runAsGroup = sc.RunAsGroup
		}
	}
	var fsGroup *int64
	var policy *corev1.PodFSGroupChangePolicy
	if sc := spec.SecurityContext; sc != nil {
		fsGroup = sc.FSGroup
		policy = sc.FSGroupChangePolicy
	}
	check := gameplanev1alpha1.VolumeOwnershipCheckEveryStart
	if policy != nil && *policy == corev1.FSGroupChangeOnRootMismatch {
		check = gameplanev1alpha1.VolumeOwnershipCheckOnlyWhenWrong
	}
	return gameplanev1alpha1.SecuritySettings{
		RunAsUser:            fallbackIDStatus(runAsUser),
		RunAsGroup:           fallbackIDStatus(runAsGroup),
		FSGroup:              fallbackIDStatus(fsGroup),
		VolumeOwnershipCheck: gameplanev1alpha1.SecurityCheckStatus{Value: check, Source: gameplanev1alpha1.SecuritySourceBuiltInDefault},
	}
}

func fallbackIDStatus(v *int64) gameplanev1alpha1.SecurityIDStatus {
	if v == nil {
		return gameplanev1alpha1.SecurityIDStatus{Source: gameplanev1alpha1.SecuritySourceImageDefault}
	}
	return gameplanev1alpha1.SecurityIDStatus{Value: copyInt64(*v), Source: gameplanev1alpha1.SecuritySourceModuleDefault}
}

// readAppliedSecurity returns the values the data volume was last set up with
// (research R6), or nil when the server has never started. It reads the
// StatefulSet observed annotation first. Without it, a StatefulSet from this
// feature reports nothing until a pod has passed volume setup, and a StatefulSet
// from before the feature is read from its running pod, or from its pod
// template when no pod runs.
func readAppliedSecurity(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (*gameplanev1alpha1.SecuritySettings, error) {
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read statefulset for applied security: %w", err)
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return nil, nil
	}
	if raw := ss.Annotations[securityAppliedObservedAnnotation]; raw != "" {
		return decodeSecurityAnnotation(raw)
	}

	pod, err := ownedTemplatePod(ctx, reader, gs)
	if err != nil {
		return nil, fmt.Errorf("read template pod for applied security: %w", err)
	}

	if ss.Spec.Template.Annotations[securityAppliedAnnotation] == "" {
		// Pre-feature StatefulSet: no security annotations exist yet.
		if pod != nil {
			s := settingsFromPodSpec(&pod.Spec)
			return &s, nil
		}
		s := settingsFromPodSpec(&ss.Spec.Template.Spec)
		return &s, nil
	}

	// Feature StatefulSet without the observed annotation yet. Only a pod that
	// has passed volume setup reports values. A pending or never-started
	// start reports nothing.
	if pod == nil || !podVolumesSetUp(pod) {
		return nil, nil
	}
	raw := pod.Annotations[securityAppliedAnnotation]
	if raw == "" {
		raw = ss.Spec.Template.Annotations[securityAppliedAnnotation]
	}
	return decodeSecurityAnnotation(raw)
}

// observedSecurityFromPod returns the value the StatefulSet observed annotation
// should copy from its pod, or "" when the pod has not passed volume setup yet
// or carries no security annotation. An empty result means leave the
// annotation as it is (research R6, step 2). The caller writes it with
// setObservedSecurityAnnotation.
func observedSecurityFromPod(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (string, error) {
	pod, err := ownedTemplatePod(ctx, reader, gs)
	if err != nil || pod == nil {
		return "", err
	}
	if !podVolumesSetUp(pod) {
		return "", nil
	}
	return pod.Annotations[securityAppliedAnnotation], nil
}

// setObservedSecurityAnnotation sets the observed annotation on a StatefulSet
// object. It reports whether the object changed. An empty value is ignored.
func setObservedSecurityAnnotation(ss *appsv1.StatefulSet, value string) bool {
	if value == "" || ss.Annotations[securityAppliedObservedAnnotation] == value {
		return false
	}
	if ss.Annotations == nil {
		ss.Annotations = map[string]string{}
	}
	ss.Annotations[securityAppliedObservedAnnotation] = value
	return true
}

// recordObservedSecurity copies the security-applied annotation from the owned
// pod onto the StatefulSet observed annotation once the pod has passed volume
// setup (research R6). It writes only the annotation, as a merge patch.
func (r *GameServerReconciler) recordObservedSecurity(ctx context.Context, gs *gameplanev1alpha1.GameServer, ss *appsv1.StatefulSet) error {
	value, err := observedSecurityFromPod(ctx, r.wipeReader(), gs)
	if err != nil {
		return fmt.Errorf("read applied security from pod: %w", err)
	}
	base := ss.DeepCopy()
	if !setObservedSecurityAnnotation(ss, value) {
		return nil
	}
	if err := r.Client.Patch(ctx, ss, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("record observed security on statefulset: %w", err)
	}
	return nil
}

// restoreContainerSecurityContext sets the named container's SecurityContext in
// pod to the value it has in prior. A container missing from either spec is
// left as built.
func restoreContainerSecurityContext(pod, prior *corev1.PodSpec, name string) {
	before := podContainerNamed(prior, name)
	if before == nil {
		return
	}
	if current := podContainerNamed(pod, name); current != nil {
		current.SecurityContext = before.SecurityContext.DeepCopy()
	}
}

// podContainerNamed returns a pointer to the regular or init container with the
// given name, or nil.
func podContainerNamed(spec *corev1.PodSpec, name string) *corev1.Container {
	for i := range spec.Containers {
		if spec.Containers[i].Name == name {
			return &spec.Containers[i]
		}
	}
	for i := range spec.InitContainers {
		if spec.InitContainers[i].Name == name {
			return &spec.InitContainers[i]
		}
	}
	return nil
}

// backupDefaultFSGroup is the filesystem group a backup or restore pod uses
// when no group applies (buildBackupPodSecurityContext).
const backupDefaultFSGroup int64 = 65532

// appliedBackupFSGroup returns the filesystem group a backup or restore pod for
// gs runs with (spec 030, FR-011). It is the group the data volume was last set
// up with, read by readAppliedSecurity from the same StatefulSet and pod state
// as status.security.applied. A server that has never started has no applied
// record, so the desired group applies: the resolved override or template
// default. An applied record with no group means the image default, which the
// backup pod gives as 65532. A StatefulSet from before spec 030 (no security
// annotations) also has no applied record here, so its backups keep using the
// template group as before. The result is never nil.
func appliedBackupFSGroup(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) (*int64, error) {
	recorded, err := securityAppliedRecorded(ctx, reader, gs)
	if err != nil {
		return nil, fmt.Errorf("read applied filesystem group: %w", err)
	}
	var applied *gameplanev1alpha1.SecuritySettings
	if recorded {
		applied, err = readAppliedSecurity(ctx, reader, gs)
		if err != nil {
			return nil, fmt.Errorf("read applied filesystem group: %w", err)
		}
	}
	var group *int64
	if applied != nil {
		group = applied.FSGroup.Value
	} else {
		group = resolveGameSecurity(tmpl, gs).FSGroup.Value
	}
	if group == nil {
		def := backupDefaultFSGroup
		return &def, nil
	}
	return copyInt64(*group), nil
}

// securityAppliedRecorded reports whether gs's own StatefulSet carries the
// spec 030 security annotations (observed, or on its pod template). A missing
// or foreign StatefulSet, or one from before the feature, reports false.
func securityAppliedRecorded(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (bool, error) {
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read statefulset for applied security: %w", err)
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, nil
	}
	return ss.Annotations[securityAppliedObservedAnnotation] != "" || ss.Spec.Template.Annotations[securityAppliedAnnotation] != "", nil
}
