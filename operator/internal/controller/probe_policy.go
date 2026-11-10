package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// probePolicyName is the name of the per-GameServer NetworkPolicy admitting
// kubelet probe traffic (see reconcileProbePolicy).
func probePolicyName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-probes"
}

// reconcileProbePolicy maintains `<gs>-probes`, admitting kubelet probe
// traffic from ProbeIngressFromCIDRs to exactly the ports the game
// container's network probes (tcpSocket, httpGet, grpc) use. It replaces the
// chart's allow-kubelet-probes rule, which admitted those CIDRs, and with
// them every pod in the cluster, to ANY port of every game pod: RCON and
// query ports included.
//
// Like reconcileNetworkPolicy, a rule with no ports would admit all ports,
// so when the feature is off or the server has no network probe (exec
// probes need no network) the policy is deleted instead.
func (r *GameServerReconciler) reconcileProbePolicy(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: probePolicyName(gs), Namespace: gs.Namespace},
	}
	ports := probeNetworkPolicyPorts(gs, tmpl)
	if !r.ProbeIngressPolicyEnabled || len(ports) == 0 || len(r.ProbeIngressFromCIDRs) == 0 {
		// Get from the cache first so heartbeat-driven reconciles don't send a
		// DELETE every time, and never remove a policy we don't own.
		var existing networkingv1.NetworkPolicy
		if err := r.Get(ctx, client.ObjectKeyFromObject(np), &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !metav1.IsControlledBy(&existing, gs) {
			return nil
		}
		return client.IgnoreNotFound(r.Delete(ctx, &existing))
	}

	from := make([]networkingv1.NetworkPolicyPeer, 0, len(r.ProbeIngressFromCIDRs))
	for _, cidr := range r.ProbeIngressFromCIDRs {
		from = append(from, networkingv1.NetworkPolicyPeer{IPBlock: &networkingv1.IPBlock{CIDR: cidr}})
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Spec.PodSelector = metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name":     "gameplane-game",
				"app.kubernetes.io/instance": gs.Name,
			},
		}
		np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
		np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{From: from, Ports: ports}}
		return controllerutil.SetControllerReference(gs, np, r.Scheme)
	})
	return err
}

// effectiveGameProbes returns the game container's readiness, liveness and
// startup probes: the template's, with the server's per-probe overrides
// winning, exactly as the StatefulSet builder applies them.
func effectiveGameProbes(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) []*corev1.Probe {
	var readiness, liveness, startup *corev1.Probe
	if p := tmpl.Spec.Probes; p != nil {
		readiness, liveness, startup = p.Readiness, p.Liveness, p.Startup
	}
	if p := gs.Spec.Probes; p != nil {
		if p.Readiness != nil {
			readiness = p.Readiness
		}
		if p.Liveness != nil {
			liveness = p.Liveness
		}
		if p.Startup != nil {
			startup = p.Startup
		}
	}
	return []*corev1.Probe{readiness, liveness, startup}
}

// probeNetworkPolicyPorts lists the distinct TCP ports the game container's
// network probes dial. A named port is kept as a name: NetworkPolicy
// resolves it against the pod's container ports, like the probe does. A
// probe aimed at another host is skipped, since it doesn't reach the pod.
func probeNetworkPolicyPorts(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) []networkingv1.NetworkPolicyPort {
	seen := map[string]bool{}
	var out []networkingv1.NetworkPolicyPort
	for _, probe := range effectiveGameProbes(gs, tmpl) {
		if probe == nil {
			continue
		}
		var port intstr.IntOrString
		switch {
		case probe.TCPSocket != nil && probe.TCPSocket.Host == "":
			port = probe.TCPSocket.Port
		case probe.HTTPGet != nil && probe.HTTPGet.Host == "":
			port = probe.HTTPGet.Port
		case probe.GRPC != nil:
			port = intstr.FromInt32(probe.GRPC.Port)
		default:
			continue
		}
		if (port.Type == intstr.Int && port.IntVal <= 0) || (port.Type == intstr.String && port.StrVal == "") {
			continue
		}
		if seen[port.String()] {
			continue
		}
		seen[port.String()] = true
		proto := corev1.ProtocolTCP
		p := port
		out = append(out, networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &p})
	}
	return out
}
