package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// securitySettingsConfigMapName is the per-cluster security settings
	// ConfigMap. Helm creates it in the operator control-plane namespace (OD-8).
	securitySettingsConfigMapName = "gameplane-security-settings"

	// securitySettingsAllowRootKey holds "true" when the cluster allows root
	// overrides. Only that exact value allows them.
	securitySettingsAllowRootKey = "allowRootOverrides"

	// securityConditionReasonAllowed is the SecurityAllowed=True reason used
	// once root overrides are allowed in the cluster.
	securityConditionReasonAllowed = "RootOverrideAllowed"

	// securityConditionReasonNoOverride is the SecurityAllowed=True reason used
	// once a previously held server no longer saves a root override.
	securityConditionReasonNoOverride = "NoRootOverride"
)

// securitySettingsAllowRoot reports whether the settings ConfigMap allows root
// overrides. A nil ConfigMap, a missing key or any value other than exactly
// "true" is off (fail closed).
func securitySettingsAllowRoot(cm *corev1.ConfigMap) bool {
	if cm == nil {
		return false
	}
	return cm.Data[securitySettingsAllowRootKey] == "true"
}

// rootOverridesAllowed reads this cluster's setting from the manager cache. An
// empty ControlPlaneNamespace, a missing ConfigMap or a read error is off.
func (r *GameServerReconciler) rootOverridesAllowed(ctx context.Context) bool {
	if r.ControlPlaneNamespace == "" {
		return false
	}
	var cm corev1.ConfigMap
	key := types.NamespacedName{Namespace: r.ControlPlaneNamespace, Name: securitySettingsConfigMapName}
	if err := r.Get(ctx, key, &cm); err != nil {
		if !apierrors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "read security settings ConfigMap; root overrides stay disallowed",
				"namespace", key.Namespace, "name", key.Name)
		}
		return false
	}
	return securitySettingsAllowRoot(&cm)
}

// isRootIDOverride reports whether one override id is a root value. A value of
// 0 is root. useImageDefault on runAsUser or runAsGroup is root too, because the
// image may run as root. fsGroup useImageDefault is not a root rule.
func isRootIDOverride(o *gameplanev1alpha1.SecurityIDOverride, imageDefaultIsRoot bool) bool {
	if o == nil {
		return false
	}
	if o.Value != nil && *o.Value == 0 {
		return true
	}
	return imageDefaultIsRoot && o.UseImageDefault != nil && *o.UseImageDefault
}

// rootOverrideSettings names the overrides on gs that are root values, in
// spec order (runAsUser, runAsGroup, fsGroup). Module defaults are never
// overrides and are not named here.
func rootOverrideSettings(gs *gameplanev1alpha1.GameServer) []string {
	if gs == nil || gs.Spec.Security == nil {
		return nil
	}
	sec := gs.Spec.Security
	var names []string
	if isRootIDOverride(sec.RunAsUser, true) {
		names = append(names, "runAsUser")
	}
	if isRootIDOverride(sec.RunAsGroup, true) {
		names = append(names, "runAsGroup")
	}
	if isRootIDOverride(sec.FSGroup, false) {
		names = append(names, "fsGroup")
	}
	return names
}

// hasRootOverride reports whether gs saves at least one root override.
func hasRootOverride(gs *gameplanev1alpha1.GameServer) bool {
	return len(rootOverrideSettings(gs)) > 0
}

// rootOverrideHeld reports whether gs must not start: it saves a root override
// while the cluster does not allow one (OD-4, FR-005a).
func (r *GameServerReconciler) rootOverrideHeld(ctx context.Context, gs *gameplanev1alpha1.GameServer) bool {
	return hasRootOverride(gs) && !r.rootOverridesAllowed(ctx)
}

// recordRootOverrideHold emits a Warning event on gs when the reconciler holds
// it back. It is a no-op without an EventRecorder (unit and envtest setups).
func (r *GameServerReconciler) recordRootOverrideHold(gs *gameplanev1alpha1.GameServer) {
	if r.EventRecorder == nil {
		return
	}
	message := fmt.Sprintf("not started: saved root overrides (%s) are not allowed in this cluster; clear the override or allow root overrides in the cluster's Configuration",
		strings.Join(rootOverrideSettings(gs), ", "))
	reason := gameplanev1alpha1.GameServerReasonRootOverrideDisallowed
	r.EventRecorder.Eventf(gs, nil, corev1.EventTypeWarning, reason, reason, "%s", message)
}

// applySecurityAllowedCondition sets the SecurityAllowed condition. While the
// cluster does not allow root overrides, a server that saves one reports False
// with reason RootOverrideDisallowed. A running server keeps running; a stopped
// one stays at zero replicas. Once allowed, or once the override is cleared, the
// condition is True. A server that never saved a root override has no condition
// unless one was already set, in which case it is set to True.
func (r *GameServerReconciler) applySecurityAllowedCondition(
	ctx context.Context, conds []metav1.Condition, gs *gameplanev1alpha1.GameServer, running bool,
) []metav1.Condition {
	condType := gameplanev1alpha1.GameServerConditionSecurityAllowed
	if !hasRootOverride(gs) {
		if meta.FindStatusCondition(conds, condType) == nil {
			return conds
		}
		return upsertCondition(conds, metav1.Condition{
			Type:               condType,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gs.Generation,
			Reason:             securityConditionReasonNoOverride,
			Message:            "No root override is saved for this server.",
		})
	}
	settings := strings.Join(rootOverrideSettings(gs), ", ")
	if r.rootOverridesAllowed(ctx) {
		return upsertCondition(conds, metav1.Condition{
			Type:               condType,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gs.Generation,
			Reason:             securityConditionReasonAllowed,
			Message: fmt.Sprintf("Root overrides (%s) are allowed in this cluster.",
				settings),
		})
	}
	const clearHint = "the override is cleared or root overrides are allowed in this cluster's Configuration"
	message := fmt.Sprintf("Saved root overrides (%s) are not allowed in this cluster, so the server will not start until %s.", settings, clearHint)
	if running {
		message = fmt.Sprintf("Saved root overrides (%s) are not allowed in this cluster. The server keeps running but will not start again until %s.", settings, clearHint)
	}
	return upsertCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: gs.Generation,
		Reason:             gameplanev1alpha1.GameServerReasonRootOverrideDisallowed,
		Message:            message,
	})
}

// mapSecuritySettingsToGameServers enqueues the GameServers that save a root
// override when the settings ConfigMap changes (created, updated or deleted) in
// the control-plane namespace. Other ConfigMaps and other servers are ignored.
func (r *GameServerReconciler) mapSecuritySettingsToGameServers(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.ControlPlaneNamespace == "" || obj.GetName() != securitySettingsConfigMapName ||
		obj.GetNamespace() != r.ControlPlaneNamespace {
		return nil
	}
	var servers gameplanev1alpha1.GameServerList
	if err := r.List(ctx, &servers); err != nil {
		log.FromContext(ctx).Error(err, "list servers for security settings change")
		return nil
	}
	var requests []reconcile.Request
	for i := range servers.Items {
		gs := &servers.Items[i]
		if hasRootOverride(gs) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
		}
	}
	return requests
}
