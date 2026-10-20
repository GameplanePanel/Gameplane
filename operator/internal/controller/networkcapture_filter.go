package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// defaultCaptureFilter builds the pcap-filter expression a capture uses
// when its spec gives none: one "<proto> port <n>" term per advertised
// template port, in template order, duplicates dropped, joined with "or".
// A port with no protocol is TCP, the CRD default. It returns "" when the
// template advertises no port, so the caller can fail the capture instead
// of recording everything on the pod network.
func defaultCaptureFilter(tmpl *gameplanev1alpha1.GameTemplate) string {
	terms := make([]string, 0, len(tmpl.Spec.Ports))
	seen := map[string]bool{}
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise || p.ContainerPort <= 0 {
			continue
		}
		proto := "tcp"
		if p.Protocol == corev1.ProtocolUDP {
			proto = "udp"
		}
		term := fmt.Sprintf("%s port %d", proto, p.ContainerPort)
		if seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return strings.Join(terms, " or ")
}

// captureFilter returns the filter to send to the sidecar: the capture's
// own spec.filter when set, otherwise the default built from the
// GameServer's template ports. A non-empty message means the capture must
// fail because no default can be built.
func (r *NetworkCaptureReconciler) captureFilter(
	ctx context.Context, nc *gameplanev1alpha1.NetworkCapture, gs *gameplanev1alpha1.GameServer,
) (*string, string, error) {
	if nc.Spec.Filter != nil && *nc.Spec.Filter != "" {
		return nc.Spec.Filter, "", nil
	}
	name := gs.Spec.TemplateRef.Name
	var tmpl gameplanev1alpha1.GameTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Sprintf("gametemplate %s not found; cannot build the default capture filter", name), nil
		}
		return nil, "", fmt.Errorf("get gametemplate %s for the default capture filter: %w", name, err)
	}
	f := defaultCaptureFilter(&tmpl)
	if f == "" {
		return nil, fmt.Sprintf("gametemplate %s advertises no ports; supply a capture filter", name), nil
	}
	return &f, "", nil
}
