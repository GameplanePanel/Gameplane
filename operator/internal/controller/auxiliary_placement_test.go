package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestAuxiliaryWorkerPlacement(t *testing.T) {
	for _, raw := range []string{"bad key=value", "pool=", "pool", "pool=a,b=c"} {
		if _, err := ParseAuxiliaryWorkerPool(raw); err == nil {
			t.Errorf("accepted invalid pool %q", raw)
		}
	}
	r, gs, tmpl := revisionFixture(t)
	ctx := t.Context()
	gs.Spec.NodeSelector = map[string]string{"rack": "west"}
	r.GameWorkloadPolicy, _, _ = gamePolicyFixture(t)
	for _, step := range []struct{ setting, want string }{
		{"gameplane.local/pool=relays", "relays"},
		{"", "games"},
		{"", ""},
	} {
		var err error
		r.AuxiliaryWorkerPool, err = ParseAuxiliaryWorkerPool(step.setting)
		if err != nil {
			t.Fatal(err)
		}
		if step.want == "" {
			r.GameWorkloadPolicy = nil
		}
		check := func(suffix string) {
			t.Helper()
			var dep appsv1.Deployment
			key := client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + suffix}
			if err := r.Get(ctx, key, &dep); err != nil {
				t.Fatal(err)
			}
			pod := dep.Spec.Template.Spec
			if step.want == "" {
				if len(pod.Tolerations) != 0 || len(pod.NodeSelector) != 0 {
					t.Fatalf("stale placement on %s: %v %v", key, pod.NodeSelector, pod.Tolerations)
				}
				return
			}
			want := corev1.Toleration{Key: "gameplane.local/pool", Operator: corev1.TolerationOpEqual, Value: step.want, Effect: corev1.TaintEffectNoSchedule}
			if len(pod.NodeSelector) != 1 || pod.NodeSelector[want.Key] != step.want || len(pod.Tolerations) != 1 || pod.Tolerations[0] != want {
				t.Fatalf("placement on %s: %v %v", key, pod.NodeSelector, pod.Tolerations)
			}
		}
		if err := r.reconcileSentinel(ctx, gs, tmpl, true); err != nil {
			t.Fatal(err)
		}
		check("-waker")
		for _, provider := range []string{"frp", "tailscale", "playit"} {
			gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
				Enabled: true, Provider: provider, Frp: &gameplanev1alpha1.FrpTunnelSpec{},
				Tailscale: &gameplanev1alpha1.TailscaleTunnelSpec{}, Playit: &gameplanev1alpha1.PlayitTunnelSpec{},
			}
			if err := r.reconcileTunnel(ctx, gs, tmpl, true); err != nil {
				t.Fatal(err)
			}
			check("-tunnel")
		}
	}
}
