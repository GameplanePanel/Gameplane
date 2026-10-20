package controller

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBuildAgentContainer_DefaultsAndOverrides(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"

	t.Run("falls back to default image and mountPath when template doesn't specify", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		tmpl.Name = "minecraft"
		tmpl.Spec.Game = "minecraft"
		c := buildAgentContainer(gs, tmpl, nil, "ghcr.io/agent:fallback", "", "")
		if c.Image != "ghcr.io/agent:fallback" {
			t.Fatalf("Image=%q", c.Image)
		}
		if c.VolumeMounts[0].MountPath != "/data" {
			t.Fatalf("MountPath=%q", c.VolumeMounts[0].MountPath)
		}
	})

	t.Run("template Agent.Image overrides fallback", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		tmpl.Name = "minecraft"
		tmpl.Spec.Game = "minecraft"
		tmpl.Spec.Agent = &gameplanev1alpha1.AgentSpec{
			Image: "ghcr.io/agent:custom",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
			},
		}
		c := buildAgentContainer(gs, tmpl, nil, "ghcr.io/agent:fallback", "", "")
		if c.Image != "ghcr.io/agent:custom" {
			t.Fatalf("Image=%q", c.Image)
		}
		if c.Resources.Limits.Cpu().String() != "500m" {
			t.Fatalf("Resources=%+v", c.Resources)
		}
	})

	t.Run("template Storage.MountPath overrides /data", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		tmpl.Spec.Storage.MountPath = "/srv"
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		if c.VolumeMounts[0].MountPath != "/srv" {
			t.Fatalf("MountPath=%q", c.VolumeMounts[0].MountPath)
		}
	})

	t.Run("security context locks down the sidecar", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		sc := c.SecurityContext
		if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Fatalf("RunAsNonRoot not set: %+v", sc)
		}
		if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
			t.Fatal("ReadOnlyRootFilesystem not set")
		}
		if len(sc.Capabilities.Drop) == 0 || sc.Capabilities.Drop[0] != "ALL" {
			t.Fatalf("Capabilities.Drop=%v", sc.Capabilities.Drop)
		}
	})

	t.Run("env vars include server/template/game", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		tmpl.Name = "minecraft"
		tmpl.Spec.Game = "minecraft-java"
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		envs := map[string]string{}
		for _, e := range c.Env {
			envs[e.Name] = e.Value
		}
		if envs["GAMEPLANE_SERVER_NAME"] != "alpha" {
			t.Fatalf("server env=%q", envs["GAMEPLANE_SERVER_NAME"])
		}
		if envs["GAMEPLANE_TEMPLATE"] != "minecraft" {
			t.Fatalf("template env=%q", envs["GAMEPLANE_TEMPLATE"])
		}
		if envs["GAMEPLANE_GAME"] != "minecraft-java" {
			t.Fatalf("game env=%q", envs["GAMEPLANE_GAME"])
		}
	})

	t.Run("port 8090 advertised by agent", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		if len(c.Ports) != 2 || c.Ports[0].ContainerPort != 8090 {
			t.Fatalf("Ports=%+v", c.Ports)
		}
	})

	// F-216: the agent's separate plain-HTTP metrics listener
	// (agent/cmd/main.go's --metrics-addr, default :9090) must be declared
	// as a named containerPort so the chart's PodMonitor can target it by
	// name (podMetricsEndpoints[].port) instead of a bare portNumber, which
	// requires a recent Prometheus-Operator CRD version to exist at all.
	t.Run("named metrics port 9090 advertised by agent", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		var found bool
		for _, p := range c.Ports {
			if p.Name == "metrics" {
				found = true
				if p.ContainerPort != 9090 {
					t.Fatalf("metrics containerPort=%d, want 9090", p.ContainerPort)
				}
			}
		}
		if !found {
			t.Fatalf("Ports=%+v does not include a named metrics port", c.Ports)
		}
	})
}

func TestBuildAgentContainer_RconEnabledEnv(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	gs.Namespace = "g"

	envOf := func(c corev1.Container) map[string]string {
		out := map[string]string{}
		for _, e := range c.Env {
			out[e.Name] = e.Value
		}
		return out
	}

	t.Run("rcon template advertises enabled", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			RCON: &gameplanev1alpha1.RCONSpec{Protocol: "source"},
		}}
		if got := envOf(buildAgentContainer(gs, tmpl, nil, "fb", "", ""))["GAMEPLANE_RCON_ENABLED"]; got != "true" {
			t.Fatalf("GAMEPLANE_RCON_ENABLED=%q, want true", got)
		}
	})

	t.Run("no rcon block disables", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		if got := envOf(buildAgentContainer(gs, tmpl, nil, "fb", "", ""))["GAMEPLANE_RCON_ENABLED"]; got != "false" {
			t.Fatalf("GAMEPLANE_RCON_ENABLED=%q, want false", got)
		}
	})

	t.Run("protocol none disables", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			RCON: &gameplanev1alpha1.RCONSpec{Protocol: "none"},
		}}
		if got := envOf(buildAgentContainer(gs, tmpl, nil, "fb", "", ""))["GAMEPLANE_RCON_ENABLED"]; got != "false" {
			t.Fatalf("GAMEPLANE_RCON_ENABLED=%q, want false", got)
		}
	})
}

func TestBuildAgentContainer_GameLogPathArg(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	gs.Namespace = "g"

	hasArg := func(c corev1.Container, want string) bool {
		for _, a := range c.Args {
			if a == want {
				return true
			}
		}
		return false
	}

	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
		LogPath: "/data/logs/latest.log",
	}}
	if !hasArg(buildAgentContainer(gs, tmpl, nil, "fb", "", ""), "--game-log-path=/data/logs/latest.log") {
		t.Fatal("expected --game-log-path arg when template declares logPath")
	}

	bare := &gameplanev1alpha1.GameTemplate{}
	for _, a := range buildAgentContainer(gs, bare, nil, "fb", "", "").Args {
		if strings.HasPrefix(a, "--game-log-path") {
			t.Fatalf("unexpected log-path arg without template logPath: %s", a)
		}
	}
}

func TestBuildAgentContainer_RconPasswordFile(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "factorio"
	gs.Namespace = "g"

	hasArg := func(c corev1.Container, want string) bool {
		for _, a := range c.Args {
			if a == want {
				return true
			}
		}
		return false
	}

	t.Run("passwordFile mode uses data mount path", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			RCON: &gameplanev1alpha1.RCONSpec{
				Protocol:     "source",
				PasswordFile: "config/rconpw",
			},
		}}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		if !hasArg(c, "--rcon-password-file=/data/config/rconpw") {
			t.Fatalf("expected --rcon-password-file=/data/config/rconpw in args: %v", c.Args)
		}
		// Should not mount rcon-password volume
		for _, m := range c.VolumeMounts {
			if m.Name == "rcon-password" {
				t.Fatalf("passwordFile mode should not mount rcon-password volume")
			}
		}
	})

	t.Run("passwordFile mode with custom mount path", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			Storage: gameplanev1alpha1.GameStorageSpec{MountPath: "/srv"},
			RCON: &gameplanev1alpha1.RCONSpec{
				Protocol:     "source",
				PasswordFile: "config/rconpw",
			},
		}}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		if !hasArg(c, "--rcon-password-file=/srv/config/rconpw") {
			t.Fatalf("expected --rcon-password-file=/srv/config/rconpw in args: %v", c.Args)
		}
	})
}

// TestBuildAgentContainer_DataRootArg guards the bug where the agent always
// rooted its file ops at its own --data-root default (/data) regardless of
// where the template actually mounts the data volume: for any of the 7
// shipped modules whose storage.mountPath isn't /data (rust, factorio,
// valheim, terraria, palworld, cs2, ark-survival-ascended), the Mods tab,
// Files tab, and disk-usage stats all silently pointed at the wrong path.
// The fix must pass --data-root resolved from the same effectiveMountPath
// value used for the "data" VolumeMount, so the two can never drift apart —
// this test asserts that invariant directly rather than just the arg value.
func TestBuildAgentContainer_DataRootArg(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	gs.Namespace = "g"

	dataRootArg := func(c corev1.Container) (string, bool) {
		const prefix = "--data-root="
		for _, a := range c.Args {
			if strings.HasPrefix(a, prefix) {
				return strings.TrimPrefix(a, prefix), true
			}
		}
		return "", false
	}
	dataVolumeMountPath := func(c corev1.Container) (string, bool) {
		for _, m := range c.VolumeMounts {
			if m.Name == "data" {
				return m.MountPath, true
			}
		}
		return "", false
	}

	t.Run("custom mountPath (rust)", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
			Storage: gameplanev1alpha1.GameStorageSpec{MountPath: "/steamcmd/rust"},
		}}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		got, ok := dataRootArg(c)
		if !ok || got != "/steamcmd/rust" {
			t.Fatalf("--data-root=%q, ok=%v, want /steamcmd/rust", got, ok)
		}
		mountPath, ok := dataVolumeMountPath(c)
		if !ok {
			t.Fatal("no \"data\" VolumeMount on agent container")
		}
		if got != mountPath {
			t.Fatalf("--data-root=%q != data VolumeMount path %q; agent would look in the wrong place", got, mountPath)
		}
	})

	t.Run("mountPath omitted defaults to /data", func(t *testing.T) {
		tmpl := &gameplanev1alpha1.GameTemplate{}
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		got, ok := dataRootArg(c)
		if !ok || got != "/data" {
			t.Fatalf("--data-root=%q, ok=%v, want /data", got, ok)
		}
		mountPath, ok := dataVolumeMountPath(c)
		if !ok {
			t.Fatal("no \"data\" VolumeMount on agent container")
		}
		if got != mountPath {
			t.Fatalf("--data-root=%q != data VolumeMount path %q", got, mountPath)
		}
	})
}

func TestBuildAgentContainer_CapabilitiesEnv(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	gs.Namespace = "g"

	envValue := func(c corev1.Container, name string) (string, bool) {
		for _, e := range c.Env {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{
		Capabilities: &gameplanev1alpha1.CapabilitiesSpec{
			Players: &gameplanev1alpha1.PlayerActionsSpec{
				Kick: "kick {{.Player}}",
			},
			Quiesce: &gameplanev1alpha1.QuiesceSpec{
				Quiesce:   []string{"save-off", "save-all flush"},
				Unquiesce: []string{"save-on"},
			},
		},
	}}
	got, ok := envValue(buildAgentContainer(gs, tmpl, nil, "fb", "", ""), "GAMEPLANE_CAPABILITIES")
	if !ok {
		t.Fatal("expected GAMEPLANE_CAPABILITIES env when template declares capabilities")
	}
	var parsed gameplanev1alpha1.CapabilitiesSpec
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("env is not valid JSON: %v", err)
	}
	if parsed.Players == nil || parsed.Players.Kick != "kick {{.Player}}" {
		t.Errorf("players round-trip = %+v", parsed.Players)
	}
	if parsed.Quiesce == nil || len(parsed.Quiesce.Quiesce) != 2 {
		t.Errorf("quiesce round-trip = %+v", parsed.Quiesce)
	}

	bare := &gameplanev1alpha1.GameTemplate{}
	if _, ok := envValue(buildAgentContainer(gs, bare, nil, "fb", "", ""), "GAMEPLANE_CAPABILITIES"); ok {
		t.Fatal("unexpected GAMEPLANE_CAPABILITIES env without declared capabilities")
	}
}

func TestBuildAgentContainer_LogLevel(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	tmpl := &gameplanev1alpha1.GameTemplate{}
	envOf := func(c corev1.Container) map[string]string {
		out := map[string]string{}
		for _, e := range c.Env {
			out[e.Name] = e.Value
		}
		return out
	}

	if got := envOf(buildAgentContainer(gs, tmpl, nil, "fb", "debug", ""))["GAMEPLANE_LOG_LEVEL"]; got != "debug" {
		t.Errorf("GAMEPLANE_LOG_LEVEL = %q, want debug", got)
	}
	// Unset must inject nothing: the env var's mere presence would differ
	// from pre-flag pod specs and roll every game StatefulSet on upgrade.
	if v, ok := envOf(buildAgentContainer(gs, tmpl, nil, "fb", "", ""))["GAMEPLANE_LOG_LEVEL"]; ok {
		t.Errorf("GAMEPLANE_LOG_LEVEL unexpectedly injected: %q", v)
	}
}

// TestBuildAgentContainer_ImagePullPolicy guards the fix for stale cached
// agents on floating tags (e.g. :edge): Kubernetes only defaults
// ImagePullPolicy to Always for a ":latest" tag, so without an explicit
// policy a node reuses whatever agent image it already cached and new agent
// features never reach existing (or even newly scheduled, same-node) game
// pods. buildAgentContainer must carry the configured policy through
// verbatim, and must leave the field unset (not merely "") when no policy
// is configured, so pre-flag pod specs — and the CreateOrUpdate diff against
// them — don't change.
func TestBuildAgentContainer_ImagePullPolicy(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "smp"
	tmpl := &gameplanev1alpha1.GameTemplate{}

	t.Run("configured policy is set verbatim", func(t *testing.T) {
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "Always")
		if c.ImagePullPolicy != corev1.PullAlways {
			t.Fatalf("ImagePullPolicy = %q, want %q", c.ImagePullPolicy, corev1.PullAlways)
		}
	})

	t.Run("empty policy leaves the field unset", func(t *testing.T) {
		c := buildAgentContainer(gs, tmpl, nil, "fb", "", "")
		if c.ImagePullPolicy != "" {
			t.Fatalf("ImagePullPolicy = %q, want unset", c.ImagePullPolicy)
		}
	})
}

func TestBuildAgentContainerBindsServerUID(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "same-name"
	gs.UID = "original-server-uid"
	container := buildAgentContainer(gs, &gameplanev1alpha1.GameTemplate{}, nil, "agent:test", "", "")
	for _, env := range container.Env {
		if env.Name == "GAMEPLANE_SERVER_UID" {
			if env.Value != string(gs.UID) || env.ValueFrom != nil {
				t.Fatalf("identity env=%+v", env)
			}
			return
		}
	}
	t.Fatal("agent must receive the immutable GameServer UID")
}
