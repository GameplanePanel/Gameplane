package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// F-051: capture-file cleanup must never block the NetworkCapture CR delete
// indefinitely when the sidecar is unreachable (e.g. the GameServer is
// suspended and its pod, and the -agent it hosts, is gone). Cleanup is
// best-effort and bounded: it retries for up to captureFileCleanupBudget
// from the first failure, then abandons the file and deletes the CR anyway.

// alwaysFailDeleteSidecar is a SidecarCaptureClient whose DeleteCaptureFile
// always fails, simulating an unreachable sidecar; the other methods are
// unused by expireCapture and fail loudly if called.
type alwaysFailDeleteSidecar struct{ deleteCalls int }

func (s *alwaysFailDeleteSidecar) StartCapture(context.Context, string, string, string, string, string, *string, int64, int64) error {
	return errors.New("unexpected StartCapture")
}

func (s *alwaysFailDeleteSidecar) StopCapture(context.Context, string, string, string, string, string) error {
	return errors.New("unexpected StopCapture")
}

func (s *alwaysFailDeleteSidecar) GetCaptureStatus(context.Context, string, string, string, string, string) (string, int64, int64, string, error) {
	return "", 0, 0, "", errors.New("unexpected GetCaptureStatus")
}

func (s *alwaysFailDeleteSidecar) DeleteCaptureFile(context.Context, string, string, string, string, string) error {
	s.deleteCalls++
	return errors.New("dial tcp: connect: connection refused")
}

const expireTestNS = "games"

func expireTestCapture(phase gameplanev1alpha1.CapturePhase, conditions ...metav1.Condition) *gameplanev1alpha1.NetworkCapture {
	nc := &gameplanev1alpha1.NetworkCapture{
		ObjectMeta: metav1.ObjectMeta{Name: "cap-expire", Namespace: expireTestNS},
		Spec:       gameplanev1alpha1.NetworkCaptureSpec{ServerRef: corev1.LocalObjectReference{Name: "srv"}},
		Status: gameplanev1alpha1.NetworkCaptureStatus{
			Phase:          phase,
			CompletionTime: &metav1.Time{Time: time.Now().Add(-time.Hour)},
			Conditions:     conditions,
		},
	}
	return nc
}

func newExpireReconciler(t *testing.T, sidecar SidecarCaptureClient, nc *gameplanev1alpha1.NetworkCapture) (*NetworkCaptureReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(nc).
		WithStatusSubresource(&gameplanev1alpha1.NetworkCapture{}).
		Build()
	r := &NetworkCaptureReconciler{Client: c, Scheme: c.Scheme(), SidecarClient: sidecar, CaptureEnabled: true}
	return r, c
}

// TestExpireCapture_SidecarUnreachableRetriesWithoutDeleting verifies that a
// single (recent) cleanup failure requeues for a retry and does not yet
// delete the CR.
func TestExpireCapture_SidecarUnreachableRetriesWithoutDeleting(t *testing.T) {
	sidecar := &alwaysFailDeleteSidecar{}
	nc := expireTestCapture(gameplanev1alpha1.CapturePhaseCompleted)
	r, c := newExpireReconciler(t, sidecar, nc)

	res, err := r.expireCapture(context.Background(), nc)
	if err != nil {
		t.Fatalf("expireCapture: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("RequeueAfter = %v, want > 0 (retry)", res.RequeueAfter)
	}
	if sidecar.deleteCalls != 1 {
		t.Errorf("DeleteCaptureFile calls = %d, want 1", sidecar.deleteCalls)
	}

	var got gameplanev1alpha1.NetworkCapture
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: expireTestNS, Name: nc.Name}, &got); err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if got.Status.Phase != gameplanev1alpha1.CapturePhaseExpired {
		t.Errorf("phase = %q, want Expired", got.Status.Phase)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, "FileCleanupFailed")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("FileCleanupFailed condition = %+v, want True", cond)
	}
}

// TestExpireCapture_CleanupBudgetExhaustedDeletesCRAnyway verifies that once
// the FileCleanupFailed condition has stood for at least
// captureFileCleanupBudget, expireCapture gives up on the file and deletes
// the CR instead of requeuing forever.
func TestExpireCapture_CleanupBudgetExhaustedDeletesCRAnyway(t *testing.T) {
	sidecar := &alwaysFailDeleteSidecar{}
	staleFailure := metav1.NewTime(time.Now().Add(-captureFileCleanupBudget - time.Minute))
	nc := expireTestCapture(gameplanev1alpha1.CapturePhaseExpired, metav1.Condition{
		Type:               "FileCleanupFailed",
		Status:             metav1.ConditionTrue,
		Reason:             "delete_failed",
		Message:            "previous failure",
		LastTransitionTime: staleFailure,
	})
	r, c := newExpireReconciler(t, sidecar, nc)

	res, err := r.expireCapture(context.Background(), nc)
	if err != nil {
		t.Fatalf("expireCapture: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 (no further retry once budget is exhausted)", res.RequeueAfter)
	}
	if sidecar.deleteCalls != 1 {
		t.Errorf("DeleteCaptureFile calls = %d, want 1", sidecar.deleteCalls)
	}

	var got gameplanev1alpha1.NetworkCapture
	err = c.Get(context.Background(), types.NamespacedName{Namespace: expireTestNS, Name: nc.Name}, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("get capture after budget exhausted: err = %v, want NotFound (CR must be deleted)", err)
	}
}
