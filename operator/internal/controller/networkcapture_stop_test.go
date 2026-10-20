package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
)

// These tests cover the F-259 stop flow (maintainer decision 2026-09-25,
// operator/specs.md "Network capture stop flow"): the API only sets
// stopRequestedAnnotation, and this reconciler stops the sidecar before it
// marks the capture Completed.

// stopRecordingSidecar is a SidecarCaptureClient that records StopCapture
// calls and fails StartCapture/DeleteCaptureFile, so a test notices any
// sidecar call it did not expect. GetCaptureStatus reports the capture as
// unknown (a 404 *agent.HTTPError, matching the real sidecar) unless known
// is set, in which case it returns packets/bytes; statusErr overrides both,
// for simulating a non-404 (transient) status error.
type stopRecordingSidecar struct {
	stopCalls   []string
	stopErr     error
	known       bool
	packets     int64
	bytes       int64
	statusErr   error
	statusCalls int
}

func (s *stopRecordingSidecar) StartCapture(context.Context, string, string, string, string, string, *string, int64, int64) error {
	return errors.New("unexpected StartCapture")
}

func (s *stopRecordingSidecar) StopCapture(_ context.Context, _, serverName, captureID, _, _ string) error {
	s.stopCalls = append(s.stopCalls, serverName+"/"+captureID)
	return s.stopErr
}

func (s *stopRecordingSidecar) GetCaptureStatus(context.Context, string, string, string, string, string) (string, int64, int64, string, error) {
	s.statusCalls++
	if s.statusErr != nil {
		return "", 0, 0, "", s.statusErr
	}
	if !s.known {
		return "", 0, 0, "", &agent.HTTPError{Op: "get status", StatusCode: 404, Body: "capture not found"}
	}
	return "running", s.packets, s.bytes, "", nil
}

func (s *stopRecordingSidecar) DeleteCaptureFile(context.Context, string, string, string, string, string) error {
	return errors.New("unexpected DeleteCaptureFile")
}

const stopTestNS = "games"

func stopTestCapture(name string, phase gameplanev1alpha1.CapturePhase, annotated bool) *gameplanev1alpha1.NetworkCapture {
	nc := &gameplanev1alpha1.NetworkCapture{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: stopTestNS},
		Spec:       gameplanev1alpha1.NetworkCaptureSpec{ServerRef: corev1.LocalObjectReference{Name: "srv"}},
		Status:     gameplanev1alpha1.NetworkCaptureStatus{Phase: phase},
	}
	if annotated {
		nc.Annotations = map[string]string{stopRequestedAnnotation: "2026-09-25T10:00:00Z"}
	}
	return nc
}

func stopTestServer(activeCapture string) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "srv", Namespace: stopTestNS},
	}
	if activeCapture != "" {
		gs.Status.Capture = &gameplanev1alpha1.CaptureStatus{ActiveCapture: ptrTo(activeCapture)}
	}
	return gs
}

// stopTestPod is the game pod "srv-0" in phase, with the given UID.
func stopTestPod(uid types.UID, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "srv-0", Namespace: stopTestNS, UID: uid},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

// withCapturePodUID records uid as the pod the Pending branch observed
// before it called StartCapture, as Reconcile does.
func withCapturePodUID(nc *gameplanev1alpha1.NetworkCapture, uid types.UID) *gameplanev1alpha1.NetworkCapture {
	if nc.Annotations == nil {
		nc.Annotations = map[string]string{}
	}
	nc.Annotations[capturePodUIDAnnotation] = string(uid)
	return nc
}

// withStopRequestedAt overrides the stop-request time recorded in the
// annotation.
func withStopRequestedAt(nc *gameplanev1alpha1.NetworkCapture, at time.Time) *gameplanev1alpha1.NetworkCapture {
	if nc.Annotations == nil {
		nc.Annotations = map[string]string{}
	}
	nc.Annotations[stopRequestedAnnotation] = at.UTC().Format(time.RFC3339)
	return nc
}

// runStopReconcile reconciles nc once against a fake client holding gs, nc
// and extra, returning the client and the Reconcile error.
func runStopReconcile(t *testing.T, sidecar *stopRecordingSidecar, gs *gameplanev1alpha1.GameServer, nc *gameplanev1alpha1.NetworkCapture, extra ...client.Object) (client.Client, error) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(append([]client.Object{gs, nc}, extra...)...).
		WithStatusSubresource(&gameplanev1alpha1.GameServer{}, &gameplanev1alpha1.NetworkCapture{}).
		Build()
	r := &NetworkCaptureReconciler{Client: c, Scheme: c.Scheme(), SidecarClient: sidecar, CaptureEnabled: true}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: stopTestNS, Name: nc.Name}})
	return c, err
}

func reconcileStopTest(t *testing.T, sidecar *stopRecordingSidecar, gs *gameplanev1alpha1.GameServer, nc *gameplanev1alpha1.NetworkCapture, extra ...client.Object) (*gameplanev1alpha1.NetworkCapture, *gameplanev1alpha1.GameServer) {
	t.Helper()
	c, err := runStopReconcile(t, sidecar, gs, nc, extra...)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var gotNC gameplanev1alpha1.NetworkCapture
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: stopTestNS, Name: nc.Name}, &gotNC); err != nil {
		t.Fatalf("get capture: %v", err)
	}
	var gotGS gameplanev1alpha1.GameServer
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: stopTestNS, Name: gs.Name}, &gotGS); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	return &gotNC, &gotGS
}

func assertUserStopCompleted(t *testing.T, nc *gameplanev1alpha1.NetworkCapture, wantReason string) {
	t.Helper()
	if nc.Status.Phase != gameplanev1alpha1.CapturePhaseCompleted {
		t.Errorf("phase = %q, want Completed", nc.Status.Phase)
	}
	if nc.Status.Message != userStoppedMessage {
		t.Errorf("message = %q, want %q", nc.Status.Message, userStoppedMessage)
	}
	if nc.Status.CompletionTime == nil {
		t.Error("completionTime not set")
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, SidecarStoppedCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("SidecarStopped condition = %+v, want True", cond)
	}
	if cond.Reason != wantReason {
		t.Errorf("SidecarStopped reason = %q, want %q", cond.Reason, wantReason)
	}
	if _, ok := nc.Annotations[stopRequestedAnnotation]; !ok {
		t.Error("stop-requested annotation should be kept as a record")
	}
}

func TestNetworkCaptureStopRequested_RunningStopsSidecarThenCompletes(t *testing.T) {
	sidecar := &stopRecordingSidecar{}
	nc, gs := reconcileStopTest(t, sidecar, stopTestServer("cap-run"), stopTestCapture("cap-run", gameplanev1alpha1.CapturePhaseRunning, true))

	if len(sidecar.stopCalls) != 1 || sidecar.stopCalls[0] != "srv/cap-run" {
		t.Errorf("StopCapture calls = %v, want exactly [srv/cap-run]", sidecar.stopCalls)
	}
	assertUserStopCompleted(t, nc, "stopped")
	if meta.FindStatusCondition(nc.Status.Conditions, "SidecarStopFailed") != nil {
		t.Error("SidecarStopFailed must not be set when the sidecar stop succeeds")
	}
	if gs.Status.Capture == nil || gs.Status.Capture.ActiveCapture != nil {
		t.Errorf("gameserver activeCapture = %+v, want released (nil)", gs.Status.Capture)
	}
	if gs.Status.Capture != nil && gs.Status.Capture.LastCaptureTime == nil {
		t.Error("gameserver lastCaptureTime should be recorded")
	}
}

func TestNetworkCaptureStopRequested_SidecarStopFailureStillCompletes(t *testing.T) {
	sidecar := &stopRecordingSidecar{stopErr: errors.New("pod gone")}
	nc, _ := reconcileStopTest(t, sidecar, stopTestServer("cap-gone"), stopTestCapture("cap-gone", gameplanev1alpha1.CapturePhaseRunning, true))

	if len(sidecar.stopCalls) != 1 {
		t.Errorf("StopCapture calls = %v, want exactly one", sidecar.stopCalls)
	}
	assertUserStopCompleted(t, nc, "stopped")
	failed := meta.FindStatusCondition(nc.Status.Conditions, "SidecarStopFailed")
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Errorf("SidecarStopFailed condition = %+v, want True", failed)
	}
}

func TestNetworkCaptureStopRequested_PendingCompletesWithoutSidecar(t *testing.T) {
	for _, phase := range []gameplanev1alpha1.CapturePhase{"", gameplanev1alpha1.CapturePhasePending} {
		t.Run("phase="+string(phase), func(t *testing.T) {
			sidecar := &stopRecordingSidecar{}
			nc, _ := reconcileStopTest(t, sidecar, stopTestServer(""), stopTestCapture("cap-pend", phase, true))

			if len(sidecar.stopCalls) != 0 {
				t.Errorf("StopCapture calls = %v, want none for a capture that never started", sidecar.stopCalls)
			}
			if sidecar.statusCalls != 0 {
				t.Errorf("GetCaptureStatus calls = %d, want none without a recorded capture pod", sidecar.statusCalls)
			}
			assertUserStopCompleted(t, nc, "never_started")
		})
	}
}

// TestNetworkCaptureStopRequested_TerminalIgnored: once Completed, the
// annotation no longer drives anything, so a later reconcile (e.g. the
// retention pass) never calls the sidecar's stop again.
func TestNetworkCaptureStopRequested_TerminalIgnored(t *testing.T) {
	sidecar := &stopRecordingSidecar{}
	nc := stopTestCapture("cap-done", gameplanev1alpha1.CapturePhaseCompleted, true)
	now := metav1.NewTime(time.Now())
	nc.Status.CompletionTime = &now
	nc.Status.Message = userStoppedMessage
	meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
		Type: SidecarStoppedCondition, Status: metav1.ConditionTrue, Reason: "stopped", LastTransitionTime: now,
	})

	got, _ := reconcileStopTest(t, sidecar, stopTestServer(""), nc)
	if len(sidecar.stopCalls) != 0 {
		t.Errorf("StopCapture calls = %v, want none for an already-completed capture", sidecar.stopCalls)
	}
	if got.Status.Phase != gameplanev1alpha1.CapturePhaseCompleted {
		t.Errorf("phase = %q, want Completed", got.Status.Phase)
	}
}

// TestNetworkCaptureStopRequested_LegacyFallback: an API from before F-259
// (mid rolling upgrade) writes phase=Completed + userStoppedMessage with no
// annotation. The reconciler must still stop the sidecar once and record
// SidecarStopped.
func TestNetworkCaptureStopRequested_LegacyFallback(t *testing.T) {
	sidecar := &stopRecordingSidecar{}
	nc := stopTestCapture("cap-legacy", gameplanev1alpha1.CapturePhaseCompleted, false)
	now := metav1.NewTime(time.Now())
	nc.Status.CompletionTime = &now
	nc.Status.Message = userStoppedMessage

	got, gs := reconcileStopTest(t, sidecar, stopTestServer("cap-legacy"), nc)
	if len(sidecar.stopCalls) != 1 || sidecar.stopCalls[0] != "srv/cap-legacy" {
		t.Errorf("StopCapture calls = %v, want exactly [srv/cap-legacy]", sidecar.stopCalls)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, SidecarStoppedCondition) {
		t.Errorf("conditions = %+v, want SidecarStopped=True", got.Status.Conditions)
	}
	if gs.Status.Capture == nil || gs.Status.Capture.ActiveCapture != nil {
		t.Errorf("gameserver activeCapture = %+v, want released (nil)", gs.Status.Capture)
	}
}

// TestNetworkCaptureStopRequested_PendingKnownToSidecarStops: the Pending
// branch starts the sidecar before its status write, so a stop request that
// makes that write conflict leaves the object Pending while the sidecar is
// capturing. The stop must then go to the sidecar, not short-circuit as
// never_started.
func TestNetworkCaptureStopRequested_PendingKnownToSidecarStops(t *testing.T) {
	for _, phase := range []gameplanev1alpha1.CapturePhase{"", gameplanev1alpha1.CapturePhasePending} {
		t.Run("phase="+string(phase), func(t *testing.T) {
			sidecar := &stopRecordingSidecar{known: true, packets: 7, bytes: 4096}
			nc, _ := reconcileStopTest(t, sidecar, stopTestServer("cap-race"),
				withCapturePodUID(stopTestCapture("cap-race", phase, true), "pod-1"),
				stopTestPod("pod-1", corev1.PodRunning))

			if len(sidecar.stopCalls) != 1 || sidecar.stopCalls[0] != "srv/cap-race" {
				t.Errorf("StopCapture calls = %v, want exactly [srv/cap-race]", sidecar.stopCalls)
			}
			assertUserStopCompleted(t, nc, "stopped")
			if nc.Status.PacketsWritten != 7 {
				t.Errorf("packetsWritten = %d, want 7", nc.Status.PacketsWritten)
			}
		})
	}
}

// TestNetworkCaptureStopRequested_PendingStatusErrorRequeues: while the
// recorded capture pod is still up and Running, a GetCaptureStatus error
// that is not a 404 (e.g. a transient network failure) must not be read as
// "the sidecar never heard of this capture" - only a genuine 404 means
// that. Within pendingStopUnreachableTimeout the error is returned so the
// reconcile is requeued and retried.
func TestNetworkCaptureStopRequested_PendingStatusErrorRequeues(t *testing.T) {
	for _, phase := range []gameplanev1alpha1.CapturePhase{"", gameplanev1alpha1.CapturePhasePending} {
		t.Run("phase="+string(phase), func(t *testing.T) {
			sidecar := &stopRecordingSidecar{statusErr: errors.New("connection reset")}
			nc := withStopRequestedAt(withCapturePodUID(stopTestCapture("cap-transient", phase, true), "pod-1"), time.Now())
			c, err := runStopReconcile(t, sidecar, stopTestServer(""), nc, stopTestPod("pod-1", corev1.PodRunning))
			if err == nil {
				t.Fatal("Reconcile: want an error to requeue on a non-404 status error, got nil")
			}
			if len(sidecar.stopCalls) != 0 {
				t.Errorf("StopCapture calls = %v, want none while the status error is unresolved", sidecar.stopCalls)
			}

			var got gameplanev1alpha1.NetworkCapture
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: stopTestNS, Name: nc.Name}, &got); err != nil {
				t.Fatalf("get capture: %v", err)
			}
			if got.Status.Phase == gameplanev1alpha1.CapturePhaseCompleted {
				t.Error("capture must not be completed as never_started on a transient status error")
			}
		})
	}
}

// TestNetworkCaptureStopRequested_PendingNeverReachedSidecar: with positive
// local evidence that the capture never reached a running sidecar, a
// stop-requested Pending capture completes as never_started even when the
// sidecar status call fails transiently (the GameServer is stopped or
// asleep, so its pod and <gs>-agent Service are down). Requeueing instead
// would hold the GameServer's capture lock until the pod came back.
func TestNetworkCaptureStopRequested_PendingNeverReachedSidecar(t *testing.T) {
	transient := errors.New("dial tcp: connection refused")
	cases := []struct {
		name            string
		recordedPodUID  types.UID
		pod             *corev1.Pod
		statusErr       error
		wantStatusCalls bool
	}{
		{name: "no recorded pod UID", pod: stopTestPod("pod-1", corev1.PodRunning), statusErr: transient},
		{name: "pod missing", recordedPodUID: "pod-1", statusErr: transient},
		{name: "pod recreated", recordedPodUID: "pod-1", pod: stopTestPod("pod-2", corev1.PodRunning), statusErr: transient},
		{name: "pod not running", recordedPodUID: "pod-1", pod: stopTestPod("pod-1", corev1.PodPending), statusErr: transient},
		{name: "sidecar client disabled", recordedPodUID: "pod-1", pod: stopTestPod("pod-1", corev1.PodRunning),
			statusErr: fmt.Errorf("wrapped: %w", agent.ErrCaptureClientDisabled), wantStatusCalls: true},
		{name: "sidecar 404", recordedPodUID: "pod-1", pod: stopTestPod("pod-1", corev1.PodRunning),
			statusErr: &agent.HTTPError{Op: "get status", StatusCode: 404}, wantStatusCalls: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sidecar := &stopRecordingSidecar{statusErr: tc.statusErr}
			nc := withStopRequestedAt(stopTestCapture("cap-nostart", gameplanev1alpha1.CapturePhasePending, true), time.Now())
			if tc.recordedPodUID != "" {
				withCapturePodUID(nc, tc.recordedPodUID)
			}
			var extra []client.Object
			if tc.pod != nil {
				extra = append(extra, tc.pod)
			}
			got, gs := reconcileStopTest(t, sidecar, stopTestServer("cap-nostart"), nc, extra...)

			if len(sidecar.stopCalls) != 0 {
				t.Errorf("StopCapture calls = %v, want none for a capture that never started", sidecar.stopCalls)
			}
			if (sidecar.statusCalls > 0) != tc.wantStatusCalls {
				t.Errorf("GetCaptureStatus calls = %d, want sidecar asked = %v", sidecar.statusCalls, tc.wantStatusCalls)
			}
			assertUserStopCompleted(t, got, "never_started")
			if meta.FindStatusCondition(got.Status.Conditions, "SidecarStopFailed") != nil {
				t.Error("SidecarStopFailed must not be set for a capture that never started")
			}
			if gs.Status.Capture == nil || gs.Status.Capture.ActiveCapture != nil {
				t.Errorf("gameserver activeCapture = %+v, want released (nil)", gs.Status.Capture)
			}
		})
	}
}

// TestNetworkCaptureStopRequested_PendingStatusErrorBounded: once
// pendingStopUnreachableTimeout has passed since the stop request, an
// ambiguous status error no longer blocks completion: a best-effort stop is
// sent, its failure is recorded as SidecarStopFailed, and the capture
// completes and releases the lock.
func TestNetworkCaptureStopRequested_PendingStatusErrorBounded(t *testing.T) {
	sidecar := &stopRecordingSidecar{statusErr: errors.New("i/o timeout"), stopErr: errors.New("i/o timeout")}
	nc := withStopRequestedAt(withCapturePodUID(stopTestCapture("cap-slow", gameplanev1alpha1.CapturePhasePending, true), "pod-1"),
		time.Now().Add(-pendingStopUnreachableTimeout-time.Minute))
	got, gs := reconcileStopTest(t, sidecar, stopTestServer("cap-slow"), nc, stopTestPod("pod-1", corev1.PodRunning))

	if len(sidecar.stopCalls) != 1 || sidecar.stopCalls[0] != "srv/cap-slow" {
		t.Errorf("StopCapture calls = %v, want exactly [srv/cap-slow]", sidecar.stopCalls)
	}
	assertUserStopCompleted(t, got, "stopped")
	failed := meta.FindStatusCondition(got.Status.Conditions, "SidecarStopFailed")
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Errorf("SidecarStopFailed condition = %+v, want True", failed)
	}
	if gs.Status.Capture == nil || gs.Status.Capture.ActiveCapture != nil {
		t.Errorf("gameserver activeCapture = %+v, want released (nil)", gs.Status.Capture)
	}
}

// TestStopRequestAge covers the annotation parse and its fallbacks.
func TestStopRequestAge(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	nc := stopTestCapture("cap-age", gameplanev1alpha1.CapturePhasePending, true)
	if got := stopRequestAge(nc, now); got != 2*time.Hour {
		t.Errorf("age from annotation = %v, want 2h", got)
	}
	nc.Annotations[stopRequestedAnnotation] = "not-a-time"
	nc.CreationTimestamp = metav1.NewTime(now.Add(-time.Minute))
	if got := stopRequestAge(nc, now); got != time.Minute {
		t.Errorf("age from creation time = %v, want 1m", got)
	}
	nc.CreationTimestamp = metav1.Time{}
	if got := stopRequestAge(nc, now); got < pendingStopUnreachableTimeout {
		t.Errorf("age with no usable time = %v, want at least the retry bound", got)
	}
}

// TestNetworkCaptureStopRequested_AlreadyStoppedBySidecarIsSuccess: if the
// stop request races the sidecar's own natural completion (duration/size
// limit), StopCapture may report the capture as "not running". That is not
// a failure - the capture ends up stopped either way - so it must not
// record a spurious SidecarStopFailed.
func TestNetworkCaptureStopRequested_AlreadyStoppedBySidecarIsSuccess(t *testing.T) {
	sidecar := &stopRecordingSidecar{
		known:   true,
		packets: 3,
		bytes:   1024,
		stopErr: &agent.HTTPError{Op: "stop capture", StatusCode: 409, Body: "capture is not running"},
	}
	nc, _ := reconcileStopTest(t, sidecar, stopTestServer("cap-race2"), stopTestCapture("cap-race2", gameplanev1alpha1.CapturePhaseRunning, true))

	assertUserStopCompleted(t, nc, "stopped")
	if meta.FindStatusCondition(nc.Status.Conditions, "SidecarStopFailed") != nil {
		t.Error("SidecarStopFailed must not be set when the sidecar reports the capture as already stopped")
	}
}

// TestNetworkCaptureStopRequested_RunningRefreshesFinalCounts: after a
// successful stop the final counts come from the sidecar.
func TestNetworkCaptureStopRequested_RunningRefreshesFinalCounts(t *testing.T) {
	sidecar := &stopRecordingSidecar{known: true, packets: 42, bytes: 8192}
	nc, _ := reconcileStopTest(t, sidecar, stopTestServer("cap-cnt"), stopTestCapture("cap-cnt", gameplanev1alpha1.CapturePhaseRunning, true))

	assertUserStopCompleted(t, nc, "stopped")
	if nc.Status.PacketsWritten != 42 {
		t.Errorf("packetsWritten = %d, want 42", nc.Status.PacketsWritten)
	}
	if nc.Status.BytesWritten == nil || nc.Status.BytesWritten.Value() != 8192 {
		t.Errorf("bytesWritten = %v, want 8192", nc.Status.BytesWritten)
	}
}

// TestGameServerStopActiveCaptures_RequestsStopWithoutWritingPhase: capture
// disable must not complete a capture before its sidecar is stopped (F-259).
// It only sets the stop-requested annotation on non-terminal captures of
// that server, keeps an existing annotation value, and leaves terminal and
// other servers' captures alone.
func TestGameServerStopActiveCaptures_RequestsStopWithoutWritingPhase(t *testing.T) {
	running := stopTestCapture("cap-run", gameplanev1alpha1.CapturePhaseRunning, false)
	pending := stopTestCapture("cap-pend", gameplanev1alpha1.CapturePhasePending, false)
	already := stopTestCapture("cap-already", gameplanev1alpha1.CapturePhaseRunning, true)
	done := stopTestCapture("cap-done", gameplanev1alpha1.CapturePhaseCompleted, false)
	other := stopTestCapture("cap-other", gameplanev1alpha1.CapturePhaseRunning, false)
	other.Spec.ServerRef.Name = "other"

	gs := stopTestServer("cap-run")
	c := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(gs, running, pending, already, done, other).
		WithStatusSubresource(&gameplanev1alpha1.GameServer{}, &gameplanev1alpha1.NetworkCapture{}).
		Build()
	r := &GameServerReconciler{Client: c, Scheme: c.Scheme()}

	if err := r.stopActiveCaptures(context.Background(), gs); err != nil {
		t.Fatalf("stopActiveCaptures: %v", err)
	}
	if gs.Status.Capture.ActiveCapture != nil {
		t.Errorf("activeCapture = %q, want cleared", *gs.Status.Capture.ActiveCapture)
	}
	if gs.Status.Capture.LastCaptureTime == nil {
		t.Error("lastCaptureTime should be recorded")
	}

	get := func(name string) *gameplanev1alpha1.NetworkCapture {
		t.Helper()
		var nc gameplanev1alpha1.NetworkCapture
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: stopTestNS, Name: name}, &nc); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return &nc
	}

	for name, wantPhase := range map[string]gameplanev1alpha1.CapturePhase{
		"cap-run":  gameplanev1alpha1.CapturePhaseRunning,
		"cap-pend": gameplanev1alpha1.CapturePhasePending,
	} {
		nc := get(name)
		if _, ok := nc.Annotations[stopRequestedAnnotation]; !ok {
			t.Errorf("%s: stop-requested annotation not set", name)
		}
		if nc.Status.Phase != wantPhase {
			t.Errorf("%s: phase = %q, want %q unchanged (the capture reconciler completes it)", name, nc.Status.Phase, wantPhase)
		}
	}
	if v := get("cap-already").Annotations[stopRequestedAnnotation]; v != "2026-09-25T10:00:00Z" {
		t.Errorf("cap-already annotation = %q, want the original value kept", v)
	}
	if _, ok := get("cap-done").Annotations[stopRequestedAnnotation]; ok {
		t.Error("terminal capture must not be annotated")
	}
	if _, ok := get("cap-other").Annotations[stopRequestedAnnotation]; ok {
		t.Error("another server's capture must not be annotated")
	}
}

// TestGameServerStopActiveCaptures_LastCaptureTimeOnlyOnNewAnnotation:
// LastCaptureTime must only be set on the reconcile that actually adds the
// stop-requested annotation. Otherwise a capture already stopped (annotated
// on a prior reconcile, still non-terminal because the capture reconciler
// has not caught up) would get LastCaptureTime bumped on every subsequent
// reconcile of the GameServer, churning its status patch forever.
func TestGameServerStopActiveCaptures_LastCaptureTimeOnlyOnNewAnnotation(t *testing.T) {
	already := stopTestCapture("cap-already", gameplanev1alpha1.CapturePhaseRunning, true)
	gs := stopTestServer("cap-already")
	c := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(gs, already).
		WithStatusSubresource(&gameplanev1alpha1.GameServer{}, &gameplanev1alpha1.NetworkCapture{}).
		Build()
	r := &GameServerReconciler{Client: c, Scheme: c.Scheme()}

	if err := r.stopActiveCaptures(context.Background(), gs); err != nil {
		t.Fatalf("stopActiveCaptures: %v", err)
	}
	if gs.Status.Capture.LastCaptureTime != nil {
		t.Errorf("lastCaptureTime = %v, want left unset when the annotation already existed", gs.Status.Capture.LastCaptureTime)
	}

	var nc gameplanev1alpha1.NetworkCapture
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: stopTestNS, Name: "cap-already"}, &nc); err != nil {
		t.Fatalf("get cap-already: %v", err)
	}
	if v := nc.Annotations[stopRequestedAnnotation]; v != "2026-09-25T10:00:00Z" {
		t.Errorf("annotation = %q, want the original value kept", v)
	}
}
