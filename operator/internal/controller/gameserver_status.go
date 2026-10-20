package controller

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// heartbeatFreshness defines how long an agent heartbeat is trusted.
// Reported values (playersOnline, etc.) are kept across short dropouts;
// the Healthy condition flips false once this window elapses.
const heartbeatFreshness = 60 * time.Second

// GameServerConditionReasonPVCProvisioningFailed is the reason for a PVC provisioning failure.
const GameServerConditionReasonPVCProvisioningFailed = "PVCProvisioningFailed"

// reconcileStatus derives phase / conditions / endpoints / startedAt
// from observed StatefulSet, Service, and the agent heartbeat. It's a
// pure computation — no child objects are mutated here.
//
// svcEvents are the recent events on the game Service (used to derive
// address-manager failure reasons). conflictingServer names another GameServer
// holding the requested explicit address, or is empty if none was found.
//
// tunnelEndpoints are prepended to the Service endpoints so the dashboard's
// Connection card reads the tunnel address when present.
func (r *GameServerReconciler) reconcileStatus(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
	idle idleState, idleStatus *gameplanev1alpha1.IdleStatus, idleScheduleErr error, tunnelPlan tunnelPlan,
	tmpl *gameplanev1alpha1.GameTemplate, svcEvents []corev1.Event, conflictingServer string,
) (time.Duration, error) {
	// base captures the object as fetched so we can issue a JSON merge
	// patch of only the fields this reconciler owns. The agent sidecar
	// concurrently patches status.agent (its heartbeat); a full
	// Status().Update would carry the stale agent value we read at the
	// start of reconcile and revert a fresher heartbeat (and race it for
	// the resourceVersion). MergeFrom touches only changed fields, so
	// status.agent is left untouched and there is nothing to conflict on.
	base := gs.DeepCopy()
	orig := gs.Status.DeepCopy()

	var ss appsv1.StatefulSet
	ssErr := r.Get(ctx, types.NamespacedName{Name: gs.Name, Namespace: gs.Namespace}, &ss)
	if ssErr != nil && !apierrors.IsNotFound(ssErr) {
		return 0, ssErr
	}
	ssExists := ssErr == nil

	var svc corev1.Service
	svcErr := r.Get(ctx, types.NamespacedName{Name: gs.Name, Namespace: gs.Namespace}, &svc)
	if svcErr != nil && !apierrors.IsNotFound(svcErr) {
		return 0, svcErr
	}
	svcExists := svcErr == nil
	var svcPtr *corev1.Service
	if svcExists {
		svcPtr = &svc
	}

	// Recomputed rather than threaded down from reconcileService: the plan is
	// a pure function of the GameServer and the configured flavor, so both
	// callers reach the same decision without the reconcile carrying it.
	addrPlan := r.addressPlanFor(gs)

	phase := derivePhase(gs, ssExists, ss.Status.ReadyReplicas > 0, heartbeatFresh(gs), idle)

	// Check for PVC provisioning failures early. If the game-data PVC is stuck
	// Pending due to a missing StorageClass or other provisioning error, report it
	// clearly so the dashboard shows a clear error message. This check runs on every
	// reconcile (not just when events arrive) to satisfy SC-002: the error must
	// surface within 30 seconds of the GameServer going Pending. The server stays
	// in a non-terminal phase (Pending or Starting) per the spec (not escalated to Failed).
	var prov *provisioningInfo
	if phase == gameplanev1alpha1.GameServerPhasePending || phase == gameplanev1alpha1.GameServerPhaseStarting {
		pvcName := gs.Name + "-data"
		if scName, pvcMsg, provisioning, err := checkPVCProvisioningFailure(ctx, r.Client, gs.Namespace, pvcName); err != nil {
			// Non-NotFound error checking the PVC or StorageClass; surface it.
			return 0, fmt.Errorf("check PVC provisioning: %w", err)
		} else if provisioning {
			// PVC provisioning error detected. checkPVCProvisioningFailure already
			// names the missing StorageClass, so prefix only the PVC and keep the
			// terse phrasing FR-005 asks for rather than restating the class twice.
			prov = &provisioningInfo{
				reason:  GameServerConditionReasonPVCProvisioningFailed,
				message: fmt.Sprintf("PVC %q: %s", pvcName, pvcMsg),
				args:    map[string]string{"pvc": pvcName, "storageClass": scName},
			}
		}
	}

	// While Starting, read the pod's container states to either explain
	// *why* it isn't Running yet (pulling the image, creating the
	// container, installing on first boot, waiting for the agent) — feeding
	// the dashboard's provisioning sub-status — or, if startup has
	// terminally failed, escalate the phase to Failed.
	if phase == gameplanev1alpha1.GameServerPhaseStarting && prov == nil {
		var pod corev1.Pod
		podErr := r.Get(ctx, types.NamespacedName{Name: gs.Name + "-0", Namespace: gs.Namespace}, &pod)
		switch {
		case apierrors.IsNotFound(podErr):
			prov = &provisioningInfo{reason: "Pending", message: "scheduling the pod"}
		case podErr != nil:
			return 0, fmt.Errorf("get pod %s-0: %w", gs.Name, podErr)
		default:
			if fr, fm, failed := startupFailure(&pod); failed {
				// A terminal startup failure (unpullable image, persistent
				// crash-loop, non-zero exit) — escalate to Failed so the
				// dashboard stops showing a perpetual "Starting". Not sticky:
				// derivePhase re-evaluates every reconcile, so a later
				// recovery returns the phase to Running.
				phase = gameplanev1alpha1.GameServerPhaseFailed
				prov = &provisioningInfo{reason: fr, message: fm, args: provisioningArgs(&pod, fr)}
			} else {
				reason, message := provisioningReason(&pod, heartbeatFresh(gs))
				prov = &provisioningInfo{reason: reason, message: message, args: provisioningArgs(&pod, reason)}
			}
		}
	}

	gs.Status.Phase = phase
	gs.Status.ObservedGeneration = gs.Generation

	// failure starts as:
	// - MetalLB: Service events (a MetalLB AllocationFailed warning, passed
	//   through verbatim — see extractAddressFailureFromEvents). When a pool
	//   was requested, a direct GET of the IPAddressPool CR can answer "does
	//   this pool exist at all" without waiting for — or outliving — an event
	//   (Events have a ~1h TTL and MetalLB does not re-emit while a failure
	//   persists). That direct signal overrides rather than supplements.
	// - Cilium: Service status conditions (cilium.io/IPAMRequestSatisfied —
	//   see extractAddressFailureFromCiliumCondition). Cilium emits no Events
	//   at all for allocation failures.
	failure := extractAddressFailureFromEvents(svcEvents)
	if addrPlan.Outcome == addressPlanTranslated && addrPlan.Manager == addressManagerCilium {
		// Check Cilium's condition path.
		failure = extractAddressFailureFromCiliumCondition(svcPtr)
	} else if addrPlan.Outcome == addressPlanTranslated && addrPlan.Manager == addressManagerMetalLB && addrPlan.Pool != "" {
		// Check MetalLB's direct pool-existence signal.
		switch r.checkMetalLBPoolExists(ctx, addrPlan.Pool) {
		case poolExistenceMissing:
			failure = addressFailureReason{
				reason: "PoolNotFound",
				message: fmt.Sprintf(
					"IPAddressPool %q not found in namespace %q.", addrPlan.Pool, r.metalLBNamespace()),
			}
		case poolExistenceFound:
			// Definitive, live evidence the pool exists. Unlike an event,
			// this check never expires, so it can safely release a
			// previously-latched PoolNotFound outright rather than waiting
			// for the address to actually get assigned — see
			// addressFailureReason.confirmedClear. Only set when nothing
			// fresher (an actual event failure) already claimed this pass.
			if failure.reason == "" {
				failure.confirmedClear = true
			}
		case poolExistenceUnknown:
			// MetalLB CRDs not installed, or the GET forbidden by RBAC:
			// existence is genuinely unknown this pass. Fail soft — leave
			// failure exactly as the event path found it, and do NOT set
			// confirmedClear, so a PoolNotFound latched on an earlier pass
			// survives a transient "can't check" pass instead of being
			// wrongly released.
		}
	}
	gs.Status.Conditions = computeConditions(gs, phase, prov, idle, addrPlan, svcPtr, failure, conflictingServer)

	// Fetch tunnel Deployment to compute TunnelReady condition.
	var tunnelDep *appsv1.Deployment
	if tunnelPlan.wantTunnel {
		var dep appsv1.Deployment
		tunnelDepErr := r.Get(ctx, types.NamespacedName{
			Name:      gs.Name + "-tunnel",
			Namespace: gs.Namespace,
		}, &dep)
		if tunnelDepErr == nil {
			tunnelDep = &dep
		}
	}

	// Compute tunnel conditions.
	gs.Status.Conditions = computeTunnelConditions(gs, tunnelPlan, tunnelDep)

	// Validate and merge playit tunnel endpoints if present.
	var playitErr string
	if gs.Spec.Networking.Tunnel != nil && gs.Spec.Networking.Tunnel.Provider == "playit" &&
		len(gs.Status.TunnelEndpoints) > 0 {
		advertisedPorts := getAdvertisedPortNames(tmpl)
		validEndpoints, errMsg := validatePlayitEndpoints(gs.Status.TunnelEndpoints, advertisedPorts)
		playitErr = errMsg
		if errMsg != "" {
			// Set condition on validation failure; invalid entries are discarded
			invalidCond := metav1.Condition{
				Type:               "TunnelAddressInvalid",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: gs.Generation,
				Reason:             "ValidationFailed",
				Message:            errMsg,
			}
			gs.Status.Conditions = upsertCondition(gs.Status.Conditions, invalidCond)
			// Use only the valid endpoints. These are still genuine
			// playit-sourced endpoints (only some of the batch failed
			// validation), so TunnelProvider must be stamped here too —
			// otherwise a partially-invalid playit endpoint set would leave
			// TunnelProvider=="" entries that findAddressConflict's holds
			// discriminator would wrongly treat as a real LoadBalancer
			// address holder.
			for i := range validEndpoints {
				validEndpoints[i].TunnelProvider = "playit"
			}
			tunnelPlan.endpoints = validEndpoints
		} else {
			// All valid: remove the invalid condition and merge into tunnel plan
			gs.Status.Conditions = removeCondition(gs.Status.Conditions, "TunnelAddressInvalid")
			for i := range validEndpoints {
				validEndpoints[i].TunnelProvider = "playit"
			}
			tunnelPlan.endpoints = validEndpoints
		}
	}

	// Folded into this one status patch rather than written by reconcileIdle
	// itself: the agent concurrently patches status.agent, and a second writer
	// here would be a second thing to race. A nil block clears status.idle,
	// which is how disabling the feature drops its stale read model.
	gs.Status.Idle = idleStatus

	// F-057: an unparseable wake-window entry used to appear only in
	// status.idle.reason (and only while asleep). Surface it as its own
	// condition instead, so it is visible whether or not the server happens
	// to be asleep right now, and cleared once the schedule is fixed.
	if idleScheduleErr != nil {
		gs.Status.Conditions = upsertCondition(gs.Status.Conditions, metav1.Condition{
			Type:               "IdleScheduleInvalid",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gs.Generation,
			Reason:             "WakeWindowUnparseable",
			Message:            idleScheduleErr.Error(),
			LastTransitionTime: metav1.Now(),
		})
	} else {
		gs.Status.Conditions = removeCondition(gs.Status.Conditions, "IdleScheduleInvalid")
	}

	// Endpoints: tunnel endpoints come first (when present) so the dashboard's
	// Connection card reads the address players actually use.
	if svcExists {
		gs.Status.Endpoints = endpointsFromService(&svc, addrPlan)
	}
	if tunnelPlan.wantTunnel && len(tunnelPlan.endpoints) > 0 {
		gs.Status.Endpoints = append(tunnelPlan.endpoints, gs.Status.Endpoints...)
	}
	if phase == gameplanev1alpha1.GameServerPhaseRunning {
		// Track the running game container's start so the dashboard's
		// "up …" reflects the current pod, not the first time the server
		// ever reached Running (a restart or pod recreation resets it).
		if started := r.gameContainerStartedAt(ctx, gs); started != nil {
			if gs.Status.StartedAt == nil || !gs.Status.StartedAt.Equal(started) {
				gs.Status.StartedAt = started
			}
		} else if gs.Status.StartedAt == nil {
			now := metav1.Now()
			gs.Status.StartedAt = &now
		}
	}
	if phase == gameplanev1alpha1.GameServerPhaseStopped || phase == gameplanev1alpha1.GameServerPhaseSuspended {
		gs.Status.StartedAt = nil
	}

	// Recomputed from the final conditions so the args follow each condition
	// and are dropped when its reason has none. Out-of-band writers
	// (setPhase, setTunnelCredentialRefused, clearTunnelCredentialRefused)
	// maintain their own entries; see gameserver_condition_args.go.
	gs.Status.ConditionArgs = deriveConditionArgs(gs, conditionArgsInput{
		prov:         prov,
		plan:         addrPlan,
		assigned:     loadBalancerAddress(svcPtr),
		conflicting:  conflictingServer,
		tunnelPlan:   tunnelPlan,
		playitErr:    playitErr,
		idleSchedErr: idleScheduleErr,
	})

	if !reflect.DeepEqual(orig, &gs.Status) {
		if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil {
			return 0, err
		}
	}

	// Re-check when heartbeat is about to go stale so Healthy flips promptly.
	if phase == gameplanev1alpha1.GameServerPhaseRunning {
		return heartbeatFreshness, nil
	}
	return 15 * time.Second, nil
}

func derivePhase(
	gs *gameplanev1alpha1.GameServer, ssExists, ssReady, hbFresh bool, idle idleState,
) gameplanev1alpha1.GameServerPhase {
	// An idle sleep reports the same phase as an explicit stop. Without this
	// arm a sleeping server would read as Starting — the StatefulSet exists but
	// has no ready replica — and look permanently stuck. The two are told apart
	// by the IdleAsleep condition reason and status.idle, not by the phase.
	if gs.Spec.Suspend || idle == idleAsleep {
		if ssExists && ssReady {
			return gameplanev1alpha1.GameServerPhaseStopping
		}
		return gameplanev1alpha1.GameServerPhaseSuspended
	}
	if !ssExists {
		return gameplanev1alpha1.GameServerPhasePending
	}
	if !ssReady {
		return gameplanev1alpha1.GameServerPhaseStarting
	}
	if !hbFresh {
		// Pod is ready but the agent isn't reporting — treat as
		// Starting until the first heartbeat. A long timeout here
		// could escalate to Failed; for now, optimistic.
		return gameplanev1alpha1.GameServerPhaseStarting
	}
	return gameplanev1alpha1.GameServerPhaseRunning
}

func heartbeatFresh(gs *gameplanev1alpha1.GameServer) bool {
	if gs.Status.Agent == nil || gs.Status.Agent.LastHeartbeat == nil {
		return false
	}
	return time.Since(gs.Status.Agent.LastHeartbeat.Time) < heartbeatFreshness
}

// computeConditions derives Ready / Progressing / Healthy from the phase, plus
// AddressAssignment for the servers that requested a load-balancer pool or
// address. addrPlan is the reconciler's decision about that request and svc is
// the game Service as observed this pass (nil when it does not exist yet).
// failure supplies a derived address-manager failure — from a direct
// IPAddressPool existence check (PoolNotFound) or, failing that, from a
// MetalLB Service event (AllocationFailed) — see reconcileStatus. The zero
// value means neither found anything this pass.
// conflictingServer names another GameServer holding the requested explicit address.
func computeConditions(
	gs *gameplanev1alpha1.GameServer,
	phase gameplanev1alpha1.GameServerPhase,
	prov *provisioningInfo,
	idle idleState,
	addrPlan addressPlan,
	svc *corev1.Service,
	failure addressFailureReason,
	conflictingServer string,
) []metav1.Condition {
	conds := gs.Status.Conditions

	var ready, progressing, healthy metav1.Condition
	ready = metav1.Condition{Type: "Ready", ObservedGeneration: gs.Generation}
	progressing = metav1.Condition{Type: "Progressing", ObservedGeneration: gs.Generation}
	healthy = metav1.Condition{Type: "Healthy", ObservedGeneration: gs.Generation}

	switch phase {
	case gameplanev1alpha1.GameServerPhaseRunning:
		ready.Status = metav1.ConditionTrue
		ready.Reason = "Running"
		ready.Message = "server is ready and the agent is reporting heartbeats"
		progressing.Status = metav1.ConditionFalse
		progressing.Reason = "Stable"
		healthy.Status = metav1.ConditionTrue
		healthy.Reason = "AgentFresh"
	case gameplanev1alpha1.GameServerPhasePending:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "Pending"
		progressing.Status = metav1.ConditionTrue
		progressing.Reason = "Pending"
		healthy.Status = metav1.ConditionFalse
		healthy.Reason = "Pending"
		// Refine the generic "Pending" with what is actually pending if known.
		// A PVC provisioning failure also names the missing StorageClass on
		// Ready (not just Progressing): the phase stays Pending — this is
		// recoverable once an admin creates the StorageClass — but the
		// dashboard's Ready condition is what the e2e/envtest suites and the
		// UI poll for a clear, specific error, mirroring the Failed case below.
		if prov != nil && prov.reason != "" {
			ready.Reason = prov.reason
			ready.Message = prov.message
			progressing.Reason = prov.reason
			progressing.Message = prov.message
		}
	case gameplanev1alpha1.GameServerPhaseStarting:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "Starting"
		progressing.Status = metav1.ConditionTrue
		progressing.Reason = "Starting"
		// Refine the generic "Starting" with what the pod is actually
		// doing, so the dashboard can show "Pulling image" /
		// "Installing server files" / "Waiting for agent".
		if prov != nil {
			// Promote PVC provisioning failures to the Ready condition so the
			// error is visible (mirroring the Pending case). Other prov reasons
			// (pod scheduling, image pull, etc.) affect only Progressing —
			// they're benign progress refinements, not failures.
			if prov.reason == GameServerConditionReasonPVCProvisioningFailed {
				ready.Reason = prov.reason
				ready.Message = prov.message
			}
			if prov.reason != "" {
				progressing.Reason = prov.reason
			}
			progressing.Message = prov.message
		}
		healthy.Status = metav1.ConditionFalse
		healthy.Reason = "AgentStale"
	case gameplanev1alpha1.GameServerPhaseStopping:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "Stopping"
		progressing.Status = metav1.ConditionTrue
		progressing.Reason = "Stopping"
		healthy.Status = metav1.ConditionFalse
		healthy.Reason = "Stopping"
	case gameplanev1alpha1.GameServerPhaseSuspended:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "Suspended"
		progressing.Status = metav1.ConditionFalse
		progressing.Reason = "Suspended"
		healthy.Status = metav1.ConditionFalse
		healthy.Reason = "Suspended"
		// The phase is shared with an explicit stop, so the reason is the only
		// machine-readable way to tell "the operator parked this because it was
		// empty" from "a human turned it off".
		if idle == idleAsleep {
			ready.Reason = "IdleAsleep"
			ready.Message = "asleep: no players online"
			progressing.Reason = "IdleAsleep"
			healthy.Reason = "IdleAsleep"
		}
	case gameplanev1alpha1.GameServerPhaseFailed:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "Failed"
		progressing.Status = metav1.ConditionFalse
		progressing.Reason = "Failed"
		healthy.Status = metav1.ConditionFalse
		healthy.Reason = "Failed"
		// Carry the specific startup-failure reason (image pull, crash-loop,
		// exit) so the dashboard can explain *why* it failed.
		if prov != nil && prov.reason != "" {
			ready.Reason = prov.reason
			ready.Message = prov.message
			progressing.Reason = prov.reason
			progressing.Message = prov.message
		}
	default:
		ready.Status = metav1.ConditionUnknown
		ready.Reason = "Unknown"
		progressing.Status = metav1.ConditionUnknown
		progressing.Reason = "Unknown"
		healthy.Status = metav1.ConditionUnknown
		healthy.Reason = "Unknown"
	}

	conds = upsertCondition(conds, ready)
	conds = upsertCondition(conds, progressing)
	conds = upsertCondition(conds, healthy)
	return addressAssignmentCondition(conds, gs, addrPlan, svc, failure, conflictingServer)
}

// addressFailureReason describes a failure derived from Service events, a
// direct IPAddressPool existence check, or conflict detection. The zero
// value means nothing was determined this pass — which is NOT the same as
// "confirmed fine": see confirmedClear.
type addressFailureReason struct {
	reason  string // e.g. "PoolNotFound", "AllocationFailed", "AddressInUse"
	message string // human-readable detail from the event or check

	// confirmedClear is set whenever this pass has direct, live evidence
	// that the requested IPAddressPool exists (see reconcileStatus) and no
	// fresher failure was found. It is meaningless whenever reason != ""
	// (a fresh failure always wins outright over any latch).
	//
	// Despite the name, this signal only DEFINITIVELY disproves one latched
	// reason: PoolNotFound. It says nothing about whether an
	// AllocationFailed condition (e.g. an exhausted pool) has cleared — a
	// pool that exists can still be fully allocated, so "the pool exists"
	// is not evidence an allocation failure resolved. addressAssignmentCondition
	// therefore only lets confirmedClear release the latch when the
	// previously-latched reason was PoolNotFound; an AllocationFailed latch
	// ignores confirmedClear entirely and can only be released by a fresh
	// failure, a new address, or a changed request (see the LATCHING doc
	// comment on addressAssignmentCondition). Reconstructing that
	// distinction here would require reconcileStatus to know the previously
	// latched reason before it's computed, so the field itself stays a
	// simple "pool exists" signal and the reason-scoping lives at the one
	// call site that already has `existing` in scope.
	confirmedClear bool
}

// addressManagerEventSources lists the reporting components whose Warning
// events on a game Service are treated as address-manager failures. Anything
// else on the Service (kubelet, unrelated controllers) is ignored so an
// unrelated warning is never misreported as an allocation failure.
//
// Matched case-insensitively as a substring, because deployments vary the exact
// component string (e.g. "metallb-controller", "metallb-speaker").
//
// "cilium" is deliberately absent: verified directly against a live cluster,
// Cilium's LB-IPAM emits NO Events at all for an allocation failure — it only
// sets a Service condition (cilium.io/IPAMRequestSatisfied=False, reasons
// no_pool / out_of_ips). Matching a Cilium component here was therefore dead
// code that could never fire; a future Cilium AddressAssignment story needs a
// Service-condition reader, not this event path.
//
// kube-vip / purelb / openelb remain unverified (no observed fixture for any
// of them) but are kept because they're free: extractAddressFailureFromEvents
// only acts on Reason=="AllocationFailed" (MetalLB's verified reason), so an
// unrelated event from any of these components is ignored regardless.
var addressManagerEventSources = []string{
	"metallb",
	"kube-vip",
	"purelb",
	"openelb",
}

// fromAddressManager reports whether the event was emitted by something that
// plausibly manages load-balancer addresses, based on the legacy
// Source.Component or the events.k8s.io ReportingController.
func fromAddressManager(ev *corev1.Event) bool {
	for _, field := range []string{ev.Source.Component, ev.ReportingController} {
		if field == "" {
			continue
		}
		lower := strings.ToLower(field)
		for _, want := range addressManagerEventSources {
			if strings.Contains(lower, want) {
				return true
			}
		}
	}
	return false
}

// extractAddressFailureFromEvents inspects Service events for a MetalLB
// allocation failure and passes its message straight through, rather than
// trying to classify it.
//
// VERIFIED against a live cluster (MetalLB v0.14.9): every allocation
// failure — missing pool, an address outside the pool's configured range, an
// exhausted pool — emits the exact same Warning event shape:
//
//	Reason="AllocationFailed"  Source.Component="metallb-controller"
//	Message: `Failed to allocate IP for "<ns>/<name>": <allocator error>`
//
// MetalLB does not hand out a distinct Reason per failure kind, so this does
// NOT attempt to invent PoolNotFound/PoolExhausted/etc. enums from the
// Message text the way an earlier version of this function did — that
// matched substrings ("pool not found", "exhausted") MetalLB never actually
// emits, so it never fired. Instead the single AllocationFailed reason is
// surfaced and MetalLB's own allocator-error text (redundant
// `Failed to allocate IP for "ns/name": ` prefix stripped) becomes the
// condition message: it is more specific than any enum this code could
// invent, and it can't go stale relative to a future MetalLB release.
//
// Only Warning events whose reporting component looks like an address
// manager (see fromAddressManager) are considered at all, so an unrelated
// kubelet or controller warning on the same Service can never be misreported
// as an allocation failure. When nothing matches, the zero value is returned
// and the AddressAssignment condition degrades to the generic
// AssignmentPending rather than inventing a reason.
//
// Treat extracted text as informational — never crash or blank the condition if
// events are missing or unparseable.
func extractAddressFailureFromEvents(events []corev1.Event) addressFailureReason {
	for i := range events {
		ev := &events[i]
		if ev.Type != corev1.EventTypeWarning || ev.Reason != "AllocationFailed" {
			continue
		}
		if !fromAddressManager(ev) {
			continue
		}
		return addressFailureReason{
			reason:  "AllocationFailed",
			message: stripAllocationFailedPrefix(ev.Message),
		}
	}
	return addressFailureReason{}
}

// allocationFailedPrefix is the boilerplate MetalLB prepends to every
// AllocationFailed event's Message, ahead of the allocator's own error text.
// See extractAddressFailureFromEvents for the verified fixture this is
// stripping.
const allocationFailedPrefix = `Failed to allocate IP for "`

// stripAllocationFailedPrefix removes MetalLB's `Failed to allocate IP for
// "ns/name": ` boilerplate from an AllocationFailed event's Message, keeping
// only the allocator's own error text (e.g. `unknown pool "x"`). The
// GameServer this belongs to is already obvious from context (it's on that
// server's own condition), so the boilerplate adds nothing. If the message
// doesn't match the expected shape it is returned unchanged — this is a
// cosmetic trim, not a parser the condition's correctness depends on.
func stripAllocationFailedPrefix(msg string) string {
	if !strings.HasPrefix(msg, allocationFailedPrefix) {
		return msg
	}
	rest := msg[len(allocationFailedPrefix):]
	if idx := strings.Index(rest, `": `); idx != -1 {
		return rest[idx+3:]
	}
	return msg
}

// extractAddressFailureFromCiliumCondition inspects a Service's status
// conditions for a Cilium LB-IPAM allocation failure and derives an
// addressFailureReason from it.
//
// This mapping is derived from Cilium's documented LB-IPAM behavior
// (lines 419-425 record a genuine verification against a live cluster):
// Cilium sets a Service status condition of type cilium.io/IPAMRequestSatisfied
// with status False and a Reason field that is one of:
//   - "no_pool": the requested pool does not exist
//   - "out_of_ips": the pool is exhausted
//
// Cilium does NOT emit Events for allocation failures (unlike MetalLB) — the
// condition is the sole signal. This function maps the Cilium reasons onto
// the same reason names as the MetalLB path so both address managers integrate
// cleanly: "no_pool" → "PoolNotFound", "out_of_ips" → "AllocationFailed".
// Cilium's own condition Message is carried verbatim into the
// addressFailureReason, matching the philosophy of the MetalLB path — report,
// never re-classify. Unknown reason values fall through to the zero value,
// so if Cilium's reason strings ever differ from the contract, failures would
// go silently unreported until someone runs this function against a live cluster.
//
// The zero value is returned when the condition is absent, satisfied, or
// the Service pointer is nil.
func extractAddressFailureFromCiliumCondition(svc *corev1.Service) addressFailureReason {
	if svc == nil {
		return addressFailureReason{}
	}
	const ciliumConditionType = "cilium.io/IPAMRequestSatisfied"
	cond := meta.FindStatusCondition(svc.Status.Conditions, ciliumConditionType)
	if cond == nil {
		return addressFailureReason{}
	}
	if cond.Status == metav1.ConditionTrue {
		// Satisfied: no failure.
		return addressFailureReason{}
	}
	// Condition is False; map the Reason onto our reason enum.
	switch cond.Reason {
	case "no_pool":
		return addressFailureReason{
			reason:  "PoolNotFound",
			message: cond.Message,
		}
	case "out_of_ips":
		return addressFailureReason{
			reason:  "AllocationFailed",
			message: cond.Message,
		}
	default:
		// Unknown reason; treat as "no failure derived" rather than inventing
		// a reason we haven't verified against a real cluster.
		return addressFailureReason{}
	}
}

// addressAssignmentCondition reports what became of a
// spec.networking.addressPool / .address request on the AddressAssignment
// condition, or removes the condition when nothing was requested.
//
// A server that never asked for a pool or an address carries no
// AddressAssignment condition at all rather than a False one: emitting it
// unconditionally would add a permanent condition to every GameServer in the
// cluster for a feature they do not use. Removing it also means clearing the
// spec fields clears the report on the next reconcile.
//
// failure supplies a derived address-manager failure — a Cilium Service
// condition, a direct MetalLB IPAddressPool-not-found check, or a MetalLB
// Service event — see reconcileStatus, extractAddressFailureFromCiliumCondition,
// and extractAddressFailureFromEvents. conflictingServer, when non-empty, names
// another GameServer that already holds the requested explicit address. Both are
// treated as informational — never crash or blank the condition if they're
// unavailable.
//
// LATCHING: a Kubernetes Event has a ~1h TTL and MetalLB does not re-emit
// AllocationFailed while the failure persists, so a reconcile pass that finds
// no fresh failure is NOT evidence the problem cleared — it's equally
// consistent with the event having simply expired. To avoid the condition
// oscillating between the terminal reason and the generic AssignmentPending
// on every reconcile (this happened during development of this feature: a
// too-eager "skip the event read once terminal" optimization let the
// condition get recomputed from a now-empty failure and fall through to
// AssignmentPending, overwriting the real diagnosis within roughly an hour),
// a previously-derived PoolNotFound/AllocationFailed reason already recorded
// on conds is preserved rather than recomputed to empty. It is released only
// when one of three things happens:
//   - This pass has its own fresh failure signal (failure.reason != "",
//     checked first — above the latch in switch order — so it always wins).
//   - This pass has DEFINITIVE evidence the failure cleared:
//     failure.confirmedClear, AND the latched reason on record is
//     specifically PoolNotFound. Unlike AllocationFailed, PoolNotFound has a
//     live, non-expiring recheck available every single pass (a direct
//     IPAddressPool GET — see reconcileStatus), so it does not actually need
//     latching against a TTL; confirmedClear is how that live "no, it
//     definitely exists now" answer overrides a stale PoolNotFound latch
//     without waiting for the address to actually get assigned. The plain
//     zero value of failure (nothing observed this pass) does NOT set this —
//     that's the ordinary "the event expired" case the latch must survive.
//     confirmedClear is scoped to PoolNotFound specifically because "the
//     pool exists" is not evidence an AllocationFailed (e.g. exhausted pool)
//     has cleared — that latch is released only by the other two bullets.
//   - The address actually gets assigned (assigned != "", which routes past
//     the latch's assigned=="" guard into the Assigned case below), or the
//     request itself changed. "Changed" is detected via ObservedGeneration:
//     it was stamped with gs.Generation at the moment the failure was
//     written, and any spec edit (including changing
//     spec.networking.addressPool/address) bumps Generation, so a mismatch
//     means the latched reason no longer describes what's currently
//     requested and must not be trusted.
func addressAssignmentCondition(
	conds []metav1.Condition,
	gs *gameplanev1alpha1.GameServer,
	plan addressPlan,
	svc *corev1.Service,
	failure addressFailureReason,
	conflictingServer string,
) []metav1.Condition {
	if !plan.requested() {
		return removeCondition(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	}

	existing := meta.FindStatusCondition(conds, gameplanev1alpha1.GameServerConditionAddressAssignment)
	cond := metav1.Condition{
		Type:               gameplanev1alpha1.GameServerConditionAddressAssignment,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: gs.Generation,
	}
	assigned := loadBalancerAddress(svc)
	switch {
	case plan.Outcome == addressPlanIgnoredForExposureMode:
		expose := gs.Spec.Networking.Expose
		if expose == "" {
			expose = "ClusterIP"
		}
		cond.Reason = "IgnoredForExposureMode"
		cond.Message = fmt.Sprintf(
			"Requested %s is ignored: it applies only to expose mode 'LoadBalancer', "+
				"and this server is exposed as '%s'.",
			addressRequestSummary(plan), expose)
	case plan.Outcome == addressPlanInvalidAddress:
		// spec.networking.address is not a parseable IP address (F-056):
		// report a clear format error instead of handing the raw value to
		// the address manager, which would otherwise apply it verbatim and
		// report a generic pending reason.
		cond.Reason = "InvalidAddress"
		cond.Message = fmt.Sprintf(
			"Requested address %q is not a valid IP address.", gs.Spec.Networking.Address)
	case plan.Outcome == addressPlanNoAddressManagerConfigured:
		// Reported rather than passed over in silence: with no address manager
		// the server still comes up on whatever the cluster's default policy
		// hands out, which looks assigned but honors nothing that was asked for.
		cond.Reason = "NoAddressManagerConfigured"
		cond.Message = fmt.Sprintf(
			"Requested %s cannot be honored: this cluster has no load-balancer address manager configured, "+
				"so any address the server receives comes from the default assignment policy.",
			addressRequestSummary(plan))
	case conflictingServer != "":
		// Explicit address requested, but another GameServer already holds it.
		cond.Reason = "AddressInUse"
		cond.Message = fmt.Sprintf(
			"Requested address %q is already in use by GameServer %q.",
			gs.Spec.Networking.Address, conflictingServer)
	case failure.reason != "":
		// Fresh signal this pass — a direct PoolNotFound check or a MetalLB
		// AllocationFailed event. Always wins over a stale latch (below).
		cond.Reason = failure.reason
		cond.Message = failure.message
	case assigned == "" && existing != nil && existing.ObservedGeneration == gs.Generation &&
		(existing.Reason == "PoolNotFound" || existing.Reason == "AllocationFailed") &&
		(!failure.confirmedClear || existing.Reason != "PoolNotFound"):
		// Latch: no fresh failure this pass, but a previously derived terminal
		// reason is still on record for this exact request (see the doc
		// comment above) and no address has appeared. Preserve it rather than
		// falling through to the generic AssignmentPending below.
		//
		// confirmedClear only releases the latch when the latched reason is
		// PoolNotFound (a live IPAddressPool GET is definitive evidence
		// against that specific reason — see addressFailureReason.confirmedClear).
		// It must NOT release a latched AllocationFailed: "the pool exists"
		// says nothing about whether the pool is still exhausted, so without
		// this scoping an exhausted-pool diagnosis would flip to
		// AssignmentPending and back to AllocationFailed every reconcile once
		// the originating event's ~1h TTL expired.
		cond.Reason = existing.Reason
		cond.Message = existing.Message
	case svc == nil || svc.Spec.Type != corev1.ServiceTypeLoadBalancer:
		cond.Reason = "ServiceNotReady"
		cond.Message = fmt.Sprintf(
			"Requested %s: waiting for the LoadBalancer Service carrying the request to exist.",
			addressRequestSummary(plan))
	case assigned == "":
		cond.Reason = "AssignmentPending"
		cond.Message = fmt.Sprintf(
			"Requested %s: the address manager has not assigned an address yet.",
			addressRequestSummary(plan))
	case plan.Pool != "":
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Assigned"
		cond.Message = fmt.Sprintf("Address %s assigned from pool '%s'.", assigned, plan.Pool)
	default:
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Assigned"
		cond.Message = fmt.Sprintf("Address %s assigned.", assigned)
	}
	return upsertCondition(conds, cond)
}

// addressRequestSummary renders a pool/address request the way a condition
// message names it back to the user. The values are quoted verbatim: they are
// what was spec'd, not what the address manager resolved them to.
func addressRequestSummary(plan addressPlan) string {
	switch {
	case plan.Pool != "" && plan.Address != "":
		return fmt.Sprintf("address '%s' from pool '%s'", plan.Address, plan.Pool)
	case plan.Address != "":
		return fmt.Sprintf("address '%s'", plan.Address)
	default:
		return fmt.Sprintf("pool '%s'", plan.Pool)
	}
}

// gameContainerStartedAt returns when the game container of pod <name>-0
// last entered Running, or nil when the pod or that state is unavailable.
// Best-effort: a failed read just leaves status.startedAt as it was.
func (r *GameServerReconciler) gameContainerStartedAt(ctx context.Context, gs *gameplanev1alpha1.GameServer) *metav1.Time {
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{Name: gs.Name + "-0", Namespace: gs.Namespace}, &pod); err != nil {
		return nil
	}
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == gameContainerName && cs.State.Running != nil {
			started := cs.State.Running.StartedAt
			return &started
		}
	}
	return nil
}

// gameContainerName is the name the controller gives the game container in
// every pod (see buildGameContainer). The pod-log proxy keys off the same
// name — keep them in sync.
const gameContainerName = "game"

// provisioningInfo is the human-facing refinement of the Starting phase:
// a short Reason and a sentence-long Message describing what the pod is
// currently doing. It surfaces on the Progressing condition.
type provisioningInfo struct {
	reason  string
	message string
	// args are the values the localized text of reason needs; see
	// provisioningArgs. nil when the reason has none.
	args map[string]string
}

// provisioningReason inspects a Starting pod's container states to explain
// why it isn't Running yet — image pull, container creation, first-run
// install (game container up but not Ready), or waiting for the agent's
// first heartbeat. It's a pure function so it can be unit-tested without a
// live kubelet (envtest never runs one). hbFresh is the heartbeat result
// the caller already computed.
func provisioningReason(pod *corev1.Pod, hbFresh bool) (reason, message string) {
	// Init containers (config-init) run before the game container; if one
	// is stuck pulling/creating, surface that first.
	for i := range pod.Status.InitContainerStatuses {
		if w := pod.Status.InitContainerStatuses[i].State.Waiting; w != nil {
			if r, m := waitingReason(w.Reason); r != "" {
				return r, m
			}
		}
	}

	var game *corev1.ContainerStatus
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == gameContainerName {
			game = &pod.Status.ContainerStatuses[i]
			break
		}
	}
	if game == nil {
		// Pod scheduled but the kubelet hasn't reported the game container
		// yet — still being created.
		return "ContainerCreating", "creating the container"
	}

	switch {
	case game.State.Waiting != nil:
		if r, m := waitingReason(game.State.Waiting.Reason); r != "" {
			return r, m
		}
		return "ContainerCreating", "creating the container"
	case game.State.Terminated != nil:
		// Exited during startup — a failed install or crash before ready.
		return "ContainerExited", "the container exited during startup; check the logs"
	case game.State.Running != nil:
		if podReady(pod) && !hbFresh {
			return "WaitingForAgent", "server is up; waiting for the agent's first heartbeat"
		}
		// Running but not Ready: the entrypoint is still installing/
		// generating before the readiness probe passes.
		return "InstallingServerFiles", "downloading game files / waiting for readiness"
	default:
		return "Starting", "starting up"
	}
}

// waitingReason maps a container's Waiting.Reason to a Gameplane reason +
// message, or ("", "") if it's not one we specifically explain.
func waitingReason(reason string) (string, string) {
	switch reason {
	case "ImagePullBackOff", "ErrImagePull":
		return "PullingImage", "pulling the game image"
	case "ContainerCreating", "PodInitializing":
		return "ContainerCreating", "creating the container"
	case "CrashLoopBackOff":
		return "CrashLoopBackOff", "the container is crash-looping during startup; check the logs"
	}
	return "", ""
}

// crashLoopFailureThreshold is how many restarts of the game container we
// tolerate during startup before declaring the server Failed. A first boot
// that crashes once or twice and then succeeds stays Starting; a persistent
// crash-loop escalates so the dashboard stops showing a perpetual
// "Starting".
const crashLoopFailureThreshold = 3

// startupFailure reports whether a Starting pod has hit a terminal startup
// failure that will not clear on its own — an unpullable image, a persistent
// crash-loop, or a container that exited non-zero — with a human-facing
// reason and message. It's a pure function (envtest has no kubelet, so the
// container states are supplied by the test). The result is advisory only:
// derivePhase re-evaluates every reconcile, so a pod that later recovers
// returns to Running.
func startupFailure(pod *corev1.Pod) (reason, message string, failed bool) {
	// Init containers (config-init) gate the game container; a stuck image
	// pull there is just as terminal.
	for i := range pod.Status.InitContainerStatuses {
		if r, m, f := containerFailure(&pod.Status.InitContainerStatuses[i]); f {
			return r, m, true
		}
	}
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == gameContainerName {
			return containerFailure(&pod.Status.ContainerStatuses[i])
		}
	}
	return "", "", false
}

// containerFailure classifies a single container status as a terminal
// startup failure (true) or a state still worth waiting on (false).
func containerFailure(cs *corev1.ContainerStatus) (reason, message string, failed bool) {
	if w := cs.State.Waiting; w != nil {
		switch {
		case w.Reason == "ImagePullBackOff":
			// The kubelet already retried the pull and is backing off — a
			// bad image reference, not a transient first-attempt blip.
			return "ImagePullFailed", "cannot pull the image — check the image reference", true
		case w.Reason == "CrashLoopBackOff" && cs.RestartCount >= crashLoopFailureThreshold:
			return "CrashLoopBackOff", fmt.Sprintf(
				"the container has crash-looped %d times during startup; check the logs",
				cs.RestartCount), true
		}
	}
	if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
		return "ContainerExited", fmt.Sprintf(
			"the container exited with code %d during startup; check the logs",
			t.ExitCode), true
	}
	return "", "", false
}

// checkPVCProvisioningFailure inspects the game-data PVC for provisioning errors.
// It returns (storageClass, message, provisioning, err) where:
//   - provisioning=true and err==nil: a provisioning failure was detected (e.g., StorageClass not found)
//   - provisioning=false and err!=nil: an error occurred checking the PVC or StorageClass (e.g., RBAC issue)
//   - provisioning=false and err==nil: no provisioning failure
//
// When the PVC is Bound, no failure is reported. When the PVC is Pending and references
// a StorageClass that does not exist on the cluster, that is reported as a provisioning
// failure. Errors (other than NotFound for StorageClass) are not silently swallowed but
// are returned to the caller to surface.
func checkPVCProvisioningFailure(ctx context.Context, r client.Client, ns, pvcName string) (storageClass, message string, provisioning bool, err error) {
	var pvc corev1.PersistentVolumeClaim
	if getErr := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ns}, &pvc); getErr != nil {
		if apierrors.IsNotFound(getErr) {
			// PVC doesn't exist yet; no provisioning failure.
			return "", "", false, nil
		}
		// PVC Get failed for a real reason (RBAC, API server, etc.); return the error.
		return "", "", false, fmt.Errorf("fetch PVC: %w", getErr)
	}

	// If the PVC is Bound, there is no failure.
	if pvc.Status.Phase == corev1.ClaimBound {
		return "", "", false, nil
	}

	// PVC is not Bound (likely Pending). If it references a specific StorageClass, verify it exists.
	if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		scName := *pvc.Spec.StorageClassName
		var sc storagev1.StorageClass
		getErr := r.Get(ctx, types.NamespacedName{Name: scName}, &sc)
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				// StorageClass does not exist; this is a provisioning failure.
				msg := fmt.Sprintf("StorageClass '%s' not found on cluster.", scName)
				return scName, msg, true, nil
			}
			// StorageClass Get failed for a real reason (RBAC, API server, etc.);
			// return the error so the operator knows there's a configuration problem.
			return "", "", false, fmt.Errorf("check StorageClass '%s': %w", scName, getErr)
		}
	}

	// No provisioning failure detected.
	return "", "", false, nil
}

// podReady reports whether the pod's Ready condition is true.
func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// computeTunnelConditions sets the TunnelReady condition based on the tunnel
// Deployment's readiness and configuration state. When the tunnel is disabled,
// the condition is removed from the status.
func computeTunnelConditions(
	gs *gameplanev1alpha1.GameServer,
	plan tunnelPlan,
	tunnelDep *appsv1.Deployment,
) []metav1.Condition {
	conds := gs.Status.Conditions

	// If tunnel is not wanted, remove the TunnelReady condition along with
	// the informational TunnelHostnameIgnored condition, which only applies
	// while a tunnel is active.
	if !plan.wantTunnel {
		conds = removeCondition(conds, "TunnelReady")
		conds = removeCondition(conds, "TunnelHostnameIgnored")
		return conds
	}

	// Report informational conditions when tunnel settings don't apply to tunnel
	// traffic. Computed up front, before the readiness checks below, so that
	// TunnelHostnameIgnored always reflects the current spec rather than going
	// stale behind an early return (e.g. a hostname cleared while the tunnel
	// Deployment isn't ready yet, or the provider config is invalid) — F-055.
	ignoredSettings := ignoredTunnelSettings(gs)
	if len(ignoredSettings) > 0 {
		conds = upsertCondition(conds, metav1.Condition{
			Type:               "TunnelHostnameIgnored",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gs.Generation,
			Reason:             "SettingIgnored",
			Message:            fmt.Sprintf("%s apply to the backing Service, not tunnel traffic", strings.Join(ignoredSettings, ", ")),
		})
	} else {
		// None of the settings the tunnel ignores are set anymore (e.g. the
		// hostname was cleared, or the tunnel was disabled): drop the stale
		// informational condition rather than leaving an outdated True value
		// on status indefinitely.
		conds = removeCondition(conds, "TunnelHostnameIgnored")
	}

	// Tunnel is wanted: determine readiness.
	var tunnelReady metav1.Condition
	tunnelReady.Type = "TunnelReady"
	tunnelReady.ObservedGeneration = gs.Generation

	tunnel := gs.Spec.Networking.Tunnel
	if tunnel == nil {
		// Should not happen if plan.wantTunnel is true, but guard anyway.
		tunnelReady.Status = metav1.ConditionFalse
		tunnelReady.Reason = "InvalidConfig"
		tunnelReady.Message = "tunnel enabled but no tunnel spec"
		conds = upsertCondition(conds, tunnelReady)
		return conds
	}

	// Tunnel Deployment not yet created or not ready.
	if tunnelDep == nil {
		tunnelReady.Status = metav1.ConditionFalse
		tunnelReady.Reason = "DeploymentNotReady"
		tunnelReady.Message = "waiting for tunnel deployment to be created"
		conds = upsertCondition(conds, tunnelReady)
		return conds
	}

	if tunnelDep.Status.ReadyReplicas == 0 {
		tunnelReady.Status = metav1.ConditionFalse
		tunnelReady.Reason = "DeploymentNotReady"
		tunnelReady.Message = "tunnel deployment has no ready replicas"
		conds = upsertCondition(conds, tunnelReady)
		return conds
	}

	// Provider-specific readiness checks.
	switch tunnel.Provider {
	case "frp":
		if tunnel.Frp == nil {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "InvalidConfig"
			tunnelReady.Message = "frp provider selected but no frp config"
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// Check for unmapped ports: frp requires explicit RemotePorts mapping.
		if len(plan.noMapping) > 0 {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "PortNotMapped"
			tunnelReady.Message = fmt.Sprintf(
				"frp remote ports not configured for: %s",
				strings.Join(plan.noMapping, ", "),
			)
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// All endpoints known: deployment ready and mappings complete.
		tunnelReady.Status = metav1.ConditionTrue
		tunnelReady.Reason = "Ready"
		tunnelReady.Message = "tunnel deployment ready and all ports mapped"

	case "tailscale":
		if tunnel.Tailscale == nil {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "InvalidConfig"
			tunnelReady.Message = "tailscale provider selected but no tailscale config"
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// Check if credentials are provided (optional, but if missing, tunnel won't authenticate).
		if tunnel.CredentialsSecretRef == nil {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "NoCredentials"
			tunnelReady.Message = "no credentials secret provided; tailscale won't authenticate"
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// Deployment is ready and credentials are configured.
		tunnelReady.Status = metav1.ConditionTrue
		tunnelReady.Reason = "Ready"
		tunnelReady.Message = "tunnel deployment ready and authenticated"

	case "playit":
		if tunnel.Playit == nil {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "InvalidConfig"
			tunnelReady.Message = "playit provider selected but no playit config"
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// Check if credentials are provided (playit requires authentication).
		if tunnel.CredentialsSecretRef == nil {
			tunnelReady.Status = metav1.ConditionFalse
			tunnelReady.Reason = "NoCredentials"
			tunnelReady.Message = "no credentials secret provided; playit requires authentication"
			conds = upsertCondition(conds, tunnelReady)
			return conds
		}

		// Deployment is ready; playit endpoint will arrive asynchronously.
		// Report AwaitingAddress while the tunnel pod hasn't yet reported an address.
		// This is optimistic: the condition flips based on whether the tunnel pod
		// has written to status.tunnelEndpoint. The agent or operator reconcile must
		// populate it separately.
		tunnelReady.Status = metav1.ConditionTrue
		tunnelReady.Reason = "Ready"
		tunnelReady.Message = "tunnel deployment ready; waiting for playit endpoint assignment"

	default:
		tunnelReady.Status = metav1.ConditionFalse
		tunnelReady.Reason = "UnknownProvider"
		tunnelReady.Message = fmt.Sprintf("unknown tunnel provider: %s", tunnel.Provider)
	}

	conds = upsertCondition(conds, tunnelReady)
	return conds
}

// removeCondition removes a condition by type from the list, or returns the list unchanged.
func removeCondition(conds []metav1.Condition, condType string) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(conds))
	for i := range conds {
		if conds[i].Type != condType {
			out = append(out, conds[i])
		}
	}
	return out
}

// loadBalancerAddress returns the external address the cluster's address
// manager has published on a LoadBalancer Service, or "" while none is
// assigned (or the Service is not a LoadBalancer at all). Only the first
// ingress entry is read, and a hostname wins over an IP: a cloud LB that
// publishes both wants players resolving the name.
func loadBalancerAddress(svc *corev1.Service) string {
	if svc == nil || svc.Spec.Type != corev1.ServiceTypeLoadBalancer ||
		len(svc.Status.LoadBalancer.Ingress) == 0 {
		return ""
	}
	ing := svc.Status.LoadBalancer.Ingress[0]
	if ing.Hostname != "" {
		return ing.Hostname
	}
	return ing.IP
}

// endpointsFromService lists the per-port externally reachable address
// for a Service. For ClusterIP we report the cluster IP; for NodePort
// we report the Service's declared port (the node IP is left to the API
// layer since the operator doesn't know which node a user will hit).
//
// plan supplies the pool each endpoint's address was drawn from. That name is
// the pool the operator *requested*, not one the address manager confirmed:
// neither MetalLB nor Cilium writes the pool it allocated from back onto the
// Service, so there is no authoritative source to read and the request is the
// honest one. Consumers must read endpoint.Pool as "asked for from", not
// "verified to be from".
//
// It is stamped only once an address has actually been published and only on a
// translated request, so it never claims a pool for the ClusterIP fallback
// address shown while assignment is pending, for a request no address manager
// was configured to act on, or for one ignored because the expose mode is not
// LoadBalancer.
func endpointsFromService(
	svc *corev1.Service, plan addressPlan,
) []gameplanev1alpha1.GameServerEndpoint {
	out := make([]gameplanev1alpha1.GameServerEndpoint, 0, len(svc.Spec.Ports))
	host := svc.Spec.ClusterIP
	pool := ""
	if addr := loadBalancerAddress(svc); addr != "" {
		host = addr
		if plan.Outcome == addressPlanTranslated {
			pool = plan.Pool
		}
	}
	for _, p := range svc.Spec.Ports {
		ep := gameplanev1alpha1.GameServerEndpoint{
			Name:     p.Name,
			Host:     host,
			Port:     p.Port,
			Protocol: p.Protocol,
			Pool:     pool,
		}
		if svc.Spec.Type == corev1.ServiceTypeNodePort && p.NodePort != 0 {
			ep.Port = p.NodePort
		}
		out = append(out, ep)
	}
	return out
}

// getAdvertisedPortNames returns the list of advertised port names from the template.
func getAdvertisedPortNames(tmpl *gameplanev1alpha1.GameTemplate) []string {
	var names []string
	for _, p := range tmpl.Spec.Ports {
		if p.Advertise {
			names = append(names, p.Name)
		}
	}
	return names
}

// validatePlayitEndpoints validates a slice of playit tunnel endpoints.
// It checks each endpoint for validity (host, port range, advertised port name)
// and rejects duplicate port names. Returns the valid endpoints and an error
// message if any entries were invalid or duplicates were found.
// Invalid entries are filtered out, but the error message is still returned
// so a condition can be set on the GameServer.
func validatePlayitEndpoints(
	endpoints []gameplanev1alpha1.GameServerEndpoint, advertisedPorts []string,
) ([]gameplanev1alpha1.GameServerEndpoint, string) {
	var valid []gameplanev1alpha1.GameServerEndpoint
	var errs []string
	seenPorts := make(map[string]bool)

	for i, ep := range endpoints {
		// Check for duplicate port names.
		if seenPorts[ep.Name] {
			errs = append(errs, fmt.Sprintf("endpoint %d: duplicate port name %q", i, ep.Name))
			continue
		}
		seenPorts[ep.Name] = true

		// Validate this entry.
		if ok, msg := validatePlayitEndpoint(&ep, advertisedPorts); !ok {
			errs = append(errs, fmt.Sprintf("endpoint %d: %s", i, msg))
			continue
		}

		valid = append(valid, ep)
	}

	var errMsg string
	if len(errs) > 0 {
		errMsg = strings.Join(errs, "; ")
	}
	return valid, errMsg
}

// validatePlayitEndpoint validates a playit tunnel endpoint for host validity,
// port range, and port name. Returns (true, "") on success, or (false, message)
// on validation failure.
func validatePlayitEndpoint(endpoint *gameplanev1alpha1.GameServerEndpoint, advertisedPorts []string) (bool, string) {
	// Validate host.
	if err := validatePlayitHost(endpoint.Host); err != nil {
		return false, fmt.Sprintf("invalid hostname: %v", err)
	}

	// Validate port range.
	if endpoint.Port < 1 || endpoint.Port > 65535 {
		return false, fmt.Sprintf("invalid port: %d (must be 1-65535)", endpoint.Port)
	}

	// Validate port name against advertised ports.
	found := false
	for _, name := range advertisedPorts {
		if name == endpoint.Name {
			found = true
			break
		}
	}
	if !found {
		return false, fmt.Sprintf("port name not advertised: %s", endpoint.Name)
	}

	return true, ""
}

// validatePlayitHost validates the playit endpoint hostname/IP for syntactic validity.
// It rejects control characters, whitespace, embedded schemes, and absurd lengths.
func validatePlayitHost(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// Reject absurd lengths: DNS labels max 253 chars, IPv6 max ~45 chars.
	if len(host) > 253 {
		return fmt.Errorf("host too long (%d > 253 characters)", len(host))
	}

	// Reject control characters and whitespace. Whitespace is checked first
	// since tab/newline/CR are also control characters (< 32); the more
	// specific "whitespace" message should win for those.
	for _, ch := range host {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			return fmt.Errorf("host contains whitespace")
		}
		if ch < 32 || ch == 127 {
			return fmt.Errorf("host contains control character")
		}
	}

	// Reject embedded schemes (http://, https://, etc).
	if strings.Contains(host, "://") {
		return fmt.Errorf("host contains embedded scheme")
	}

	// Try to parse as an IP address (v4 or v6).
	if ip := net.ParseIP(host); ip != nil {
		// Reject private/internal IPs: a reported public tunnel address
		// should not point at cluster-internal addresses.
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("host is a private or internal address (%s)", ip)
		}
		return nil
	}

	// Not a raw IP; try to validate as a DNS name.
	// Reject cluster-local DNS names (these point at internal services).
	if strings.HasSuffix(host, ".svc.cluster.local") {
		return fmt.Errorf("host is a cluster-local service name; tunnel addresses must be public")
	}

	// DNS names must be valid labels separated by dots.
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("host contains empty DNS label")
		}
		if len(label) > 63 {
			return fmt.Errorf("DNS label too long (%d > 63 characters)", len(label))
		}
		// DNS labels must start and end with alphanumeric, may contain hyphens.
		if !isValidDNSLabel(label) {
			return fmt.Errorf("invalid DNS label: %s", label)
		}
	}

	return nil
}

// isValidDNSLabel checks if a single DNS label is valid (alphanumeric/hyphen,
// starting and ending with alphanumeric).
func isValidDNSLabel(label string) bool {
	if len(label) == 0 {
		return false
	}
	for i, ch := range label {
		if !isAlphaNumeric(ch) && ch != '-' {
			return false
		}
		// First and last must not be hyphen.
		if ch == '-' && (i == 0 || i == len(label)-1) {
			return false
		}
	}
	return true
}

// isAlphaNumeric checks if a rune is an alphanumeric character (letter or digit).
func isAlphaNumeric(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}
