package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	tunnelLabel        = "app.kubernetes.io/name"
	tunnelValue        = "gameplane-tunnel"
	tunnelAuthVolume   = "tunnel-auth"
	tunnelAuthMountDir = "/etc/gameplane/tunnel-auth"
)

var tailscaleTagPattern = regexp.MustCompile(`^(tag:)?[A-Za-z][A-Za-z0-9-]*$`)

// validTailscaleTags mirrors the supervisor's normalization before it can
// register a node. Invalid configuration must be visible before Pod startup.
func validTailscaleTags(tags []string) bool {
	for _, tag := range strings.Split(strings.Join(tags, ","), ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" && !tailscaleTagPattern.MatchString(tag) {
			return false
		}
	}
	return true
}

// tunnelCredentialKey returns the Secret key name for a given tunnel provider.
// Must match the keys in tunnel/main.go allowedCredentialKeys and
// api/internal/handlers/tunnelcreds.go credentialKeysByProvider.
// Returns (key, true) for known providers, ("", false) for unknown/empty providers.
func tunnelCredentialKey(provider string) (string, bool) {
	switch provider {
	case "frp":
		return "token", true
	case "tailscale":
		return "authKey", true
	case "playit":
		return "secretKey", true
	default:
		return "", false
	}
}

// tunnelPlan is one reconcile pass's decision about the tunnel pod:
// whether it should exist, and what endpoints to advertise once it does.
type tunnelPlan struct {
	wantTunnel bool
	// endpoints are the computed per-provider addresses advertised via the tunnel.
	// Only set when wantTunnel is true and an endpoint is known.
	endpoints []gameplanev1alpha1.GameServerEndpoint
	// noMapping is the set of advertised port names that have no frp remote-port mapping.
	// Used only by frp to report a helpful condition reason.
	noMapping        []string
	unsupportedPorts []string
}

// planTunnel is the single source of truth for the tunnel pod's existence
// and the endpoints it advertises. Like planSentinel, this is deliberately
// separate from the reconcile steps so there is one place deciding "should
// the tunnel exist" instead of multiple CreateOrUpdate calls racing.
//
// CRITICAL: the tunnel Deployment is NEVER torn down when the server sleeps,
// unlike the sentinel. That is the entire point — it must keep holding the
// public address so a player's connection attempt to the tunnel can trigger
// wake-on-connect. The tunnel pod stays running across the full lifecycle.
func (r *GameServerReconciler) planTunnel(
	_ context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) tunnelPlan {
	if gs.Spec.Networking.Tunnel == nil || !gs.Spec.Networking.Tunnel.Enabled {
		return tunnelPlan{}
	}

	tunnel := gs.Spec.Networking.Tunnel
	endpoints := make([]gameplanev1alpha1.GameServerEndpoint, 0)
	var noMapping []string
	var unsupportedPorts []string

	switch tunnel.Provider {
	case "frp":
		// For frp, compute endpoints from the RemotePorts mapping.
		if tunnel.Frp == nil {
			return tunnelPlan{}
		}

		for _, p := range tmpl.Spec.Ports {
			if !p.Advertise {
				continue
			}

			// Find the remote port mapping for this port name.
			var remotePort int32
			found := false
			for _, rm := range tunnel.Frp.RemotePorts {
				if rm.Name == p.Name {
					remotePort = rm.RemotePort
					found = true
					break
				}
			}

			if !found {
				noMapping = append(noMapping, p.Name)
				continue
			}

			ep := gameplanev1alpha1.GameServerEndpoint{
				Name:           p.Name,
				Host:           tunnel.Frp.ServerAddr,
				Port:           remotePort,
				Protocol:       p.Protocol,
				TunnelProvider: "frp",
			}
			endpoints = append(endpoints, ep)
		}

	case "tailscale":
		// For Tailscale, use the specified hostname (or the GameServer name as default).
		if tunnel.Tailscale == nil {
			return tunnelPlan{}
		}

		hostname := tunnel.Tailscale.Hostname
		if hostname == "" {
			hostname = gs.Name
		}

		for _, p := range tmpl.Spec.Ports {
			if !p.Advertise {
				continue
			}
			if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP && p.Protocol != corev1.ProtocolUDP {
				unsupportedPorts = append(unsupportedPorts, p.Name)
				continue
			}

			ep := gameplanev1alpha1.GameServerEndpoint{
				Name:           p.Name,
				Host:           hostname,
				Port:           p.ContainerPort,
				Protocol:       p.Protocol,
				Private:        true, // Tailscale is always private (tailnet-only)
				TunnelProvider: "tailscale",
			}
			endpoints = append(endpoints, ep)
		}

	case "playit":
		// For Playit, the address is assigned at runtime by the tunnel pod and
		// arrives via a later change. No endpoint is computed here.
		if tunnel.Playit == nil {
			return tunnelPlan{}
		}
		// The tunnel pod polls playitd's IPC socket for the assigned address
		// and patches it into status.tunnelEndpoints (tunnel/playit_reporter.go);
		// reconcileStatus validates and merges those into status.endpoints.
	}

	return tunnelPlan{
		wantTunnel:       true,
		endpoints:        endpoints,
		noMapping:        noMapping,
		unsupportedPorts: unsupportedPorts,
	}
}

// reconcileTunnel maintains a 1-replica Deployment `<gs>-tunnel` that runs
// the tunnel relay process. Unlike the sentinel, this Deployment is NEVER
// torn down when the server sleeps — the tunnel must stay alive to hold the
// public address so a connection attempt can wake the server.
//
// want is planTunnel's decision for this pass: this function only builds the
// desired Deployment or deletes it, so there is a single place (planTunnel)
// that owns "should the tunnel exist right now".
func (r *GameServerReconciler) reconcileTunnel(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	want bool,
) error {
	if !want {
		if err := r.deleteTunnel(ctx, gs.Namespace, gs.Name); err != nil {
			return err
		}
		return r.clearTunnelCredentialRefused(ctx, gs)
	}

	tunnel := gs.Spec.Networking.Tunnel
	if tunnel == nil {
		return r.clearTunnelCredentialRefused(ctx, gs)
	}
	if tunnel.Provider == "tailscale" && tunnel.Tailscale != nil && !validTailscaleTags(tunnel.Tailscale.Tags) {
		// Stop a previous configuration rather than crash-looping a replacement
		// or registering without the requested access-control tags.
		if err := r.deleteTunnel(ctx, gs.Namespace, gs.Name); err != nil {
			return err
		}
		return r.clearTunnelCredentialRefused(ctx, gs)
	}

	// Create or update the tunnel Deployment.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name + "-tunnel",
			Namespace: gs.Namespace,
		},
	}

	// refusedSecret is set when the credentials Secret is refused. The
	// Deployment is still written (with no credential volume and zero
	// replicas) so an already-running tunnel stops using the refused Secret;
	// the refusal is returned after CreateOrUpdate.
	var refusedSecret string
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		refusedSecret = ""
		replicas := int32(1)
		dep.Spec.Replicas = &replicas
		// A provider identity owns one relay session. Recreate unregisters the
		// old child before its replacement can claim the same names/ports.
		if tunnel.Provider == "frp" || tunnel.Provider == "playit" || tunnel.Provider == "tailscale" {
			dep.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		}
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				tunnelLabel:                  tunnelValue,
				"app.kubernetes.io/instance": gs.Name,
			},
		}
		dep.Spec.Template.Labels = map[string]string{
			tunnelLabel:                  tunnelValue,
			"app.kubernetes.io/instance": gs.Name,
		}

		// Container ports: advertised ports only, mirroring buildGameContainer
		// and reconcileSentinel. These are the container ports the tunnel pod
		// listens on (the tunnel relay forwards these to the game Service).
		ports := make([]corev1.ContainerPort, 0)
		for _, p := range tmpl.Spec.Ports {
			if !p.Advertise {
				continue
			}
			cp := corev1.ContainerPort{
				Name:          p.Name,
				ContainerPort: p.ContainerPort,
				Protocol:      p.Protocol,
			}
			// The generated health port owns this discovery name. Forwarded
			// port metadata is optional; the provider still uses the unchanged
			// template/API name in its actual mapping.
			if cp.Name == "relay-metrics" {
				cp.Name = ""
			}
			ports = append(ports, cp)
		}

		// Security context: matching the sentinel's hardened context.
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

		// ServiceAccountName: only playit needs the grant to patch status with
		// the assigned address. Other providers (frp, tailscale) use static
		// addresses configured beforehand, so they run as default.
		if tunnel.Provider == "playit" {
			dep.Spec.Template.Spec.ServiceAccountName = tunnelServiceAccountName(gs)
		}

		// Tunnel image selection and environment per provider.
		var image string
		var envVars []corev1.EnvVar

		// Backing Service DNS: used by tunnel pod to reach the game pod.
		backingServiceDNS := fmt.Sprintf("%s.%s.svc", gs.Name, gs.Namespace)

		switch tunnel.Provider {
		case "frp":
			if tunnel.Frp == nil {
				return fmt.Errorf("frp provider selected but no frp config for %s/%s", gs.Namespace, gs.Name)
			}

			image = r.TunnelFrpImage
			if image == "" {
				image = DefaultTunnelFrpImage
			}

			// frp env: server address, server port, backing service, port mappings.
			serverPort := tunnel.Frp.ServerPort
			if serverPort == 0 {
				serverPort = 7000
			}

			envVars = []corev1.EnvVar{
				{Name: "GAMESERVER_NAME", Value: gs.Name},
				{Name: "GAMESERVER_NAMESPACE", Value: gs.Namespace},
				{Name: "GAMESERVER_UID", Value: string(gs.UID)},
				{Name: "TUNNEL_TYPE", Value: "frp"},
				{Name: "FRP_SERVER_ADDR", Value: tunnel.Frp.ServerAddr},
				{Name: "FRP_SERVER_PORT", Value: fmt.Sprintf("%d", serverPort)},
				{Name: "BACKING_SERVICE_DNS", Value: backingServiceDNS},
				{Name: "BACKING_SERVICE_PORT", Value: buildFrpRemotePortsConfig(tunnel.Frp, tmpl)},
			}

		case "tailscale":
			if tunnel.Tailscale == nil {
				return fmt.Errorf("tailscale provider selected but no tailscale config for %s/%s", gs.Namespace, gs.Name)
			}

			image = r.TunnelTailscaleImage
			if image == "" {
				image = DefaultTunnelTailscaleImage
			}

			hostname := tunnel.Tailscale.Hostname
			if hostname == "" {
				hostname = gs.Name
			}

			tagsStr := ""
			if len(tunnel.Tailscale.Tags) > 0 {
				for _, tag := range tunnel.Tailscale.Tags {
					if tagsStr != "" {
						tagsStr += ","
					}
					tagsStr += tag
				}
			}

			envVars = []corev1.EnvVar{
				{Name: "GAMESERVER_NAME", Value: gs.Name},
				{Name: "GAMESERVER_NAMESPACE", Value: gs.Namespace},
				{Name: "TUNNEL_TYPE", Value: "tailscale"},
				{Name: "TAILSCALE_HOSTNAME", Value: hostname},
				{Name: "TAILSCALE_TAGS", Value: tagsStr},
				{Name: "BACKING_SERVICE_DNS", Value: backingServiceDNS},
				{Name: "BACKING_SERVICE_PORTS", Value: buildTailscalePortsConfig(tmpl)},
			}

		case "playit":
			if tunnel.Playit == nil {
				return fmt.Errorf("playit provider selected but no playit config for %s/%s", gs.Namespace, gs.Name)
			}

			image = r.TunnelPlayitImage
			if image == "" {
				image = DefaultTunnelPlayitImage
			}

			tunnelName := tunnel.Playit.TunnelName
			if tunnelName == "" {
				tunnelName = gs.Name
			}

			envVars = []corev1.EnvVar{
				{Name: "GAMESERVER_NAME", Value: gs.Name},
				{Name: "GAMESERVER_NAMESPACE", Value: gs.Namespace},
				{Name: "TUNNEL_TYPE", Value: "playit"},
				{Name: "PLAYIT_TUNNEL_NAME", Value: tunnelName},
				{Name: "BACKING_SERVICE_DNS", Value: backingServiceDNS},
				{Name: "BACKING_SERVICE_PORTS", Value: buildPlayitPortsConfig(tmpl)},
			}
		}

		// Mount credentials Secret if provided.
		var volumes []corev1.Volume
		var volumeMounts []corev1.VolumeMount

		if tunnel.CredentialsSecretRef != nil && tunnel.CredentialsSecretRef.Name != "" {
			secName := tunnel.CredentialsSecretRef.Name
			credKey, credKeyKnown := tunnelCredentialKey(tunnel.Provider)

			// Only mount Secret if the provider is known (has a valid credential key).
			if credKeyKnown {
				var sec corev1.Secret
				err := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: secName}, &sec)
				switch {
				case err == nil && !isServerOwnedSecret(&sec, gs):
					// Refused: mount nothing and scale to zero, so a tunnel
					// that already runs with this Secret mounted stops.
					refusedSecret = secName
					zero := int32(0)
					dep.Spec.Replicas = &zero
				case err == nil:
					// Mount only the active provider's credential key to avoid exposing
					// stale keys from a previous provider (e.g., "token" still in the Secret
					// after switching from frp to tailscale). Items projection selects only
					// the active key; Optional:true allows the mount to succeed even if the
					// new key isn't yet in the Secret during a provider switch.
					optional := true
					volumes = append(volumes, corev1.Volume{
						Name: tunnelAuthVolume,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: secName,
								Items: []corev1.KeyToPath{
									{Key: credKey, Path: credKey},
								},
								Optional: &optional,
							},
						},
					})
					volumeMounts = append(volumeMounts, corev1.VolumeMount{
						Name:      tunnelAuthVolume,
						MountPath: tunnelAuthMountDir,
						ReadOnly:  true,
					})
				case !apierrors.IsNotFound(err):
					return fmt.Errorf("tunnel credentials secret %q: %w", secName, err)
				}
			}
		}

		var readiness *corev1.Probe
		if tunnel.Provider == "tailscale" {
			volumes = append(volumes, corev1.Volume{Name: "tailscale-state", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "tailscale-state", MountPath: "/var/lib/gameplane-tailscale"})
			readiness = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt(8091)}}, PeriodSeconds: 5, TimeoutSeconds: 2, FailureThreshold: 1}
		}
		dep.Spec.Template.Spec.Volumes = volumes
		ports = append(ports, corev1.ContainerPort{Name: "relay-metrics", ContainerPort: 8091, Protocol: corev1.ProtocolTCP})
		dep.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:           "tunnel",
			Image:          image,
			Resources:      auxiliaryResources(r.TunnelResources, "25m", "64Mi"),
			Env:            envVars,
			Ports:          ports,
			VolumeMounts:   volumeMounts,
			ReadinessProbe: readiness,
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             &nonRoot,
				RunAsUser:                &uid,
				AllowPrivilegeEscalation: &noPrivEsc,
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		}}
		if tunnel.Provider == "frp" || tunnel.Provider == "playit" {
			container := &dep.Spec.Template.Spec.Containers[0]
			container.StartupProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(8091)}}, PeriodSeconds: 2, TimeoutSeconds: 1, FailureThreshold: 30}
			container.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt32(8091)}}, PeriodSeconds: 2, TimeoutSeconds: 1, FailureThreshold: 1}
			if tunnel.Provider == "playit" {
				// The pinned Playit IPC has no live connection signal. Pod
				// readiness uses process health; TunnelReady remains Unknown.
				container.ReadinessProbe.HTTPGet.Path = "/healthz"
			}
		}

		return controllerutil.SetControllerReference(gs, dep, r.Scheme)
	})
	if refusedSecret == "" {
		if err != nil {
			return err
		}
		return r.clearTunnelCredentialRefused(ctx, gs)
	}
	refusal := fmt.Errorf("tunnel credentials secret %q is not owned by GameServer %s/%s", refusedSecret, gs.Namespace, gs.Name)
	if err != nil {
		return fmt.Errorf("%w (writing the credential-free tunnel deployment failed: %w)", refusal, err)
	}
	if condErr := r.setTunnelCredentialRefused(ctx, gs, refusedSecret); condErr != nil {
		return fmt.Errorf("%w (status update failed: %w)", refusal, condErr)
	}
	return refusal
}

// setTunnelCredentialRefused upserts a TunnelReady=False/TunnelCredentialRefused
// condition on gs so a credential Secret the operator refuses to mount (e.g. one
// with no ownerReference to this GameServer) is visible on status instead of only
// in the operator log. This must be written BEFORE reconcileTunnel returns its
// error: Reconcile (gameserver_controller.go) returns immediately on that error,
// so reconcileStatus's own TunnelReady computation (computeTunnelConditions,
// gameserver_status.go) never runs this pass — this is the only place the
// condition gets written for a refused pass.
//
// Once the credential becomes acceptable (or the tunnel is no longer wanted),
// reconcileTunnel removes this condition itself via clearTunnelCredentialRefused
// before returning success, so it cannot outlive the refusal when a later step
// (NetworkPolicy, Service, StatefulSet) fails before reconcileStatus runs;
// reconcileStatus's computeTunnelConditions then writes the real TunnelReady.
func (r *GameServerReconciler) setTunnelCredentialRefused(ctx context.Context, gs *gameplanev1alpha1.GameServer, secretName string) error {
	// The dashboard and the credentials API always write <server>-tunnel-auth
	// and answer 409 when a Secret of that name exists without this
	// GameServer's ownerReference, so for that name they cannot repair the
	// refusal until the Secret is fixed or deleted.
	remedy := "save the credential via the dashboard or the PUT /servers/{name}:tunnel-credentials API, or add an ownerReference to this GameServer to the Secret"
	if secretName == gs.Name+"-tunnel-auth" {
		remedy = "add an ownerReference to this GameServer to the Secret, or delete the Secret and save the credential again via the dashboard or the PUT /servers/{name}:tunnel-credentials API"
	}
	base := gs.DeepCopy()
	gs.Status.Conditions = upsertCondition(gs.Status.Conditions, metav1.Condition{
		Type:               "TunnelReady",
		Status:             metav1.ConditionFalse,
		Reason:             "TunnelCredentialRefused",
		Message:            fmt.Sprintf("tunnel credentials secret %q is not owned by this GameServer (no ownerReference matching its name and UID); %s", secretName, remedy),
		ObservedGeneration: gs.Generation,
	})
	remedyKind := "other"
	if secretName == gs.Name+"-tunnel-auth" {
		remedyKind = "defaultName"
	}
	setConditionArgs(&gs.Status, "TunnelReady", map[string]string{"secret": secretName, "remedy": remedyKind})
	if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch TunnelReady condition for %s/%s: %w", gs.Namespace, gs.Name, err)
	}
	return nil
}

// clearTunnelCredentialRefused removes a TunnelReady condition left by
// setTunnelCredentialRefused, and is a no-op otherwise. reconcileTunnel calls
// it on every successful pass so the refusal does not linger on status when a
// later reconcile step fails before reconcileStatus recomputes TunnelReady.
func (r *GameServerReconciler) clearTunnelCredentialRefused(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	c := meta.FindStatusCondition(gs.Status.Conditions, "TunnelReady")
	if c == nil || c.Reason != "TunnelCredentialRefused" {
		return nil
	}
	base := gs.DeepCopy()
	gs.Status.Conditions = removeCondition(gs.Status.Conditions, "TunnelReady")
	setConditionArgs(&gs.Status, "TunnelReady", nil)
	// A GameServer that no longer exists has no status left to clear.
	if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("clear TunnelCredentialRefused condition for %s/%s: %w", gs.Namespace, gs.Name, err)
	}
	return nil
}

// reconcileTunnelNetworkPolicy maintains a per-server NetworkPolicy admitting
// the tunnel pod's outbound traffic. The games namespace runs a default-deny-
// egress policy (allow-game-public-egress selects app.kubernetes.io/name=gameplane-game),
// which would silently drop the tunnel pod's connection to the relay without
// an explicit admit rule. Without this, the tunnel feature silently does nothing.
//
// The policy admits egress to any destination on the advertised ports and DNS.
func (r *GameServerReconciler) reconcileTunnelNetworkPolicy(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	plan tunnelPlan,
) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gs.Name + "-tunnel-egress",
			Namespace: gs.Namespace,
		},
	}

	if !plan.wantTunnel {
		// Converge-to-absent: only delete if we own it.
		var existing networkingv1.NetworkPolicy
		if err := r.Get(ctx, client.ObjectKeyFromObject(np), &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !metav1.IsControlledBy(&existing, gs) {
			return nil
		}
		return client.IgnoreNotFound(r.Delete(ctx, &existing))
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		// Select the tunnel pod's labels.
		np.Spec.PodSelector = metav1.LabelSelector{
			MatchLabels: map[string]string{
				tunnelLabel:                  tunnelValue,
				"app.kubernetes.io/instance": gs.Name,
			},
		}

		// Egress rules: allow DNS, advertised ports for inbound relay traffic,
		// and the provider's relay endpoint ports for outbound control traffic.
		egressPorts := make([]networkingv1.NetworkPolicyPort, 0)

		// DNS: UDP and TCP port 53 to any destination (required by all providers).
		tcpProto := corev1.ProtocolTCP
		udpProto := corev1.ProtocolUDP
		dnsPort := intstr.FromInt(53)
		egressPorts = append(egressPorts,
			networkingv1.NetworkPolicyPort{
				Protocol: &udpProto,
				Port:     &dnsPort,
			},
			networkingv1.NetworkPolicyPort{
				Protocol: &tcpProto,
				Port:     &dnsPort,
			},
		)

		// Provider relay egress: the ports the tunnel process dials outbound.
		tunnel := gs.Spec.Networking.Tunnel
		if tunnel != nil {
			switch tunnel.Provider {
			case "frp":
				// frp: outbound to the frp server on ServerAddr:ServerPort (default 7000).
				if tunnel.Frp != nil {
					serverPort := tunnel.Frp.ServerPort
					if serverPort == 0 {
						serverPort = 7000
					}
					port := intstr.FromInt32(serverPort)
					egressPorts = append(egressPorts, networkingv1.NetworkPolicyPort{
						Protocol: &tcpProto,
						Port:     &port,
					})
				}

			case "tailscale":
				// tailscale: outbound to control plane on TCP 443 and DERP relays on UDP 41641.
				httpsPort := intstr.FromInt(443)
				derpPort := intstr.FromInt(41641)
				egressPorts = append(egressPorts,
					networkingv1.NetworkPolicyPort{
						Protocol: &tcpProto,
						Port:     &httpsPort,
					},
					networkingv1.NetworkPolicyPort{
						Protocol: &udpProto,
						Port:     &derpPort,
					},
				)

			case "playit":
				// playit does not publish a fixed set of relay endpoints or ports:
				// the playit agent dials its control plane and relay nodes on
				// varying TCP and UDP ports that are not known ahead of time.
				// A per-port allow list (like frp's ServerPort or tailscale's
				// 443/41641) can't express that, so playit gets its own egress
				// rule below with no Ports field at all -- which, per the K8s
				// NetworkPolicy semantics, means all ports/protocols -- to any
				// destination, matching how the frp/tailscale rule above omits
				// "To" to allow any destination. The DNS and advertised-port
				// rule built here is left untouched for playit.
			}
		}

		// Advertised ports: the game's container ports (tunnel receives here, forwards inbound).
		for _, p := range tmpl.Spec.Ports {
			if !p.Advertise {
				continue
			}
			proto := p.Protocol
			if proto == "" {
				proto = corev1.ProtocolTCP
			}
			port := intstr.FromInt32(p.ContainerPort)
			egressPorts = append(egressPorts, networkingv1.NetworkPolicyPort{
				Protocol: &proto,
				Port:     &port,
			})
		}

		// Egress with no "To" means "to any destination".
		np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
		egressRules := []networkingv1.NetworkPolicyEgressRule{{
			Ports: egressPorts,
			// Empty To = allow to any destination
		}}
		if tunnel != nil && tunnel.Provider == "playit" {
			// playit's relay/control-plane ports aren't statically known, so
			// grant it a separate rule with no Ports (= all ports/protocols)
			// and no To (= any destination), on top of the DNS/advertised-port
			// rule above.
			egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{})
		}
		np.Spec.Egress = egressRules

		return controllerutil.SetControllerReference(gs, np, r.Scheme)
	})
	return err
}

// getTunnel fetches the tunnel Deployment, reporting exists=false when absent.
func (r *GameServerReconciler) getTunnel(ctx context.Context, namespace, gsName string) (*appsv1.Deployment, bool, error) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gsName + "-tunnel",
			Namespace: namespace,
		},
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(dep), dep)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get tunnel deployment: %w", err)
	}
	return dep, true, nil
}

// deleteTunnel removes the tunnel Deployment if it exists.
func (r *GameServerReconciler) deleteTunnel(ctx context.Context, namespace, gsName string) error {
	dep, exists, err := r.getTunnel(ctx, namespace, gsName)
	if err != nil || !exists {
		return err
	}
	policy := metav1.DeletePropagationBackground
	return client.IgnoreNotFound(r.Delete(ctx, dep, &client.DeleteOptions{PropagationPolicy: &policy}))
}

// buildFrpRemotePortsConfig constructs the BACKING_SERVICE_PORT env var for
// frp. Format: "port_name:local_port:remote_port:protocol,..." e.g.
// "game:34197:30000:udp". local_port and protocol come from the matching
// GameTemplate port (the backing Service's own port and protocol);
// remote_port is the public frps-side port the user picked in
// spec.networking.tunnel.frp.remotePorts. The two ports are independent, so
// this always carries both rather than assuming remotePort also names the
// Service port and the port is always TCP -- the old format let frp work
// only when a user's remotePort happened to equal the Service port and the
// game used TCP (F-052). A RemotePorts mapping whose Name has no matching
// advertised template port is skipped, same as before.
func buildFrpRemotePortsConfig(frp *gameplanev1alpha1.FrpTunnelSpec, tmpl *gameplanev1alpha1.GameTemplate) string {
	if frp == nil || tmpl == nil {
		return ""
	}
	var entries []string
	for _, mapping := range frp.RemotePorts {
		for _, p := range tmpl.Spec.Ports {
			if p.Name != mapping.Name {
				continue
			}
			protocol := strings.ToLower(string(p.Protocol))
			if protocol == "" {
				protocol = "tcp"
			}
			entries = append(entries, fmt.Sprintf("%s:%d:%d:%s", mapping.Name, p.ContainerPort, mapping.RemotePort, protocol))
			break
		}
	}
	if len(entries) == 0 {
		return ""
	}
	var result string
	for i, e := range entries {
		if i > 0 {
			result += ","
		}
		result += e
	}
	return result
}

// buildTailscalePortsConfig constructs the BACKING_SERVICE_PORTS env var for Tailscale.
// Format: "port_name:port:protocol,..." e.g. "java:25565:tcp,bedrock:19133:udp".
func buildTailscalePortsConfig(tmpl *gameplanev1alpha1.GameTemplate) string {
	var entries []string
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		protocol := strings.ToLower(string(p.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		entries = append(entries, fmt.Sprintf("%s:%d:%s", p.Name, p.ContainerPort, protocol))
	}
	if len(entries) == 0 {
		return ""
	}
	var result string
	for i, e := range entries {
		if i > 0 {
			result += ","
		}
		result += e
	}
	return result
}

// buildPlayitPortsConfig constructs the BACKING_SERVICE_PORTS env var for Playit.
// Format: "port_name:port,..." e.g. "java:25565,bedrock:19133"
func buildPlayitPortsConfig(tmpl *gameplanev1alpha1.GameTemplate) string {
	var entries []string
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		entries = append(entries, fmt.Sprintf("%s:%d", p.Name, p.ContainerPort))
	}
	if len(entries) == 0 {
		return ""
	}
	var result string
	for i, e := range entries {
		if i > 0 {
			result += ","
		}
		result += e
	}
	return result
}

// Tunnel image defaults.
const (
	DefaultTunnelFrpImage       = "ghcr.io/gameplanepanel/gameplane/tunnel-frp:dev"
	DefaultTunnelTailscaleImage = "ghcr.io/gameplanepanel/gameplane/tunnel-tailscale:dev"
	DefaultTunnelPlayitImage    = "ghcr.io/gameplanepanel/gameplane/tunnel-playit:dev"
)
