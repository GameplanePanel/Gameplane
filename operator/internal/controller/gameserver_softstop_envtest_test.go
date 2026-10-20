//go:build envtest

package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// fakeStopper records calls to the agent's soft-stop endpoint. disabled
// mirrors *agent.Client's Disabled flag (set when no mTLS material is
// configured): Stop still no-ops rather than erroring — matching the real
// client's contract — but Enabled() reports false so selectStopTransport
// can tell a disabled-but-non-nil client apart from a usable one.
type fakeStopper struct {
	mu       sync.Mutex
	calls    int
	disabled bool
}

func (f *fakeStopper) Stop(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil
}

func (f *fakeStopper) Enabled() bool {
	return !f.disabled
}

func (f *fakeStopper) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func withGameServerReconcilerStopper(t *testing.T, ns string, stop AgentStopper) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
			AgentClient:            stop,
		}).SetupWithManager(mgr)
	}
}

// fakePodAttacher records calls to the pod-attach stop transport in place
// of a real Kubernetes pod attach (which envtest, having no kubelet, can't
// actually serve).
type fakePodAttacher struct {
	mu       sync.Mutex
	calls    int
	lastPod  string
	lastCtr  string
	lastCmds []string
}

func (f *fakePodAttacher) Stop(_ context.Context, _, podName, container string, commands []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastPod = podName
	f.lastCtr = container
	f.lastCmds = commands
	return nil
}

func (f *fakePodAttacher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakePodAttacher) snapshot() (pod, container string, cmds []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastPod, f.lastCtr, f.lastCmds
}

func withGameServerReconcilerAttacher(t *testing.T, ns string, attach PodStopAttacher) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
			PodAttacher:            attach,
		}).SetupWithManager(mgr)
	}
}

// setSSReady fakes a running (or stopped) game in envtest, which has no kubelet
// to populate StatefulSet status.
func setSSReady(t *testing.T, ns, name string, ready int32) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
			return err
		}
		ss.Status.Replicas = ready
		ss.Status.ReadyReplicas = ready
		return k8sClient.Status().Update(context.Background(), &ss)
	}); err != nil {
		t.Fatalf("set ss ready=%d: %v", ready, err)
	}
}

func ssReplicas(t *testing.T, ns, name string) int32 {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
		// Used inside eventually() polls, including before the reconciler has
		// created the StatefulSet — report "not there" rather than failing.
		return -1
	}
	if ss.Spec.Replicas == nil {
		return -1
	}
	return *ss.Spec.Replicas
}

// TestGameServer_SoftStopRunsStopThenScalesDown verifies the soft-stop path:
// suspending a server whose template declares a stop sequence issues that
// sequence over the agent and keeps the pod up while the game saves, then
// scales to zero once the game goes not-ready.
func TestGameServer_SoftStopRunsStopThenScalesDown(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	// selectStopTransport requires the template to actually declare RCON
	// before it will pick stopTransportRCON — buildGameTemplate leaves RCON
	// unset, so without this the stop sequence is never issued and the
	// eventually() below times out waiting on stopper.count().
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575}
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Long grace so the scale-down in this test is driven by the game going
	// not-ready, not by the backstop deadline firing mid-assertion.
	grace := int32(120)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// Wait for the initial create to land replicas=1, then fake a running game.
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "smp") == 1, "replicas not yet 1"
	})
	setSSReady(t, ns, "smp", 1)

	// Suspend. The reconciler should issue the stop sequence and keep the pod
	// running while it waits for the game to go down.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Suspend = true
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	eventually(t, func() (bool, string) {
		if stopper.count() == 0 {
			return false, "stop sequence not issued yet"
		}
		g := getGameServer(t, ns, "smp")
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; !ok {
			return false, "stop-requested annotation not set"
		}
		if r := ssReplicas(t, ns, "smp"); r != 1 {
			return false, "pod scaled down before the game went not-ready"
		}
		return true, ""
	})

	// The game finishes saving and goes not-ready: the operator scales to zero.
	setSSReady(t, ns, "smp", 0)
	eventuallyWith(t, 15*time.Second, func() (bool, string) {
		return ssReplicas(t, ns, "smp") == 0, "replicas not yet zero"
	})

	// Resuming clears the soft-stop bookkeeping and scales back up.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "smp")
		gs.Spec.Suspend = false
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; ok {
			return false, "stop annotation not cleared on resume"
		}
		return ssReplicas(t, ns, "smp") == 1, "not scaled back to 1"
	})
}

// TestGameServer_SoftStop_PTYTransportRunsAttachThenScalesDown verifies the
// second stop transport: a template with NO RCON but consoleMode: pty runs
// the declared sequence over a pod attach to the game container's stdin
// (selectStopTransport -> stopTransportPTY), and otherwise follows the same
// hold-then-scale-down shape as the RCON transport above.
func TestGameServer_SoftStop_PTYTransportRunsAttachThenScalesDown(t *testing.T) {
	ns := newNamespace(t)
	attacher := &fakePodAttacher{}
	startMgr(t, ns, withGameServerReconcilerAttacher(t, ns, attacher))

	tmpl := buildGameTemplate(uniqueName("terraria"))
	tmpl.Spec.ConsoleMode = "pty" // no RCON block set
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "exit"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "world1", tmpl.Name)
	grace := int32(120)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "world1") == 1, "replicas not yet 1"
	})
	setSSReady(t, ns, "world1", 1)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "world1")
		gs.Spec.Suspend = true
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	eventually(t, func() (bool, string) {
		if attacher.count() == 0 {
			return false, "attach stop sequence not issued yet"
		}
		g := getGameServer(t, ns, "world1")
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; !ok {
			return false, "stop-requested annotation not set"
		}
		if r := ssReplicas(t, ns, "world1"); r != 1 {
			return false, "pod scaled down before the game went not-ready"
		}
		return true, ""
	})

	pod, ctr, cmds := attacher.snapshot()
	if pod != "world1-0" {
		t.Fatalf("attached pod = %q, want %q", pod, "world1-0")
	}
	if ctr != gameContainerName {
		t.Fatalf("attached container = %q, want %q", ctr, gameContainerName)
	}
	if len(cmds) != 2 || cmds[0] != "save" || cmds[1] != "exit" {
		t.Fatalf("attached commands = %v, want [save exit]", cmds)
	}

	setSSReady(t, ns, "world1", 0)
	eventuallyWith(t, 15*time.Second, func() (bool, string) {
		return ssReplicas(t, ns, "world1") == 0, "replicas not yet zero"
	})
}

// TestGameServer_SoftStop_NoTransportScalesDownImmediately verifies the
// third case: a template that declares a stop sequence but exposes NEITHER
// RCON nor a pty console (selectStopTransport -> stopTransportNone) must
// scale straight to zero, exactly like "no sequence declared" — it must
// NOT stamp the stop-requested annotation or sit through
// spec.stopGracePeriodSeconds waiting on a transport that doesn't exist.
// The same outcome (and same code path) covers a declared PTY transport
// whose PodAttacher was never wired (nil), since softStop treats that
// identically to "no transport".
func TestGameServer_SoftStop_NoTransportScalesDownImmediately(t *testing.T) {
	ns := newNamespace(t)
	// The plain reconciler wires neither AgentClient nor PodAttacher.
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("headless"))
	// No RCON block, no ConsoleMode override -> EffectiveConsoleMode ==
	// "none", so selectStopTransport has nothing to pick.
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "notransport", tmpl.Name)
	// A long grace period so a pass/fail on "scaled down immediately" is
	// unambiguous: if softStop mistakenly took the wait-out-the-grace
	// path, replicas would still read 1 well within this test's (10s)
	// default eventually timeout.
	grace := int32(300)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "notransport") == 1, "replicas not yet 1"
	})
	setSSReady(t, ns, "notransport", 1)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "notransport")
		gs.Spec.Suspend = true
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "notransport") == 0, "replicas not scaled to zero"
	})

	g := getGameServer(t, ns, "notransport")
	if _, ok := g.Annotations[stopRequestedAtAnnotation]; ok {
		t.Fatalf("stop-requested annotation was set; softStop took the wait-out-grace path instead of scaling down immediately")
	}
}

// TestGameServer_SoftStop_DisabledAgentClientScalesDownImmediately covers a
// should-fix: agent.New returns a non-nil *Client with Disabled: true when
// no mTLS material is configured (any dev/non-chart install) — its Stop is
// a silent no-op. Before Enabled() existed, selectStopTransport only
// checked AgentClient == nil, so a template with RCON would still pick
// stopTransportRCON for this non-nil-but-disabled client, stamp the
// stop-requested annotation, no-op the stop, and then sit through the full
// stopGracePeriodSeconds for nothing — the exact stall softStop exists to
// avoid, just reached through a different door. A disabled client must
// resolve to stopTransportNone and scale straight to zero, exactly like
// "no transport at all".
func TestGameServer_SoftStop_DisabledAgentClientScalesDownImmediately(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{disabled: true}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	// Declare RCON so selectStopTransport would pick stopTransportRCON on
	// the template alone — this test exists to prove the disabled *client*
	// (Enabled() == false) is what forces stopTransportNone, not merely an
	// RCON-less template (that's TestGameServer_SoftStop_NoTransportScalesDownImmediately).
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575}
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "disabledclient", tmpl.Name)
	// A long grace period so a pass/fail on "scaled down immediately" is
	// unambiguous, same rationale as the no-transport case above.
	grace := int32(300)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "disabledclient") == 1, "replicas not yet 1"
	})
	setSSReady(t, ns, "disabledclient", 1)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs = getGameServer(t, ns, "disabledclient")
		gs.Spec.Suspend = true
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "disabledclient") == 0, "replicas not scaled to zero"
	})

	if stopper.count() != 0 {
		t.Fatalf("disabled stopper was called %d times; a disabled client must never be selected as a transport", stopper.count())
	}
	g := getGameServer(t, ns, "disabledclient")
	if _, ok := g.Annotations[stopRequestedAtAnnotation]; ok {
		t.Fatalf("stop-requested annotation was set; softStop took the wait-out-grace path for a disabled agent client instead of scaling down immediately")
	}
}
