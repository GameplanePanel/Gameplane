package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// sidecarTCPPorts are the TCP ports the operator's own containers listen on
// inside the game pod's network namespace. A template that advertises one of
// them would publish the sidecar (for 9090, the agent's plain-HTTP metrics
// listener) on the game Service, Hostport or tunnel instead of the game.
var sidecarTCPPorts = map[int32]string{
	8090: "agent control (mTLS)",
	9090: "agent metrics",
	9091: "capture sidecar",
}

// validateSidecarPorts refuses a template that advertises a TCP port the
// agent or capture sidecar listens on. It returns a workloadPolicyRejection,
// so a suspended server can still drain and a rollout is refused before it
// starts.
func validateSidecarPorts(tmpl *gameplanev1alpha1.GameTemplate) error {
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise || (p.Protocol != "" && p.Protocol != corev1.ProtocolTCP) {
			continue
		}
		if use, ok := sidecarTCPPorts[p.ContainerPort]; ok {
			return workloadPolicyRejection{fmt.Errorf(
				"template port %s (TCP %d) is the %s port inside every game pod and cannot be advertised; move the game to another port",
				p.Name, p.ContainerPort, use)}
		}
	}
	return nil
}

// validateServiceAccountOverride refuses a spec.serviceAccountName that names
// a ServiceAccount another object controls (another server's agent, tunnel or
// sentinel account, a chart component's account) or one Helm manages, so a
// GameServer cannot borrow a Gameplane component's API grants. The API already
// limits the field to administrators; this is the operator's own check. A
// missing ServiceAccount is left to the StatefulSet controller as before.
func (r *GameServerReconciler) validateServiceAccountOverride(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	name := gs.Spec.ServiceAccountName
	if name == "" || name == agentServiceAccountName(gs) {
		return nil
	}
	// This server's own tunnel and sentinel accounts carry their component's
	// grants, not the game's.
	if name == tunnelServiceAccountName(gs) || name == sentinelSAName(gs) {
		return workloadPolicyRejection{fmt.Errorf(
			"spec.serviceAccountName %q belongs to this server's tunnel or sentinel; choose a ServiceAccount created for the game", name)}
	}
	var sa corev1.ServiceAccount
	if err := r.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: name}, &sa); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get ServiceAccount %s/%s: %w", gs.Namespace, name, err)
	}
	if owner := metav1.GetControllerOf(&sa); owner != nil && owner.UID != gs.UID {
		return workloadPolicyRejection{fmt.Errorf(
			"spec.serviceAccountName %q is controlled by %s %q; choose a ServiceAccount created for this server",
			name, owner.Kind, owner.Name)}
	}
	if sa.Labels["app.kubernetes.io/managed-by"] == "Helm" {
		return workloadPolicyRejection{fmt.Errorf(
			"spec.serviceAccountName %q is managed by Helm; choose a ServiceAccount created for this server", name)}
	}
	return nil
}

// validateImageOverride refuses a spec.image outside AllowedImageRegistries.
// An empty list (the default) leaves the override unrestricted. Template and
// version images are module-controlled and not checked here.
func (r *GameServerReconciler) validateImageOverride(gs *gameplanev1alpha1.GameServer) error {
	if gs.Spec.Image == "" || len(r.AllowedImageRegistries) == 0 {
		return nil
	}
	if imageAllowed(gs.Spec.Image, r.AllowedImageRegistries) {
		return nil
	}
	return workloadPolicyRejection{fmt.Errorf(
		"spec.image %q is outside the operator's allowed image registries (%s)",
		gs.Spec.Image, strings.Join(r.AllowedImageRegistries, ", "))}
}

// imageAllowed reports whether image sits under one of the prefixes, on a
// path boundary. A prefix is a registry host ("ghcr.io") or a host and path
// ("ghcr.io/gameplanepanel"). Docker Hub short names are expanded first
// ("itzg/x" is "docker.io/itzg/x", "nginx" is "docker.io/library/nginx"), so a
// prefix of "docker.io/itzg" matches both spellings.
func imageAllowed(image string, prefixes []string) bool {
	ref := normalizeImageRef(image)
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(prefix)), "/")
		if prefix == "" {
			continue
		}
		// A prefix names a registry or a path on one, never an image, so
		// Docker Hub's implicit library/ is not added to it.
		host, rest, hasPath := strings.Cut(prefix, "/")
		if host == "index.docker.io" {
			host = "docker.io"
		}
		switch {
		case !hasPath:
			prefix = host
		case strings.ContainsAny(host, ".:") || host == "localhost":
			prefix = host + "/" + rest
		default:
			prefix = "docker.io/" + prefix
		}
		if ref == prefix || strings.HasPrefix(ref, prefix+"/") ||
			strings.HasPrefix(ref, prefix+":") || strings.HasPrefix(ref, prefix+"@") {
			return true
		}
	}
	return false
}

// normalizeImageRef lowercases ref and spells out its registry host.
func normalizeImageRef(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	first, rest, found := strings.Cut(ref, "/")
	switch {
	case ref == "":
		return ""
	case !found:
		return "docker.io/library/" + ref
	case first == "docker.io" || first == "index.docker.io":
		if !strings.Contains(rest, "/") {
			return "docker.io/library/" + rest
		}
		return "docker.io/" + rest
	case strings.ContainsAny(first, ".:") || first == "localhost":
		return ref
	default:
		return "docker.io/" + ref
	}
}
