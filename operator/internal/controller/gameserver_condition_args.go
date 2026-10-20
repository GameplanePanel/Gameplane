package controller

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// setConditionArgs replaces the args of one condition type. Empty args delete
// the entry; an empty map becomes nil so omitempty drops the field.
func setConditionArgs(st *gameplanev1alpha1.GameServerStatus, condType string, args map[string]string) {
	if len(args) == 0 {
		delete(st.ConditionArgs, condType)
		if len(st.ConditionArgs) == 0 {
			st.ConditionArgs = nil
		}
		return
	}
	if st.ConditionArgs == nil {
		st.ConditionArgs = map[string]map[string]string{}
	}
	st.ConditionArgs[condType] = args
}

// conditionArgsInput is what reconcileStatus knows when it derives the map.
type conditionArgsInput struct {
	prov         *provisioningInfo // may be nil
	plan         addressPlan
	assigned     string // loadBalancerAddress(svcPtr)
	conflicting  string // conflictingServer
	tunnelPlan   tunnelPlan
	playitErr    string // errMsg from validatePlayitEndpoints ("" when none)
	idleSchedErr error
}

// deriveConditionArgs walks gs.Status.Conditions and returns the new map (nil when empty).
func deriveConditionArgs(gs *gameplanev1alpha1.GameServer, in conditionArgsInput) map[string]map[string]string {
	var out map[string]map[string]string
	for i := range gs.Status.Conditions {
		cond := &gs.Status.Conditions[i]
		args := conditionArgsFor(gs, cond, in)
		if len(args) == 0 {
			continue
		}
		if out == nil {
			out = map[string]map[string]string{}
		}
		out[cond.Type] = args
	}
	return out
}

// conditionArgsFor returns the args of one condition's current reason, or nil
// when that (type, reason) pair has no localized arguments.
func conditionArgsFor(gs *gameplanev1alpha1.GameServer, cond *metav1.Condition, in conditionArgsInput) map[string]string {
	if (cond.Type == "Ready" || cond.Type == "Progressing") && in.prov != nil &&
		cond.Reason == in.prov.reason && len(in.prov.args) > 0 {
		return in.prov.args
	}
	switch cond.Type {
	case "Ready":
		if cond.Reason == "Failed" {
			return map[string]string{"detail": cond.Message}
		}
	case gameplanev1alpha1.GameServerConditionAddressAssignment:
		return addressConditionArgs(gs, cond, in)
	case "TunnelReady":
		switch cond.Reason {
		case "InvalidConfig", "NoCredentials", "Ready", "UnknownProvider":
			return map[string]string{"provider": tunnelProviderArg(gs)}
		case "PortNotMapped":
			return map[string]string{"ports": strings.Join(in.tunnelPlan.noMapping, ", ")}
		}
	case "TunnelHostnameIgnored":
		if cond.Reason == "SettingIgnored" {
			return map[string]string{"settings": strings.Join(ignoredTunnelSettings(gs), ", ")}
		}
	case "TunnelAddressInvalid":
		if cond.Reason == "ValidationFailed" {
			return map[string]string{"details": in.playitErr}
		}
	case "IdleScheduleInvalid":
		if cond.Reason == "WakeWindowUnparseable" && in.idleSchedErr != nil {
			return map[string]string{"details": in.idleSchedErr.Error()}
		}
	}
	return nil
}

// addressConditionArgs returns the args of an AddressAssignment reason.
func addressConditionArgs(gs *gameplanev1alpha1.GameServer, cond *metav1.Condition, in conditionArgsInput) map[string]string {
	plan := in.plan
	switch cond.Reason {
	case "IgnoredForExposureMode":
		expose := gs.Spec.Networking.Expose
		if expose == "" {
			expose = "ClusterIP"
		}
		return map[string]string{
			"requestKind": addressRequestKind(plan),
			"address":     plan.Address,
			"pool":        plan.Pool,
			"expose":      expose,
		}
	case "InvalidAddress":
		return map[string]string{"address": gs.Spec.Networking.Address}
	case "NoAddressManagerConfigured", "ServiceNotReady", "AssignmentPending":
		return map[string]string{
			"requestKind": addressRequestKind(plan),
			"address":     plan.Address,
			"pool":        plan.Pool,
		}
	case "AddressInUse":
		return map[string]string{"address": gs.Spec.Networking.Address, "server": in.conflicting}
	case "PoolNotFound":
		return map[string]string{"pool": plan.Pool}
	case "AllocationFailed":
		return map[string]string{"detail": cond.Message}
	case "Assigned":
		assignedFrom := "default"
		if plan.Pool != "" {
			assignedFrom = "pool"
		}
		return map[string]string{"address": in.assigned, "pool": plan.Pool, "assignedFrom": assignedFrom}
	}
	return nil
}

// addressRequestKind names the shape of a pool/address request for the
// localized REQ phrase; it mirrors addressRequestSummary.
func addressRequestKind(plan addressPlan) string {
	switch {
	case plan.Pool != "" && plan.Address != "":
		return "addressAndPool"
	case plan.Address != "":
		return "address"
	default:
		return "pool"
	}
}

// tunnelProviderArg is the provider name the TunnelReady text names, or "none"
// when the server has no tunnel spec.
func tunnelProviderArg(gs *gameplanev1alpha1.GameServer) string {
	if gs.Spec.Networking.Tunnel == nil {
		return "none"
	}
	return gs.Spec.Networking.Tunnel.Provider
}

// ignoredTunnelSettings lists the networking settings that are set but do not
// apply to tunnel traffic (they configure the backing Service instead).
func ignoredTunnelSettings(gs *gameplanev1alpha1.GameServer) []string {
	var ignoredSettings []string
	if gs.Spec.Networking.Hostname != "" {
		ignoredSettings = append(ignoredSettings, "hostname")
	}
	if len(gs.Spec.Networking.SourceRanges) > 0 {
		ignoredSettings = append(ignoredSettings, "sourceRanges")
	}
	for _, po := range gs.Spec.Networking.PortOverrides {
		if po.NodePort != 0 {
			ignoredSettings = append(ignoredSettings, "nodePort")
			break
		}
	}
	return ignoredSettings
}

// provisioningArgs returns the values the localized text of a pod-derived
// reason needs. CrashLoopBackOff: restartCount of the first init-or-game container
// whose State.Waiting.Reason is "CrashLoopBackOff" (init containers first, then the
// container named gameContainerName). ContainerExited: exitCode of the first
// init-or-game container that exited non-zero, falling back to the game
// container's terminated state. Other reasons: nil.
func provisioningArgs(pod *corev1.Pod, reason string) map[string]string {
	switch reason {
	case "CrashLoopBackOff":
		if cs := findContainerStatus(pod, func(cs *corev1.ContainerStatus) bool {
			return cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff"
		}); cs != nil {
			return map[string]string{"restartCount": strconv.FormatInt(int64(cs.RestartCount), 10)}
		}
	case "ContainerExited":
		if cs := findContainerStatus(pod, func(cs *corev1.ContainerStatus) bool {
			return cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0
		}); cs != nil {
			return map[string]string{"exitCode": strconv.FormatInt(int64(cs.State.Terminated.ExitCode), 10)}
		}
		for i := range pod.Status.ContainerStatuses {
			cs := &pod.Status.ContainerStatuses[i]
			if cs.Name == gameContainerName && cs.State.Terminated != nil {
				return map[string]string{"exitCode": strconv.FormatInt(int64(cs.State.Terminated.ExitCode), 10)}
			}
		}
	}
	return nil
}

// findContainerStatus returns the first init container status matching match,
// then the first game container status matching match, or nil.
func findContainerStatus(pod *corev1.Pod, match func(*corev1.ContainerStatus) bool) *corev1.ContainerStatus {
	for i := range pod.Status.InitContainerStatuses {
		if cs := &pod.Status.InitContainerStatuses[i]; match(cs) {
			return cs
		}
	}
	for i := range pod.Status.ContainerStatuses {
		if cs := &pod.Status.ContainerStatuses[i]; cs.Name == gameContainerName && match(cs) {
			return cs
		}
	}
	return nil
}
