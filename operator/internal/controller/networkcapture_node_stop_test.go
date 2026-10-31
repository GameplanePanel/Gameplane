package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type nodeArtifactStatusSidecar struct{ stopRecordingSidecar }

func (s *nodeArtifactStatusSidecar) GetCaptureStatus(context.Context, string, string, string, string, string) (string, int64, int64, string, error) {
	s.statusCalls++
	return "completed", 7, 128, "capture complete", s.statusErr
}

func TestStoppedNodeCaptureWaitsForArtifactsAfterWorkerDrain(t *testing.T) {
	for _, outcome := range []string{"recovered", "expired"} {
		t.Run(outcome, func(t *testing.T) {
			gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "game", Namespace: "games", UID: "server"}, Spec: gameplanev1alpha1.GameServerSpec{Capture: &gameplanev1alpha1.CaptureConfiguration{Enabled: true, NodeDiagnostic: true}}, Status: gameplanev1alpha1.GameServerStatus{Capture: &gameplanev1alpha1.CaptureStatus{ActiveCapture: ptrTo("cap")}}}
			nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", Namespace: gs.Namespace, UID: "capture", Annotations: map[string]string{nodeCaptureNamespaceAnnotation: "diagnostics", stopRequestedAnnotation: "requested"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: gs.Name, UID: gs.UID, Controller: ptrTo(true)}}}, Spec: gameplanev1alpha1.NetworkCaptureSpec{ServerRef: corev1.LocalObjectReference{Name: gs.Name}}, Status: gameplanev1alpha1.NetworkCaptureStatus{Phase: gameplanev1alpha1.CapturePhaseRunning}}
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "diagnostics", Labels: map[string]string{"pod-security.kubernetes.io/enforce": "privileged"}}}
			labels := map[string]string{nodeCaptureUIDLabel: string(nc.UID), nodeCaptureComponentLabel: "worker"}
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: nodeCaptureName(nc) + "-worker", Namespace: ns.Name, UID: "job", Labels: labels}}
			worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: ns.Name, UID: "worker", Labels: labels}}
			r := nodeReconcilerFixture(t, gs, nc, ns, job, worker)
			sidecar := &nodeArtifactStatusSidecar{stopRecordingSidecar: stopRecordingSidecar{statusErr: errors.New("artifact service unavailable")}}
			r.SidecarClient = sidecar
			reconcile := func() {
				t.Helper()
				result, err := r.reconcileNodeCapture(t.Context(), nc)
				if err != nil || (!isTerminalCapturePhase(nc.Status.Phase) && result.RequeueAfter != time.Second) {
					t.Fatalf("reconcile result=%+v error=%v", result, err)
				}
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
					t.Fatal(err)
				}
			}
			reconcile()
			if sidecar.statusCalls != 0 || nc.Annotations[nodeCaptureFinishedAnnotation] != "" || nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
				t.Fatalf("artifact wait started before observed worker drain: calls=%d capture=%+v", sidecar.statusCalls, nc)
			}
			reconcile()
			if _, err := time.Parse(time.RFC3339, nc.Annotations[nodeCaptureFinishedAnnotation]); err != nil {
				t.Fatalf("drained worker did not persist finished time: %v", err)
			}
			if sidecar.statusCalls != 1 || nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning || gs.Status.Capture.ActiveCapture == nil {
				t.Fatalf("transient artifact error released capture: calls=%d status=%+v lock=%+v", sidecar.statusCalls, nc.Status, gs.Status.Capture)
			}
			// Backdate without sleeping to prove each reconcile preserves the
			// original bounded window rather than resetting it indefinitely.
			finished := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
			nc.Annotations[nodeCaptureFinishedAnnotation] = finished
			if err := r.Update(t.Context(), nc); err != nil {
				t.Fatal(err)
			}
			reconcile()
			if nc.Annotations[nodeCaptureFinishedAnnotation] != finished || nc.Status.Phase != gameplanev1alpha1.CapturePhaseRunning || gs.Status.Capture.ActiveCapture == nil {
				t.Fatalf("retry changed deadline or released capture: %+v %+v", nc, gs.Status.Capture)
			}
			if outcome == "recovered" {
				sidecar.statusErr = nil
			} else {
				nc.Annotations[nodeCaptureFinishedAnnotation] = time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)
				if err := r.Update(t.Context(), nc); err != nil {
					t.Fatal(err)
				}
			}
			reconcile()
			if gs.Status.Capture.ActiveCapture != nil || nc.Status.CompletionTime == nil {
				t.Fatalf("terminal result retained capture lock: %+v %+v", nc.Status, gs.Status.Capture)
			}
			if outcome == "recovered" {
				if nc.Status.Phase != gameplanev1alpha1.CapturePhaseCompleted || nc.Status.PacketsWritten != 7 || nc.Status.BytesWritten == nil || nc.Status.BytesWritten.Value() != 128 {
					t.Fatalf("recovered artifact was not completed: %+v", nc.Status)
				}
			} else if nc.Status.Phase != gameplanev1alpha1.CapturePhaseFailed || !strings.Contains(nc.Status.Message, "capture stopped before a completed artifact") {
				t.Fatalf("expired artifact wait lost stopped failure: %+v", nc.Status)
			}
		})
	}
}

func TestStoppedNodeCaptureWithoutWorkerBindingFailsImmediately(t *testing.T) {
	nc := &gameplanev1alpha1.NetworkCapture{ObjectMeta: metav1.ObjectMeta{Name: "cap", Namespace: "games", UID: "capture"}}
	r := nodeReconcilerFixture(t, nc)
	sidecar := &nodeArtifactStatusSidecar{}
	r.SidecarClient = sidecar
	if _, err := r.finishNodeCapture(t.Context(), nc, true); err != nil {
		t.Fatal(err)
	}
	if nc.Status.Phase != gameplanev1alpha1.CapturePhaseFailed || sidecar.statusCalls != 0 || !strings.Contains(nc.Status.Message, "before the node diagnostic worker started") {
		t.Fatalf("unbound capture was retried: %+v calls=%d", nc.Status, sidecar.statusCalls)
	}
}
