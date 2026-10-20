//go:build envtest

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestTunnelReconciliation_CredentialKeyProjection tests that only the active
// provider's credential key is mounted via Items projection, preventing exposure
// of stale keys from a previous provider during a spec-direct provider switch.
func TestTunnelReconciliation_CredentialKeyProjection(t *testing.T) {
	t.Run("tailscale provider mounts only authKey when Secret contains both token and authKey", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		// Create a GameTemplate
		tmpl := &gameplanev1alpha1.GameTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("tunnel-test-tmpl")},
			Spec: gameplanev1alpha1.GameTemplateSpec{
				DisplayName: "Tunnel Test",
				Game:        "test",
				Version:     "1.0",
				Image:       "test:latest",
				Ports: []gameplanev1alpha1.GamePort{
					{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
				},
			},
		}
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		// Create a Secret with both frp "token" and tailscale "authKey"
		// (simulating a stale credential from a previous provider switch)
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tunnel-creds",
				Namespace: ns,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: gameplanev1alpha1.GroupVersion.String(),
						Kind:       "GameServer",
						Name:       "test-tunnel-creds",
						UID:        "test-uid",
						Controller: boolPtr(true),
					},
				},
			},
			Data: map[string][]byte{
				"token":   []byte("stale-frp-token"),
				"authKey": []byte("valid-tailscale-key"),
			},
		}
		if err := k8sClient.Create(ctx, sec); err != nil {
			t.Fatalf("create secret: %v", err)
		}

		// Create a GameServer with tailscale tunnel provider
		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-tunnel-creds", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Networking: gameplanev1alpha1.GameServerNetworking{
					Tunnel: &gameplanev1alpha1.GameServerTunnel{
						Enabled:  true,
						Provider: "tailscale",
						Tailscale: &gameplanev1alpha1.TailscaleTunnelSpec{
							Hostname: "my-server",
						},
						CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{
							Name: "tunnel-creds",
						},
					},
				},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}
		// The operator only mounts a Secret owned by this GameServer's UID,
		// which exists only after the GameServer is created.
		sec.OwnerReferences[0].UID = gs.UID
		if err := k8sClient.Update(ctx, sec); err != nil {
			t.Fatalf("set secret owner uid: %v", err)
		}

		// Reconcile the tunnel
		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileTunnel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileTunnel: %v", err)
		}

		// Verify the tunnel Deployment was created
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-tunnel-creds-tunnel", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get tunnel deployment: %v", err)
		}

		// Verify the Secret volume has Items projection with only "authKey"
		if len(dep.Spec.Template.Spec.Volumes) == 0 {
			t.Fatal("no volumes in tunnel pod template")
		}

		var authVol *corev1.Volume
		for i := range dep.Spec.Template.Spec.Volumes {
			if dep.Spec.Template.Spec.Volumes[i].Name == tunnelAuthVolume {
				authVol = &dep.Spec.Template.Spec.Volumes[i]
				break
			}
		}

		if authVol == nil {
			t.Fatalf("tunnel-auth volume not found in pod template")
		}

		if authVol.Secret == nil {
			t.Fatalf("tunnel-auth volume is not a Secret source")
		}

		// Check Items projection
		if len(authVol.Secret.Items) != 1 {
			t.Errorf("want 1 item in Secret projection, got %d", len(authVol.Secret.Items))
		} else if authVol.Secret.Items[0].Key != "authKey" {
			t.Errorf("want item key 'authKey', got %q", authVol.Secret.Items[0].Key)
		} else if authVol.Secret.Items[0].Path != "authKey" {
			t.Errorf("want item path 'authKey', got %q", authVol.Secret.Items[0].Path)
		}

		// Check Optional is true
		if authVol.Secret.Optional == nil || !*authVol.Secret.Optional {
			t.Errorf("want Optional=true, got %v", authVol.Secret.Optional)
		}
	})

	t.Run("frp provider mounts only token key when Secret contains both keys", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		// Create a GameTemplate
		tmpl := &gameplanev1alpha1.GameTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("tunnel-test-frp-tmpl")},
			Spec: gameplanev1alpha1.GameTemplateSpec{
				DisplayName: "Tunnel Test FRP",
				Game:        "test",
				Version:     "1.0",
				Image:       "test:latest",
				Ports: []gameplanev1alpha1.GamePort{
					{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
				},
			},
		}
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		// Create a Secret with both tailscale "authKey" and frp "token"
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tunnel-creds-frp",
				Namespace: ns,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: gameplanev1alpha1.GroupVersion.String(),
						Kind:       "GameServer",
						Name:       "test-tunnel-frp",
						UID:        "test-uid",
						Controller: boolPtr(true),
					},
				},
			},
			Data: map[string][]byte{
				"authKey": []byte("stale-tailscale-key"),
				"token":   []byte("valid-frp-token"),
			},
		}
		if err := k8sClient.Create(ctx, sec); err != nil {
			t.Fatalf("create secret: %v", err)
		}

		// Create a GameServer with frp tunnel provider
		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-tunnel-frp", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Networking: gameplanev1alpha1.GameServerNetworking{
					Tunnel: &gameplanev1alpha1.GameServerTunnel{
						Enabled:  true,
						Provider: "frp",
						Frp: &gameplanev1alpha1.FrpTunnelSpec{
							ServerAddr: "frp.example.com",
							ServerPort: 7000,
							RemotePorts: []gameplanev1alpha1.RemotePortMapping{
								{Name: "game", RemotePort: 30000},
							},
						},
						CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{
							Name: "tunnel-creds-frp",
						},
					},
				},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}
		// The operator only mounts a Secret owned by this GameServer's UID,
		// which exists only after the GameServer is created.
		sec.OwnerReferences[0].UID = gs.UID
		if err := k8sClient.Update(ctx, sec); err != nil {
			t.Fatalf("set secret owner uid: %v", err)
		}

		// Reconcile the tunnel
		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileTunnel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileTunnel: %v", err)
		}

		// Verify the tunnel Deployment was created
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-tunnel-frp-tunnel", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get tunnel deployment: %v", err)
		}

		// Verify the Secret volume has Items projection with only "token"
		var authVol *corev1.Volume
		for i := range dep.Spec.Template.Spec.Volumes {
			if dep.Spec.Template.Spec.Volumes[i].Name == tunnelAuthVolume {
				authVol = &dep.Spec.Template.Spec.Volumes[i]
				break
			}
		}

		if authVol == nil {
			t.Fatalf("tunnel-auth volume not found")
		}

		if authVol.Secret == nil {
			t.Fatalf("tunnel-auth volume is not a Secret source")
		}

		// Check Items projection contains only "token"
		if len(authVol.Secret.Items) != 1 {
			t.Errorf("want 1 item in Secret projection, got %d", len(authVol.Secret.Items))
		} else if authVol.Secret.Items[0].Key != "token" {
			t.Errorf("want item key 'token', got %q", authVol.Secret.Items[0].Key)
		} else if authVol.Secret.Items[0].Path != "token" {
			t.Errorf("want item path 'token', got %q", authVol.Secret.Items[0].Path)
		}

		// Check Optional is true
		if authVol.Secret.Optional == nil || !*authVol.Secret.Optional {
			t.Errorf("want Optional=true, got %v", authVol.Secret.Optional)
		}
	})

	t.Run("playit provider mounts only secretKey", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		// Create a GameTemplate
		tmpl := &gameplanev1alpha1.GameTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("tunnel-test-playit-tmpl")},
			Spec: gameplanev1alpha1.GameTemplateSpec{
				DisplayName: "Tunnel Test Playit",
				Game:        "test",
				Version:     "1.0",
				Image:       "test:latest",
				Ports: []gameplanev1alpha1.GamePort{
					{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
				},
			},
		}
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		// Create a Secret with playit "secretKey"
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tunnel-creds-playit",
				Namespace: ns,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: gameplanev1alpha1.GroupVersion.String(),
						Kind:       "GameServer",
						Name:       "test-tunnel-playit",
						UID:        "test-uid",
						Controller: boolPtr(true),
					},
				},
			},
			Data: map[string][]byte{
				"secretKey": []byte("valid-playit-secret"),
			},
		}
		if err := k8sClient.Create(ctx, sec); err != nil {
			t.Fatalf("create secret: %v", err)
		}

		// Create a GameServer with playit tunnel provider
		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-tunnel-playit", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Networking: gameplanev1alpha1.GameServerNetworking{
					Tunnel: &gameplanev1alpha1.GameServerTunnel{
						Enabled:  true,
						Provider: "playit",
						Playit: &gameplanev1alpha1.PlayitTunnelSpec{
							TunnelName: "my-tunnel",
						},
						CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{
							Name: "tunnel-creds-playit",
						},
					},
				},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}
		// The operator only mounts a Secret owned by this GameServer's UID,
		// which exists only after the GameServer is created.
		sec.OwnerReferences[0].UID = gs.UID
		if err := k8sClient.Update(ctx, sec); err != nil {
			t.Fatalf("set secret owner uid: %v", err)
		}

		// Reconcile the tunnel
		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileTunnel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileTunnel: %v", err)
		}

		// Verify the tunnel Deployment was created
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-tunnel-playit-tunnel", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get tunnel deployment: %v", err)
		}

		// Verify the Secret volume has Items projection with only "secretKey"
		var authVol *corev1.Volume
		for i := range dep.Spec.Template.Spec.Volumes {
			if dep.Spec.Template.Spec.Volumes[i].Name == tunnelAuthVolume {
				authVol = &dep.Spec.Template.Spec.Volumes[i]
				break
			}
		}

		if authVol == nil {
			t.Fatalf("tunnel-auth volume not found")
		}

		if authVol.Secret == nil {
			t.Fatalf("tunnel-auth volume is not a Secret source")
		}

		// Check Items projection contains only "secretKey"
		if len(authVol.Secret.Items) != 1 {
			t.Errorf("want 1 item in Secret projection, got %d", len(authVol.Secret.Items))
		} else if authVol.Secret.Items[0].Key != "secretKey" {
			t.Errorf("want item key 'secretKey', got %q", authVol.Secret.Items[0].Key)
		}

		// Check Optional is true
		if authVol.Secret.Optional == nil || !*authVol.Secret.Optional {
			t.Errorf("want Optional=true, got %v", authVol.Secret.Optional)
		}
	})
}

// boolPtr is a helper to create a pointer to a bool value for test setup.
func boolPtr(v bool) *bool {
	return &v
}
