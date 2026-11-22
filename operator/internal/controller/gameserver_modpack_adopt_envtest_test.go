//go:build envtest

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// ssOf returns the server's StatefulSet.
func ssOf(t *testing.T, ns, name string) *appsv1.StatefulSet {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	return &ss
}

// podTemplateJSON is the pod template as a comparable string.
func podTemplateJSON(t *testing.T, ns, name string) string {
	t.Helper()
	b, err := json.Marshal(ssOf(t, ns, name).Spec.Template)
	if err != nil {
		t.Fatalf("marshal pod template: %v", err)
	}
	return string(b)
}

// startLegacyModrinthServer creates a running (replicas 1) server whose pre-035
// pack env lives in spec.env, with no spec.modpack.
func startLegacyModrinthServer(t *testing.T, ns, name string) {
	t.Helper()
	tmpl := buildModpackTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)
	gs := buildGameServer(ns, name, tmpl.Name)
	gs.Spec.Version = "1.21.4-forge"
	gs.Spec.Env = []corev1.EnvVar{
		{Name: "MODRINTH_MODPACK", Value: "legacy-slug"},
		{Name: "TYPE", Value: "MODRINTH"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, name)
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "MODRINTH_MODPACK"); got != "legacy-slug" {
			return false, "MODRINTH_MODPACK=" + got
		}
		ss := ssOf(t, ns, name)
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
			return false, "statefulset not at replicas 1"
		}
		return true, ""
	})
}

// adoptServer simulates the API's legacy sweep: spec.modpack plus the adopted annotation.
func adoptServer(t *testing.T, ns, name, version string) {
	t.Helper()
	mutateGameServer(t, ns, name, func(g *gameplanev1alpha1.GameServer) {
		g.Spec.Modpack = modpackSpecOf("modrinth", "legacy-slug", version)
		if g.Annotations == nil {
			g.Annotations = map[string]string{}
		}
		g.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation] = "true"
	})
}

func assertPinRendered(t *testing.T, ns, name string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		env, why := gameEnvOf(ns, name)
		if why != "" {
			return false, why
		}
		if got, _ := envValue(env, "MODRINTH_VERSION"); got != "v-abc" {
			return false, "MODRINTH_VERSION=" + got
		}
		if n := envCount(env, "MODRINTH_MODPACK"); n != 1 {
			return false, fmt.Sprintf("MODRINTH_MODPACK appears %d times, want 1", n)
		}
		return true, ""
	})
}

// TestGameServer_ModpackAdoptedKeepsLegacyEnvUntilQuiet covers spec 035 T029
// (rule 2, OD-8 B): a legacy server and an adopted server whose pod runs keep an
// identical pod template while players are online or unknown; with a known 0
// players the pin is rendered (one rollout) and the adopted annotation removed.
func TestGameServer_ModpackAdoptedKeepsLegacyEnvUntilQuiet(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	startLegacyModrinthServer(t, ns, "pack")
	legacy := podTemplateJSON(t, ns, "pack")

	// Not-yet-swept legacy server: nothing moves.
	consistently(t, time.Second, func() (bool, string) {
		if got := podTemplateJSON(t, ns, "pack"); got != legacy {
			return false, "pod template changed for a legacy server"
		}
		if getGameServer(t, ns, "pack").Status.Modpack != nil {
			return false, "status.modpack created for a legacy server"
		}
		return true, ""
	})

	// Sweep: adopted with a version. Players unknown, then online: still no roll.
	adoptServer(t, ns, "pack", "v-abc")
	setAgentPlayers(t, ns, "pack", nil)
	consistently(t, 2*time.Second, func() (bool, string) {
		if got := podTemplateJSON(t, ns, "pack"); got != legacy {
			return false, "pod template changed while players are unknown"
		}
		return true, ""
	})
	setAgentPlayers(t, ns, "pack", int32Ptr(3))
	consistently(t, 2*time.Second, func() (bool, string) {
		if got := podTemplateJSON(t, ns, "pack"); got != legacy {
			return false, "pod template changed while players are online"
		}
		live := getGameServer(t, ns, "pack")
		if live.Status.Modpack != nil {
			return false, fmt.Sprintf("status.modpack = %+v while players are online", live.Status.Modpack)
		}
		if live.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation] != "true" {
			return false, "adopted annotation removed before the pin was rendered"
		}
		return true, ""
	})

	// Quiet: the pin is rendered, exactly one rollout, annotation removed.
	before := ssOf(t, ns, "pack").Generation
	setAgentPlayers(t, ns, "pack", int32Ptr(0))
	assertPinRendered(t, ns, "pack")
	modpackReadyIs(t, ns, "pack", "True")
	eventually(t, func() (bool, string) {
		if _, ok := getGameServer(t, ns, "pack").Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation]; ok {
			return false, "adopted annotation still set"
		}
		return true, ""
	})
	after := ssOf(t, ns, "pack").Generation
	if after != before+1 {
		t.Fatalf("statefulset generation %d -> %d, want exactly one rollout", before, after)
	}
	consistently(t, time.Second, func() (bool, string) {
		if g := ssOf(t, ns, "pack").Generation; g != after {
			return false, fmt.Sprintf("statefulset generation moved to %d after the pin", g)
		}
		return true, ""
	})
}

// TestGameServer_ModpackAdoptedStoppedPinsImmediately: an adopted server that is
// stopped (replicas 0) gets the pin right away, whatever the player count.
func TestGameServer_ModpackAdoptedStoppedPinsImmediately(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	startLegacyModrinthServer(t, ns, "pack")
	setAgentPlayers(t, ns, "pack", int32Ptr(5))

	mutateGameServer(t, ns, "pack", func(g *gameplanev1alpha1.GameServer) { g.Spec.Suspend = true })
	eventually(t, func() (bool, string) {
		ss := ssOf(t, ns, "pack")
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			return false, "statefulset not scaled to zero"
		}
		return true, ""
	})

	adoptServer(t, ns, "pack", "v-abc")
	assertPinRendered(t, ns, "pack")
	modpackReadyIs(t, ns, "pack", "True")
}
