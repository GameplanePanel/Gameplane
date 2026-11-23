//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// buildModpackTemplate is the versioned minecraft-like template plus the three
// registry providers spec 035 declares in the minecraft-java module.
func buildModpackTemplate(name string) *gameplanev1alpha1.GameTemplate {
	t := buildVersionedTemplate(name)
	gameID := int32(432)
	t.Spec.Capabilities.Mods.Registry = &gameplanev1alpha1.ModRegistrySpec{
		Providers: []gameplanev1alpha1.ModProvider{
			{
				Provider: "modrinth",
				Modpacks: &gameplanev1alpha1.ModpackSpec{
					RefEnv:     "MODRINTH_MODPACK",
					VersionEnv: "MODRINTH_VERSION",
					Env:        []corev1.EnvVar{{Name: "TYPE", Value: "MODRINTH"}},
				},
			},
			{
				Provider:         "curseforge",
				CurseForgeGameID: &gameID,
				Modpacks: &gameplanev1alpha1.ModpackSpec{
					RefEnv:     "CF_SLUG",
					VersionEnv: "CF_FILE_ID",
					APIKeyEnv:  "CF_API_KEY",
					Env: []corev1.EnvVar{
						{Name: "TYPE", Value: "AUTO_CURSEFORGE"},
						{Name: "CF_DOWNLOADS_REPO", Value: "/data/downloads"},
					},
				},
			},
			{
				Provider:     "ftb",
				ModpacksOnly: true,
				Modpacks: &gameplanev1alpha1.ModpackSpec{
					RefEnv:     "FTB_MODPACK_ID",
					VersionEnv: "FTB_MODPACK_VERSION_ID",
					Env:        []corev1.EnvVar{{Name: "TYPE", Value: "FTBA"}},
				},
			},
		},
	}
	return t
}

// gameEnvOf returns the game container's env from the server's StatefulSet.
func gameEnvOf(ns, name string) ([]corev1.EnvVar, string) {
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		return nil, "statefulset: " + err.Error()
	}
	game := containerByName(ss.Spec.Template.Spec.Containers, gameContainerName)
	if game == nil {
		return nil, "no game container"
	}
	return game.Env, ""
}

// markStatefulSetRolledOut fakes the StatefulSet controller (envtest has none):
// it reports the latest template generation as observed and every pod updated.
func markStatefulSetRolledOut(ns, name string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
			return err
		}
		ss.Status.ObservedGeneration = ss.Generation
		ss.Status.UpdateRevision = fmt.Sprintf("%s-rev-%d", name, ss.Generation)
		ss.Status.Replicas, ss.Status.UpdatedReplicas = 1, 1
		return k8sClient.Status().Update(context.Background(), &ss)
	})
}

// modpackReadyIs polls until ModpackReady has the wanted status, faking a
// finished rollout on every poll (a reconcile may bump the StatefulSet
// generation between polls).
func modpackReadyIs(t *testing.T, ns, name string, want string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		if err := markStatefulSetRolledOut(ns, name); err != nil {
			return false, "mark rolled out: " + err.Error()
		}
		gs := getGameServer(t, ns, name)
		c := meta.FindStatusCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionModpackReady)
		if c == nil {
			return false, "no ModpackReady condition"
		}
		if string(c.Status) != want {
			return false, fmt.Sprintf("ModpackReady=%s reason=%s", c.Status, c.Reason)
		}
		return true, ""
	})
}

func setModpack(t *testing.T, ns, name string, mp *gameplanev1alpha1.GameServerModpackSpec) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		gs.Spec.Modpack = mp
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("set spec.modpack: %v", err)
	}
}

func modpackSpecOf(provider, project, version string) *gameplanev1alpha1.GameServerModpackSpec {
	return &gameplanev1alpha1.GameServerModpackSpec{
		ModpackRef: gameplanev1alpha1.ModpackRef{Provider: provider, Project: project, Version: version},
	}
}

// TestGameServer_ModpackEnvRenderingAndReady covers spec 035 rule 1 and the
// backup-less state machine: stale pack env in spec.env is stripped, each
// provider's fixed env + refEnv + versionEnv is rendered, switching providers
// leaves no trace of the previous one, removal returns the version's loader
// TYPE, and ModpackReady reaches True once the StatefulSet has rolled.
func TestGameServer_ModpackEnvRenderingAndReady(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "pack", tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Env = []corev1.EnvVar{
		{Name: "FOO", Value: "bar"},
		// Stale pack env a pre-035 API write (or a user) left in spec.env.
		{Name: "MODRINTH_MODPACK", Value: "stale-slug"},
		{Name: "FTB_MODPACK_ID", Value: "9"},
		{Name: "TYPE", Value: "MODRINTH"},
	}
	gs.Spec.Modpack = modpackSpecOf("curseforge", "all-the-mods-10", "6512345")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// CurseForge applied: its set is rendered, every other provider's names are gone.
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		for name, want := range map[string]string{
			"CF_SLUG": "all-the-mods-10", "CF_FILE_ID": "6512345",
			"TYPE": "AUTO_CURSEFORGE", "CF_DOWNLOADS_REPO": "/data/downloads", "FOO": "bar",
		} {
			if got, ok := envValue(env, name); !ok || got != want {
				return false, fmt.Sprintf("%s=%q (present=%v), want %q", name, got, ok, want)
			}
		}
		for _, gone := range []string{"MODRINTH_MODPACK", "FTB_MODPACK_ID", "MODRINTH_VERSION"} {
			if envCount(env, gone) != 0 {
				return false, gone + " still rendered"
			}
		}
		// spec.env's stale TYPE=MODRINTH is stripped; the version's TYPE=FORGE stays
		// before the pack's, so TYPE appears exactly twice with the pack's last.
		if envCount(env, "TYPE") != 2 {
			return false, fmt.Sprintf("TYPE appears %d times, want 2", envCount(env, "TYPE"))
		}
		return true, ""
	})
	modpackReadyIs(t, ns, "pack", "True")
	if st := getGameServer(t, ns, "pack").Status.Modpack; st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseApplied ||
		st.Applied == nil || st.Applied.Project != "all-the-mods-10" {
		t.Fatalf("status.modpack = %+v, want Applied all-the-mods-10", st)
	}

	// Switch to FTB: no CurseForge name survives.
	setModpack(t, ns, "pack", modpackSpecOf("ftb", "777", "4242"))
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		for name, want := range map[string]string{
			"FTB_MODPACK_ID": "777", "FTB_MODPACK_VERSION_ID": "4242", "TYPE": "FTBA",
		} {
			if got, ok := envValue(env, name); !ok || got != want {
				return false, fmt.Sprintf("%s=%q (present=%v), want %q", name, got, ok, want)
			}
		}
		for _, gone := range []string{"CF_SLUG", "CF_FILE_ID", "CF_DOWNLOADS_REPO"} {
			if envCount(env, gone) != 0 {
				return false, gone + " still rendered"
			}
		}
		return true, ""
	})

	// Remove: nothing of any provider remains and the loader's TYPE is back.
	setModpack(t, ns, "pack", nil)
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, "pack")
		if why != "" {
			return false, why
		}
		for _, gone := range []string{"FTB_MODPACK_ID", "FTB_MODPACK_VERSION_ID", "CF_SLUG", "MODRINTH_MODPACK"} {
			if envCount(env, gone) != 0 {
				return false, gone + " still rendered"
			}
		}
		if got, _ := envValue(env, "TYPE"); got != "FORGE" {
			return false, "TYPE=" + got + ", want the version's FORGE"
		}
		if got, _ := envValue(env, "FOO"); got != "bar" {
			return false, "unrelated spec.env FOO lost"
		}
		return true, ""
	})
	modpackReadyIs(t, ns, "pack", "True")
	if st := getGameServer(t, ns, "pack").Status.Modpack; st == nil || st.Applied != nil {
		t.Fatalf("status.modpack after removal = %+v, want applied nil", st)
	}
}

// TestGameServer_ModpackLegacyEnvUnchanged proves a server that never had
// spec.modpack keeps rendering its spec.env verbatim (no stripping, no
// status.modpack, no ModpackReady) so an operator upgrade never rolls it.
func TestGameServer_ModpackLegacyEnvUnchanged(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "legacy", tmpl.Name)
	gs.Spec.Env = []corev1.EnvVar{
		{Name: "MODRINTH_MODPACK", Value: "legacy-slug"},
		{Name: "TYPE", Value: "MODRINTH"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, "legacy")
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "MODRINTH_MODPACK"); got != "legacy-slug" {
			return false, "MODRINTH_MODPACK=" + got
		}
		if got, _ := envValue(env, "TYPE"); got != "MODRINTH" {
			return false, "TYPE=" + got
		}
		return true, ""
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		live := getGameServer(t, ns, "legacy")
		if live.Status.Modpack != nil {
			return false, "status.modpack was created for a legacy server"
		}
		if meta.FindStatusCondition(live.Status.Conditions, gameplanev1alpha1.GameServerConditionModpackReady) != nil {
			return false, "ModpackReady set for a legacy server"
		}
		return true, ""
	})
}
