package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// The restore and forget pods run restic only, so they carry no API token.
func TestResticPodsMountNoServiceAccountToken(t *testing.T) {
	b := &gameplanev1alpha1.Backup{Spec: gameplanev1alpha1.BackupSpec{RepoRef: &gameplanev1alpha1.SecretKeySelector{Name: "repo"}}}
	tmpl := &gameplanev1alpha1.GameTemplate{}
	forget := (&BackupReconciler{}).buildForgetPodSpec(b)
	restore := (&RestoreReconciler{}).buildRestorePodSpec(&gameplanev1alpha1.Restore{}, b, tmpl)
	for name, automount := range map[string]*bool{"forget": forget.AutomountServiceAccountToken, "restore": restore.AutomountServiceAccountToken} {
		if automount == nil || *automount {
			t.Errorf("%s pod automounts the ServiceAccount token", name)
		}
	}
}

// Only a playit tunnel needs the API (to publish its address); frp and
// Tailscale pods get no token, including after a switch away from playit.
func TestTunnelPodTokenOnlyForPlayit(t *testing.T) {
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{Tunnel: &gameplanev1alpha1.GameServerTunnel{Enabled: true, Provider: "playit", Playit: &gameplanev1alpha1.PlayitTunnelSpec{}}}}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	for _, provider := range []string{"playit", "frp"} {
		gs.Spec.Networking.Tunnel.Provider = provider
		if provider == "frp" {
			gs.Spec.Networking.Tunnel.Frp = &gameplanev1alpha1.FrpTunnelSpec{ServerAddr: "relay.example"}
		}
		if err := r.reconcileTunnel(t.Context(), gs, singlePortTemplate(true, "minecraft"), true); err != nil {
			t.Fatal(err)
		}
		var dep appsv1.Deployment
		if err := c.Get(t.Context(), client.ObjectKey{Namespace: "games", Name: "server-tunnel"}, &dep); err != nil {
			t.Fatal(err)
		}
		spec := dep.Spec.Template.Spec
		if provider == "playit" {
			if spec.ServiceAccountName != tunnelServiceAccountName(gs) || (spec.AutomountServiceAccountToken != nil && !*spec.AutomountServiceAccountToken) {
				t.Fatalf("playit tunnel lost its ServiceAccount token: sa=%q automount=%v", spec.ServiceAccountName, spec.AutomountServiceAccountToken)
			}
			continue
		}
		if spec.ServiceAccountName != "" || spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
			t.Fatalf("%s tunnel mounts a ServiceAccount token: sa=%q automount=%v", provider, spec.ServiceAccountName, spec.AutomountServiceAccountToken)
		}
	}
}
