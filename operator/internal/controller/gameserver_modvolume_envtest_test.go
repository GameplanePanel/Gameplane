//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// buildVersionedTemplate is a minecraft-like template with a version
// catalog (distinct image per loader) and a per-(version+loader) mod
// volume map (paper→plugins, forge→mods).
func buildVersionedTemplate(name string) *gameplanev1alpha1.GameTemplate {
	t := buildGameTemplate(name)
	t.Spec.Image = "itzg/minecraft-server:fallback"
	t.Spec.Versions = []gameplanev1alpha1.GameVersion{
		{
			ID: "1.21.4-paper", DisplayName: "1.21.4 Paper",
			Image: "itzg/minecraft-server:paper", Loader: "paper", Default: true,
			Env: []corev1.EnvVar{{Name: "TYPE", Value: "PAPER"}, {Name: "VERSION", Value: "1.21.4"}},
		},
		{
			ID: "1.21.4-forge", DisplayName: "1.21.4 Forge",
			Image: "itzg/minecraft-server:forge", Loader: "forge",
			Env: []corev1.EnvVar{{Name: "TYPE", Value: "FORGE"}, {Name: "VERSION", Value: "1.21.4"}},
		},
	}
	t.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			Loaders: map[string]gameplanev1alpha1.ModLoaderSpec{
				"paper": {Path: "plugins", Extensions: []string{".jar"}},
				"forge": {Path: "mods", Extensions: []string{".jar"}},
			},
			Install: &gameplanev1alpha1.ModInstallSpec{AllowedHosts: []string{"cdn.modrinth.com"}},
		},
	}
	return t
}

func containerByName(cs []corev1.Container, name string) *corev1.Container {
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

func mountPathOf(c *corev1.Container, volName string) string {
	if c == nil {
		return ""
	}
	for _, m := range c.VolumeMounts {
		if m.Name == volName {
			return m.MountPath
		}
	}
	return ""
}

// TestGameServer_VersionResolvesImageEnvAndModVolume — selecting a
// version pins that entry's image + env, provisions the per-(version+
// loader) mod PVC, and mounts it (nested) on both the game and agent
// containers; the agent's GAMEPLANE_CAPABILITIES carries the resolved path.
func TestGameServer_VersionResolvesImageEnvAndModVolume(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildVersionedTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		game := containerByName(ss.Spec.Template.Spec.Containers, gameContainerName)
		if game == nil {
			return false, "no game container"
		}
		if game.Image != "itzg/minecraft-server:forge" {
			return false, "image=" + game.Image
		}
		env := map[string]string{}
		for _, e := range game.Env {
			env[e.Name] = e.Value
		}
		if env["TYPE"] != "FORGE" || env["VERSION"] != "1.21.4" {
			return false, "version env not applied"
		}
		// Forge mods mount nested at /data/mods on game + agent.
		volName := "mods-1-21-4-forge"
		if got := mountPathOf(game, volName); got != "/data/mods" {
			return false, "game mod mount=" + got
		}
		agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
		if got := mountPathOf(agent, volName); got != "/data/mods" {
			return false, "agent mod mount=" + got
		}
		// Pod volume references the per-combo PVC.
		var hasVol bool
		for _, v := range ss.Spec.Template.Spec.Volumes {
			if v.Name == volName && v.PersistentVolumeClaim != nil &&
				v.PersistentVolumeClaim.ClaimName == "smp-mods-1-21-4-forge" {
				hasVol = true
			}
		}
		if !hasVol {
			return false, "pod volume for mod PVC missing"
		}
		// Agent capabilities collapsed to the forge path.
		var caps string
		for _, e := range agent.Env {
			if e.Name == "GAMEPLANE_CAPABILITIES" {
				caps = e.Value
			}
		}
		if !strings.Contains(caps, `"path":"mods"`) {
			return false, "GAMEPLANE_CAPABILITIES path not collapsed: " + caps
		}
		return true, ""
	})

	// The per-combo PVC exists.
	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-mods-1-21-4-forge"}, &pvc); err != nil {
			return false, "mod pvc: " + err.Error()
		}
		return true, ""
	})
}

// TestGameServer_SwitchVersionRetainsModPVC — switching version mounts the
// new combo's PVC while leaving the previous combo's PVC intact, so each
// version+loader keeps its own mod set.
func TestGameServer_SwitchVersionRetainsModPVC(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildVersionedTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Version = "1.21.4-paper"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-mods-1-21-4-paper"}, &pvc); err != nil {
			return false, "paper pvc: " + err.Error()
		}
		return true, ""
	})

	// Switch to forge.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &cur); err != nil {
			return err
		}
		cur.Spec.Version = "1.21.4-forge"
		return k8sClient.Update(context.Background(), &cur)
	}); err != nil {
		t.Fatalf("switch version: %v", err)
	}

	// New forge PVC appears, paper PVC is retained, game mount moves to /data/mods.
	eventually(t, func() (bool, string) {
		var forge corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-mods-1-21-4-forge"}, &forge); err != nil {
			return false, "forge pvc not created: " + err.Error()
		}
		var paper corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-mods-1-21-4-paper"}, &paper); err != nil {
			return false, "paper pvc was not retained: " + err.Error()
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		game := containerByName(ss.Spec.Template.Spec.Containers, gameContainerName)
		if got := mountPathOf(game, "mods-1-21-4-forge"); got != "/data/mods" {
			return false, "game not remounted to forge volume: " + got
		}
		return true, ""
	})
}

// TestGameServer_UnknownVersionFailsPhase — a spec.version that names no
// catalog entry fails the GameServer loudly instead of silently falling
// back to the template image.
func TestGameServer_UnknownVersionFailsPhase(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildVersionedTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Version = "9.9-bogus"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &cur); err != nil {
			return false, err.Error()
		}
		if cur.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase=" + string(cur.Status.Phase)
		}
		return true, ""
	})

	// No StatefulSet should have been created for the failed server.
	var ss appsv1.StatefulSet
	err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "smp"}, &ss)
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("expected no StatefulSet for failed version, got err=%v", err)
	}
}
