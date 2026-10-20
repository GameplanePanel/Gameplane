//go:build envtest

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_CreatesStatefulSetServicePVC — minimal happy path.
// One GameServer + GameTemplate ⇒ StatefulSet, Service, PVC all exist
// in the test namespace.
func TestGameServer_CreatesStatefulSetServicePVC(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &svc); err != nil {
			return false, "service: " + err.Error()
		}
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, "pvc: " + err.Error()
		}
		return true, ""
	})
}

// TestGameServer_AgentSidecarInjected — pod template has 2 containers,
// agent has the documented identity env vars.
func TestGameServer_AgentSidecarInjected(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("valheim"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		cs := ss.Spec.Template.Spec.Containers
		if len(cs) != 2 {
			return false, "expected 2 containers, got " + sprintArgs(containerNames(cs))
		}
		var agent *corev1.Container
		for i := range cs {
			if cs[i].Name == "agent" {
				agent = &cs[i]
				break
			}
		}
		if agent == nil {
			return false, "no agent sidecar container"
		}
		want := map[string]string{
			"GAMEPLANE_SERVER_NAME": "smp",
			"GAMEPLANE_TEMPLATE":    tmpl.Name,
			"GAMEPLANE_GAME":        tmpl.Spec.Game,
		}
		got := map[string]string{}
		for _, e := range agent.Env {
			got[e.Name] = e.Value
		}
		for k, v := range want {
			if got[k] != v {
				return false, "env " + k + " = " + got[k] + ", want " + v
			}
		}
		// Sidecar should drop ALL caps and run as non-root.
		if agent.SecurityContext == nil {
			return false, "agent has no SecurityContext"
		}
		if agent.SecurityContext.RunAsNonRoot == nil || !*agent.SecurityContext.RunAsNonRoot {
			return false, "agent RunAsNonRoot != true"
		}
		if agent.SecurityContext.Capabilities == nil ||
			len(agent.SecurityContext.Capabilities.Drop) == 0 ||
			agent.SecurityContext.Capabilities.Drop[0] != "ALL" {
			return false, "agent does not drop ALL caps"
		}
		return true, ""
	})
}

// TestGameServer_AgentImagePullPolicy proves the --agent-image-pull-policy
// operator flag reaches the agent sidecar's ImagePullPolicy end to end
// (main.go -> GameServerReconciler.AgentImagePullPolicy -> buildAgentContainer),
// and that leaving it unset (today's default) leaves the field unset on the
// pod spec rather than forcing a default policy — the fix for game pods
// silently running a stale agent forever on a floating tag like :edge.
func TestGameServer_AgentImagePullPolicy(t *testing.T) {
	t.Run("configured policy lands on the agent container", func(t *testing.T) {
		ns := newNamespace(t)
		startMgr(t, ns, withGameServerReconcilerAgentPullPolicy(t, ns, "Always"))

		tmpl := buildGameTemplate(uniqueName("valheim"))
		if err := k8sClient.Create(context.Background(), tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		eventually(t, func() (bool, string) {
			var ss appsv1.StatefulSet
			if err := k8sClient.Get(context.Background(),
				types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
				return false, err.Error()
			}
			agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
			if agent == nil {
				return false, "no agent sidecar container"
			}
			if agent.ImagePullPolicy != corev1.PullAlways {
				return false, "agent ImagePullPolicy = " + string(agent.ImagePullPolicy) + ", want Always"
			}
			return true, ""
		})
	})

	// Leaving the policy unset does NOT leave the field empty: the apiserver
	// defaults it to IfNotPresent for any tag that isn't :latest. That default
	// IS the bug this flag exists to fix — with a floating tag like :edge it
	// pins every game pod to whatever agent the node already cached (we found
	// one running a 12-day-old build). This asserts the default we're
	// overriding, so if Kubernetes ever changes it, we hear about it here.
	// The truly-unset case is covered pre-apiserver by the buildAgentContainer
	// unit test, which is the only place "unset" is observable.
	t.Run("unset policy falls back to the apiserver default", func(t *testing.T) {
		ns := newNamespace(t)
		startMgr(t, ns, withGameServerReconciler(t, ns))

		tmpl := buildGameTemplate(uniqueName("valheim"))
		if err := k8sClient.Create(context.Background(), tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		eventually(t, func() (bool, string) {
			var ss appsv1.StatefulSet
			if err := k8sClient.Get(context.Background(),
				types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
				return false, err.Error()
			}
			agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
			if agent == nil {
				return false, "no agent sidecar container"
			}
			if agent.ImagePullPolicy != corev1.PullIfNotPresent {
				return false, "agent ImagePullPolicy = " + string(agent.ImagePullPolicy) +
					", want the apiserver default IfNotPresent"
			}
			return true, ""
		})
	})
}

// TestGameServer_SecurityContextAppliedToGameContainerAndPod proves the
// full reconcile wiring for GameTemplate.spec.security (added for games
// like ARK: Survival Ascended, whose image requires uid 25000 and can't
// initialise Proton as root): runAsUser/runAsGroup land on the GAME
// container's SecurityContext, fsGroup lands on the pod's SecurityContext
// (it's a pod-level-only field that governs volume ownership), and the
// agent sidecar's own fixed SecurityContext (distroless, uid 65532) is
// untouched by any of it.
func TestGameServer_SecurityContextAppliedToGameContainerAndPod(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	uid := int64(25000)
	gid := int64(25000)
	tmpl := buildGameTemplate(uniqueName("ark-survival-ascended"))
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{
		RunAsUser:  &uid,
		RunAsGroup: &gid,
		FSGroup:    &gid,
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "ark", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "ark"}, &ss); err != nil {
			return false, err.Error()
		}
		cs := ss.Spec.Template.Spec.Containers
		var game, agent *corev1.Container
		for i := range cs {
			switch cs[i].Name {
			case gameContainerName:
				game = &cs[i]
			case "agent":
				agent = &cs[i]
			}
		}
		if game == nil {
			return false, "no game container yet"
		}
		if agent == nil {
			return false, "no agent sidecar container"
		}

		// Game container: runAsUser/runAsGroup from spec.security.
		gsc := game.SecurityContext
		if gsc == nil {
			return false, "game container has no SecurityContext"
		}
		if gsc.RunAsUser == nil || *gsc.RunAsUser != uid {
			return false, "game RunAsUser = " + fmt.Sprintf("%v", gsc.RunAsUser) + ", want " + intToStr(int32(uid))
		}
		if gsc.RunAsGroup == nil || *gsc.RunAsGroup != gid {
			return false, "game RunAsGroup = " + fmt.Sprintf("%v", gsc.RunAsGroup) + ", want " + intToStr(int32(gid))
		}

		// Pod level: fsGroup, not on the game container.
		psc := ss.Spec.Template.Spec.SecurityContext
		if psc == nil || psc.FSGroup == nil || *psc.FSGroup != gid {
			return false, "pod SecurityContext.FSGroup = " + fmt.Sprintf("%+v", psc) + ", want " + intToStr(int32(gid))
		}

		// Agent sidecar: untouched — still its own fixed distroless identity,
		// never the game's uid/gid.
		asc := agent.SecurityContext
		if asc == nil {
			return false, "agent has no SecurityContext"
		}
		if asc.RunAsUser == nil || *asc.RunAsUser == uid {
			return false, "agent RunAsUser leaked the game's uid: " + fmt.Sprintf("%v", asc.RunAsUser)
		}
		if asc.RunAsNonRoot == nil || !*asc.RunAsNonRoot {
			return false, "agent RunAsNonRoot != true"
		}
		if asc.ReadOnlyRootFilesystem == nil || !*asc.ReadOnlyRootFilesystem {
			return false, "agent ReadOnlyRootFilesystem != true"
		}
		if asc.Capabilities == nil || len(asc.Capabilities.Drop) == 0 || asc.Capabilities.Drop[0] != "ALL" {
			return false, "agent does not drop ALL caps"
		}
		return true, ""
	})
}

// TestGameServer_SecurityContextUnsetRendersNoSecurityContext is the
// regression guard: a template with no spec.security must render neither
// a game-container SecurityContext nor a pod-level SecurityContext — an
// empty `securityContext: {}` would still change the pod spec (and roll
// every existing game StatefulSet) even though nothing was requested.
func TestGameServer_SecurityContextUnsetRendersNoSecurityContext(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("valheim"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		// The apiserver defaults SecurityContext to an empty struct even when
		// none is explicitly set. What matters is that FSGroup is unset.
		podSC := ss.Spec.Template.Spec.SecurityContext
		if podSC != nil && podSC.FSGroup != nil {
			return false, "pod SecurityContext.FSGroup = " + fmt.Sprintf("%+v", podSC.FSGroup) + ", want nil"
		}
		for _, c := range ss.Spec.Template.Spec.Containers {
			if c.Name == gameContainerName {
				cSC := c.SecurityContext
				if cSC != nil && (cSC.RunAsUser != nil || cSC.RunAsGroup != nil) {
					return false, "game container SecurityContext = " + fmt.Sprintf("%+v", cSC) + ", want RunAsUser/RunAsGroup nil"
				}
			}
		}
		return true, ""
	})
}

// TestGameServer_StatusPatchPreservesAgentHeartbeat — the reconciler must
// not clobber status.agent (written by the sidecar's heartbeat) when it
// updates phase/conditions. We reproduce the lost-update race
// deterministically: seed status.agent in the cluster, then drive
// reconcileStatus with a stale in-memory copy whose Agent is nil (as if
// the reconciler had read the object before the heartbeat landed). The
// MergeFrom status patch touches only changed fields, so the seeded
// heartbeat survives; a full Status().Update would have reverted it.
func TestGameServer_StatusPatchPreservesAgentHeartbeat(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Seed status.agent as the in-pod sidecar's heartbeat would.
	var seeded gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "smp"}, &seeded); err != nil {
		t.Fatalf("get for seed: %v", err)
	}
	players := int32(7)
	now := metav1.Now()
	seeded.Status.Agent = &gameplanev1alpha1.AgentStatus{LastHeartbeat: &now, PlayersOnline: &players}
	if err := k8sClient.Status().Update(ctx, &seeded); err != nil {
		t.Fatalf("seed agent status: %v", err)
	}

	// Drive reconcileStatus with a STALE copy whose Agent is nil — the
	// reconciler's pre-heartbeat view. No manager runs, so this is the
	// only writer and the test is deterministic.
	stale := seeded.DeepCopy()
	stale.Status.Agent = nil
	r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
	if _, err := r.reconcileStatus(ctx, stale, idleAwake, nil, nil, tunnelPlan{}, tmpl, nil, ""); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}

	var got gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "smp"}, &got); err != nil {
		t.Fatalf("get after reconcile: %v", err)
	}
	if got.Status.Agent == nil || got.Status.Agent.PlayersOnline == nil {
		t.Fatal("reconciler clobbered status.agent — heartbeat lost")
	}
	if *got.Status.Agent.PlayersOnline != 7 {
		t.Fatalf("playersOnline = %d, want 7 (heartbeat clobbered)", *got.Status.Agent.PlayersOnline)
	}
	// And it still did its own job: phase was derived and written.
	if got.Status.Phase == "" {
		t.Fatal("reconciler did not set phase")
	}
}

// TestGameServer_RCONProvisioning — a template that exposes RCON gets a
// generated <gs>-rcon Secret, the password injected into the game
// container via the declared env var, and the same value mounted for the
// agent sidecar with --rcon-password-file.
func TestGameServer_RCONProvisioning(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("mc"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575, PasswordEnv: "RCON_PASSWORD"}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-rcon"}, &sec); err != nil {
			return false, "rcon secret: " + err.Error()
		}
		if len(sec.Data["password"]) == 0 {
			return false, "rcon secret has no password"
		}

		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var game, agent *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			c := &ss.Spec.Template.Spec.Containers[i]
			switch c.Name {
			case "game":
				game = c
			case "agent":
				agent = c
			}
		}
		if game == nil || agent == nil {
			return false, "missing containers"
		}
		// Game container: RCON_PASSWORD from the rcon Secret.
		var ok bool
		for _, e := range game.Env {
			if e.Name == "RCON_PASSWORD" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
				e.ValueFrom.SecretKeyRef.Name == "smp-rcon" && e.ValueFrom.SecretKeyRef.Key == "password" {
				ok = true
			}
		}
		if !ok {
			return false, "game container missing RCON_PASSWORD secret env"
		}
		// Agent: --rcon-password-file arg + rcon-password mount.
		hasFlag := false
		for _, a := range agent.Args {
			if a == "--rcon-password-file=/etc/gameplane/rcon/password" {
				hasFlag = true
			}
		}
		if !hasFlag {
			return false, "agent missing --rcon-password-file"
		}
		mounted := false
		for _, m := range agent.VolumeMounts {
			if m.Name == "rcon-password" {
				mounted = true
			}
		}
		if !mounted {
			return false, "agent missing rcon-password mount"
		}
		return true, ""
	})
}

// TestGameServer_RCONPort — agent container receives --rcon-port argument
// when RCON is enabled. Defaults to 25575 when spec.rcon.port is unset,
// uses the declared port when set (e.g., 27015 for factorio).
func TestGameServer_RCONPort(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	// Test 1: custom RCON port (e.g., factorio at 27015)
	tmpl := buildGameTemplate(uniqueName("factorio"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 27015, PasswordFile: "config/rconpw"}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create factorio template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "factorio-srv", tmpl.Name)); err != nil {
		t.Fatalf("create factorio gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "factorio-srv"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var agent *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "agent" {
				agent = &ss.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if agent == nil {
			return false, "no agent container"
		}
		// Check for --rcon-port=27015
		hasPort := false
		for _, a := range agent.Args {
			if a == "--rcon-port=27015" {
				hasPort = true
				break
			}
		}
		if !hasPort {
			return false, "agent missing --rcon-port=27015, got: " + strings.Join(agent.Args, " ")
		}
		return true, ""
	})

	// Test 2: default RCON port (25575) when spec.rcon.port is unset
	tmpl2 := buildGameTemplate(uniqueName("minecraft"))
	tmpl2.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", PasswordEnv: "RCON_PASSWORD"}
	// Note: Port is 0/unset, should default to 25575
	if err := k8sClient.Create(context.Background(), tmpl2); err != nil {
		t.Fatalf("create minecraft template: %v", err)
	}
	deleteCleanup(t, tmpl2)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "mc-srv", tmpl2.Name)); err != nil {
		t.Fatalf("create minecraft gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "mc-srv"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var agent *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "agent" {
				agent = &ss.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if agent == nil {
			return false, "no agent container"
		}
		// Check for --rcon-port=25575
		hasPort := false
		for _, a := range agent.Args {
			if a == "--rcon-port=25575" {
				hasPort = true
				break
			}
		}
		if !hasPort {
			return false, "agent missing --rcon-port=25575, got: " + strings.Join(agent.Args, " ")
		}
		return true, ""
	})
}

// TestGameServer_TemplateNotFound_PhaseFailed — referencing a missing
// template flips Status.Phase to Failed with a reasonable message.
func TestGameServer_TemplateNotFound_PhaseFailed(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "orphan", "does-not-exist")); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		gs := getGameServer(t, ns, "orphan")
		if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase = " + string(gs.Status.Phase)
		}
		return true, ""
	})
}

// TestGameServer_SuspendScalesToZero — Spec.Suspend=true ⇒ underlying
// StatefulSet replicas=0.
func TestGameServer_SuspendScalesToZero(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for initial create to land replicas=1.
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		return ss.Spec.Replicas != nil && *ss.Spec.Replicas == 1, ""
	})

	// Flip Suspend=true. Retry on conflict — the reconciler patches
	// status concurrently, racing a bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Suspend = true
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			r := int32(-1)
			if ss.Spec.Replicas != nil {
				r = *ss.Spec.Replicas
			}
			return false, "replicas not yet zero (got " + sprintArgs([]string{intToStr(r)}) + ")"
		}
		return true, ""
	})
}

// TestGameServer_BackupPolicyMaterializesSchedule — adding an inline
// BackupPolicy to a GameServer materializes a managed BackupSchedule.
func TestGameServer_BackupPolicyMaterializesSchedule(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.BackupPolicy = &gameplanev1alpha1.InlineBackupPolicy{
		Schedule: "0 */6 * * *",
		RepoRef:  gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "url"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var bs gameplanev1alpha1.BackupSchedule
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-auto"}, &bs); err != nil {
			return false, "get schedule: " + err.Error()
		}
		if bs.Spec.Schedule != "0 */6 * * *" {
			return false, "schedule = " + bs.Spec.Schedule
		}
		ok := false
		for _, ref := range bs.OwnerReferences {
			if ref.Kind == "GameServer" && ref.Name == "smp" && ref.Controller != nil && *ref.Controller {
				ok = true
				break
			}
		}
		if !ok {
			return false, "schedule not owned by GameServer"
		}
		return true, ""
	})
}

// TestGameServer_BackupPolicyMaterializesQuiescedSchedule covers F-045:
// InlineBackupPolicy exposes no quiesce field, so the materialized
// BackupSchedule must still get the CRD's documented default (true) rather
// than the Go zero value the typed client would otherwise send explicitly on
// the wire (defeating the apiserver's own `default: true`).
func TestGameServer_BackupPolicyMaterializesQuiescedSchedule(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.BackupPolicy = &gameplanev1alpha1.InlineBackupPolicy{
		Schedule: "0 */6 * * *",
		RepoRef:  gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "url"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var bs gameplanev1alpha1.BackupSchedule
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-auto"}, &bs); err != nil {
			return false, "get schedule: " + err.Error()
		}
		if !bs.Spec.Quiesce {
			return false, "schedule.spec.quiesce = false, want true (CRD default)"
		}
		return true, ""
	})
}

// TestGameServer_BackupPolicyRemovedDeletesSchedule — clearing
// Spec.BackupPolicy deletes the managed schedule.
func TestGameServer_BackupPolicyRemovedDeletesSchedule(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.BackupPolicy = &gameplanev1alpha1.InlineBackupPolicy{
		Schedule: "0 0 * * *",
		RepoRef:  gameplanev1alpha1.SecretKeySelector{Name: "repo", Key: "url"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var bs gameplanev1alpha1.BackupSchedule
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-auto"}, &bs)
		return err == nil, "waiting for managed schedule"
	})

	// Now drop the policy. Retry on conflict — the reconciler updates the
	// GameServer concurrently, so a bare Get+Update races its resourceVersion
	// (envtest flake: "the object has been modified").
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, "smp")
		gs.Spec.BackupPolicy = nil
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var bs gameplanev1alpha1.BackupSchedule
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-auto"}, &bs)
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		if err != nil {
			return false, err.Error()
		}
		return false, "schedule still exists"
	})
}

// TestGameServer_StorageOverride — Spec.Storage.Size overrides the
// template default and lands on the PVC.
func TestGameServer_StorageOverride(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	want := resource.MustParse("25Gi")
	gs.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{Size: want}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, err.Error()
		}
		got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if got.Cmp(want) != 0 {
			return false, "PVC size = " + got.String() + ", want " + want.String()
		}
		return true, ""
	})

	// Sanity: PVC is immutable on size, so a re-reconcile shouldn't fail.
	consistently(t, 500*time.Millisecond, func() (bool, string) {
		var pvc corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &pvc); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

// TestGameServer_ExtraVolumesProvisionedAndMounted — a template declaring
// two spec.storage.extra entries (modeling 7 Days to Die's serverfiles/
// install and .local/share/ world-saves directories, which share no safe
// common parent) gets one PVC per entry and both mounted on the GAME
// container at their own absolute MountPath — never nested under the
// primary data mount. The primary "data" PVC/mount must behave exactly as
// before, and (per the deliberate agent-container decision in
// gameserver_rcon.go's agentVolumeMounts doc comment) neither extra volume
// should be mounted into the agent sidecar.
func TestGameServer_ExtraVolumesProvisionedAndMounted(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("sdtd"))
	installSize := resource.MustParse("15Gi")
	savesSize := resource.MustParse("2Gi")
	tmpl.Spec.Storage.Extra = []gameplanev1alpha1.ExtraVolumeSpec{
		{Name: "install", MountPath: "/home/sdtdserver/serverfiles", Size: installSize},
		{Name: "saves", MountPath: "/home/sdtdserver/.local/share/7DaysToDie", Size: savesSize},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
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
		if got := mountPathOf(game, "extra-install"); got != "/home/sdtdserver/serverfiles" {
			return false, "game install mount=" + got
		}
		if got := mountPathOf(game, "extra-saves"); got != "/home/sdtdserver/.local/share/7DaysToDie" {
			return false, "game saves mount=" + got
		}
		// The primary data mount is untouched by the extra volumes.
		if got := mountPathOf(game, "data"); got != "/data" {
			return false, "game data mount=" + got
		}
		agent := containerByName(ss.Spec.Template.Spec.Containers, "agent")
		if agent == nil {
			return false, "no agent sidecar container"
		}
		if got := mountPathOf(agent, "extra-install"); got != "" {
			return false, "agent must not mount extra-install, got " + got
		}
		if got := mountPathOf(agent, "extra-saves"); got != "" {
			return false, "agent must not mount extra-saves, got " + got
		}
		// Pod volumes reference the per-extra PVCs.
		wantVols := map[string]string{
			"extra-install": "smp-extra-install",
			"extra-saves":   "smp-extra-saves",
		}
		for volName, claim := range wantVols {
			found := false
			for _, v := range ss.Spec.Template.Spec.Volumes {
				if v.Name == volName && v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim {
					found = true
				}
			}
			if !found {
				return false, "pod volume " + volName + " -> " + claim + " missing"
			}
		}
		return true, ""
	})

	// Both extra PVCs exist, sized from their ExtraVolumeSpec entry, and
	// the primary data PVC still behaves exactly as before (default size).
	eventually(t, func() (bool, string) {
		var install corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-extra-install"}, &install); err != nil {
			return false, "install pvc: " + err.Error()
		}
		if got := install.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(installSize) != 0 {
			return false, "install pvc size = " + got.String() + ", want " + installSize.String()
		}
		var saves corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-extra-saves"}, &saves); err != nil {
			return false, "saves pvc: " + err.Error()
		}
		if got := saves.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(savesSize) != 0 {
			return false, "saves pvc size = " + got.String() + ", want " + savesSize.String()
		}
		var data corev1.PersistentVolumeClaim
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-data"}, &data); err != nil {
			return false, "data pvc: " + err.Error()
		}
		wantData := resource.MustParse("10Gi")
		if got := data.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(wantData) != 0 {
			return false, "data pvc size = " + got.String() + ", want " + wantData.String()
		}
		return true, ""
	})
}

// TestGameServer_NoExtraVolumesNoCaptureComponent is the regression guard
// for the amended FR-001/SC-007 (specs/done_003-network-capture-sidecar/spec.md,
// specs/done_003-network-capture-sidecar/plan.md human Decision 2): a template
// declaring no spec.storage.extra can no longer render a pod spec
// byte-identical to before the capture feature, because the capture
// "captures" emptyDir volume is now pre-provisioned UNCONDITIONALLY on
// every game pod's StatefulSet template — opted into capture or not. This
// is a platform constraint, not a design regression: the
// pods/ephemeralcontainers subresource cannot add a volume, and
// pod.spec.volumes is immutable on a running pod, so the volume must
// already exist before the capture sidecar can be injected restart-free.
//
// What the amended FR-001 actually still guarantees — and what this test
// now asserts precisely — is the weaker, true claim: "no capture
// *component* attached". That means: the captures volume is present but is
// an empty, size-limited emptyDir; it is not mounted on ANY container
// (neither the game container nor the agent — see agentVolumeMounts' doc
// comment for why the agent cannot gain a second root); and no ephemeral
// container (the capture sidecar) exists on the pod. Asserting the mount
// absence on every container is what actually enforces "no component
// attached" and is what makes the volume-count loosening above safe rather
// than a silently weakened guarantee.
func TestGameServer_NoExtraVolumesNoCaptureComponent(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		if len(ss.Spec.Template.Spec.Volumes) != 3 {
			return false, fmt.Sprintf("pod volumes = %v, want exactly [data agent-tls captures]", sprintArgs(volumeNames(ss.Spec.Template.Spec.Volumes)))
		}
		captures := volumeByName(ss.Spec.Template.Spec.Volumes, "captures")
		if captures == nil {
			return false, "no captures volume"
		}
		if captures.EmptyDir == nil {
			return false, "captures volume is not an emptyDir"
		}
		if captures.EmptyDir.SizeLimit == nil || captures.EmptyDir.SizeLimit.IsZero() {
			return false, "captures emptyDir must carry a nonzero sizeLimit"
		}
		for _, c := range ss.Spec.Template.Spec.Containers {
			for _, vm := range c.VolumeMounts {
				if vm.Name == "captures" {
					return false, "captures volume must not be mounted on container " + c.Name
				}
			}
		}
		if len(ss.Spec.Template.Spec.EphemeralContainers) != 0 {
			return false, fmt.Sprintf("pod has %d ephemeral containers, want none (no capture component attached)", len(ss.Spec.Template.Spec.EphemeralContainers))
		}
		game := containerByName(ss.Spec.Template.Spec.Containers, gameContainerName)
		if game == nil {
			return false, "no game container"
		}
		if len(game.VolumeMounts) != 1 || game.VolumeMounts[0].Name != "data" {
			return false, fmt.Sprintf("game VolumeMounts = %v, want exactly [data]", game.VolumeMounts)
		}
		return true, ""
	})
}

// volumeByName finds a pod Volume by name, or nil if absent.
func volumeByName(vols []corev1.Volume, name string) *corev1.Volume {
	for i := range vols {
		if vols[i].Name == name {
			return &vols[i]
		}
	}
	return nil
}

// volumeNames extracts pod Volume names for assertion messages.
func volumeNames(vols []corev1.Volume) []string {
	out := make([]string, 0, len(vols))
	for _, v := range vols {
		out = append(out, v.Name)
	}
	return out
}

// TestGameServer_AgentDataRootMatchesMountPath guards against the agent
// silently rooting all its file ops (Files tab, Mods tab, disk-usage stats)
// at its own /data default when the template mounts the data volume
// somewhere else. The operator must pass --data-root resolved from the same
// value used for the "data" VolumeMount on both containers, so the two can
// never drift apart end-to-end through a real reconcile.
func TestGameServer_AgentDataRootMatchesMountPath(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	// A custom mountPath, as shipped modules like rust (/steamcmd/rust) use.
	tmpl := buildGameTemplate(uniqueName("rust"))
	tmpl.Spec.Storage.MountPath = "/steamcmd/rust"
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var agent *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "agent" {
				agent = &ss.Spec.Template.Spec.Containers[i]
			}
		}
		if agent == nil {
			return false, "no agent sidecar container"
		}
		dataRoot := ""
		hasArg := false
		for _, a := range agent.Args {
			if strings.HasPrefix(a, "--data-root=") {
				dataRoot = strings.TrimPrefix(a, "--data-root=")
				hasArg = true
			}
		}
		if !hasArg {
			return false, "agent missing --data-root arg: " + strings.Join(agent.Args, " ")
		}
		if dataRoot != "/steamcmd/rust" {
			return false, "--data-root=" + dataRoot + ", want /steamcmd/rust"
		}
		mountPath := ""
		mounted := false
		for _, m := range agent.VolumeMounts {
			if m.Name == "data" {
				mountPath = m.MountPath
				mounted = true
			}
		}
		if !mounted {
			return false, "agent missing \"data\" VolumeMount"
		}
		// The invariant that was violated: the flag must match the actual
		// mount, not just some hardcoded expectation, so the two can't drift.
		if dataRoot != mountPath {
			return false, "--data-root=" + dataRoot + " != data VolumeMount path " + mountPath
		}
		return true, ""
	})
}

// TestGameServer_LoadBalancerSourceRanges asserts the CIDR allow-list is
// applied to the fronting Service only when Expose=LoadBalancer.
func TestGameServer_LoadBalancerSourceRanges(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{
		Expose:       "LoadBalancer",
		SourceRanges: []string{"203.0.113.0/24", "10.0.0.0/8"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false, "type = " + string(svc.Spec.Type)
		}
		if !equalStrings(svc.Spec.LoadBalancerSourceRanges, []string{"203.0.113.0/24", "10.0.0.0/8"}) {
			return false, "ranges = " + strings.Join(svc.Spec.LoadBalancerSourceRanges, ",")
		}
		return true, ""
	})
}

// TestGameServer_RemovedNetworkingConverges — removing a serviceAnnotation
// and clearing the nodeSelector from the GameServer must converge the child
// Service/StatefulSet rather than leaving the removed settings active.
func TestGameServer_RemovedNetworkingConverges(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "conv", tmpl.Name)
	gs.Spec.NodeSelector = map[string]string{"disktype": "ssd"}
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{
		ServiceAnnotations: map[string]string{"k1": "v1", "k2": "v2"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Initial: both annotations present and the nodeSelector set.
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "conv"}, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Annotations["k1"] != "v1" || svc.Annotations["k2"] != "v2" {
			return false, "annotations not applied"
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "conv"}, &ss); err != nil {
			return false, err.Error()
		}
		if ss.Spec.Template.Spec.NodeSelector["disktype"] != "ssd" {
			return false, "nodeSelector not set"
		}
		return true, ""
	})

	// Remove k2 from the annotations and clear the nodeSelector.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "conv"}, &cur); err != nil {
			return err
		}
		cur.Spec.NodeSelector = nil
		cur.Spec.Networking.ServiceAnnotations = map[string]string{"k1": "v1"}
		return k8sClient.Update(context.Background(), &cur)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	// Converged: k2 pruned, k1 kept, nodeSelector cleared.
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "conv"}, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Annotations["k1"] != "v1" {
			return false, "k1 missing"
		}
		if _, ok := svc.Annotations["k2"]; ok {
			return false, "k2 not pruned"
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "conv"}, &ss); err != nil {
			return false, err.Error()
		}
		if len(ss.Spec.Template.Spec.NodeSelector) != 0 {
			return false, "nodeSelector not cleared"
		}
		return true, ""
	})
}

// TestGameServer_HostnameSetsExternalDNSAnnotation — spec.networking.hostname
// is published as the external-dns hostname annotation on the game Service and
// pruned (via the managed-annotations sentinel) once the hostname is cleared.
func TestGameServer_HostnameSetsExternalDNSAnnotation(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "dns", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{Hostname: "mc.example.com"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// The external-dns hint is stamped on the game Service and recorded as a
	// managed key so a later hostname removal converges.
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "dns"}, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Annotations[externalDNSHostnameAnnotation] != "mc.example.com" {
			return false, "external-dns annotation not set: " + svc.Annotations[externalDNSHostnameAnnotation]
		}
		if !strings.Contains(svc.Annotations[managedServiceAnnotationsKey], externalDNSHostnameAnnotation) {
			return false, "sentinel missing external-dns key: " + svc.Annotations[managedServiceAnnotationsKey]
		}
		return true, ""
	})

	// Clear the hostname: the annotation drops out of the desired set and the
	// existing prune logic removes it from the Service.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "dns"}, &cur); err != nil {
			return err
		}
		cur.Spec.Networking.Hostname = ""
		return k8sClient.Update(context.Background(), &cur)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "dns"}, &svc); err != nil {
			return false, err.Error()
		}
		if _, ok := svc.Annotations[externalDNSHostnameAnnotation]; ok {
			return false, "external-dns annotation not pruned: " + svc.Annotations[externalDNSHostnameAnnotation]
		}
		return true, ""
	})
}

// TestGameServer_HostportSetsContainerHostPort — expose: Hostport binds each
// game container port on the node (HostPort == ContainerPort) while the game
// Service stays ClusterIP.
func TestGameServer_HostportSetsContainerHostPort(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "hp", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "hp"}, &ss); err != nil {
			return false, err.Error()
		}
		var game *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == gameContainerName {
				game = &ss.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if game == nil {
			return false, "no game container yet"
		}
		if len(game.Ports) == 0 {
			return false, "game container has no ports"
		}
		for _, p := range game.Ports {
			if p.HostPort != p.ContainerPort {
				return false, "hostPort not set for port " + p.Name
			}
		}
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "hp"}, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Spec.Type != corev1.ServiceTypeClusterIP {
			return false, "service type = " + string(svc.Spec.Type)
		}
		return true, ""
	})
}

// TestGameServer_ConsoleMode_PTY — when the GameTemplate selects PTY
// console, the rendered StatefulSet's "game" container must have
// tty=true and stdin=true. These fields are immutable post-create, so
// getting them right at first reconcile is critical.
func TestGameServer_ConsoleMode_PTY(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("ptygame"))
	tmpl.Spec.ConsoleMode = "pty"
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "ptysrv", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "ptysrv"}, &ss); err != nil {
			return false, err.Error()
		}
		var game *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "game" {
				game = &ss.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if game == nil {
			return false, "no game container yet"
		}
		if !game.TTY {
			return false, "game.TTY = false, want true"
		}
		if !game.Stdin {
			return false, "game.Stdin = false, want true"
		}
		return true, ""
	})
}

// TestGameServer_ConsoleMode_RCONNoTTY — the default (RCON) console
// must NOT set tty/stdin on the game container. Setting them
// gratuitously would still work, but it changes how the container
// runtime hooks up stdio, and is a behavior the dashboard would not
// expect for the rcon path.
func TestGameServer_ConsoleMode_RCONNoTTY(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("rcongame"))
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575}
	// ConsoleMode left empty — defaults to "rcon" via EffectiveConsoleMode.
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "rconsrv", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "rconsrv"}, &ss); err != nil {
			return false, err.Error()
		}
		var game *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "game" {
				game = &ss.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if game == nil {
			return false, "no game container yet"
		}
		if game.TTY {
			return false, "game.TTY = true, want false"
		}
		if game.Stdin {
			return false, "game.Stdin = true, want false"
		}
		return true, ""
	})
}

func containerNames(cs []corev1.Container) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func intToStr(n int32) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	buf := [12]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestGameServer_AgentServiceAlwaysClusterIP — the dedicated `<gs>-agent`
// Service exists with port 8090 and stays ClusterIP even when the game's
// own Service is externally exposed via spec.networking.expose.
//
// reconcileAgentService now also adds a second port, "capture" (9091,
// numeric targetPort — ephemeral containers cannot declare a named
// containerPort), so the API can reach the network-capture sidecar's
// control endpoint through the same Service DNS name and mTLS cert SANs
// used for the agent (specs/done_003-network-capture-sidecar/plan.md). This
// test now asserts the full 2-port set precisely, rather than a single
// port, and — since CreateOrUpdate rebuilds Spec.Ports on every
// reconcile — that repeated reconciles don't accumulate duplicate ports.
func TestGameServer_AgentServiceAlwaysClusterIP(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("factorio"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Networking.Expose = "NodePort"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent"}, &svc); err != nil {
			return false, "agent service: " + err.Error()
		}
		if svc.Spec.Type != corev1.ServiceTypeClusterIP {
			return false, "agent service type = " + string(svc.Spec.Type)
		}
		if msg, ok := wantAgentServicePorts(svc.Spec.Ports); !ok {
			return false, msg
		}
		if !svc.Spec.PublishNotReadyAddresses {
			return false, "agent service must publish not-ready addresses"
		}
		if svc.Spec.Selector["app.kubernetes.io/instance"] != "smp" {
			return false, "agent service selector missing instance label"
		}
		var game corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &game); err != nil {
			return false, "game service: " + err.Error()
		}
		if game.Spec.Type != corev1.ServiceTypeNodePort {
			return false, "game service should be NodePort, got " + string(game.Spec.Type)
		}
		return true, ""
	})

	// Force a second reconcile (reconcileAgentService rebuilds Spec.Ports
	// from scratch on every CreateOrUpdate call) and confirm the port list
	// still holds exactly the same two ports rather than accumulating
	// duplicates.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		if gs.Labels == nil {
			gs.Labels = map[string]string{}
		}
		gs.Labels["capture-retest-trigger"] = "1"
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver to trigger second reconcile: %v", err)
	}

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent"}, &svc); err != nil {
			return false, "agent service: " + err.Error()
		}
		if msg, ok := wantAgentServicePorts(svc.Spec.Ports); !ok {
			return false, "after second reconcile: " + msg
		}
		return true, ""
	})
}

// wantAgentServicePorts asserts the `<gs>-agent` Service carries exactly
// the agent port (8090) and the network-capture sidecar control port
// (9091, name "capture", numeric targetPort) — no fewer, no more, and no
// duplicates, however many times reconcileAgentService has run.
func wantAgentServicePorts(ports []corev1.ServicePort) (string, bool) {
	if len(ports) != 2 {
		return fmt.Sprintf("agent service ports = %v, want exactly [agent:8090 capture:9091]", ports), false
	}
	byName := map[string]corev1.ServicePort{}
	for _, p := range ports {
		if _, dup := byName[p.Name]; dup {
			return fmt.Sprintf("agent service ports has duplicate name %q: %v", p.Name, ports), false
		}
		byName[p.Name] = p
	}
	agent, ok := byName["agent"]
	if !ok || agent.Port != 8090 || agent.TargetPort.IntValue() != 8090 {
		return fmt.Sprintf("agent service missing/wrong \"agent\" port: %v", ports), false
	}
	capture, ok := byName["capture"]
	if !ok || capture.Port != 9091 || capture.TargetPort.IntValue() != 9091 || capture.TargetPort.Type != intstr.Int {
		return fmt.Sprintf("agent service missing/wrong \"capture\" port: %v", ports), false
	}
	return "", true
}

// TestGameServer_AgentHeartbeatRBAC — the per-GameServer SA, Role, and
// RoleBinding exist, the Role is resourceNames-scoped to this server's
// status subresource, and the pod template runs as the SA (unless
// spec.serviceAccountName overrides it).
func TestGameServer_AgentHeartbeatRBAC(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("terraria"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	if err := k8sClient.Create(context.Background(), buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var sa corev1.ServiceAccount
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent"}, &sa); err != nil {
			return false, "serviceaccount: " + err.Error()
		}
		var role rbacv1.Role
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent-heartbeat"}, &role); err != nil {
			return false, "role: " + err.Error()
		}
		if len(role.Rules) != 1 {
			return false, "expected 1 rule"
		}
		rule := role.Rules[0]
		if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != "smp" {
			return false, "role not resourceNames-scoped to the GameServer"
		}
		if len(rule.Resources) != 1 || rule.Resources[0] != "gameservers/status" {
			return false, "role grants more than gameservers/status"
		}
		var rb rbacv1.RoleBinding
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-agent-heartbeat"}, &rb); err != nil {
			return false, "rolebinding: " + err.Error()
		}
		if len(rb.Subjects) != 1 || rb.Subjects[0].Name != "smp-agent" {
			return false, "rolebinding subject is not the agent SA"
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		if got := ss.Spec.Template.Spec.ServiceAccountName; got != "smp-agent" {
			return false, "pod SA = " + got
		}
		return true, ""
	})
}

// TestGameServer_ServiceAccountOverrideWins — spec.serviceAccountName
// replaces the operator-managed default on the pod template.
func TestGameServer_ServiceAccountOverrideWins(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("astroneer"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.ServiceAccountName = "my-own-sa"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		if got := ss.Spec.Template.Spec.ServiceAccountName; got != "my-own-sa" {
			return false, "pod SA = " + got
		}
		return true, ""
	})
}

// TestGameServer_ConfigMaterializesAsEnv — spec.config resolved against
// the template's configSchema lands on the game container with the
// documented precedence: template env < config < spec.env.
func TestGameServer_ConfigMaterializesAsEnv(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.Env = []corev1.EnvVar{
		{Name: "EULA", Value: "TRUE"},
		{Name: "TYPE", Value: "VANILLA"}, // config below must override this
	}
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "TYPE", Type: "enum", Enum: []string{"VANILLA", "PAPER"}, Default: "VANILLA", Required: true},
		{Name: "VERSION", Type: "string", Default: "LATEST"},
		{Name: "MAX_PLAYERS", Type: "int", Default: "20"},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Config = map[string]string{"TYPE": "PAPER"}
	gs.Spec.Env = []corev1.EnvVar{{Name: "MAX_PLAYERS", Value: "64"}} // explicit env beats config default
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		var game *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "game" {
				game = &ss.Spec.Template.Spec.Containers[i]
			}
		}
		if game == nil {
			return false, "no game container"
		}
		// Later duplicates win in the kubelet; assert on the last
		// occurrence of each name.
		got := map[string]string{}
		for _, e := range game.Env {
			got[e.Name] = e.Value
		}
		want := map[string]string{
			"EULA":        "TRUE",   // template env untouched
			"TYPE":        "PAPER",  // config overrides template env
			"VERSION":     "LATEST", // schema default applied
			"MAX_PLAYERS": "64",     // spec.env overrides config
		}
		for k, v := range want {
			if got[k] != v {
				return false, "env " + k + " = " + got[k] + ", want " + v
			}
		}
		if ss.Spec.Template.Annotations["gameplane.local/config-hash"] == "" {
			return false, "config-hash annotation missing"
		}
		return true, ""
	})
}

// TestGameServer_AutoMemoryConfigFromLimit — a configSchema field with
// autoFromMemoryLimit resolves to percent% of the server's memory limit
// when the user leaves it empty, and an explicit config value still wins.
func TestGameServer_AutoMemoryConfigFromLimit(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "MAX_MEMORY", Type: "string",
			AutoFromMemoryLimit: &gameplanev1alpha1.AutoFromMemoryLimit{Percent: 75}},
		{Name: "INIT_MEMORY", Type: "string",
			AutoFromMemoryLimit: &gameplanev1alpha1.AutoFromMemoryLimit{Percent: 50}},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "automem", tmpl.Name)
	gs.Spec.Resources = &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("8Gi")},
	}
	gs.Spec.Config = map[string]string{"INIT_MEMORY": "1G"} // explicit value beats auto
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "automem"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		got := map[string]string{}
		for _, c := range ss.Spec.Template.Spec.Containers {
			if c.Name != "game" {
				continue
			}
			for _, e := range c.Env {
				got[e.Name] = e.Value
			}
		}
		if got["MAX_MEMORY"] != "6144M" {
			return false, "MAX_MEMORY = " + got["MAX_MEMORY"] + ", want 6144M (75% of 8Gi)"
		}
		if got["INIT_MEMORY"] != "1G" {
			return false, "INIT_MEMORY = " + got["INIT_MEMORY"] + ", want the explicit 1G"
		}
		return true, ""
	})
}

// TestGameServer_InvalidConfigFailsThenRecovers — a config violating the
// schema flips phase to Failed with a pointed message and creates no
// StatefulSet; fixing spec.config materializes the server.
func TestGameServer_InvalidConfigFailsThenRecovers(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "MODE", Type: "enum", Enum: []string{"survival", "creative"}},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Config = map[string]string{"MODE": "hardcore"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getGameServer(t, ns, "smp")
		if got.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase = " + string(got.Status.Phase)
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" && strings.Contains(c.Message, "MODE") {
				return true, ""
			}
		}
		return false, "Ready condition does not mention the offending field"
	})

	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); !apierrors.IsNotFound(err) {
		t.Fatalf("StatefulSet should not exist while config is invalid, get err = %v", err)
	}

	// Retry on conflict — the reconciler patches status concurrently,
	// racing a bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Config["MODE"] = "creative"
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		got := getGameServer(t, ns, "smp")
		if got.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase still Failed after fixing config"
		}
		return true, ""
	})
}

// TestGameServer_PasswordConfigStoredInSecret — password-type config
// lands in the owned `<gs>-config` Secret and the pod spec carries only
// a SecretKeyRef; dropping the password deletes the Secret again.
func TestGameServer_PasswordConfigStoredInSecret(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("valheim"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "SERVER_PASS", Type: "password"},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Config = map[string]string{"SERVER_PASS": "hunter22"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-config"}, &sec); err != nil {
			return false, "config secret: " + err.Error()
		}
		if string(sec.Data["SERVER_PASS"]) != "hunter22" {
			return false, "secret does not hold the password"
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		for _, c := range ss.Spec.Template.Spec.Containers {
			for _, e := range c.Env {
				if e.Value == "hunter22" {
					return false, "password appears inline in pod spec env " + e.Name
				}
				if c.Name == "game" && e.Name == "SERVER_PASS" {
					if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil ||
						e.ValueFrom.SecretKeyRef.Name != "smp-config" {
						return false, "SERVER_PASS env is not a SecretKeyRef into smp-config"
					}
				}
			}
		}
		return true, ""
	})

	// Dropping the password must clean the Secret up. Retry on conflict —
	// the reconciler patches status concurrently, racing a bare
	// Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Config = nil
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-config"}, &sec)
		if err == nil {
			return false, "config secret still exists"
		}
		if !apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		return true, ""
	})
}

// TestGameServer_ConfigFilesMaterialize — target:file config renders
// into the owned `<gs>-files` Secret, wires the config-init container
// and the config-files volume, never leaks into env, and is torn down
// again when the template stops declaring configFiles.
func TestGameServer_ConfigFilesMaterialize(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("terraria"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "MOTD", Type: "string", Target: "file", Default: "hello"},
		{Name: "SERVER_PASS", Type: "password", Target: "file"},
	}
	tmpl.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{
		Path:     "cfg/server.cfg",
		Template: "motd={{ .Values.MOTD }}\npass={{ .Values.SERVER_PASS }}\n",
	}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Config = map[string]string{"SERVER_PASS": "hunter22"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	firstHash := ""
	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-files"}, &sec); err != nil {
			return false, "files secret: " + err.Error()
		}
		if got := string(sec.Data["file-0"]); got != "motd=hello\npass=hunter22\n" {
			return false, "file-0 content = " + got
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		inits := ss.Spec.Template.Spec.InitContainers
		if len(inits) != 1 || inits[0].Name != "config-init" {
			return false, "init containers missing config-init"
		}
		var vol *corev1.Volume
		for i := range ss.Spec.Template.Spec.Volumes {
			if ss.Spec.Template.Spec.Volumes[i].Name == "config-files" {
				vol = &ss.Spec.Template.Spec.Volumes[i]
			}
		}
		if vol == nil || vol.Secret == nil || vol.Secret.SecretName != "smp-files" {
			return false, "config-files volume not backed by smp-files"
		}
		if len(vol.Secret.Items) != 1 || vol.Secret.Items[0].Key != "file-0" ||
			vol.Secret.Items[0].Path != "cfg/server.cfg" {
			return false, "volume items do not map file-0 to cfg/server.cfg"
		}
		for _, c := range ss.Spec.Template.Spec.Containers {
			for _, e := range c.Env {
				if e.Name == "MOTD" || e.Name == "SERVER_PASS" || e.Value == "hunter22" {
					return false, "file-target value leaked into env " + e.Name + " of " + c.Name
				}
			}
		}
		firstHash = ss.Spec.Template.Annotations["gameplane.local/config-hash"]
		if firstHash == "" {
			return false, "config-hash annotation missing"
		}
		return true, ""
	})

	// Changing a file-target value must re-render the Secret and roll
	// the pod template hash. Retry on conflict — the reconciler patches
	// status concurrently, so a bare Get+Update races its resourceVersion
	// (envtest flake: "the object has been modified").
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Config["MOTD"] = "welcome"
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		var sec corev1.Secret
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-files"}, &sec); err != nil {
			return false, "files secret: " + err.Error()
		}
		if got := string(sec.Data["file-0"]); got != "motd=welcome\npass=hunter22\n" {
			return false, "file-0 content = " + got
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		if ss.Spec.Template.Annotations["gameplane.local/config-hash"] == firstHash {
			return false, "config-hash did not roll"
		}
		return true, ""
	})

	// Dropping configFiles from the template must delete the Secret and
	// strip the init container + volume. Template edits don't trigger a
	// GameServer reconcile (no cross-watch), so poke the GameServer.
	// Retry on conflict — reconcilers patch status concurrently, racing a
	// bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		tmplNow := &gameplanev1alpha1.GameTemplate{}
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Name: tmpl.Name}, tmplNow); err != nil {
			return err
		}
		tmplNow.Spec.ConfigSchema = nil
		tmplNow.Spec.ConfigFiles = nil
		return k8sClient.Update(context.Background(), tmplNow)
	}); err != nil {
		t.Fatalf("update template: %v", err)
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Config = nil
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		// Re-poke each poll: the reconcile racing the template cache
		// update must not strand the test.
		poke := getGameServer(t, ns, "smp")
		if poke.Annotations == nil {
			poke.Annotations = map[string]string{}
		}
		poke.Annotations["test.gameplane.local/poke"] = time.Now().Format(time.RFC3339Nano)
		_ = k8sClient.Update(context.Background(), poke)

		var sec corev1.Secret
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-files"}, &sec)
		if err == nil {
			return false, "files secret still exists"
		}
		if !apierrors.IsNotFound(err) {
			return false, err.Error()
		}
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, "statefulset: " + err.Error()
		}
		if len(ss.Spec.Template.Spec.InitContainers) != 0 {
			return false, "config-init container still present"
		}
		for _, v := range ss.Spec.Template.Spec.Volumes {
			if v.Name == "config-files" {
				return false, "config-files volume still present"
			}
		}
		return true, ""
	})
}

// TestGameServer_BadConfigFileTemplateFails — a template that fails to
// render flips the GameServer to Failed with a pointed message and
// creates no StatefulSet; fixing the template recovers.
func TestGameServer_BadConfigFileTemplateFails(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("terraria"))
	tmpl.Spec.ConfigFiles = []gameplanev1alpha1.ConfigFile{{
		Path:     "server.cfg",
		Template: "{{ .Values.NO_SUCH_FIELD }}",
	}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getGameServer(t, ns, "smp")
		if got.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
			return false, "phase = " + string(got.Status.Phase)
		}
		for _, c := range got.Status.Conditions {
			if c.Type == "Ready" && strings.Contains(c.Message, "server.cfg") {
				return true, ""
			}
		}
		return false, "Ready condition does not mention the offending file"
	})

	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); !apierrors.IsNotFound(err) {
		t.Fatalf("StatefulSet should not exist while the template is broken, get err = %v", err)
	}
}

// TestGameServer_ConfigChangeRollsPodTemplate — editing a config value
// must change the pod template (env + hash annotation) so the
// StatefulSet rolls the pod.
func TestGameServer_ConfigChangeRollsPodTemplate(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "DIFFICULTY", Type: "string", Default: "easy"},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	firstHash := ""
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		firstHash = ss.Spec.Template.Annotations["gameplane.local/config-hash"]
		return firstHash != "", "config-hash annotation missing"
	})

	// Retry on conflict — the reconciler patches status concurrently,
	// racing a bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Config = map[string]string{"DIFFICULTY": "hard"}
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("update gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp"}, &ss); err != nil {
			return false, err.Error()
		}
		hash := ss.Spec.Template.Annotations["gameplane.local/config-hash"]
		if hash == firstHash {
			return false, "config-hash unchanged"
		}
		var game *corev1.Container
		for i := range ss.Spec.Template.Spec.Containers {
			if ss.Spec.Template.Spec.Containers[i].Name == "game" {
				game = &ss.Spec.Template.Spec.Containers[i]
			}
		}
		got := map[string]string{}
		for _, e := range game.Env {
			got[e.Name] = e.Value
		}
		if got["DIFFICULTY"] != "hard" {
			return false, "DIFFICULTY = " + got["DIFFICULTY"]
		}
		return true, ""
	})
}

// gameContainerEnv fetches the reconciled StatefulSet's "game" container
// env as the raw slice, in declaration order. Deliberately NOT collapsed
// into a name->value map here: a map can't see a duplicate entry (the
// mods-by-id projection once appended a shadow duplicate instead of
// replacing in place — see modIDListEnv), so callers that care about
// "exactly one entry" must use envCount/envValue below on this slice
// instead of losing that information to a map.
//
// It returns an error instead of calling t.Fatal on a miss: callers poll
// this from inside an eventually() condition, and eventually's cond runs
// in the test's own goroutine — a t.Fatal in there would abort the whole
// test on the very first (pre-reconcile) attempt instead of letting the
// poll retry.
func gameContainerEnv(t *testing.T, ns, name string) ([]corev1.EnvVar, error) {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		return nil, fmt.Errorf("get statefulset: %w", err)
	}
	for _, c := range ss.Spec.Template.Spec.Containers {
		if c.Name == gameContainerName {
			return c.Env, nil
		}
	}
	return nil, nil
}

// envCount returns how many entries in env are named name — used to
// assert "exactly one", a property a name->value map can't express since
// it silently collapses duplicates.
func envCount(env []corev1.EnvVar, name string) int {
	n := 0
	for _, e := range env {
		if e.Name == name {
			n++
		}
	}
	return n
}

// envValue returns the value of the LAST entry named name in env — the
// kubelet's own duplicate-resolution rule — and whether one was found.
func envValue(env []corev1.EnvVar, name string) (string, bool) {
	var val string
	var ok bool
	for _, e := range env {
		if e.Name == name {
			val, ok = e.Value, true
		}
	}
	return val, ok
}

// TestGameServer_ModIDList_ReplaceMode — a template declaring
// capabilities.mods.idList in (default) replace mode gets its env var set
// to the GameServer's selected ids, joined with the default "," separator.
func TestGameServer_ModIDList_ReplaceMode(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("zomboid"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			IDList: &gameplanev1alpha1.ModIDListSpec{Env: "MOD_IDS"},
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "zomboid", tmpl.Name)
	gs.Spec.Mods = &gameplanev1alpha1.GameServerModsSpec{
		IDs: []gameplanev1alpha1.ModRef{{ID: "111"}, {ID: "222"}},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		got, err := gameContainerEnv(t, ns, "zomboid")
		if err != nil {
			return false, err.Error()
		}
		if v, ok := envValue(got, "MOD_IDS"); !ok || v != "111,222" {
			return false, fmt.Sprintf("MOD_IDS = %q (present=%v), want \"111,222\"", v, ok)
		}
		// Guards the modIDListEnv duplicate-env regression: a stray
		// shadow entry is invisible to a name->value map but must still
		// fail this test.
		if n := envCount(got, "MOD_IDS"); n != 1 {
			return false, fmt.Sprintf("MOD_IDS appears %d times, want exactly 1", n)
		}
		return true, ""
	})
}

// TestGameServer_ModIDList_AppendMode — an ARK-shaped template (append
// mode, a config-schema-provided launch string) keeps the config value
// and gains the rendered mod flag onto the end of it.
func TestGameServer_ModIDList_AppendMode(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("ark"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{
		{Name: "ASA_START_PARAMS", Type: "string", Default: "TheIsland_WP?listen"},
	}
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			IDList: &gameplanev1alpha1.ModIDListSpec{
				Env:    "ASA_START_PARAMS",
				Format: " -mods={{ids}}",
				Mode:   "append",
			},
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "ark", tmpl.Name)
	gs.Spec.Mods = &gameplanev1alpha1.GameServerModsSpec{
		IDs: []gameplanev1alpha1.ModRef{{ID: "111"}, {ID: "222"}},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	want := "TheIsland_WP?listen -mods=111,222"
	eventually(t, func() (bool, string) {
		got, err := gameContainerEnv(t, ns, "ark")
		if err != nil {
			return false, err.Error()
		}
		if v, ok := envValue(got, "ASA_START_PARAMS"); !ok || v != want {
			return false, fmt.Sprintf("ASA_START_PARAMS = %q (present=%v), want %q", v, ok, want)
		}
		// This is the exact shape of the live-cluster bug: the
		// config-schema default and the mods-by-id projection share a
		// name (ASA_START_PARAMS). modIDListEnv must merge them into one
		// entry, not append a second one that only the kubelet's
		// last-wins rule happens to paper over.
		if n := envCount(got, "ASA_START_PARAMS"); n != 1 {
			return false, fmt.Sprintf("ASA_START_PARAMS appears %d times, want exactly 1", n)
		}
		return true, ""
	})
}

// TestGameServer_ModIDList_NoIDsProjectsNothing — a template declaring
// idList but a GameServer with no selected mods must leave the target env
// var completely absent, not set to an empty string (an empty
// "-mods=" would break games like ARK that don't tolerate a trailing
// empty flag).
func TestGameServer_ModIDList_NoIDsProjectsNothing(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("zomboid"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			IDList: &gameplanev1alpha1.ModIDListSpec{Env: "MOD_IDS"},
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// No spec.mods at all — the common case for a server that just never
	// picked any mods.
	gs := buildGameServer(ns, "noids", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for the StatefulSet to exist (reconciliation settled) before
	// asserting the negative — otherwise a not-yet-reconciled object
	// would pass a "key absent" check for the wrong reason.
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "noids"}, &ss); err != nil {
			return false, err.Error()
		}
		return len(ss.Spec.Template.Spec.Containers) > 0, "statefulset not yet populated"
	})

	got, err := gameContainerEnv(t, ns, "noids")
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if v, ok := envValue(got, "MOD_IDS"); ok {
		t.Fatalf("MOD_IDS should not be projected with no selected ids, got %q", v)
	}
}

// TestGameServer_ModIDList_CustomSeparator — a non-default Separator is
// honored when joining ids.
func TestGameServer_ModIDList_CustomSeparator(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("zomboid"))
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{
		Mods: &gameplanev1alpha1.ModsSpec{
			IDList: &gameplanev1alpha1.ModIDListSpec{Env: "MOD_IDS", Separator: ";"},
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "sepzomboid", tmpl.Name)
	gs.Spec.Mods = &gameplanev1alpha1.GameServerModsSpec{
		IDs: []gameplanev1alpha1.ModRef{{ID: "111"}, {ID: "222"}, {ID: "333"}},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		got, err := gameContainerEnv(t, ns, "sepzomboid")
		if err != nil {
			return false, err.Error()
		}
		if v, ok := envValue(got, "MOD_IDS"); !ok || v != "111;222;333" {
			return false, fmt.Sprintf("MOD_IDS = %q (present=%v), want \"111;222;333\"", v, ok)
		}
		if n := envCount(got, "MOD_IDS"); n != 1 {
			return false, fmt.Sprintf("MOD_IDS appears %d times, want exactly 1", n)
		}
		return true, ""
	})
}

// ---------------------------------------------------------------------
// reconcileNetworkPolicy — per-GameServer ingress NetworkPolicy
//
// These tests call r.reconcileNetworkPolicy directly against the envtest
// client instead of going through startMgr/Reconcile (the same direct-call
// style TestGameServer_StatusPatchPreservesAgentHeartbeat uses above): the
// method takes *GameTemplate as a plain argument rather than fetching it
// itself, and GameIngressPolicyEnabled/GameIngressFromCIDRs need to vary
// per test, which the shared withGameServerReconciler helper (fixed fields,
// used by every other GameServer envtest) doesn't support. The GameTemplate
// in every test below is deliberately NOT persisted via k8sClient.Create:
// reconcileNetworkPolicy never fetches it from the API server (the
// Reconcile loop does that once and passes the result in), so an in-memory
// struct is sufficient — and for the Protocol-defaulting test it's
// required, since the GameTemplate CRD carries +kubebuilder:default=TCP on
// spec.ports[].protocol and the real apiserver would fill an empty
// Protocol in on admission, masking the controller's own fallback.
// ---------------------------------------------------------------------

// npPortKey is a comparable (protocol, port) pair used to assert on a
// NetworkPolicy's Ports list as a set, independent of slice order.
type npPortKey struct {
	proto corev1.Protocol
	port  int32
}

func npPortKeySet(ports []networkingv1.NetworkPolicyPort) map[npPortKey]bool {
	out := make(map[npPortKey]bool, len(ports))
	for _, p := range ports {
		if p.Protocol == nil || p.Port == nil {
			continue
		}
		out[npPortKey{proto: *p.Protocol, port: p.Port.IntVal}] = true
	}
	return out
}

// TestGameServer_NetworkPolicyAdvertisedPortsOnly proves reconcileNetworkPolicy's
// core shape: a template mixing an advertised TCP port, an advertised UDP
// port, and a non-advertised RCON port produces a NetworkPolicy whose Ports
// list contains exactly the two advertised ports (UDP preserved, RCON
// absent), scoped to this GameServer's pods only (podSelector name+instance
// labels), and owned by it via a controller ownerRef.
func TestGameServer_NetworkPolicyAdvertisedPortsOnly(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("mixedports"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
		{Name: "voice", ContainerPort: 24454, Protocol: corev1.ProtocolUDP, Advertise: true},
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	}

	if err := k8sClient.Create(ctx, buildGameServer(ns, "smp", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "smp"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	r := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: true,
		GameIngressFromCIDRs:     []string{"10.0.0.0/8", "192.168.1.0/24"},
	}
	if err := r.reconcileNetworkPolicy(ctx, &gs, tmpl); err != nil {
		t.Fatalf("reconcileNetworkPolicy: %v", err)
	}

	var np networkingv1.NetworkPolicy
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "smp-game-ingress"}, &np); err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}

	sel := np.Spec.PodSelector.MatchLabels
	if len(sel) != 2 || sel["app.kubernetes.io/name"] != "gameplane-game" || sel["app.kubernetes.io/instance"] != "smp" {
		t.Fatalf("podSelector.matchLabels = %+v, want name=gameplane-game instance=smp", sel)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Fatalf("policyTypes = %v, want [Ingress]", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("ingress rules = %d, want 1", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]

	if len(rule.From) != 2 {
		t.Fatalf("From has %d peers, want 2: %+v", len(rule.From), rule.From)
	}
	for i, wantCIDR := range []string{"10.0.0.0/8", "192.168.1.0/24"} {
		if rule.From[i].IPBlock == nil || rule.From[i].IPBlock.CIDR != wantCIDR {
			t.Fatalf("From[%d] = %+v, want IPBlock CIDR %q", i, rule.From[i], wantCIDR)
		}
	}

	got := npPortKeySet(rule.Ports)
	if len(rule.Ports) != 2 || len(got) != 2 {
		t.Fatalf("ports = %+v, want exactly 2 entries", rule.Ports)
	}
	if !got[npPortKey{corev1.ProtocolTCP, 25565}] {
		t.Fatalf("ports missing advertised TCP game port 25565: %+v", rule.Ports)
	}
	if !got[npPortKey{corev1.ProtocolUDP, 24454}] {
		t.Fatalf("ports missing advertised UDP port 24454: %+v", rule.Ports)
	}
	if got[npPortKey{corev1.ProtocolTCP, 25575}] {
		t.Fatalf("non-advertised RCON port 25575 leaked into policy: %+v", rule.Ports)
	}

	if len(np.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences = %+v, want exactly 1", np.OwnerReferences)
	}
	owner := np.OwnerReferences[0]
	if owner.Kind != "GameServer" || owner.Name != "smp" || owner.UID != gs.UID {
		t.Fatalf("owner = %+v, want Kind=GameServer Name=smp UID=%s", owner, gs.UID)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("owner.Controller = %v, want true (cascade delete)", owner.Controller)
	}
}

// TestGameServer_NetworkPolicyPortProtocolDefaultsToTCP proves a template
// port with an unset Protocol renders as TCP in the policy. The
// GameTemplate is intentionally never persisted (see file-header comment
// above) so the field really does reach networkPolicyPortsFromTemplate
// empty, exercising the controller's own fallback rather than the CRD's
// admission-time default.
func TestGameServer_NetworkPolicyPortProtocolDefaultsToTCP(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("noproto"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 7777, Advertise: true}, // Protocol left "" on purpose.
	}

	if err := k8sClient.Create(ctx, buildGameServer(ns, "defproto", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "defproto"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	r := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: true,
		GameIngressFromCIDRs:     []string{"0.0.0.0/0"},
	}
	if err := r.reconcileNetworkPolicy(ctx, &gs, tmpl); err != nil {
		t.Fatalf("reconcileNetworkPolicy: %v", err)
	}

	var np networkingv1.NetworkPolicy
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "defproto-game-ingress"}, &np); err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 {
		t.Fatalf("ingress = %+v, want exactly 1 rule with 1 port", np.Spec.Ingress)
	}
	p := np.Spec.Ingress[0].Ports[0]
	if p.Protocol == nil || *p.Protocol != corev1.ProtocolTCP {
		t.Fatalf("protocol = %v, want TCP (default)", p.Protocol)
	}
	if p.Port == nil || p.Port.IntVal != 7777 {
		t.Fatalf("port = %v, want 7777", p.Port)
	}
}

// TestGameServer_NetworkPolicyZeroAdvertisedPortsNoPolicy pins the critical
// security case documented on reconcileNetworkPolicy: a Kubernetes
// NetworkPolicy ingress rule with a From but an empty/absent Ports list
// means "all ports open", not "no ports". A template that advertises
// nothing (e.g. only RCON/query) must therefore produce NO NetworkPolicy
// at all, never one with an empty Ports slice.
func TestGameServer_NetworkPolicyZeroAdvertisedPortsNoPolicy(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("hiddenports"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
		{Name: "query", ContainerPort: 25566, Protocol: corev1.ProtocolUDP, Advertise: false},
	}

	if err := k8sClient.Create(ctx, buildGameServer(ns, "noadvertise", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "noadvertise"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	r := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: true,
		GameIngressFromCIDRs:     []string{"0.0.0.0/0"},
	}
	if err := r.reconcileNetworkPolicy(ctx, &gs, tmpl); err != nil {
		t.Fatalf("reconcileNetworkPolicy: %v", err)
	}

	var np networkingv1.NetworkPolicy
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "noadvertise-game-ingress"}, &np)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for a template with zero advertised ports, got err=%v np=%+v", err, np)
	}
}

// TestGameServer_NetworkPolicyDisabledRemovesExisting proves toggling
// GameIngressPolicyEnabled off converges an existing GameServer to no
// policy — not just skips creating new ones. Same GameServer/GameTemplate,
// reconciled once with the feature enabled (policy created) and once with
// it disabled (policy deleted).
func TestGameServer_NetworkPolicyDisabledRemovesExisting(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("toggleports"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
	}

	if err := k8sClient.Create(ctx, buildGameServer(ns, "toggle", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "toggle"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	npKey := types.NamespacedName{Namespace: ns, Name: "toggle-game-ingress"}

	enabled := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: true,
		GameIngressFromCIDRs:     []string{"0.0.0.0/0"},
	}
	if err := enabled.reconcileNetworkPolicy(ctx, &gs, tmpl); err != nil {
		t.Fatalf("reconcileNetworkPolicy (enabled): %v", err)
	}
	var np networkingv1.NetworkPolicy
	if err := k8sClient.Get(ctx, npKey, &np); err != nil {
		t.Fatalf("policy should exist after an enabled reconcile: %v", err)
	}

	disabled := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: false,
		GameIngressFromCIDRs:     []string{"0.0.0.0/0"},
	}
	if err := disabled.reconcileNetworkPolicy(ctx, &gs, tmpl); err != nil {
		t.Fatalf("reconcileNetworkPolicy (disabled): %v", err)
	}

	err := k8sClient.Get(ctx, npKey, &np)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected policy deleted after disabling the feature, got err=%v np=%+v", err, np)
	}
}

// TestGameServer_TemplateAdvertiseFalseSurvivesTypedRoundTrip guards against
// the inverse mistake to the rest of this section: unlike the tests above,
// which deliberately keep the GameTemplate in memory because
// reconcileNetworkPolicy never re-fetches it, this test's whole point is to
// force the template through k8sClient.Create — the exact typed-client
// marshal path module_controller.go uses when it persists a bundle's
// template.yaml (see gametemplate_types.go's GamePort.Advertise doc comment).
//
// GamePort.Advertise carries `+kubebuilder:default=true`. If its json tag
// ever regains `omitempty`, a Go `false` marshals to no key at all, the
// apiserver re-applies the `true` default on admission, and this test's
// first assertion (the read-back Advertise) fails before reconciliation
// even starts. With the tag fixed (no omitempty), `false` is sent
// explicitly and round-trips correctly, and the RCON port then never
// reaches networkPolicyPortsFromTemplate.
func TestGameServer_TemplateAdvertiseFalseSurvivesTypedRoundTrip(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	tmpl := buildGameTemplate(uniqueName("typedadvertise"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	}

	// The typed Create is the step the rest of this file's tests skip.
	// This is precisely where omitempty would drop "advertise" for the
	// rcon port and let CRD defaulting flip it back to true.
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Read the template back through the same typed client a reconciler
	// would use, rather than trusting the in-memory struct above.
	var persisted gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &persisted); err != nil {
		t.Fatalf("get template: %v", err)
	}
	var rconPort *gameplanev1alpha1.GamePort
	for i := range persisted.Spec.Ports {
		if persisted.Spec.Ports[i].Name == "rcon" {
			rconPort = &persisted.Spec.Ports[i]
		}
	}
	if rconPort == nil {
		t.Fatalf("persisted template lost its rcon port entirely: %+v", persisted.Spec.Ports)
	}
	if rconPort.Advertise {
		t.Fatalf("rcon port read back Advertise=true, want false (CRD defaulting flipped a typed-client-omitted field)")
	}

	if err := k8sClient.Create(ctx, buildGameServer(ns, "typedadv", tmpl.Name)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "typedadv"}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}

	r := &GameServerReconciler{
		Client:                   k8sClient,
		APIReader:                k8sClient,
		Scheme:                   scheme,
		GameIngressPolicyEnabled: true,
		GameIngressFromCIDRs:     []string{"0.0.0.0/0"},
	}
	// Reconcile against the persisted (round-tripped) template, not the
	// original in-memory one — that's the object a real reconcile loop
	// would have fetched.
	if err := r.reconcileNetworkPolicy(ctx, &gs, &persisted); err != nil {
		t.Fatalf("reconcileNetworkPolicy: %v", err)
	}

	var np networkingv1.NetworkPolicy
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "typedadv-game-ingress"}, &np); err != nil {
		t.Fatalf("get networkpolicy: %v", err)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("ingress rules = %d, want 1", len(np.Spec.Ingress))
	}
	got := npPortKeySet(np.Spec.Ingress[0].Ports)
	if len(np.Spec.Ingress[0].Ports) != 1 || len(got) != 1 {
		t.Fatalf("ports = %+v, want exactly 1 entry (game only)", np.Spec.Ingress[0].Ports)
	}
	if !got[npPortKey{corev1.ProtocolTCP, 25565}] {
		t.Fatalf("ports missing advertised TCP game port 25565: %+v", np.Spec.Ingress[0].Ports)
	}
	if got[npPortKey{corev1.ProtocolTCP, 25575}] {
		t.Fatalf("non-advertised RCON port 25575 leaked into policy after a typed-client round trip: %+v", np.Spec.Ingress[0].Ports)
	}
}

// TestGameServer_TunnelCreatesDeploymentAndPolicy tests that tunnel configuration
// creates a tunnel Deployment and the corresponding NetworkPolicy for egress.
func TestGameServer_TunnelCreatesDeploymentAndPolicy(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("tunnel-test"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "tunnel-gs", tmpl.Name)
	// Enable frp tunnel
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		Enabled:              true,
		Provider:             "frp",
		CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "tunnel-gs-tunnel-creds"},
		Frp: &gameplanev1alpha1.FrpTunnelSpec{
			ServerAddr:  "tunnel.example.com",
			ServerPort:  7000,
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "default", RemotePort: 25565}},
		},
	}

	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Verify tunnel Deployment is created
	eventually(t, func() (bool, string) {
		var dep appsv1.Deployment
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "tunnel-gs-tunnel"}, &dep); err != nil {
			return false, "tunnel deployment: " + err.Error()
		}
		if len(dep.Spec.Template.Spec.Containers) == 0 {
			return false, "tunnel deployment has no containers"
		}
		if dep.Spec.Template.Spec.Containers[0].Name != "tunnel" {
			return false, "container name is not 'tunnel'"
		}
		return true, ""
	})

	// Verify tunnel NetworkPolicy is created
	eventually(t, func() (bool, string) {
		var np networkingv1.NetworkPolicy
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "tunnel-gs-tunnel-egress"}, &np); err != nil {
			return false, "tunnel network policy: " + err.Error()
		}
		if len(np.Spec.Egress) == 0 {
			return false, "tunnel policy has no egress rules"
		}
		return true, ""
	})
}

// TestGameServer_TunnelPlayitPolicyAllowsAllPorts verifies that the tunnel
// NetworkPolicy rendered for the playit provider actually grants the "all
// ports" egress its comment promises (F-262): playit's control-plane and
// relay endpoints aren't published on a fixed set of ports, so unlike frp
// (ServerPort) and tailscale (443/41641) it gets a second egress rule with
// no Ports (all ports/protocols) and no To (any destination), in addition
// to -- not instead of -- the DNS/advertised-port rule every provider gets.
func TestGameServer_TunnelPlayitPolicyAllowsAllPorts(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("tunnel-playit-test"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "tunnel-playit-gs", tmpl.Name)
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		Enabled:              true,
		Provider:             "playit",
		CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "tunnel-playit-gs-tunnel-creds"},
		Playit:               &gameplanev1alpha1.PlayitTunnelSpec{},
	}

	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var np networkingv1.NetworkPolicy
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "tunnel-playit-gs-tunnel-egress"}, &np); err != nil {
			return false, "tunnel network policy: " + err.Error()
		}
		if len(np.Spec.Egress) != 2 {
			return false, fmt.Sprintf("egress rules = %d, want 2 (dns/advertised-ports + all-ports): %+v", len(np.Spec.Egress), np.Spec.Egress)
		}

		// The first rule (DNS + advertised ports) must be unchanged by the
		// playit branch: no provider-specific ports leak into it.
		first := np.Spec.Egress[0]
		if len(first.Ports) == 0 {
			return false, "first egress rule has no ports, want DNS ports at least"
		}
		if len(first.To) != 0 {
			return false, fmt.Sprintf("first egress rule restricts To=%+v, want any destination", first.To)
		}

		// The second rule is playit's "all ports" grant: no Ports field
		// (meaning all ports/protocols per NetworkPolicy semantics) and no
		// To field (any destination), matching how frp/tailscale pick
		// destinations.
		second := np.Spec.Egress[1]
		if len(second.Ports) != 0 {
			return false, fmt.Sprintf("second egress rule has Ports=%+v, want none (all ports)", second.Ports)
		}
		if len(second.To) != 0 {
			return false, fmt.Sprintf("second egress rule restricts To=%+v, want any destination", second.To)
		}
		return true, ""
	})
}

// TestGameServer_TunnelFrpPortCarriesLocalPortAndProtocol verifies the
// BACKING_SERVICE_PORT env var reaching the tunnel Deployment carries the
// template port's own containerPort and protocol, not just the
// operator-chosen public remotePort (F-052). Uses a UDP port with a
// remotePort that deliberately differs from the Service port, the exact
// combination that silently broke before the fix.
func TestGameServer_TunnelFrpPortCarriesLocalPortAndProtocol(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("tunnel-udp-test"))
	tmpl.Spec.Ports[0].ContainerPort = 34197
	tmpl.Spec.Ports[0].Protocol = corev1.ProtocolUDP
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "tunnel-udp-gs", tmpl.Name)
	gs.Spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		Enabled:              true,
		Provider:             "frp",
		CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "tunnel-udp-gs-tunnel-creds"},
		Frp: &gameplanev1alpha1.FrpTunnelSpec{
			ServerAddr:  "tunnel.example.com",
			ServerPort:  7000,
			RemotePorts: []gameplanev1alpha1.RemotePortMapping{{Name: "game", RemotePort: 30000}},
		},
	}

	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		var dep appsv1.Deployment
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "tunnel-udp-gs-tunnel"}, &dep); err != nil {
			return false, "tunnel deployment: " + err.Error()
		}
		if len(dep.Spec.Template.Spec.Containers) == 0 {
			return false, "tunnel deployment has no containers"
		}
		for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
			if e.Name == "BACKING_SERVICE_PORT" {
				want := "game:34197:30000:udp"
				if e.Value != want {
					return false, "BACKING_SERVICE_PORT = " + e.Value + ", want " + want
				}
				return true, ""
			}
		}
		return false, "no BACKING_SERVICE_PORT env var on tunnel container"
	})
}

// TestGameServer_PlanTunnelComputes verifies that planTunnel correctly
// computes endpoints for frp tunnel provider.
func TestGameServer_PlanTunnelComputes(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gs", Namespace: "default"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "test-tmpl"},
			Networking: gameplanev1alpha1.GameServerNetworking{
				Tunnel: &gameplanev1alpha1.GameServerTunnel{
					Enabled:  true,
					Provider: "frp",
					Frp: &gameplanev1alpha1.FrpTunnelSpec{
						ServerAddr: "tunnel.example.com",
						ServerPort: 7000,
						RemotePorts: []gameplanev1alpha1.RemotePortMapping{
							{Name: "default", RemotePort: 25565},
						},
					},
				},
			},
		},
	}

	tmpl := &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tmpl"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{
				{Name: "default", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true},
			},
		},
	}

	r := &GameServerReconciler{}
	plan := r.planTunnel(context.Background(), gs, tmpl)

	if !plan.wantTunnel {
		t.Fatal("expected wantTunnel to be true")
	}
	if len(plan.endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(plan.endpoints))
	}
	if plan.endpoints[0].Host != "tunnel.example.com" {
		t.Fatalf("expected host to be 'tunnel.example.com', got %q", plan.endpoints[0].Host)
	}
	if plan.endpoints[0].Port != 25565 {
		t.Fatalf("expected port to be 25565, got %d", plan.endpoints[0].Port)
	}
}

// TestGameServer_PlanTunnelDisabledReturnsEmpty verifies that planTunnel
// returns an empty plan when tunnel is disabled.
func TestGameServer_PlanTunnelDisabledReturnsEmpty(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gs", Namespace: "default"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "test-tmpl"},
			Networking: gameplanev1alpha1.GameServerNetworking{
				Tunnel: nil,
			},
		},
	}

	tmpl := &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tmpl"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Ports: []gameplanev1alpha1.GamePort{},
		},
	}

	r := &GameServerReconciler{}
	plan := r.planTunnel(context.Background(), gs, tmpl)

	if plan.wantTunnel {
		t.Fatal("expected wantTunnel to be false")
	}
	if len(plan.endpoints) != 0 {
		t.Fatalf("expected no endpoints, got %d", len(plan.endpoints))
	}
}

// ---------------------------------------------------------------------
// Address-pool override — spec.networking.addressPool / .address
//
// Two tiers here. The CRD-validation test goes through the real apiserver
// (that is the only thing that can prove the generated schema's bounds are
// live). The translation tests call r.reconcileService directly against the
// envtest client, the same direct-call style the reconcileNetworkPolicy
// block above uses and for the same reason: AddressManager has to vary per
// case, which the shared withGameServerReconciler helper (fixed fields)
// doesn't support. The GameTemplate is not persisted — reconcileService
// takes it as a plain argument and never fetches it.
// ---------------------------------------------------------------------

// addressManagerAnnotationKeys is every Service annotation an address-manager
// branch can write. Cases assert on the whole set — the keys a case wants
// present *and* the ones it wants absent — so a translation leaking into the
// wrong flavor's key fails instead of going unnoticed.
var addressManagerAnnotationKeys = []string{
	metalLBAddressPoolAnnotation,
	metalLBLoadBalancerIPsAnnotation,
	ciliumAddressAnnotation,
}

// newAddressPoolServer persists a uniquely-named GameServer carrying the
// given networking preference and returns a reconciler pinned to the named
// address-manager flavor, the GameServer as the apiserver stored it (needed
// for its UID, which SetControllerReference reads), and an in-memory
// GameTemplate.
func newAddressPoolServer(
	ctx context.Context, t *testing.T, ns, manager string, net gameplanev1alpha1.GameServerNetworking,
) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()

	tmpl := buildGameTemplate(uniqueName("pooltmpl"))
	gs := buildGameServer(ns, uniqueName("pool"), tmpl.Name)
	gs.Spec.Networking = net
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	var cur gameplanev1alpha1.GameServer
	if err := k8sClient.Get(ctx,
		types.NamespacedName{Namespace: ns, Name: gs.Name}, &cur); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme, AddressManager: manager}
	return r, &cur, tmpl
}

// addressPoolService fetches the game Service and, on the way past, asserts
// the one invariant that holds for every case in this block:
// svc.Spec.LoadBalancerIP is never written. Kubernetes deprecated the field
// in 1.24 and both address managers take the request through metadata, so a
// value appearing there is a bug no matter which flavor produced it.
func addressPoolService(ctx context.Context, t *testing.T, ns, name string) *corev1.Service {
	t.Helper()
	var svc corev1.Service
	if err := k8sClient.Get(ctx,
		types.NamespacedName{Namespace: ns, Name: name}, &svc); err != nil {
		t.Fatalf("get service %s/%s: %v", ns, name, err)
	}
	if svc.Spec.LoadBalancerIP != "" {
		t.Fatalf("svc.Spec.LoadBalancerIP = %q, want empty (deprecated since K8s 1.24; never written)",
			svc.Spec.LoadBalancerIP)
	}
	return &svc
}

// assertAddressAnnotations checks the address-manager annotation keys as a
// set: every key in want carries its value, and every other address-manager
// key is absent.
func assertAddressAnnotations(t *testing.T, svc *corev1.Service, want map[string]string) {
	t.Helper()
	for _, k := range addressManagerAnnotationKeys {
		got, present := svc.Annotations[k]
		if wantVal, expected := want[k]; expected {
			if got != wantVal {
				t.Fatalf("annotation %s = %q (present=%v), want %q", k, got, present, wantVal)
			}
			continue
		}
		if present {
			t.Fatalf("annotation %s = %q, want absent", k, got)
		}
	}
}

// TestGameServer_AddressPoolCRDValidation proves the generated CRD schema
// bounds both new fields and — just as deliberately — does *not* validate the
// address's format. Format checking is operator-side (reported through the
// AddressAssignment condition) precisely because an unbounded CEL rule on
// this CRD has been rejected by the apiserver on its cost budget, so a
// non-IP string within MaxLength must be accepted here.
//
// The accepted cases read the object back and compare: an un-regenerated CRD
// would prune the unknown fields silently, and only the round-trip catches it.
func TestGameServer_AddressPoolCRDValidation(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		pool       string
		address    string
		wantReject bool
	}{
		{name: "pool-and-address-accepted", pool: "prod-pool", address: "203.0.113.10"},
		{name: "pool-max-length-accepted", pool: strings.Repeat("a", 63)},
		// Pool names are the address manager's own object names, i.e. DNS-1123
		// subdomains — a dotted one must survive admission.
		{name: "dotted-pool-accepted", pool: "pool.us-east"},
		{name: "address-max-length-accepted", address: strings.Repeat("a", 45)},
		{name: "non-ip-address-accepted-format-is-operator-side", address: "not-an-ip"},
		{name: "pool-over-63-rejected", pool: strings.Repeat("a", 64), wantReject: true},
		{name: "address-over-45-rejected", address: strings.Repeat("a", 46), wantReject: true},
		{name: "pool-uppercase-rejected", pool: "Prod-Pool", wantReject: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := buildGameServer(ns, uniqueName("crdval"), "irrelevant-template")
			gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: tc.pool,
				Address:     tc.address,
			}
			err := k8sClient.Create(ctx, gs)
			if tc.wantReject {
				if err == nil {
					t.Fatalf("create accepted pool=%q address=%q, want rejection", tc.pool, tc.address)
				}
				if !apierrors.IsInvalid(err) {
					t.Fatalf("create failed with %v, want an Invalid (schema validation) error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("create rejected pool=%q address=%q: %v", tc.pool, tc.address, err)
			}
			var cur gameplanev1alpha1.GameServer
			if err := k8sClient.Get(ctx,
				types.NamespacedName{Namespace: ns, Name: gs.Name}, &cur); err != nil {
				t.Fatalf("get gameserver: %v", err)
			}
			if cur.Spec.Networking.AddressPool != tc.pool {
				t.Fatalf("stored addressPool = %q, want %q (pruned? regenerate the CRD)",
					cur.Spec.Networking.AddressPool, tc.pool)
			}
			if cur.Spec.Networking.Address != tc.address {
				t.Fatalf("stored address = %q, want %q (pruned? regenerate the CRD)",
					cur.Spec.Networking.Address, tc.address)
			}
		})
	}
}

// TestGameServer_AddressPoolTranslationPerFlavor walks every
// (flavor × requested field) combination through reconcileService and pins
// the resulting Service metadata. The Cilium pool case is the interesting
// one: the pool preference must land as a *label*, and explicitly not as an
// annotation of the same key, because the cluster admin selects on it from
// CiliumLoadBalancerIPPool.spec.serviceSelector.
func TestGameServer_AddressPoolTranslationPerFlavor(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	cases := []struct {
		name      string
		manager   string
		pool      string
		address   string
		wantAnns  map[string]string
		wantLabel string
	}{
		{
			name: "metallb-pool", manager: "metallb", pool: "prod-pool",
			wantAnns: map[string]string{metalLBAddressPoolAnnotation: "prod-pool"},
		},
		{
			name: "metallb-address", manager: "metallb", address: "203.0.113.10",
			wantAnns: map[string]string{metalLBLoadBalancerIPsAnnotation: "203.0.113.10"},
		},
		{
			name: "metallb-pool-and-address", manager: "metallb", pool: "prod-pool", address: "203.0.113.10",
			wantAnns: map[string]string{
				metalLBAddressPoolAnnotation:     "prod-pool",
				metalLBLoadBalancerIPsAnnotation: "203.0.113.10",
			},
		},
		{
			name: "cilium-pool-is-a-label", manager: "cilium", pool: "prod-pool",
			wantLabel: "prod-pool",
		},
		{
			name: "cilium-address", manager: "cilium", address: "203.0.113.10",
			wantAnns: map[string]string{ciliumAddressAnnotation: "203.0.113.10"},
		},
		{
			name: "cilium-pool-and-address", manager: "cilium", pool: "prod-pool", address: "203.0.113.10",
			wantAnns:  map[string]string{ciliumAddressAnnotation: "203.0.113.10"},
			wantLabel: "prod-pool",
		},
		{
			// Flavor none mutates nothing: the request is reported on the
			// AddressAssignment condition instead, never translated.
			name: "none-mutates-nothing", manager: "none", pool: "prod-pool", address: "203.0.113.10",
		},
		{
			// The empty flag value means exactly "none".
			name: "empty-flavor-mutates-nothing", manager: "", pool: "prod-pool", address: "203.0.113.10",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, gs, tmpl := newAddressPoolServer(ctx, t, ns, tc.manager, gameplanev1alpha1.GameServerNetworking{
				Expose:      "LoadBalancer",
				AddressPool: tc.pool,
				Address:     tc.address,
			})
			if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
				t.Fatalf("reconcileService: %v", err)
			}

			svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)
			assertAddressAnnotations(t, svc, tc.wantAnns)

			if got, present := svc.Annotations[ciliumPoolLabel]; present {
				t.Fatalf("%s = %q as an annotation, want it applied as a label only", ciliumPoolLabel, got)
			}
			got, present := svc.Labels[ciliumPoolLabel]
			switch {
			case tc.wantLabel != "":
				if got != tc.wantLabel {
					t.Fatalf("label %s = %q (present=%v), want %q",
						ciliumPoolLabel, got, present, tc.wantLabel)
				}
				if !strings.Contains(svc.Annotations[managedServiceLabelsKey], ciliumPoolLabel) {
					t.Fatalf("managed-labels sentinel = %q, want it to record %s (else the label orphans)",
						svc.Annotations[managedServiceLabelsKey], ciliumPoolLabel)
				}
			case present:
				t.Fatalf("label %s = %q, want absent", ciliumPoolLabel, got)
			}
		})
	}
}

// TestGameServer_AddressPoolIgnoredForNonLoadBalancerExpose — the preference
// only means anything on a LoadBalancer Service, so on any other expose mode
// the operator leaves the Service untouched (and flags the ignored request on
// the AddressAssignment condition, which the status tests cover).
func TestGameServer_AddressPoolIgnoredForNonLoadBalancerExpose(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	for _, expose := range []string{"", "ClusterIP", "NodePort", "Hostport"} {
		t.Run("expose-"+expose, func(t *testing.T) {
			r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "metallb", gameplanev1alpha1.GameServerNetworking{
				Expose:      expose,
				AddressPool: "prod-pool",
				Address:     "203.0.113.10",
			})
			if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
				t.Fatalf("reconcileService: %v", err)
			}
			svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)
			assertAddressAnnotations(t, svc, nil)
			if got, present := svc.Labels[ciliumPoolLabel]; present {
				t.Fatalf("label %s = %q, want absent", ciliumPoolLabel, got)
			}
		})
	}
}

// TestGameServer_AddressPoolUnsetPrunesManagedKeys is the regression this
// tier exists to catch: a pool that is set and then cleared must take its
// Service metadata with it. A merge-only apply would leave a live server
// pinned to a pool nobody asked for any more — silently, since the Service
// keeps working.
//
// The GameServer's spec is mutated in memory rather than through the
// apiserver: reconcileService reads the preference off the struct it is
// handed, and the pruning path under test is driven by the *Service* the
// second pass finds already stored.
func TestGameServer_AddressPoolUnsetPrunesManagedKeys(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	t.Run("metallb-annotations", func(t *testing.T) {
		r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "metallb", gameplanev1alpha1.GameServerNetworking{
			Expose:      "LoadBalancer",
			AddressPool: "prod-pool",
			Address:     "203.0.113.10",
		})
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (set): %v", err)
		}
		assertAddressAnnotations(t, addressPoolService(ctx, t, gs.Namespace, gs.Name), map[string]string{
			metalLBAddressPoolAnnotation:     "prod-pool",
			metalLBLoadBalancerIPsAnnotation: "203.0.113.10",
		})

		gs.Spec.Networking.AddressPool = ""
		gs.Spec.Networking.Address = ""
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (unset): %v", err)
		}
		assertAddressAnnotations(t, addressPoolService(ctx, t, gs.Namespace, gs.Name), nil)
	})

	t.Run("cilium-label", func(t *testing.T) {
		r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "cilium", gameplanev1alpha1.GameServerNetworking{
			Expose:      "LoadBalancer",
			AddressPool: "prod-pool",
		})
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (set): %v", err)
		}
		if got := addressPoolService(ctx, t, gs.Namespace, gs.Name).Labels[ciliumPoolLabel]; got != "prod-pool" {
			t.Fatalf("label %s = %q, want prod-pool", ciliumPoolLabel, got)
		}

		gs.Spec.Networking.AddressPool = ""
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (unset): %v", err)
		}
		svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)
		if got, present := svc.Labels[ciliumPoolLabel]; present {
			t.Fatalf("label %s = %q, want pruned once the pool was cleared", ciliumPoolLabel, got)
		}
		if got, present := svc.Annotations[managedServiceLabelsKey]; present {
			t.Fatalf("managed-labels sentinel = %q, want removed with the last managed label", got)
		}
	})
}

// TestGameServer_AddressPoolChangeMutatesServiceInPlace covers the other
// half of the prune path: switching a live server from one pool to another.
// A merge-only apply would leave the old pool name sitting alongside the new
// one — for MetalLB the annotation would simply be overwritten, but for
// Cilium a surviving second label keeps matching the old pool's
// serviceSelector and pins a running server to the wrong pool.
func TestGameServer_AddressPoolChangeMutatesServiceInPlace(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	t.Run("metallb-annotation", func(t *testing.T) {
		r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "metallb", gameplanev1alpha1.GameServerNetworking{
			Expose:      "LoadBalancer",
			AddressPool: "pool-us-east",
		})
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (pool-us-east): %v", err)
		}
		assertAddressAnnotations(t, addressPoolService(ctx, t, gs.Namespace, gs.Name), map[string]string{
			metalLBAddressPoolAnnotation: "pool-us-east",
		})

		gs.Spec.Networking.AddressPool = "pool-us-west"
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (pool-us-west): %v", err)
		}
		assertAddressAnnotations(t, addressPoolService(ctx, t, gs.Namespace, gs.Name), map[string]string{
			metalLBAddressPoolAnnotation: "pool-us-west",
		})
	})

	t.Run("cilium-label", func(t *testing.T) {
		r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "cilium", gameplanev1alpha1.GameServerNetworking{
			Expose:      "LoadBalancer",
			AddressPool: "pool-us-east",
		})
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (pool-us-east): %v", err)
		}
		if got := addressPoolService(ctx, t, gs.Namespace, gs.Name).Labels[ciliumPoolLabel]; got != "pool-us-east" {
			t.Fatalf("label %s = %q, want pool-us-east", ciliumPoolLabel, got)
		}

		gs.Spec.Networking.AddressPool = "pool-us-west"
		if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileService (pool-us-west): %v", err)
		}
		svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)
		if got := svc.Labels[ciliumPoolLabel]; got != "pool-us-west" {
			t.Fatalf("label %s = %q, want pool-us-west", ciliumPoolLabel, got)
		}
		// The pool label is a single key, so "the old value is gone" and
		// "only one pool label exists" are the same assertion — but the
		// sentinel must still name exactly that one key, or the next unset
		// leaves the label orphaned.
		if got := svc.Annotations[managedServiceLabelsKey]; got != ciliumPoolLabel {
			t.Fatalf("managed-labels sentinel = %q, want exactly %q", got, ciliumPoolLabel)
		}
	})
}

// TestGameServer_AddressPoolBeatsServiceAnnotations pins the precedence the
// contract promises: the typed addressPool field wins over a hand-written
// entry of the same key in spec.networking.serviceAnnotations, the same way
// the typed hostname field wins over an external-dns key set by hand. Without
// this, reordering the two merge lines in reconcileService would silently
// invert it.
func TestGameServer_AddressPoolBeatsServiceAnnotations(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	r, gs, tmpl := newAddressPoolServer(ctx, t, ns, "metallb", gameplanev1alpha1.GameServerNetworking{
		Expose:             "LoadBalancer",
		AddressPool:        "typed-pool",
		ServiceAnnotations: map[string]string{metalLBAddressPoolAnnotation: "hand-written-pool"},
	})
	if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
		t.Fatalf("reconcileService: %v", err)
	}
	assertAddressAnnotations(t, addressPoolService(ctx, t, gs.Namespace, gs.Name), map[string]string{
		metalLBAddressPoolAnnotation: "typed-pool",
	})
}

// TestGameServer_AddressPoolFlavorChangePrunesStaleKeys — an operator
// restarted with a different --address-manager must not leave the previous
// flavor's metadata behind. Cilium's pool label is the dangerous direction:
// unlike the MetalLB annotations, a stale label keeps matching a
// CiliumLoadBalancerIPPool serviceSelector forever.
func TestGameServer_AddressPoolFlavorChangePrunesStaleKeys(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	net := gameplanev1alpha1.GameServerNetworking{
		Expose:      "LoadBalancer",
		AddressPool: "prod-pool",
		Address:     "203.0.113.10",
	}
	cilium, gs, tmpl := newAddressPoolServer(ctx, t, ns, "cilium", net)
	if err := cilium.reconcileService(ctx, gs, tmpl, false); err != nil {
		t.Fatalf("reconcileService (cilium): %v", err)
	}
	svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)
	if got := svc.Labels[ciliumPoolLabel]; got != "prod-pool" {
		t.Fatalf("label %s = %q, want prod-pool", ciliumPoolLabel, got)
	}
	assertAddressAnnotations(t, svc, map[string]string{ciliumAddressAnnotation: "203.0.113.10"})

	metallb := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme, AddressManager: "metallb"}
	if err := metallb.reconcileService(ctx, gs, tmpl, false); err != nil {
		t.Fatalf("reconcileService (metallb): %v", err)
	}
	svc = addressPoolService(ctx, t, gs.Namespace, gs.Name)
	if got, present := svc.Labels[ciliumPoolLabel]; present {
		t.Fatalf("label %s = %q, want pruned after the flavor changed", ciliumPoolLabel, got)
	}
	assertAddressAnnotations(t, svc, map[string]string{
		metalLBAddressPoolAnnotation:     "prod-pool",
		metalLBLoadBalancerIPsAnnotation: "203.0.113.10",
	})
}

// TestGameServer_NoAddressPreferenceLeavesServiceMetadataUntouched is the
// backward-compatibility guard (FR-017 / SC-006): with neither field set, the
// Service must look exactly as it did before this feature existed — no
// address-manager annotations, no pool label, and no managed-labels sentinel
// — whatever flavor the operator happens to be configured for.
//
// The two flavors' Services are compared by annotation key *set* rather than
// by count so unrelated metadata written by another controller can't turn
// this into a flake.
func TestGameServer_NoAddressPreferenceLeavesServiceMetadataUntouched(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	keySet := func(m map[string]string) map[string]bool {
		out := make(map[string]bool, len(m))
		for k := range m {
			out[k] = true
		}
		return out
	}

	var reference map[string]bool
	for _, manager := range []string{"none", "metallb", "cilium"} {
		t.Run("manager-"+manager, func(t *testing.T) {
			r, gs, tmpl := newAddressPoolServer(ctx, t, ns, manager, gameplanev1alpha1.GameServerNetworking{
				Expose: "LoadBalancer",
			})
			if err := r.reconcileService(ctx, gs, tmpl, false); err != nil {
				t.Fatalf("reconcileService: %v", err)
			}
			svc := addressPoolService(ctx, t, gs.Namespace, gs.Name)

			assertAddressAnnotations(t, svc, nil)
			if got, present := svc.Labels[ciliumPoolLabel]; present {
				t.Fatalf("label %s = %q, want absent with no preference set", ciliumPoolLabel, got)
			}
			if got, present := svc.Annotations[managedServiceLabelsKey]; present {
				t.Fatalf("managed-labels sentinel = %q, want unwritten with no preference set", got)
			}

			got := keySet(svc.Annotations)
			if reference == nil {
				reference = got
				return
			}
			for k := range got {
				if !reference[k] {
					t.Fatalf("flavor %q added annotation %q to a Service with no preference set", manager, k)
				}
			}
			for k := range reference {
				if !got[k] {
					t.Fatalf("flavor %q dropped annotation %q from a Service with no preference set", manager, k)
				}
			}
		})
	}
}

// TestGameServer_AddressConflictDetection — when two GameServers request the
// same explicit address, the second one detects the conflict and reports
// AddressInUse on its AddressAssignment condition.
func TestGameServer_AddressConflictDetection(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerAddressManager(t, ns, "metallb"))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create the first server with an explicit address.
	gs1 := buildGameServer(ns, "server1", tmpl.Name)
	gs1.Spec.Networking.Expose = "LoadBalancer"
	gs1.Spec.Networking.Address = "203.0.113.10"
	if err := k8sClient.Create(context.Background(), gs1); err != nil {
		t.Fatalf("create server1: %v", err)
	}

	// Wait for server1's Service to be created.
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &svc); err != nil {
			return false, "service: " + err.Error()
		}
		return true, ""
	})

	// envtest runs apiserver+etcd only — there is no cloud-provider or MetalLB
	// controller to populate a LoadBalancer Service's status. Seeding the
	// GameServer's status.endpoints directly doesn't work either: SetupWithManager
	// uses a bare For(&GameServer{}) with no predicate, so the seeded status is
	// immediately overwritten by the next reconcile's reconcileStatus, which
	// derives Endpoints from the Service (falling back to ClusterIP whenever the
	// Service has no LoadBalancer ingress). So instead we write the Service's
	// status subresource ourselves, exactly as a real address manager would —
	// this persists across reconciles because it is now real cluster state, not
	// something the controller reconciles away.
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &svc); err != nil {
			return err
		}
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}}
		return k8sClient.Status().Update(context.Background(), &svc)
	}); err != nil {
		t.Fatalf("update server1 service status: %v", err)
	}

	// Wait for the controller to pick up the LB ingress and reflect it in
	// server1's status.endpoints before introducing the conflicting server.
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &gs); err != nil {
			return false, "get server1: " + err.Error()
		}
		for _, ep := range gs.Status.Endpoints {
			if ep.Host == "203.0.113.10" {
				return true, ""
			}
		}
		return false, "server1 endpoints do not yet reflect 203.0.113.10"
	})

	// Create the second server requesting the same address.
	gs2 := buildGameServer(ns, "server2", tmpl.Name)
	gs2.Spec.Networking.Expose = "LoadBalancer"
	gs2.Spec.Networking.Address = "203.0.113.10"
	if err := k8sClient.Create(context.Background(), gs2); err != nil {
		t.Fatalf("create server2: %v", err)
	}

	// server2's AddressAssignment condition should report AddressInUse.
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server2"}, &gs); err != nil {
			return false, "get server2: " + err.Error()
		}
		for _, cond := range gs.Status.Conditions {
			if cond.Type == gameplanev1alpha1.GameServerConditionAddressAssignment {
				if cond.Reason != "AddressInUse" {
					return false, "AddressAssignment reason = " + cond.Reason + ", want AddressInUse"
				}
				if !strings.Contains(cond.Message, "server1") {
					return false, "AddressAssignment message should mention server1, got: " + cond.Message
				}
				return true, ""
			}
		}
		return false, "AddressAssignment condition not found"
	})
}

// TestGameServer_ReleasedAddressBecomesAvailable — when a GameServer with
// an assigned explicit address is deleted, the address is released and becomes
// available for another GameServer to use (no orphaning).
func TestGameServer_ReleasedAddressBecomesAvailable(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerAddressManager(t, ns, "metallb"))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	// Create server1 with an explicit address.
	gs1 := buildGameServer(ns, "server1", tmpl.Name)
	gs1.Spec.Networking.Expose = "LoadBalancer"
	gs1.Spec.Networking.Address = "203.0.113.20"
	if err := k8sClient.Create(context.Background(), gs1); err != nil {
		t.Fatalf("create server1: %v", err)
	}

	// Wait for server1's Service to be created.
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &svc); err != nil {
			return false, "service: " + err.Error()
		}
		return true, ""
	})

	// envtest runs apiserver+etcd only — there is no cloud-provider or MetalLB
	// controller to populate a LoadBalancer Service's status. Seeding the
	// GameServer's status.endpoints directly doesn't work either: SetupWithManager
	// uses a bare For(&GameServer{}) with no predicate, so the seeded status is
	// immediately overwritten by the next reconcile's reconcileStatus, which
	// derives Endpoints from the Service (falling back to ClusterIP whenever the
	// Service has no LoadBalancer ingress). So instead we write the Service's
	// status subresource ourselves, exactly as a real address manager would —
	// this persists across reconciles because it is now real cluster state, not
	// something the controller reconciles away.
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &svc); err != nil {
			return err
		}
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "203.0.113.20"}}
		return k8sClient.Status().Update(context.Background(), &svc)
	}); err != nil {
		t.Fatalf("update server1 service status: %v", err)
	}

	// Wait for the controller to pick up the LB ingress and reflect it in
	// server1's status.endpoints before introducing the conflicting server.
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &gs); err != nil {
			return false, "get server1: " + err.Error()
		}
		for _, ep := range gs.Status.Endpoints {
			if ep.Host == "203.0.113.20" {
				return true, ""
			}
		}
		return false, "server1 endpoints do not yet reflect 203.0.113.20"
	})

	// Create server2 requesting the same address; it should report conflict.
	gs2 := buildGameServer(ns, "server2", tmpl.Name)
	gs2.Spec.Networking.Expose = "LoadBalancer"
	gs2.Spec.Networking.Address = "203.0.113.20"
	if err := k8sClient.Create(context.Background(), gs2); err != nil {
		t.Fatalf("create server2: %v", err)
	}

	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server2"}, &gs); err != nil {
			return false, "get server2: " + err.Error()
		}
		for _, cond := range gs.Status.Conditions {
			if cond.Type == gameplanev1alpha1.GameServerConditionAddressAssignment {
				if cond.Reason != "AddressInUse" {
					return false, "initial reason = " + cond.Reason + ", want AddressInUse"
				}
				return true, ""
			}
		}
		return false, "AddressAssignment condition not found"
	})

	// Now delete server1 to release the address.
	if err := k8sClient.Delete(context.Background(), gs1); err != nil {
		t.Fatalf("delete server1: %v", err)
	}

	// Wait for server1 to be actually deleted.
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server1"}, &gs)
		if apierrors.IsNotFound(err) {
			return true, ""
		}
		if err != nil {
			return false, err.Error()
		}
		return false, "server1 still exists"
	})

	// server2's AddressAssignment condition should change: the conflict is resolved.
	// Since server2 itself has no address assigned yet (simulated), it should show
	// AssignmentPending or similar (not AddressInUse).
	// This requires a RequeueAfter(15s) since server2 receives no watch event from
	// server1's deletion — use 20s budget to be safe.
	eventuallyWith(t, 20*time.Second, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "server2"}, &gs); err != nil {
			return false, "get server2: " + err.Error()
		}
		for _, cond := range gs.Status.Conditions {
			if cond.Type == gameplanev1alpha1.GameServerConditionAddressAssignment {
				// After server1 is deleted, server2 should no longer report AddressInUse.
				if cond.Reason == "AddressInUse" {
					return false, "AddressAssignment still reports AddressInUse after server1 deletion"
				}
				// It could be AssignmentPending, ServiceNotReady, etc., but not AddressInUse.
				return true, ""
			}
		}
		return false, "AddressAssignment condition not found"
	})
}
