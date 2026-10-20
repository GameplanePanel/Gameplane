package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	sentinelLabel = "app.kubernetes.io/name"
	sentinelValue = "gameplane-waker"

	// sentinelBackstopRequeue bounds how long a missed or coalesced watch
	// event can leave the game Service pointed at a stale target while
	// planSentinel is still waiting on the sentinel or the game pod. It is
	// only a backstop: both the sentinel Deployment and the game
	// StatefulSet are owned (see SetupWithManager's Owns(...) calls), so a
	// readiness transition normally retriggers Reconcile well before this
	// fires. This must stay bounded and must never gate skipping the rest
	// of Reconcile — see planSentinel's doc comment for the bug this fixes.
	sentinelBackstopRequeue = 5 * time.Second

	// sentinelShutdownDrainTimeout is how long a terminating sentinel keeps
	// a player connection it already handed through to the game pod alive
	// (F-179). The operator deletes the waker Deployment the moment the
	// game pod is Ready, and the game Service stops routing to the waker in
	// the same pass, so no new connection reaches it; but a player who
	// woke the server is still connected *through* it. Deleting the pod
	// sends SIGTERM, on which the sentinel closes its listeners and then
	// waits (up to this long) for its in-flight sessions to end on their
	// own. The pod's terminationGracePeriodSeconds covers the whole drain,
	// so the kubelet does not SIGKILL it first: the waker pod lingers in
	// Terminating only while it still has sessions, and exits as soon as
	// the last one closes (a session also ends by itself when the game pod
	// goes away). Must match the sentinel's defaultShutdownDrainTimeout.
	sentinelShutdownDrainTimeout = 4 * time.Hour

	// sentinelShutdownGraceSlack is the extra terminationGracePeriodSeconds
	// on top of the drain, for the sentinel to close what is left once the
	// drain runs out.
	sentinelShutdownGraceSlack = 30 * time.Second

	// sentinelHostportGracePeriod is the Hostport-mode grace period. There
	// is no drain in that mode (see planSentinel): the terminating sentinel
	// holds the very host port the game pod needs, so it must let go fast.
	sentinelHostportGracePeriod = 30 * time.Second
)

// sentinelShutdownSettings returns the sentinel's SHUTDOWN_DRAIN_TIMEOUT
// and its pod's terminationGracePeriodSeconds for this GameServer's expose
// mode. Both are always set explicitly (never left to the apiserver
// default) so CreateOrUpdate sees a stable pod template.
func sentinelShutdownSettings(gs *gameplanev1alpha1.GameServer) (drain time.Duration, graceSeconds int64) {
	if gs.Spec.Networking.Expose == "Hostport" {
		return 0, int64(sentinelHostportGracePeriod / time.Second)
	}
	return sentinelShutdownDrainTimeout, int64((sentinelShutdownDrainTimeout + sentinelShutdownGraceSlack) / time.Second)
}

// sentinelSAName builds the name of the ServiceAccount used by the wake sentinel.
// This is the single source of truth for the sentinel SA name, used by both
// reconcileSentinelRBAC (which creates it) and reconcileSentinel (which references it).
func sentinelSAName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-waker-sa"
}

// wakeOnConnectEligible reports whether this GameServer is configured for
// wake-on-connect at all: idle policy enabled, wakeOnConnect armed, and at
// least one advertised template port has a non-"none" wakeProtocol.
//
// This is deliberately independent of the current idleState. Both the
// sentinel Deployment (reconcileSentinel) and its supporting game-direct
// Service (reconcileGameDirectServiceFromTemplate) gate on this rather than
// on "currently asleep", because in the three Service-backed Expose modes
// the sentinel legitimately needs to keep running for a while *after*
// idleState has already flipped back to idleAwake — see planSentinel.
func wakeOnConnectEligible(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) bool {
	if gs.Spec.Idle == nil || !gs.Spec.Idle.Enabled || !gs.Spec.Idle.WakeOnConnect {
		return false
	}
	for _, p := range tmpl.Spec.Ports {
		if p.Advertise && p.WakeProtocol != "none" {
			return true
		}
	}
	return false
}

// sentinelPlan is one reconcile pass's decision about the wake sentinel:
// whether it should exist, and whether the game Service should route to it
// instead of the game pod.
type sentinelPlan struct {
	wantSentinel    bool
	routeToSentinel bool
}

// planSentinel is the single source of truth for the sentinel/game-Service
// relationship. reconcileSentinel (via wantSentinel) and reconcileService
// (via routeToSentinel) are both driven off its result, so there is exactly
// one place deciding "sentinel or game pod" instead of two CreateOrUpdate
// calls racing to write the same Service field — that race (updateServiceSelector
// flipping the selector to the sentinel, immediately undone by
// reconcileService flipping it back) is why wake-on-connect shipped as a
// no-op the first time.
//
// Going to sleep, the Service keeps pointing at the game pod until the
// sentinel itself reports Ready, so a player is never blackholed between
// "game pod gone" and "sentinel serving".
//
// Waking up, the three Service-backed Expose modes (ClusterIP, NodePort,
// LoadBalancer) and Hostport mode deliberately diverge — this is a design
// decision, not an oversight:
//
//   - ClusterIP / NodePort / LoadBalancer: the sentinel doesn't compete
//     with the game pod for anything, so it is kept alive — and the
//     Service kept routed to it — until the game pod itself reports Ready.
//     A player connection the sentinel is holding open can then be handed
//     straight through instead of being dropped mid-wake. Once the game
//     pod is Ready the Deployment is deleted, but the pod's long
//     terminationGracePeriodSeconds plus the sentinel's shutdown drain
//     (sentinelShutdownDrainTimeout) keep any handed-through session alive
//     until it ends on its own; only new connections stop reaching it.
//   - Hostport: the sentinel and the game pod bind the exact same host
//     port, so the sentinel cannot outlive the wake decision — the game
//     pod cannot even schedule until the sentinel releases that port. The
//     sentinel is expected to bounce any clients it is holding with a
//     protocol-native "waking up, try again shortly" response before the
//     operator tears it down here; players simply reconnect once the game
//     is up. There is no hold-open window in this mode.
func (r *GameServerReconciler) planSentinel(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, idle idleState,
) (sentinelPlan, error) {
	if !wakeOnConnectEligible(gs, tmpl) {
		return sentinelPlan{}, nil
	}

	if idle == idleAsleep {
		ready, err := r.sentinelIsReady(ctx, gs.Namespace, gs.Name)
		if err != nil {
			return sentinelPlan{}, err
		}
		return sentinelPlan{wantSentinel: true, routeToSentinel: ready}, nil
	}

	// idle == idleAwake: either a wake was just decided, or the server was
	// never asleep to begin with — sentinelExists is false in the latter
	// case, so we return immediately without the extra StatefulSet Get.
	if gs.Spec.Networking.Expose == "Hostport" {
		// See the Hostport bullet above: no hold-open window, ever.
		return sentinelPlan{}, nil
	}
	exists, err := r.sentinelExists(ctx, gs.Namespace, gs.Name)
	if err != nil || !exists {
		return sentinelPlan{}, err
	}
	gameReady, err := r.gamePodReady(ctx, gs)
	if err != nil {
		return sentinelPlan{}, err
	}
	if gameReady {
		return sentinelPlan{}, nil
	}
	return sentinelPlan{wantSentinel: true, routeToSentinel: true}, nil
}

// gamePodReady reports whether the GameServer's StatefulSet has at least
// one Ready replica — the same "is the game actually up" signal
// reconcileStatus and the restart drain gate use elsewhere in this package
// (ss.Status.ReadyReplicas > 0).
func (r *GameServerReconciler) gamePodReady(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	var ss appsv1.StatefulSet
	err := r.Get(ctx, types.NamespacedName{Name: gs.Name, Namespace: gs.Namespace}, &ss)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get statefulset %s/%s for game-pod readiness: %w", gs.Namespace, gs.Name, err)
	}
	return ss.Status.ReadyReplicas > 0, nil
}

// reconcileSentinel maintains a 1-replica Deployment `<gs>-waker` that
// serves as the wake sentinel while the server is asleep (and, in the
// Service-backed Expose modes, for a while into the wake — see
// planSentinel). The sentinel holds the advertised ports and wakes the
// server when a player connects.
//
// want is planSentinel's decision for this pass, not something computed
// here: this function only ever builds the desired Deployment or deletes
// it, so there is a single place (planSentinel) that owns "should the
// sentinel exist right now".
func (r *GameServerReconciler) reconcileSentinel(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	want bool,
) error {
	if !want {
		return r.deleteSentinel(ctx, gs.Namespace, gs.Name)
	}

	// Create or update the sentinel Deployment.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name + "-waker",
			Namespace: gs.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		replicas := int32(1)
		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				sentinelLabel:                sentinelValue,
				"app.kubernetes.io/instance": gs.Name,
			},
		}
		dep.Spec.Template.Labels = map[string]string{
			sentinelLabel:                sentinelValue,
			"app.kubernetes.io/instance": gs.Name,
		}

		// Container ports: advertised ports only, mirroring buildGameContainer.
		ports := make([]corev1.ContainerPort, 0)
		hostPort := gs.Spec.Networking.Expose == "Hostport"
		for _, p := range tmpl.Spec.Ports {
			if !p.Advertise {
				continue
			}
			cp := corev1.ContainerPort{
				Name:          p.Name,
				ContainerPort: p.ContainerPort,
				Protocol:      p.Protocol,
			}
			// Mirror buildGameContainer: only bind advertised ports on the host
			// in Hostport mode.
			if hostPort {
				cp.HostPort = p.ContainerPort
			}
			ports = append(ports, cp)
		}

		// Security context: matching the wipe Job's hardened context.
		uid := int64(65532)
		nonRoot := true
		noPrivEsc := false
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot:   &nonRoot,
			RunAsUser:      &uid,
			RunAsGroup:     &uid,
			FSGroup:        &uid,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}

		// Sentinel container passes config via env or args. For now,
		// pass the minimal set: GameServer name/namespace and per-port config.
		image := r.SentinelImage
		if image == "" {
			image = DefaultSentinelImage
		}

		// Build the per-port config env var (comma-separated list).
		// Format: "port:protocol:wakeProtocol,..." e.g. "25565:TCP:minecraft,19133:UDP:generic"
		portConfig := buildSentinelPortConfig(tmpl)
		drain, graceSeconds := sentinelShutdownSettings(gs)
		dep.Spec.Template.Spec.TerminationGracePeriodSeconds = &graceSeconds

		dep.Spec.Template.Spec.ServiceAccountName = sentinelSAName(gs)
		dep.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "sentinel",
			Image: image,
			Env: []corev1.EnvVar{
				{Name: "GAMESERVER_NAME", Value: gs.Name},
				{Name: "GAMESERVER_NAMESPACE", Value: gs.Namespace},
				{Name: "PORTS_CONFIG", Value: portConfig},
				{Name: "SHUTDOWN_DRAIN_TIMEOUT", Value: drain.String()},
			},
			Ports: ports,
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             &nonRoot,
				RunAsUser:                &uid,
				AllowPrivilegeEscalation: &noPrivEsc,
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		}}

		return controllerutil.SetControllerReference(gs, dep, r.Scheme)
	})
	return err
}

// reconcileGameDirectServiceFromTemplate maintains a ClusterIP Service
// `<gs>-game-direct` that always selects the game pod labels. This Service
// allows the sentinel to reach the game pod when the main game Service's
// selector is flipped to the sentinel.
//
// The sentinel cannot proxy through the game's own Service — while asleep
// the sentinel IS that Service's endpoint. A separate per-pod DNS Service
// lets the sentinel reach the game pod even while it is that pod's external
// endpoint. Mirrors reconcileAgentService's reasoning: per-pod DNS only
// resolves under headless Services (or Services with PublishNotReadyAddresses),
// which the game Service is not; the agent and sentinel each need their own
// internal Service to be reachable by name.
//
// Gated on wakeOnConnectEligible, the same eligibility the sentinel itself
// uses: a GameServer that never arms wake-on-connect has no sentinel to
// serve, so it doesn't need this Service either — which matters because,
// being always ClusterIP, it cannot carry a nonzero NodePort the way the
// main Service (svcPortsFromTemplate) legitimately can.
func (r *GameServerReconciler) reconcileGameDirectServiceFromTemplate(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-game-direct", Namespace: gs.Namespace},
	}

	if !wakeOnConnectEligible(gs, tmpl) {
		// Same converge-to-absent pattern as reconcileNetworkPolicy: a Get
		// off the cache instead of an unconditional Delete (avoids a
		// DELETE -> 404 on every reconcile of an ineligible server), and
		// only ever delete an instance this GameServer actually owns.
		var existing corev1.Service
		if err := r.Get(ctx, client.ObjectKeyFromObject(svc), &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !metav1.IsControlledBy(&existing, gs) {
			return nil
		}
		return client.IgnoreNotFound(r.Delete(ctx, &existing))
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		// Allow the sentinel to reach the game pod while the game pod is
		// starting (before readiness). Match the agent Service's
		// PublishNotReadyAddresses so the sentinel connects even if the game
		// is warming up.
		svc.Spec.PublishNotReadyAddresses = true
		svc.Spec.Selector = map[string]string{
			"app.kubernetes.io/name":     "gameplane-game",
			"app.kubernetes.io/instance": gs.Name,
		}
		// Expose advertised template ports only, mirroring
		// svcPortsFromTemplate — except NodePort, which svcPortsFromTemplate
		// legitimately carries through from spec.networking.portOverrides
		// for the main (possibly NodePort-typed) Service. A nonzero
		// NodePort on a ClusterIP Service is rejected by the apiserver, and
		// this Service is always ClusterIP, so it must be stripped here or
		// every GameServer merely *using* a nodePort override fails this
		// CreateOrUpdate on every single reconcile — including all of its
		// other, unrelated steps that run after this one.
		ports := svcPortsFromTemplate(tmpl, gs)
		for i := range ports {
			ports[i].NodePort = 0
		}
		svc.Spec.Ports = ports
		return controllerutil.SetControllerReference(gs, svc, r.Scheme)
	})
	return err
}

// getSentinel fetches the sentinel Deployment, reporting exists=false
// (rather than an error) when it is simply absent.
func (r *GameServerReconciler) getSentinel(ctx context.Context, namespace, gsName string) (*appsv1.Deployment, bool, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gsName + "-waker",
			Namespace: namespace,
		},
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(dep), dep)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get sentinel deployment: %w", err)
	}
	return dep, true, nil
}

// sentinelExists reports whether the sentinel Deployment is present,
// without distinguishing "ready" from "not yet ready".
func (r *GameServerReconciler) sentinelExists(ctx context.Context, namespace, gsName string) (bool, error) {
	_, exists, err := r.getSentinel(ctx, namespace, gsName)
	return exists, err
}

// sentinelIsReady reports whether the sentinel Deployment has a ready replica.
func (r *GameServerReconciler) sentinelIsReady(ctx context.Context, namespace, gsName string) (bool, error) {
	dep, exists, err := r.getSentinel(ctx, namespace, gsName)
	if err != nil || !exists {
		return false, err
	}
	return dep.Status.ReadyReplicas > 0, nil
}

// deleteSentinel removes the sentinel Deployment if it exists.
func (r *GameServerReconciler) deleteSentinel(ctx context.Context, namespace, gsName string) error {
	dep, exists, err := r.getSentinel(ctx, namespace, gsName)
	if err != nil || !exists {
		return err
	}
	policy := metav1.DeletePropagationBackground
	return client.IgnoreNotFound(r.Delete(ctx, dep, &client.DeleteOptions{PropagationPolicy: &policy}))
}

// reconcileSentinelRBAC ensures the sentinel ServiceAccount and Roles exist,
// granting permission to get and patch the GameServer's annotations.
func (r *GameServerReconciler) reconcileSentinelRBAC(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	// ServiceAccount: <gs>-waker-sa
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sentinelSAName(gs),
			Namespace: gs.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return controllerutil.SetControllerReference(gs, sa, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconcile sentinel serviceaccount: %w", err)
	}

	// Role: <gs>-waker-role, granting get + patch on this GameServer (metadata/annotations).
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name + "-waker-role",
			Namespace: gs.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups:     []string{"gameplane.local"},
			Resources:     []string{"gameservers"},
			Verbs:         []string{"get", "patch"},
			ResourceNames: []string{gs.Name},
		}}
		return controllerutil.SetControllerReference(gs, role, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconcile sentinel role: %w", err)
	}

	// RoleBinding: <gs>-waker-rb, binding the SA to the role.
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name + "-waker-rb",
			Namespace: gs.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     role.Name,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: gs.Namespace,
		}}
		return controllerutil.SetControllerReference(gs, rb, r.Scheme)
	})
	return err
}

// buildSentinelPortConfig constructs the comma-separated PORTS_CONFIG env var
// from a GameTemplate's advertised ports. Format: "port:protocol:wakeProtocol,..."
// e.g. "25565:TCP:minecraft,19133:UDP:generic"
func buildSentinelPortConfig(tmpl *gameplanev1alpha1.GameTemplate) string {
	entries := make([]string, 0)
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		proto := p.Protocol
		if proto == "" {
			proto = corev1.ProtocolTCP
		}
		entries = append(entries, fmt.Sprintf("%d:%s:%s", p.ContainerPort, proto, p.WakeProtocol))
	}
	return strings.Join(entries, ",")
}
