//go:build envtest

package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// withNetworkCaptureReconciler wires a NetworkCaptureReconciler using the
// shared manager, matching the withXReconciler convention in
// helpers_envtest_test.go. stub lets each test control the sidecar's
// reported behavior without a real capture-sidecar HTTPS endpoint.
func withNetworkCaptureReconciler(stub SidecarCaptureClient) setupReconciler {
	return func(mgr manager.Manager) error {
		return (&NetworkCaptureReconciler{
			Client:              mgr.GetClient(),
			Scheme:              mgr.GetScheme(),
			SidecarClient:       stub,
			CaptureEnabled:      true,
			CaptureSidecarImage: "ghcr.io/gameplanepanel/gameplane/capture-sidecar:test",
		}).SetupWithManager(mgr)
	}
}

// buildCapturePod constructs the minimal Pod fixture the reconciler needs
// for ephemeral-container injection: an "app.kubernetes.io/instance" label
// (read by mapPodToNetworkCaptures) and the "captures" emptyDir volume that
// gameserver_controller.go provisions unconditionally on every real game
// pod template — matching the real name, not a fixture-only stand-in.
func buildCapturePod(ns, gsName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-0", gsName),
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/instance": gsName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "game",
					Image: "test-image:latest",
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "captures",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "agent-tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: gsName + "-agent-tls",
						},
					},
				},
			},
		},
	}
}

// buildCaptureGameServer constructs a minimal GameServer with capture enabled.
func buildCaptureGameServer(ns, name string) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "test-template"},
			Capture: &gameplanev1alpha1.CaptureConfiguration{
				Enabled:          true,
				RetentionSeconds: ptrTo(int32(86400)),
			},
		},
		Status: gameplanev1alpha1.GameServerStatus{
			Phase: gameplanev1alpha1.GameServerPhaseRunning,
		},
	}
}

// buildNetworkCapture constructs a Pending NetworkCapture targeting gsName.
func buildNetworkCapture(ns, name, gsName, filter string) *gameplanev1alpha1.NetworkCapture {
	return &gameplanev1alpha1.NetworkCapture{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.NetworkCaptureSpec{
			ServerRef:   corev1.LocalObjectReference{Name: gsName},
			Filter:      ptrTo(filter),
			MaxDuration: &metav1.Duration{Duration: 5 * time.Minute},
			MaxSize:     resource.NewQuantity(10485760, resource.BinarySI), // 10 MiB, well below 900 MiB limit
		},
		Status: gameplanev1alpha1.NetworkCaptureStatus{
			Phase: gameplanev1alpha1.CapturePhasePending,
		},
	}
}

func getNetworkCapture(t *testing.T, ns, name string) *gameplanev1alpha1.NetworkCapture {
	t.Helper()
	var nc gameplanev1alpha1.NetworkCapture
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &nc); err != nil {
		t.Fatalf("get network capture %s/%s: %v", ns, name, err)
	}
	return &nc
}

func TestNetworkCapture_PendingToRunningToCompleted(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	started := false
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "completed", 100, 5242880, "capture finished", nil
		},
	}

	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gs := buildCaptureGameServer(ns, "test-server")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, "test-server")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-test-001", "test-server", "tcp port 25565")); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-test-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseCompleted {
			return false, fmt.Sprintf("phase = %s, want Completed", nc.Status.Phase)
		}
		if nc.Status.StartTime == nil {
			return false, "StartTime not set"
		}
		if nc.Status.CompletionTime == nil {
			return false, "CompletionTime not set"
		}
		if nc.Status.BytesWritten == nil || nc.Status.BytesWritten.Value() != 5242880 {
			return false, fmt.Sprintf("BytesWritten = %v, want 5242880", nc.Status.BytesWritten)
		}
		if len(nc.OwnerReferences) == 0 {
			return false, "OwnerReferences not backfilled"
		}
		return true, ""
	})

	// Ephemeral container must reference the real "captures" volume, not a
	// fixture-only name, and must use the reconciler's configured image.
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "test-server-0"}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if len(pod.Spec.EphemeralContainers) != 1 {
		t.Fatalf("EphemeralContainers = %d, want 1", len(pod.Spec.EphemeralContainers))
	}
	ec := pod.Spec.EphemeralContainers[0]
	if ec.Image != "ghcr.io/gameplanepanel/gameplane/capture-sidecar:test" {
		t.Errorf("ephemeral container image = %q, want the configured CaptureSidecarImage", ec.Image)
	}
	foundVolume := false
	for _, vm := range ec.VolumeMounts {
		if vm.Name == "captures" {
			foundVolume = true
		}
		if vm.Name == "capture-data" {
			t.Errorf("ephemeral container mounts nonexistent volume %q", vm.Name)
		}
	}
	if !foundVolume {
		t.Errorf("ephemeral container does not mount the real \"captures\" volume")
	}
}

func TestNetworkCapture_ConcurrencyRejection(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	started := false
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "running", 50, 2621440, "capture running", nil
		},
	}

	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gs := buildCaptureGameServer(ns, "test-server-conc")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, "test-server-conc")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-conc-001", "test-server-conc", "tcp port 25565")); err != nil {
		t.Fatalf("create first network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-conc-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
			return false, fmt.Sprintf("phase = %s, want Running", nc.Status.Phase)
		}
		return true, ""
	})

	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-conc-002", "test-server-conc", "tcp port 25566")); err != nil {
		t.Fatalf("create second network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-conc-002")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseFailed {
			return false, fmt.Sprintf("phase = %s, want Failed", nc.Status.Phase)
		}
		if nc.Status.Message == "" {
			return false, "no failure message recorded"
		}
		cond := meta.FindStatusCondition(nc.Status.Conditions, "Failed")
		if cond == nil || cond.Reason != "capture_already_in_progress" {
			return false, fmt.Sprintf("Failed condition reason = %+v, want \"capture_already_in_progress\"", cond)
		}
		return true, ""
	})

	// The first capture must remain Running — rejection of the second must
	// not disturb it.
	first := getNetworkCapture(t, ns, "cap-conc-001")
	if first.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
		t.Errorf("first capture phase = %s, want it to remain Running", first.Status.Phase)
	}
}

// TestNetworkCapture_PodDeletedTransitionsToFailed covers T076(b): a Running
// NetworkCapture whose Pod disappears (deleted, never recreated within this
// test) transitions to Failed with a PodRestarted-style condition instead of
// sitting in Running forever, and the parent GameServer's
// status.capture.activeCapture is cleared.
func TestNetworkCapture_PodDeletedTransitionsToFailed(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	started := false
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "running", 10, 1024, "capture running", nil
		},
	}

	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gs := buildCaptureGameServer(ns, "test-server-poddel")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, "test-server-poddel")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-poddel-001", "test-server-poddel", "tcp port 25565")); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-poddel-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
			return false, fmt.Sprintf("phase = %s, want Running", nc.Status.Phase)
		}
		return true, ""
	})

	if err := k8sClient.Delete(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "test-server-poddel-0"},
	}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-poddel-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseFailed {
			return false, fmt.Sprintf("phase = %s, want Failed", nc.Status.Phase)
		}
		cond := meta.FindStatusCondition(nc.Status.Conditions, "Failed")
		if cond == nil || cond.Reason != "PodRestarted" {
			return false, fmt.Sprintf("Failed condition reason = %+v, want \"PodRestarted\"", cond)
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "test-server-poddel"}, &got); err != nil {
			return false, "get gameserver: " + err.Error()
		}
		if got.Status.Capture != nil && got.Status.Capture.ActiveCapture != nil {
			return false, fmt.Sprintf("status.capture.activeCapture = %q, want cleared", *got.Status.Capture.ActiveCapture)
		}
		return true, ""
	})
}

// simulateCaptureEphemeralTerminated patches podName's status to report the
// capture ephemeral container as Terminated with a non-zero exit code — the
// kubelet's report of a sidecar process crash. Mirrors
// simulateCaptureEphemeralRunning's rationale: envtest runs no kubelet, so
// nothing else will populate pod.status.ephemeralContainerStatuses.
func simulateCaptureEphemeralTerminated(t *testing.T, ns, podName string, exitCode int32) {
	t.Helper()
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: podName}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", podName, err)
	}
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
		{
			Name: captureContainerName,
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{
					ExitCode:   exitCode,
					Reason:     "Error",
					FinishedAt: metav1.Now(),
				},
			},
		},
	}
	if err := k8sClient.Status().Update(context.Background(), &pod); err != nil {
		t.Fatalf("simulate capture ephemeral terminated status for pod %s: %v", podName, err)
	}
}

// TestNetworkCapture_SidecarCrashTransitionsToFailed covers T076(c): a
// Running NetworkCapture whose ephemeral capture container status shows
// Terminated (non-zero exit) while the Pod itself is still Running
// transitions to Failed with a SidecarCrashed condition and is never
// retried (it stays Failed, not requeued back to Running).
func TestNetworkCapture_SidecarCrashTransitionsToFailed(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	started := false
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "running", 10, 1024, "capture running", nil
		},
	}

	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gs := buildCaptureGameServer(ns, "test-server-crash")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, "test-server-crash")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-crash-001", "test-server-crash", "tcp port 25565")); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-crash-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
			return false, fmt.Sprintf("phase = %s, want Running", nc.Status.Phase)
		}
		return true, ""
	})

	simulateCaptureEphemeralTerminated(t, ns, "test-server-crash-0", 1)

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-crash-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseFailed {
			return false, fmt.Sprintf("phase = %s, want Failed", nc.Status.Phase)
		}
		cond := meta.FindStatusCondition(nc.Status.Conditions, "Failed")
		if cond == nil || cond.Reason != "SidecarCrashed" {
			return false, fmt.Sprintf("Failed condition reason = %+v, want \"SidecarCrashed\"", cond)
		}
		return true, ""
	})

	// Never retried: the failure must stick, not bounce back to Running on
	// a later reconcile (e.g. via the Pod watch firing again).
	time.Sleep(200 * time.Millisecond)
	final := getNetworkCapture(t, ns, "cap-crash-001")
	if final.Status.Phase != gameplanev1alpha1.CapturePhaseFailed {
		t.Errorf("capture phase = %s after settling, want it to remain Failed", final.Status.Phase)
	}
}

// TestNetworkCapture_UserStopCallsSidecar verifies that a user-requested
// stop (status patched to Completed with the API's "stopped by user
// request" message, mirroring api/internal/kube/capture.go's
// StopNetworkCapture) actually tells the sidecar to stop capturing exactly
// once, instead of leaving it to keep writing packets until it hits its own
// max-duration/max-size limit.
func TestNetworkCapture_UserStopCallsSidecar(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	started := false
	stopCalls := 0
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			started = true
			mu.Unlock()
			return nil
		},
		stopCaptureFn: func(_ context.Context, _, _, _ string) error {
			mu.Lock()
			stopCalls++
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "running", 10, 1024, "capture running", nil
		},
	}

	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gs := buildCaptureGameServer(ns, "test-server-stop")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, "test-server-stop")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildNetworkCapture(ns, "cap-stop-001", "test-server-stop", "tcp port 25565")); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		nc := getNetworkCapture(t, ns, "cap-stop-001")
		if nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
			return false, fmt.Sprintf("phase = %s, want Running", nc.Status.Phase)
		}
		return true, ""
	})

	// Emulate the API's StopNetworkCapture: patch status directly to
	// Completed with its exact message, bypassing the reconciler.
	nc := getNetworkCapture(t, ns, "cap-stop-001")
	now := metav1.NewTime(time.Now().UTC())
	nc.Status.Phase = gameplanev1alpha1.CapturePhaseCompleted
	nc.Status.CompletionTime = &now
	nc.Status.Message = "stopped by user request"
	if err := k8sClient.Status().Update(context.Background(), nc); err != nil {
		t.Fatalf("patch capture to user-stopped: %v", err)
	}

	eventually(t, func() (bool, string) {
		mu.Lock()
		calls := stopCalls
		mu.Unlock()
		if calls < 1 {
			return false, "sidecar StopCapture not called yet"
		}
		return true, ""
	})

	// Give the reconciler a further moment to settle, then confirm it fired
	// exactly once even though the terminal phase keeps getting reconciled
	// (e.g. via the Pod watch).
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	calls := stopCalls
	mu.Unlock()
	if calls != 1 {
		t.Errorf("sidecar StopCapture called %d times, want exactly 1", calls)
	}
}

// ---------------------------------------------------------------------
// T052 — GameServerReconciler's capture enable/disable path
// (reconcileCapture, gameserver_controller.go). Unlike the tests above,
// these wire GameServerReconciler itself rather than NetworkCaptureReconciler.
// ---------------------------------------------------------------------

// withGameServerReconcilerCaptureEnabled wires GameServerReconciler with the
// cluster-wide capture kill switch on (CaptureEnabled: true), for T052's
// enable/disable tests. helpers_envtest_test.go's withGameServerReconciler
// leaves CaptureEnabled at its zero value (false), which would make every
// case here a no-op (see reconcileCapture's clusterAllows check).
func withGameServerReconcilerCaptureEnabled(t *testing.T, ns string) setupReconciler {
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
			CaptureEnabled:         true,
			CaptureSidecarImage:    "ghcr.io/gameplanepanel/gameplane/capture-sidecar:test",
		}).SetupWithManager(mgr)
	}
}

// simulateCaptureEphemeralRunning patches podName's status to report the
// capture ephemeral container as Running. envtest runs no kubelet, so
// nothing else will ever populate status.ephemeralContainerStatuses —
// tests that need reconcileCapture's Ready/SidecarRestarts derivation to
// observe something must seed it themselves, exactly as
// helpers_envtest_test.go's patchJobStatus does for Jobs.
func simulateCaptureEphemeralRunning(t *testing.T, ns, podName string, restarts int32) {
	t.Helper()
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: podName}, &pod); err != nil {
		t.Fatalf("get pod %s: %v", podName, err)
	}
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{
		{
			Name:         captureContainerName,
			State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
			RestartCount: restarts,
		},
	}
	if err := k8sClient.Status().Update(context.Background(), &pod); err != nil {
		t.Fatalf("simulate capture ephemeral container status for pod %s: %v", podName, err)
	}
}

// TestGameServerCapture_EnableInjectsEphemeralContainer covers T052(a):
// enabling spec.capture.enabled on a GameServer injects the capture
// ephemeral container with the correct name, image, "captures" volume
// mount, and securityContext — live, without disturbing the game
// container — and status.capture.ready follows the sidecar's own observed
// state, not merely whether injection was attempted.
func TestGameServerCapture_EnableInjectsEphemeralContainer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerCaptureEnabled(t, ns))

	tmpl := buildGameTemplate(uniqueName("capenable"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gsName := "cap-enable"
	gs := buildGameServer(ns, gsName, tmpl.Name)
	gs.Spec.Capture = &gameplanev1alpha1.CaptureConfiguration{Enabled: true}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	// envtest runs no StatefulSet controller, so the pod never appears on
	// its own — seed the fixture reconcileCapture injects into, mirroring
	// the pod the real StatefulSet would eventually create (same fixture
	// shape buildCapturePod already establishes for NetworkCaptureReconciler
	// above, including the pre-provisioned "captures" emptyDir volume).
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
			return false, "get pod: " + err.Error()
		}
		if len(pod.Spec.EphemeralContainers) != 1 {
			return false, fmt.Sprintf("EphemeralContainers = %d, want 1", len(pod.Spec.EphemeralContainers))
		}
		ec := pod.Spec.EphemeralContainers[0]
		if ec.Name != captureContainerName {
			return false, fmt.Sprintf("ephemeral container name = %q, want %q", ec.Name, captureContainerName)
		}
		if ec.Image != "ghcr.io/gameplanepanel/gameplane/capture-sidecar:test" {
			return false, fmt.Sprintf("ephemeral container image = %q, want the configured CaptureSidecarImage", ec.Image)
		}
		foundVolume := false
		for _, vm := range ec.VolumeMounts {
			if vm.Name == "captures" {
				foundVolume = true
			}
		}
		if !foundVolume {
			return false, "ephemeral container does not mount the \"captures\" volume"
		}
		sc := ec.SecurityContext
		if sc == nil {
			return false, "ephemeral container has no SecurityContext"
		}
		if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
			return false, "RunAsNonRoot not true"
		}
		if sc.AllowPrivilegeEscalation == nil || !*sc.AllowPrivilegeEscalation {
			return false, "AllowPrivilegeEscalation not true (required: file capabilities are ignored under no_new_privs)"
		}
		// Drop ALL + Add NET_RAW: empties effective set but keeps NET_RAW
		// in bounding set, allowing the kernel to grant the setcap'd
		// binary's file capability at execve (would fail with EPERM if
		// NET_RAW were dropped).
		if sc.Capabilities == nil {
			return false, "capabilities not set"
		}
		if len(sc.Capabilities.Add) != 1 || sc.Capabilities.Add[0] != "NET_RAW" {
			return false, fmt.Sprintf("capabilities.add = %v, want [NET_RAW]", sc.Capabilities.Add)
		}
		if len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
			return false, fmt.Sprintf("capabilities.drop = %v, want [ALL]", sc.Capabilities.Drop)
		}
		return true, ""
	})

	// The game container itself is untouched by injection.
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != gameContainerName {
		t.Errorf("game container list changed by capture injection: %+v", pod.Spec.Containers)
	}

	// Ready must be false until the sidecar reports Running — injection
	// alone does not imply readiness.
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName}, &got); err != nil {
			return false, "get gameserver: " + err.Error()
		}
		if got.Status.Capture == nil {
			return false, "status.capture is nil"
		}
		if got.Status.Capture.Ready {
			return false, "status.capture.ready = true before the sidecar reported Running"
		}
		return true, ""
	})

	// Simulate the kubelet reporting the sidecar Running; ready must follow.
	simulateCaptureEphemeralRunning(t, ns, gsName+"-0", 0)
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName}, &got); err != nil {
			return false, "get gameserver: " + err.Error()
		}
		if got.Status.Capture == nil || !got.Status.Capture.Ready {
			return false, fmt.Sprintf("status.capture.ready = %+v, want true", got.Status.Capture)
		}
		return true, ""
	})
}

// TestGameServerCapture_DisableStopsActiveCaptureAndLeavesEphemeralContainer
// covers T052(b): disabling spec.capture.enabled stops routing new captures
// (status.capture.ready flips false, activeCapture clears) AND stops a
// capture that is Running (by setting the stop-requested annotation that
// networkcapture_controller.go's Reconcile acts on: it stops the sidecar and
// only then marks the capture Completed, F-259) — while the ephemeral container REMAINS in both
// pod.spec.ephemeralContainers and pod.status.ephemeralContainerStatuses.
// That persistence is the documented platform limitation (Kubernetes has no
// API to remove an ephemeral container), not a bug: this test asserts the
// container stays, it does not try to make it go away.
func TestGameServerCapture_DisableStopsActiveCaptureAndLeavesEphemeralContainer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerCaptureEnabled(t, ns))

	tmpl := buildGameTemplate(uniqueName("capdisable"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gsName := "cap-disable"
	gs := buildGameServer(ns, gsName, tmpl.Name)
	gs.Spec.Capture = &gameplanev1alpha1.CaptureConfiguration{Enabled: true}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
			return false, err.Error()
		}
		if !hasCaptureEphemeralContainer(&pod) {
			return false, "ephemeral container not yet injected"
		}
		return true, ""
	})
	simulateCaptureEphemeralRunning(t, ns, gsName+"-0", 0)

	// Create a Running NetworkCapture owned by this GameServer directly —
	// NetworkCaptureReconciler is not wired into this test's manager, so
	// this stands in for what it would normally have driven to Running,
	// exercising stopActiveCaptures' Running branch on its own.
	nc := buildNetworkCapture(ns, "cap-running", gsName, "tcp port 25565")
	if err := k8sClient.Create(context.Background(), nc); err != nil {
		t.Fatalf("create network capture: %v", err)
	}
	nc.Status.Phase = gameplanev1alpha1.CapturePhaseRunning
	nc.Status.StartTime = &metav1.Time{Time: time.Now()}
	if err := k8sClient.Status().Update(context.Background(), nc); err != nil {
		t.Fatalf("patch capture to Running: %v", err)
	}

	// Seed status.capture.activeCapture the way NetworkCaptureReconciler
	// would have, so disabling has an active pointer to actually clear.
	// The GameServer's status is also being written by the pod-injection
	// reconciler loop running concurrently in this test, so a plain
	// Get-then-Update races it; retry on conflict, re-fetching each
	// attempt, the way other envtest suites in this package do.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName}, &current); err != nil {
			return err
		}
		if current.Status.Capture == nil {
			current.Status.Capture = &gameplanev1alpha1.CaptureStatus{}
		}
		current.Status.Capture.ActiveCapture = ptrTo(nc.Name)
		return k8sClient.Status().Update(context.Background(), &current)
	}); err != nil {
		t.Fatalf("seed active capture: %v", err)
	}

	// Disable capture using Patch to avoid stale-write conflicts with concurrent
	// reconciler updates to status fields.
	var toDisable gameplanev1alpha1.GameServer
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName}, &toDisable); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	base := toDisable.DeepCopy()
	toDisable.Spec.Capture.Enabled = false
	if err := k8sClient.Patch(context.Background(), &toDisable, client.MergeFrom(base)); err != nil {
		t.Fatalf("disable capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName}, &got); err != nil {
			return false, "get gameserver: " + err.Error()
		}
		if got.Status.Capture == nil {
			return false, "status.capture is nil"
		}
		if got.Status.Capture.Ready {
			return false, "status.capture.ready = true, want false"
		}
		if got.Status.Capture.ActiveCapture != nil {
			return false, fmt.Sprintf("status.capture.activeCapture = %q, want cleared", *got.Status.Capture.ActiveCapture)
		}
		return true, ""
	})

	eventually(t, func() (bool, string) {
		// F-259: disable only requests the stop; NetworkCaptureReconciler
		// (not wired here) stops the sidecar and then completes it.
		got := getNetworkCapture(t, ns, "cap-running")
		if _, ok := got.Annotations[stopRequestedAnnotation]; !ok {
			return false, "stop-requested annotation not set"
		}
		return true, ""
	})

	// The ephemeral container REMAINS — Kubernetes offers no API to remove
	// one, and disabling must never try.
	var pod corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if !hasCaptureEphemeralContainer(&pod) {
		t.Error("ephemeral container removed from pod.spec on disable — it must remain (no removal API exists)")
	}
	found := false
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if cs.Name == captureContainerName {
			found = true
		}
	}
	if !found {
		t.Error("ephemeral container status entry removed from pod.status.ephemeralContainerStatuses on disable — it must remain")
	}
}

// TestGameServerCapture_PodRecreationReinjectsEphemeralContainer covers
// T052(c): with spec.capture.enabled left true, deleting and recreating the
// game pod (envtest has no StatefulSet controller to do this on its own, so
// the test does it directly, standing in for a real pod rebuild) gets the
// ephemeral container re-injected into the new pod — the capture setting
// persists across the rebuild rather than requiring it to be re-enabled.
func TestGameServerCapture_PodRecreationReinjectsEphemeralContainer(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerCaptureEnabled(t, ns))

	tmpl := buildGameTemplate(uniqueName("caprecreate"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gsName := "cap-recreate"
	gs := buildGameServer(ns, gsName, tmpl.Name)
	gs.Spec.Capture = &gameplanev1alpha1.CaptureConfiguration{Enabled: true}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
			return false, err.Error()
		}
		if !hasCaptureEphemeralContainer(&pod) {
			return false, "ephemeral container not yet injected into the first pod"
		}
		return true, ""
	})

	// Delete and recreate the pod (fresh object, no ephemeral container —
	// standing in for the StatefulSet's real rebuild), with
	// spec.capture.enabled still true.
	if err := k8sClient.Delete(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: gsName + "-0"},
	}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod)
		if !apierrors.IsNotFound(err) {
			return false, fmt.Sprintf("pod still present (err=%v)", err)
		}
		return true, ""
	})
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("recreate pod: %v", err)
	}

	eventually(t, func() (bool, string) {
		var pod corev1.Pod
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: gsName + "-0"}, &pod); err != nil {
			return false, err.Error()
		}
		if !hasCaptureEphemeralContainer(&pod) {
			return false, "ephemeral container not re-injected into the recreated pod"
		}
		return true, ""
	})
}

// ---------------------------------------------------------------------
// T068 — retention/expiry reconciliation (networkcapture_controller.go's
// reconcileRetention/expireCapture/expireStuckRunningCapture).
// ---------------------------------------------------------------------

// TestNetworkCapture_RetentionExpiresCompletedCapture covers T068's first
// case: a Completed capture whose TTL has already elapsed transitions to
// Expired, has its backing file cleanup attempted via the sidecar, and is
// deleted — without waiting out a live TTL. NetworkCaptureSpec's
// kubebuilder:validation:Minimum=60 means TTL itself can't be shortened
// below 60s, so this backdates status.completionTime well past that window
// instead ("short-circuit the requeue constant" per the task, achieved here
// by short-circuiting the anchor rather than the poll interval — the
// reconciler's own requeue is already computed from the exact remaining TTL,
// not a blind fixed poll, so there is no separate constant to shrink).
func TestNetworkCapture_RetentionExpiresCompletedCapture(t *testing.T) {
	ns := newNamespace(t)

	var mu sync.Mutex
	deleteCalls := 0
	stub := &StubSidecarClient{
		deleteCaptureFileFn: func(_ context.Context, _, _, _ string) error {
			mu.Lock()
			deleteCalls++
			mu.Unlock()
			return nil
		},
	}
	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	gsName := "test-server-retention"
	if err := k8sClient.Create(context.Background(), buildCaptureGameServer(ns, gsName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nc := buildNetworkCapture(ns, "cap-retention-001", gsName, "tcp port 25565")
	nc.Spec.TTLSecondsAfterFinished = ptrTo(int32(60)) // CRD minimum; already-past completionTime does the rest.
	if err := k8sClient.Create(context.Background(), nc); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	// Re-Get the capture to ensure we have the latest resourceVersion before updating status.
	nc = getNetworkCapture(t, ns, "cap-retention-001")
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	nc.Status.Phase = gameplanev1alpha1.CapturePhaseCompleted
	nc.Status.CompletionTime = &past
	nc.Status.Message = "capture finished"
	if err := k8sClient.Status().Update(context.Background(), nc); err != nil {
		t.Fatalf("patch capture to Completed with a past completionTime: %v", err)
	}

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.NetworkCapture
		err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "cap-retention-001"}, &got)
		if !apierrors.IsNotFound(err) {
			return false, fmt.Sprintf("capture still present (err=%v)", err)
		}
		return true, ""
	})

	mu.Lock()
	calls := deleteCalls
	mu.Unlock()
	if calls < 1 {
		t.Error("sidecar DeleteCaptureFile was never called for the expired capture")
	}
}

// TestNetworkCapture_RetentionTerminatesStuckRunningCapture covers T068's
// second case: a Running capture whose retention window elapses while it
// still has no completionTime (the sidecar stopped reporting without
// actually finishing) is force-stopped and cleaned up rather than polled
// forever.
func TestNetworkCapture_RetentionTerminatesStuckRunningCapture(t *testing.T) {
	ns := newNamespace(t)

	// The manager's cache spans all namespaces (startMgr), so it also
	// reconciles captures earlier tests left behind; count only calls for
	// this test's namespace.
	var mu sync.Mutex
	stopCalls, deleteCalls, getCaptureStatusCalls := 0, 0, 0
	stub := &StubSidecarClient{
		stopCaptureFn: func(_ context.Context, captureNS, _, _ string) error {
			if captureNS == ns {
				mu.Lock()
				stopCalls++
				mu.Unlock()
			}
			return nil
		},
		deleteCaptureFileFn: func(_ context.Context, captureNS, _, _ string) error {
			if captureNS == ns {
				mu.Lock()
				deleteCalls++
				mu.Unlock()
			}
			return nil
		},
		// Count GetCaptureStatus calls to verify the retention safety net short-circuits.
		getCaptureStatusFn: func(_ context.Context, captureNS, _, _ string) (string, int64, int64, string, error) {
			if captureNS == ns {
				mu.Lock()
				getCaptureStatusCalls++
				mu.Unlock()
			}
			return "running", 0, 0, "", nil
		},
	}

	gsName := "test-server-stuck"
	if err := k8sClient.Create(context.Background(), buildCaptureGameServer(ns, gsName)); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(context.Background(), buildCapturePod(ns, gsName)); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	nc := buildNetworkCapture(ns, "cap-stuck-001", gsName, "tcp port 25565")
	nc.Spec.TTLSecondsAfterFinished = ptrTo(int32(60))
	if err := k8sClient.Create(context.Background(), nc); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	// Re-Get the capture to ensure we have the latest resourceVersion before updating status.
	nc = getNetworkCapture(t, ns, "cap-stuck-001")
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	nc.Status.Phase = gameplanev1alpha1.CapturePhaseRunning
	nc.Status.StartTime = &past
	if err := k8sClient.Status().Update(context.Background(), nc); err != nil {
		t.Fatalf("patch capture to Running with a past startTime: %v", err)
	}

	// Start the manager only now, so no reconcile ever sees the capture
	// Pending (that path polls the sidecar) and the first one already finds
	// it Running past its retention window.
	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.NetworkCapture
		err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "cap-stuck-001"}, &got)
		if !apierrors.IsNotFound(err) {
			return false, fmt.Sprintf("capture still present (err=%v)", err)
		}
		return true, ""
	})

	mu.Lock()
	sCalls, dCalls, gCalls := stopCalls, deleteCalls, getCaptureStatusCalls
	mu.Unlock()
	if sCalls < 1 {
		t.Error("sidecar StopCapture was never called for the retention-expired running capture")
	}
	if dCalls < 1 {
		t.Error("sidecar DeleteCaptureFile was never called for the retention-expired running capture")
	}
	// The capture was already past its retention window when the manager
	// started, so the safety net expires it without ever polling the sidecar.
	if gCalls != 0 {
		t.Errorf("sidecar GetCaptureStatus was called %d times; the retention safety net should expire the capture without polling it", gCalls)
	}
}

// StubSidecarClient implements SidecarCaptureClient for testing.
type StubSidecarClient struct {
	startCaptureFn      func(ctx context.Context, ns, server, id string, filter *string, dur, size int64) error
	stopCaptureFn       func(ctx context.Context, ns, server, id string) error
	getCaptureStatusFn  func(ctx context.Context, ns, server, id string) (string, int64, int64, string, error)
	deleteCaptureFileFn func(ctx context.Context, ns, server, id string) error
}

func (s *StubSidecarClient) StartCapture(ctx context.Context, namespace, serverName, captureID, _, _ string, filter *string, maxDurationSeconds, maxSizeBytes int64) error {
	if s.startCaptureFn != nil {
		return s.startCaptureFn(ctx, namespace, serverName, captureID, filter, maxDurationSeconds, maxSizeBytes)
	}
	return nil
}

func (s *StubSidecarClient) StopCapture(ctx context.Context, namespace, serverName, captureID, _, _ string) error {
	if s.stopCaptureFn != nil {
		return s.stopCaptureFn(ctx, namespace, serverName, captureID)
	}
	return nil
}

func (s *StubSidecarClient) GetCaptureStatus(ctx context.Context, namespace, serverName, captureID, _, _ string) (string, int64, int64, string, error) {
	if s.getCaptureStatusFn != nil {
		return s.getCaptureStatusFn(ctx, namespace, serverName, captureID)
	}
	return "running", 0, 0, "", nil
}

func (s *StubSidecarClient) DeleteCaptureFile(ctx context.Context, namespace, serverName, captureID, _, _ string) error {
	if s.deleteCaptureFileFn != nil {
		return s.deleteCaptureFileFn(ctx, namespace, serverName, captureID)
	}
	return nil
}
