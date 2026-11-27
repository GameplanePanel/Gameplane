package controller

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// secMarshal renders a value to JSON so a test can prove a key is absent from
// the rendered pod spec, not only nil in Go.
func secMarshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// TestSecurityBuilders_UnsetRendersNoSecurityContext is the byte-identical
// contract (T016): with nothing applied, no container or pod securityContext is
// rendered, so an existing StatefulSet does not change on upgrade.
func TestSecurityBuilders_UnsetRendersNoSecurityContext(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Name = "minecraft"
	tmpl.Spec.Game = "minecraft"
	gs := &gameplanev1alpha1.GameServer{}
	gs.Name = "alpha"

	t.Run("template and server with no security", func(t *testing.T) {
		game := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})
		if game.SecurityContext != nil {
			t.Errorf("game SecurityContext = %+v, want nil", game.SecurityContext)
		}
		if strings.Contains(secMarshal(t, game), `"securityContext"`) {
			t.Error("rendered game container carries a securityContext key")
		}
		if got := gamePodSecurityContext(tmpl); got != nil {
			t.Errorf("gamePodSecurityContext = %+v, want nil", got)
		}
		if got := gameContainerSecurityContext(tmpl); got != nil {
			t.Errorf("gameContainerSecurityContext = %+v, want nil", got)
		}
		cfg := buildConfigInitContainer("busybox:1.36", tmpl)
		if cfg.SecurityContext != nil {
			t.Errorf("config-init SecurityContext = %+v, want nil", cfg.SecurityContext)
		}
		if strings.Contains(secMarshal(t, cfg), `"securityContext"`) {
			t.Error("rendered config-init container carries a securityContext key")
		}
	})

	t.Run("override that resolves to no value renders nothing", func(t *testing.T) {
		gsOverride := gs.DeepCopy()
		gsOverride.Spec.Security = &gameplanev1alpha1.GameServerSecuritySpec{
			RunAsUser: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
		}
		game := buildGameContainer(gsOverride, tmpl, "busybox:1.36", nil, &materializedConfig{})
		if game.SecurityContext != nil {
			t.Errorf("game SecurityContext = %+v, want nil for an image-default override", game.SecurityContext)
		}
	})

	t.Run("resolved helpers with nothing to apply return nil", func(t *testing.T) {
		if got := gameContainerSecurityContextResolved(nil, nil); got != nil {
			t.Errorf("gameContainerSecurityContextResolved(nil, nil) = %+v, want nil", got)
		}
		onRoot := corev1.FSGroupChangeOnRootMismatch
		if got := gamePodSecurityContextResolved(nil, &onRoot); got != nil {
			t.Errorf("gamePodSecurityContextResolved(nil, policy) = %+v, want nil: the policy has no effect without fsGroup", got)
		}
	})
}

// TestSecurityBuilders_HelperContainersKeepFixedUsers proves the helper
// containers keep their fixed identities when a server or template sets
// security (FR-012). The config-init container takes only the group that the
// data volume needs, and never a user.
func TestSecurityBuilders_HelperContainersKeepFixedUsers(t *testing.T) {
	tmpl := secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), RunAsGroup: secInt(25000), FSGroup: secInt(25000)})
	gs := secServer(&gameplanev1alpha1.GameServerSecuritySpec{
		RunAsUser: &gameplanev1alpha1.SecurityIDOverride{Value: secInt(2000)},
		FSGroup:   &gameplanev1alpha1.SecurityIDOverride{Value: secInt(3000)},
	})

	t.Run("agent keeps its fixed non-root user", func(t *testing.T) {
		agent := buildAgentContainer(gs, tmpl, nil, "agent:test", "", "")
		sc := agent.SecurityContext
		if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 65532 {
			t.Fatalf("agent RunAsUser = %+v, want fixed 65532", sc)
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			t.Error("agent RunAsNonRoot not set")
		}
	})

	t.Run("config-init takes the group from the resolved fsGroup and no user", func(t *testing.T) {
		cfg := buildConfigInitContainerResolved("busybox:1.36", tmpl, secInt(3000))
		sc := cfg.SecurityContext
		if sc == nil || sc.RunAsGroup == nil || *sc.RunAsGroup != 3000 {
			t.Fatalf("config-init SecurityContext = %+v, want RunAsGroup 3000", sc)
		}
		if sc.RunAsUser != nil {
			t.Errorf("config-init RunAsUser = %d, want unset", *sc.RunAsUser)
		}
	})

	t.Run("config-init from the template fsGroup has no user", func(t *testing.T) {
		cfg := buildConfigInitContainer("busybox:1.36", tmpl)
		if cfg.SecurityContext == nil || cfg.SecurityContext.RunAsGroup == nil || *cfg.SecurityContext.RunAsGroup != 25000 {
			t.Fatalf("config-init SecurityContext = %+v, want RunAsGroup 25000", cfg.SecurityContext)
		}
		if cfg.SecurityContext.RunAsUser != nil {
			t.Errorf("config-init RunAsUser = %d, want unset", *cfg.SecurityContext.RunAsUser)
		}
	})
}

// TestGameContainerRunAsGroupUseImageDefault proves a runAsGroup override with
// useImageDefault leaves the game container runAsGroup unset while the module
// runAsUser still applies (US2 scenario).
func TestGameContainerRunAsGroupUseImageDefault(t *testing.T) {
	tmpl := secTemplate(&gameplanev1alpha1.GameSecuritySpec{RunAsUser: secInt(25000), RunAsGroup: secInt(25000)})
	gs := secServer(&gameplanev1alpha1.GameServerSecuritySpec{
		RunAsGroup: &gameplanev1alpha1.SecurityIDOverride{UseImageDefault: secBool(true)},
	})
	game := buildGameContainer(gs, tmpl, "busybox:1.36", nil, &materializedConfig{})
	sc := game.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 25000 {
		t.Fatalf("game SecurityContext = %+v, want RunAsUser 25000", sc)
	}
	if sc.RunAsGroup != nil {
		t.Errorf("game RunAsGroup = %d, want unset for useImageDefault", *sc.RunAsGroup)
	}
}
