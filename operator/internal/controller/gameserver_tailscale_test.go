package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestTailscaleAdvertisedProtocolsAndReadiness(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{Ports: []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Advertise: true},
		{Name: "query", ContainerPort: 19132, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP},
	}}}
	if got := buildTailscalePortsConfig(tmpl); got != "game:25565:tcp,query:19132:udp" {
		t.Fatalf("advertised mapping: %s", got)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "tailscale", Tailscale: &gameplanev1alpha1.TailscaleTunnelSpec{}, CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "auth"}}}}}
	r := &GameServerReconciler{}
	plan := r.planTunnel(context.Background(), gs, tmpl)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1, ObservedGeneration: 2}}
	assert := func(want metav1.ConditionStatus) {
		t.Helper()
		c := meta.FindStatusCondition(computeTunnelConditions(gs, plan, dep), "TunnelReady")
		if c == nil || c.Status != want {
			t.Fatalf("TunnelReady=%+v, want %s", c, want)
		}
	}
	assert(metav1.ConditionFalse) // A registered legacy deployment has no forwarding probe.
	dep.Spec.Template.Spec.Containers = []corev1.Container{{Name: "tunnel", ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt(8091)}}}}}
	assert(metav1.ConditionTrue)
	dep.Status.ObservedGeneration = 1
	assert(metav1.ConditionFalse)
	dep.Status.ObservedGeneration = 2
	dep.Status.ReadyReplicas = 0
	assert(metav1.ConditionFalse)
	dep.Status.ReadyReplicas = 1
	tmpl.Spec.Ports[0].Protocol = corev1.ProtocolSCTP
	plan = r.planTunnel(context.Background(), gs, tmpl)
	assert(metav1.ConditionFalse)
}
