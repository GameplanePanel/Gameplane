package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func mcVersions() []gameplanev1alpha1.GameVersion {
	return []gameplanev1alpha1.GameVersion{
		{
			ID: "1.21.4-paper", DisplayName: "1.21.4 Paper",
			Image: "itzg/minecraft-server:stable", Loader: "paper", Default: true,
			Env: []corev1.EnvVar{{Name: "TYPE", Value: "PAPER"}, {Name: "VERSION", Value: "1.21.4"}},
		},
		{
			ID: "1.21.4-forge", DisplayName: "1.21.4 Forge",
			Image: "itzg/minecraft-server:stable", Loader: "forge",
			Env: []corev1.EnvVar{{Name: "TYPE", Value: "FORGE"}, {Name: "VERSION", Value: "1.21.4"}},
		},
	}
}

func mcModsTemplate() *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "minecraft-java"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Image:    "itzg/minecraft-server:stable",
			Storage:  gameplanev1alpha1.GameStorageSpec{MountPath: "/data"},
			Versions: mcVersions(),
			Capabilities: &gameplanev1alpha1.CapabilitiesSpec{
				Players: &gameplanev1alpha1.PlayerActionsSpec{
					Kick: "kick {{.Player}}",
					List: &gameplanev1alpha1.PlayerListSpec{
						Command:    "list",
						EntryRegex: `(?P<name>\w+)`,
					},
				},
				Mods: &gameplanev1alpha1.ModsSpec{
					Loaders: map[string]gameplanev1alpha1.ModLoaderSpec{
						"paper":   {Path: "plugins", Extensions: []string{".jar"}},
						"forge":   {Path: "mods", Extensions: []string{".jar"}},
						"vanilla": {Path: "mods"},
					},
					Install: &gameplanev1alpha1.ModInstallSpec{AllowedHosts: []string{"cdn.modrinth.com"}},
				},
			},
		},
	}
}

func TestResolveVersion(t *testing.T) {
	tmpl := mcModsTemplate()

	t.Run("no versions declared", func(t *testing.T) {
		bare := &gameplanev1alpha1.GameTemplate{}
		ver, err := resolveVersion(&gameplanev1alpha1.GameServer{}, bare)
		if err != nil || ver != nil {
			t.Fatalf("got ver=%v err=%v, want nil,nil", ver, err)
		}
	})

	t.Run("explicit valid id", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{Version: "1.21.4-forge"}}
		ver, err := resolveVersion(gs, tmpl)
		if err != nil || ver == nil || ver.ID != "1.21.4-forge" {
			t.Fatalf("got ver=%v err=%v", ver, err)
		}
	})

	t.Run("explicit unknown id fails", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{Version: "9.9-bogus"}}
		_, err := resolveVersion(gs, tmpl)
		if err == nil || !strings.Contains(err.Error(), "unknown version") {
			t.Fatalf("want unknown-version error, got %v", err)
		}
	})

	t.Run("empty selects the default entry", func(t *testing.T) {
		ver, err := resolveVersion(&gameplanev1alpha1.GameServer{}, tmpl)
		if err != nil || ver == nil || ver.ID != "1.21.4-paper" {
			t.Fatalf("got ver=%v err=%v, want default 1.21.4-paper", ver, err)
		}
	})

	t.Run("empty with no default selects first", func(t *testing.T) {
		t2 := mcModsTemplate()
		t2.Spec.Versions[0].Default = false
		ver, err := resolveVersion(&gameplanev1alpha1.GameServer{}, t2)
		if err != nil || ver == nil || ver.ID != "1.21.4-paper" {
			t.Fatalf("got ver=%v err=%v, want first entry", ver, err)
		}
	})
}

func TestResolveImage(t *testing.T) {
	tmpl := mcModsTemplate()
	ver := &tmpl.Spec.Versions[1] // forge, image stable

	t.Run("spec.image override wins", func(t *testing.T) {
		gs := &gameplanev1alpha1.GameServer{Spec: gameplanev1alpha1.GameServerSpec{Image: "fork:dev"}}
		if got := resolveImage(gs, tmpl, ver); got != "fork:dev" {
			t.Fatalf("got %q, want fork:dev", got)
		}
	})
	t.Run("version image", func(t *testing.T) {
		if got := resolveImage(&gameplanev1alpha1.GameServer{}, tmpl, ver); got != "itzg/minecraft-server:stable" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("template fallback when no version", func(t *testing.T) {
		if got := resolveImage(&gameplanev1alpha1.GameServer{}, tmpl, nil); got != "itzg/minecraft-server:stable" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestResolveCapabilities(t *testing.T) {
	tmpl := mcModsTemplate()

	t.Run("nil capabilities", func(t *testing.T) {
		if resolveCapabilities(&gameplanev1alpha1.GameTemplate{}, nil) != nil {
			t.Fatal("want nil")
		}
	})

	t.Run("per-loader collapses to active loader path", func(t *testing.T) {
		ver := &tmpl.Spec.Versions[0] // paper
		caps := resolveCapabilities(tmpl, ver)
		if caps == nil || caps.Mods == nil {
			t.Fatal("want mods")
		}
		if caps.Mods.Path != "plugins" {
			t.Fatalf("Path=%q, want plugins", caps.Mods.Path)
		}
		if len(caps.Mods.Extensions) != 1 || caps.Mods.Extensions[0] != ".jar" {
			t.Fatalf("Extensions=%v", caps.Mods.Extensions)
		}
		if caps.Mods.Loaders != nil {
			t.Fatalf("Loaders should be cleared, got %v", caps.Mods.Loaders)
		}
		if caps.Mods.Install == nil || len(caps.Mods.Install.AllowedHosts) != 1 {
			t.Fatalf("Install lost: %v", caps.Mods.Install)
		}
		// Non-mods capabilities are preserved.
		if caps.Players == nil || caps.Players.Kick != "kick {{.Player}}" {
			t.Fatalf("players lost: %v", caps.Players)
		}
		// Player list config is preserved through deep copy.
		if caps.Players.List == nil || caps.Players.List.Command != "list" || caps.Players.List.EntryRegex != `(?P<name>\w+)` {
			t.Fatalf("player list lost or corrupted: %v", caps.Players.List)
		}
		// The source template must be untouched (deep copy).
		if tmpl.Spec.Capabilities.Mods.Loaders == nil {
			t.Fatal("source template Loaders mutated")
		}
	})

	t.Run("loader without a mods entry yields no mod manager", func(t *testing.T) {
		ver := &gameplanev1alpha1.GameVersion{ID: "x", Loader: "tmodloader"}
		caps := resolveCapabilities(tmpl, ver)
		if caps == nil || caps.Mods != nil {
			t.Fatalf("want mods nil for unmapped loader, got %v", caps.Mods)
		}
	})

	t.Run("legacy single-path mods unchanged", func(t *testing.T) {
		legacy := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			Capabilities: &gameplanev1alpha1.CapabilitiesSpec{
				Mods: &gameplanev1alpha1.ModsSpec{Path: "plugins", Extensions: []string{".jar"}},
			},
		}}
		caps := resolveCapabilities(legacy, nil)
		if caps.Mods == nil || caps.Mods.Path != "plugins" {
			t.Fatalf("legacy path changed: %v", caps.Mods)
		}
	})
}

func TestModVolumeNaming(t *testing.T) {
	if got := dnsSafe("1.21.4-Paper_x"); got != "1-21-4-paper-x" {
		t.Fatalf("dnsSafe=%q", got)
	}
	if got := modVolumeName("1.21.4-paper"); got != "mods-1-21-4-paper" {
		t.Fatalf("modVolumeName=%q", got)
	}
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "smp"}}
	if got := modPVCName(gs, "1.21.4-paper"); got != "smp-mods-1-21-4-paper" {
		t.Fatalf("modPVCName=%q", got)
	}

	long := strings.Repeat("a", 80)
	got := truncateDNS("mods-"+long, 63)
	if len(got) > 63 {
		t.Fatalf("truncateDNS len=%d > 63", len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("truncated name ends with hyphen: %q", got)
	}
}

func TestModVolumeMountAndKey(t *testing.T) {
	tmpl := mcModsTemplate()
	paper := &tmpl.Spec.Versions[0]

	if got := modVolumeKey(tmpl, paper); got != "1.21.4-paper" {
		t.Fatalf("modVolumeKey=%q", got)
	}
	m := modVolumeMount(tmpl, paper)
	if m == nil || m.Name != "mods-1-21-4-paper" || m.MountPath != "/data/plugins" {
		t.Fatalf("modVolumeMount=%+v", m)
	}

	// A version whose loader has no mods entry → no key, no mount.
	noLoader := &gameplanev1alpha1.GameVersion{ID: "x", Loader: "tmodloader"}
	if modVolumeKey(tmpl, noLoader) != "" {
		t.Fatal("want empty key for unmapped loader")
	}
	if modVolumeMount(tmpl, noLoader) != nil {
		t.Fatal("want nil mount for unmapped loader")
	}
	// nil version (no catalog) → no mount.
	if modVolumeMount(&gameplanev1alpha1.GameTemplate{}, nil) != nil {
		t.Fatal("want nil mount without versions")
	}
}
