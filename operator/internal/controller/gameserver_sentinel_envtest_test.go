//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// ---------------------------------------------------------------------
// Sub-function tests: reconcileSentinel / reconcileGameDirectServiceFromTemplate
// called directly against a hand-built reconciler on the plain envtest
// client. Deliberately NOT started under startMgr/withGameServerReconciler:
// none of these need a live background controller loop, and starting one
// anyway raced it against the manually-driven calls below (flagged in
// review — a latent, non-deterministic source of test flakiness).
//
// Each GameTemplate uses a uniqueName(...): GameTemplate is cluster-scoped
// (see operator/internal/controller's CRD-scope conventions), envtest boots
// one shared apiserver for the whole test binary (suite_envtest_test.go's
// TestMain), and a fixed name reused by more than one subtest below would
// collide on create.
// ---------------------------------------------------------------------

func wakeableTestTemplate(name, wakeProtocol string) *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			DisplayName: "Test " + name,
			Game:        name,
			Version:     "test",
			Image:       "ghcr.io/test/" + name + ":latest",
			Ports: []gameplanev1alpha1.GamePort{{
				Name:          "game",
				ContainerPort: 25565,
				Protocol:      corev1.ProtocolTCP,
				Advertise:     true,
				WakeProtocol:  wakeProtocol,
			}},
		},
	}
}

func armedIdleSpec() *gameplanev1alpha1.IdleSpec {
	return &gameplanev1alpha1.IdleSpec{Enabled: true, WakeOnConnect: true}
}

func TestSentinelReconciliation(t *testing.T) {
	t.Run("sentinel created when wanted", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sentinel-create", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileSentinel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileSentinel: %v", err)
		}

		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-sentinel-create-waker", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get sentinel deployment: %v", err)
		}
		if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
			t.Errorf("want 1 replica, got %v", dep.Spec.Replicas)
		}
		if dep.Spec.Selector.MatchLabels["app.kubernetes.io/name"] != "gameplane-waker" {
			t.Error("sentinel label not set correctly")
		}
	})

	t.Run("sentinel deleted when not wanted", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sentinel-delete", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		// Create it first so there is something to delete.
		if err := r.reconcileSentinel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileSentinel create: %v", err)
		}
		if err := r.reconcileSentinel(ctx, gs, tmpl, false); err != nil {
			t.Fatalf("reconcileSentinel delete: %v", err)
		}

		var dep appsv1.Deployment
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-sentinel-delete-waker", Namespace: ns,
		}, &dep)
		if err == nil {
			t.Error("sentinel should be deleted when not wanted")
		}
	})

	t.Run("sentinel Deployment uses the correct ServiceAccount", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sentinel-sa", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileSentinel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileSentinel: %v", err)
		}

		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-sentinel-sa-waker", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get sentinel deployment: %v", err)
		}
		if dep.Spec.Template.Spec.ServiceAccountName != "test-sentinel-sa-waker-sa" {
			t.Errorf("ServiceAccountName = %q, want %q", dep.Spec.Template.Spec.ServiceAccountName, "test-sentinel-sa-waker-sa")
		}
	})

	t.Run("Hostport mode sets HostPort on sentinel container", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sentinel-hostport", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
				Networking:  gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileSentinel(ctx, gs, tmpl, true); err != nil {
			t.Fatalf("reconcileSentinel: %v", err)
		}

		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-sentinel-hostport-waker", Namespace: ns,
		}, &dep); err != nil {
			t.Fatalf("get sentinel deployment: %v", err)
		}
		if len(dep.Spec.Template.Spec.Containers) == 0 {
			t.Fatal("no containers in sentinel deployment")
		}
		container := dep.Spec.Template.Spec.Containers[0]
		if len(container.Ports) == 0 {
			t.Fatal("no ports in sentinel container")
		}
		if port := container.Ports[0]; port.HostPort != 25565 {
			t.Errorf("HostPort should be 25565, got %d", port.HostPort)
		}
	})

	t.Run("game-direct Service created and synced when eligible", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-game-direct", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileGameDirectServiceFromTemplate(ctx, gs, tmpl); err != nil {
			t.Fatalf("reconcileGameDirectServiceFromTemplate: %v", err)
		}

		var svc corev1.Service
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-game-direct-game-direct", Namespace: ns,
		}, &svc); err != nil {
			t.Fatalf("get game-direct service: %v", err)
		}
		if svc.Spec.Selector["app.kubernetes.io/name"] != "gameplane-game" {
			t.Error("game-direct Service selector should point to game pod")
		}
		if !svc.Spec.PublishNotReadyAddresses {
			t.Error("PublishNotReadyAddresses should be true")
		}
	})

	t.Run("game-direct Service absent when wake-on-connect not armed", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		// No WakeProtocol on the port and no Idle spec on the GameServer:
		// wakeOnConnectEligible must be false, so this Service should never
		// be created for the majority of GameServers that don't use this
		// feature.
		name := uniqueName("sentinel-tmpl")
		tmpl := &gameplanev1alpha1.GameTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: gameplanev1alpha1.GameTemplateSpec{
				DisplayName: "Test " + name,
				Game:        name,
				Version:     "test",
				Image:       "ghcr.io/test/" + name + ":latest",
				Ports: []gameplanev1alpha1.GamePort{{
					Name: "game", ContainerPort: 25565, Protocol: corev1.ProtocolTCP, Advertise: true,
				}},
			},
		}
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-game-direct-ineligible", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileGameDirectServiceFromTemplate(ctx, gs, tmpl); err != nil {
			t.Fatalf("reconcileGameDirectServiceFromTemplate: %v", err)
		}

		var svc corev1.Service
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-game-direct-ineligible-game-direct", Namespace: ns,
		}, &svc)
		if err == nil {
			t.Error("game-direct Service should not exist when wake-on-connect is not armed")
		}
	})

	// Regression guard for the ClusterIP/NodePort bug: the game-direct
	// Service is unconditionally ClusterIP, but svcPortsFromTemplate
	// legitimately carries spec.networking.portOverrides[].nodePort through
	// for the *main* Service. Before the fix, any GameServer merely using a
	// nodePort override made this CreateOrUpdate fail on every reconcile —
	// the apiserver rejects a nonzero nodePort on a ClusterIP Service — which
	// blocked every step that ran after it, for a GameServer that may not
	// even use wake-on-connect. Runs against the real envtest apiserver, so
	// this actually exercises that admission check rather than mocking it.
	t.Run("game-direct Service strips NodePort from portOverrides", func(t *testing.T) {
		ctx := context.Background()
		ns := newNamespace(t)

		tmpl := wakeableTestTemplate(uniqueName("sentinel-tmpl"), "minecraft")
		if err := k8sClient.Create(ctx, tmpl); err != nil {
			t.Fatalf("create template: %v", err)
		}
		deleteCleanup(t, tmpl)

		gs := &gameplanev1alpha1.GameServer{
			ObjectMeta: metav1.ObjectMeta{Name: "test-game-direct-nodeport", Namespace: ns},
			Spec: gameplanev1alpha1.GameServerSpec{
				TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
				Idle:        armedIdleSpec(),
				Networking: gameplanev1alpha1.GameServerNetworking{
					Expose:        "NodePort",
					PortOverrides: []gameplanev1alpha1.PortOverride{{Name: "game", NodePort: 30080}},
				},
			},
		}
		if err := k8sClient.Create(ctx, gs); err != nil {
			t.Fatalf("create gameserver: %v", err)
		}

		r := &GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme}
		if err := r.reconcileGameDirectServiceFromTemplate(ctx, gs, tmpl); err != nil {
			t.Fatalf("reconcileGameDirectServiceFromTemplate: %v", err)
		}

		var svc corev1.Service
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "test-game-direct-nodeport-game-direct", Namespace: ns,
		}, &svc); err != nil {
			t.Fatalf("get game-direct service: %v", err)
		}
		if svc.Spec.Type != corev1.ServiceTypeClusterIP {
			t.Errorf("game-direct Service type = %v, want ClusterIP", svc.Spec.Type)
		}
		if len(svc.Spec.Ports) != 1 {
			t.Fatalf("want 1 port, got %d", len(svc.Spec.Ports))
		}
		if svc.Spec.Ports[0].NodePort != 0 {
			t.Errorf("NodePort = %d, want 0 (stripped) on a ClusterIP Service", svc.Spec.Ports[0].NodePort)
		}

		// Reconciling again must stay idempotent (CreateOrUpdate's steady
		// state), not flip-flop or error on the second pass.
		if err := r.reconcileGameDirectServiceFromTemplate(ctx, gs, tmpl); err != nil {
			t.Fatalf("reconcileGameDirectServiceFromTemplate (2nd pass): %v", err)
		}
	})
}

// ---------------------------------------------------------------------
// Full-Reconcile tests: these drive the real GameServerReconciler.Reconcile
// via a live controller-runtime manager (startMgr), the same way every
// other envtest file in this package does. This is what fatal bug #1 (the
// Service selector flip undone one line later) needed to be caught: calling
// reconcileSentinel/reconcileService in isolation, as every subtest above
// and every subtest in the original version of this file did, can never
// observe two CreateOrUpdate calls racing on the same object within one
// real Reconcile pass.
// ---------------------------------------------------------------------

func deploymentExists(t *testing.T, ns, name string) bool {
	t.Helper()
	var dep appsv1.Deployment
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &dep)
	return err == nil
}

func gameServiceSelectorName(t *testing.T, ns, name string) string {
	t.Helper()
	var svc corev1.Service
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &svc); err != nil {
		return ""
	}
	return svc.Spec.Selector["app.kubernetes.io/name"]
}

func setAsleep(t *testing.T, ns, name string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		if gs.Annotations == nil {
			gs.Annotations = map[string]string{}
		}
		gs.Annotations[IdleAsleepSinceAnnotation] = time.Now().Format(time.RFC3339)
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("stamp asleep: %v", err)
	}
}

func clearAsleep(t *testing.T, ns, name string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		delete(gs.Annotations, IdleAsleepSinceAnnotation)
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("clear asleep: %v", err)
	}
}

// TestGameServer_WakeOnConnectServiceSelector drives the real Reconcile()
// through a full sleep -> wake cycle in a Service-backed Expose mode
// (ClusterIP, the default) and asserts on the game Service's
// .spec.selector at each step. This is the fatal-bug-#1 regression test:
// before the fix, reconcileService always won the race and the selector
// never moved off the game pod labels no matter what the sentinel was
// doing.
//
// It also locks in the hold-open half of the wake design (bug #4): once
// woken, the Service must keep routing to the sentinel — and the sentinel
// must stay up — until the game pod itself reports Ready, not the instant
// the wake is decided.
func TestGameServer_WakeOnConnectServiceSelector(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	ctx := context.Background()

	tmpl := wakeableTestTemplate(uniqueName("wakesel-tmpl"), "minecraft")
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "wakesel", Namespace: ns},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
			Idle:        armedIdleSpec(),
		},
	}
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Bring the game pod up like any normal awake server, and fake it Ready
	// (envtest has no kubelet to do this itself).
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "wakesel") == 1, "statefulset not scaled up yet"
	})
	setSSStatus(t, ns, "wakesel", 1, 1)
	eventually(t, func() (bool, string) {
		return gameServiceSelectorName(t, ns, "wakesel") == "gameplane-game", "service not routed to game pod yet"
	})

	// Put it to sleep the same way the idle policy does (see
	// applyIdleTransition): stamp the asleep annotation.
	setAsleep(t, ns, "wakesel")

	// The sentinel comes up; the Service must not cut over until it
	// reports Ready, so a player is never blackholed between "game pod
	// gone" and "sentinel serving".
	eventually(t, func() (bool, string) {
		return deploymentExists(t, ns, "wakesel-waker"), "sentinel deployment not created"
	})
	setDeploymentReady(t, ns, "wakesel-waker", 1)
	eventually(t, func() (bool, string) {
		return gameServiceSelectorName(t, ns, "wakesel") == sentinelValue, "service not routed to sentinel"
	})

	// The game pod is genuinely gone while asleep (nothing simulates the
	// kubelet actually tearing it down in envtest, so tell the fixture).
	setSSStatus(t, ns, "wakesel", 0, 0)

	// Wake it: clearing the asleep annotation is exactly what
	// applyIdleTransition does on a real wake decision.
	clearAsleep(t, ns, "wakesel")

	// Regression guard: the sentinel and the Service's routing to it must
	// both survive the wake decision until the game pod is actually Ready
	// again — not flip back (or delete the sentinel) the instant idleState
	// reads idleAwake.
	consistently(t, 2*time.Second, func() (bool, string) {
		if got := gameServiceSelectorName(t, ns, "wakesel"); got != sentinelValue {
			return false, "service selector flipped back early: " + got
		}
		if !deploymentExists(t, ns, "wakesel-waker") {
			return false, "sentinel deleted before the game pod was ready"
		}
		return true, ""
	})

	// Let the game pod actually come up.
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "wakesel") == 1, "statefulset not scaled back up"
	})
	setSSStatus(t, ns, "wakesel", 1, 1)

	// Once Ready, the Service must flip back to the game pod and the
	// sentinel must go away.
	eventually(t, func() (bool, string) {
		return gameServiceSelectorName(t, ns, "wakesel") == "gameplane-game", "service did not flip back to the game pod"
	})
	eventually(t, func() (bool, string) {
		return !deploymentExists(t, ns, "wakesel-waker"), "sentinel not deleted after wake"
	})
}

// TestGameServer_WakeOnConnectHostportNoHoldOpen locks in the deliberate
// Hostport asymmetry documented on planSentinel: the sentinel and the game
// pod bind the same host port, so on wake the sentinel must be torn down
// immediately rather than held open for the game pod to become Ready —
// otherwise the game pod could never even schedule.
func TestGameServer_WakeOnConnectHostportNoHoldOpen(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	ctx := context.Background()

	tmpl := wakeableTestTemplate(uniqueName("wakehp-tmpl"), "minecraft")
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "wakehp", Namespace: ns},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmpl.Name},
			Idle:        armedIdleSpec(),
			Networking:  gameplanev1alpha1.GameServerNetworking{Expose: "Hostport"},
		},
	}
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "wakehp") == 1, "statefulset not scaled up yet"
	})
	setSSStatus(t, ns, "wakehp", 1, 1)

	setAsleep(t, ns, "wakehp")
	eventually(t, func() (bool, string) {
		return deploymentExists(t, ns, "wakehp-waker"), "sentinel deployment not created"
	})
	setDeploymentReady(t, ns, "wakehp-waker", 1)
	setSSStatus(t, ns, "wakehp", 0, 0)

	// Wake it, but do NOT mark the game pod Ready — in the three
	// Service-backed modes this would keep the sentinel alive; in Hostport
	// mode it must not, because the game pod cannot even schedule while
	// the sentinel still holds the port.
	clearAsleep(t, ns, "wakehp")

	eventually(t, func() (bool, string) {
		return !deploymentExists(t, ns, "wakehp-waker"), "sentinel not deleted immediately on Hostport wake"
	})
}
