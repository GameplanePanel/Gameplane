//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_ModCredentialsVolume — when a ModProvider has a
// CredentialsSecretRef, the operator mounts the Secret read-only on the
// agent container at /etc/gameplane/mod-creds/<provider>/; volumes are
// present when the secret ref is set and absent when it is not.
func TestGameServer_ModCredentialsVolume(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	factorioTmpl := buildGameTemplate(uniqueName("factorio"))
	factorioTmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			Path: "mods",
			Install: &gameplanev1alpha1.ModInstallSpec{
				AllowedHosts: []string{"mods.factorio.com"},
			},
			Registry: &gameplanev1alpha1.ModRegistrySpec{
				Providers: []gameplanev1alpha1.ModProvider{
					{
						Provider: "factorio",
						CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{
							Name: "factorio-creds",
						},
					},
				},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), factorioTmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, factorioTmpl)

	gs := buildGameServer(ns, "factorio-server", factorioTmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Check that the mod-creds-factorio volume is present and mounted.
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			corev1.NamespacedName{Namespace: ns, Name: "factorio-server"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}

		// Check volume exists.
		volPresent := false
		for _, v := range ss.Spec.Template.Spec.Volumes {
			if v.Name == "mod-creds-factorio" {
				volPresent = true
				if v.Secret == nil {
					return false, "mod-creds-factorio volume is not a Secret"
				}
				if v.Secret.SecretName != "factorio-creds" {
					return false, "mod-creds-factorio secret name is " + v.Secret.SecretName
				}
				// Check items mapping.
				found := map[string]bool{}
				for _, item := range v.Secret.Items {
					if item.Key == "username" && item.Path == "username" {
						found["username"] = true
					}
					if item.Key == "token" && item.Path == "token" {
						found["token"] = true
					}
				}
				if !found["username"] || !found["token"] {
					return false, "mod-creds-factorio missing key mappings"
				}
				break
			}
		}
		if !volPresent {
			return false, "mod-creds-factorio volume not found"
		}

		// Check mount on agent container.
		agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
		if agent == nil {
			return false, "no agent container"
		}
		mountPath := mountPathOf(agent, "mod-creds-factorio")
		if mountPath != "/etc/gameplane/mod-creds/factorio" {
			return false, "agent mod-creds-factorio mount path is " + mountPath
		}
		return true, ""
	})
}

// TestGameServer_NoModCredentialsWhenNotSet — when a ModProvider has no
// CredentialsSecretRef, no credential volumes or mounts are created.
func TestGameServer_NoModCredentialsWhenNotSet(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	modrinthTmpl := buildGameTemplate(uniqueName("minecraft"))
	modrinthTmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			Path: "mods",
			Install: &gameplanev1alpha1.ModInstallSpec{
				AllowedHosts: []string{"cdn.modrinth.com"},
			},
			Registry: &gameplanev1alpha1.ModRegistrySpec{
				Providers: []gameplanev1alpha1.ModProvider{
					{
						Provider: "modrinth",
						// No CredentialsSecretRef.
					},
				},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), modrinthTmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, modrinthTmpl)

	gs := buildGameServer(ns, "minecraft-server", modrinthTmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Check that no mod credential volumes are present.
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			corev1.NamespacedName{Namespace: ns, Name: "minecraft-server"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}

		for _, v := range ss.Spec.Template.Spec.Volumes {
			if strings.HasPrefix(v.Name, "mod-creds-") {
				return false, "unexpected mod-creds volume: " + v.Name
			}
		}

		agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
		if agent != nil {
			for _, m := range agent.VolumeMounts {
				if strings.HasPrefix(m.Name, "mod-creds-") {
					return false, "unexpected mod-creds mount: " + m.Name
				}
			}
		}
		return true, ""
	})
}
