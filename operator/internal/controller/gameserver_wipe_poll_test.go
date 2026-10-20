package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func failedWipePollFixture(t *testing.T) (*gameplanev1alpha1.GameServer, *batchv1.Job) {
	t.Helper()
	gs := wipeGameServer(true, "current")
	gs.Annotations[wipeGuardAnnotation] = "current"
	gs.Status.Conditions = []metav1.Condition{{Type: gameplanev1alpha1.GameServerConditionDataWipe, Status: metav1.ConditionFalse, Reason: "JobFailed"}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-wipe", Namespace: gs.Namespace, UID: "failed-worker", Labels: map[string]string{wipeTokenLabel: "current"}},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}
	if err := controllerutil.SetControllerReference(gs, job, wipeScheme(t)); err != nil {
		t.Fatal(err)
	}
	return gs, job
}

func TestWipeFailurePollRequiresCurrentRetainedAndDrainedJob(t *testing.T) {
	for _, mode := range []string{"retained failure", "new active request with stale condition", "missing Job", "foreign Job", "old Job token", "cancelled", "replaced", "acknowledged", "deleting Job", "active Job", "live worker", "terminating terminal worker", "success cleanup", "condition not yet reported"} {
		t.Run(mode, func(t *testing.T) {
			gs, job := failedWipePollFixture(t)
			objects := []client.Object{gs}
			switch mode {
			case "new active request with stale condition":
				gs.Annotations[WipeRequestedAnnotation], gs.Annotations[wipeGuardAnnotation] = "replacement", "replacement"
				job.Labels[wipeTokenLabel] = "replacement"
				job.Status = batchv1.JobStatus{Active: 1}
			case "foreign Job":
				job.OwnerReferences = nil
			case "old Job token":
				job.Labels[wipeTokenLabel] = "old"
			case "cancelled":
				delete(gs.Annotations, WipeRequestedAnnotation)
			case "replaced":
				gs.Annotations[WipeRequestedAnnotation] = "replacement"
			case "acknowledged":
				gs.Annotations[WipeCompletedAnnotation] = "current"
			case "deleting Job":
				deleting := metav1.Now()
				job.DeletionTimestamp, job.Finalizers = &deleting, []string{"test/drain"}
			case "active Job":
				job.Status.Active = 1
			case "live worker", "terminating terminal worker":
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "remaining-worker", Namespace: gs.Namespace, Labels: map[string]string{wipeUIDLabel: string(gs.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if mode == "terminating terminal worker" {
					deleting := metav1.Now()
					pod.DeletionTimestamp, pod.Finalizers, pod.Status.Phase = &deleting, []string{"test/drain"}, corev1.PodFailed
				}
				objects = append(objects, pod)
			case "success cleanup":
				gs.Annotations[wipeSuccessAnnotation] = "current"
			case "condition not yet reported":
				gs.Status.Conditions = nil
			}
			if mode != "missing Job" {
				objects = append(objects, job)
			}
			s := wipeScheme(t)
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
			replicas, poll, err := r.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake)
			if err != nil || replicas != 0 {
				t.Fatalf("wipe exclusion changed: replicas=%d err=%v", replicas, err)
			}
			wantPoll := wipeDrainPoll
			if mode == "retained failure" {
				wantPoll = 0
			}
			if poll != wantPoll {
				t.Fatalf("poll=%s want=%s", poll, wantPoll)
			}
		})
	}
}

type unavailableWipePollReader struct{ client.Reader }

func TestWipeFailurePollSharesInventoryWithLifecycle(t *testing.T) {
	gs, job := failedWipePollFixture(t)
	s := wipeScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job).WithStatusSubresource(gs).Build()
	reader := &wipeInventoryReader{Reader: c}
	r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
	var workers wipeWorkerState
	if changed, err := r.ensureWipeGuard(t.Context(), gs, &workers); err != nil || changed {
		t.Fatalf("preflight: changed=%t err=%v", changed, err)
	}
	if _, poll, err := r.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake, &workers); err != nil || poll != 0 {
		t.Fatalf("settled failure: poll=%s err=%v", poll, err)
	}
	if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, &workers); err != nil {
		t.Fatal(err)
	}
	if reader.jobGets != 1 || reader.podLists != 1 {
		t.Fatalf("failure polling repeated worker inventory: Job GETs=%d Pod LISTs=%d", reader.jobGets, reader.podLists)
	}
}

func (unavailableWipePollReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("worker inventory unavailable")
}

func TestWipeFailureInventoryErrorRetainsStopAndPoll(t *testing.T) {
	gs, job := failedWipePollFixture(t)
	s := wipeScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job).Build()
	r := &GameServerReconciler{Client: c, APIReader: unavailableWipePollReader{Reader: c}, Scheme: s}
	replicas, poll, err := r.desiredReplicas(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, idleAwake)
	if err != nil || replicas != 0 || poll != wipeDrainPoll {
		t.Fatalf("observation error changed stop/retry: replicas=%d poll=%s err=%v", replicas, poll, err)
	}
}

type wipePollAttacher struct{}

func (wipePollAttacher) Stop(context.Context, string, string, string, []string) error { return nil }

func TestWipeRetainedFailurePreservesSoftStopDeadline(t *testing.T) {
	gs, job := failedWipePollFixture(t)
	gs.Annotations[stopRequestedAtAnnotation] = time.Now().UTC().Format(time.RFC3339)
	grace := int32(60)
	gs.Spec.StopGracePeriodSeconds = &grace
	workload := wipeStoppedSet(gs)
	workload.Status.ReadyReplicas = 1
	s := wipeScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job, workload).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s, PodAttacher: wipePollAttacher{}}
	tmpl := &gameplanev1alpha1.GameTemplate{Spec: gameplanev1alpha1.GameTemplateSpec{ConsoleMode: "pty", Capabilities: &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"exit"}}}}}
	replicas, poll, err := r.desiredReplicas(t.Context(), gs, tmpl, idleAwake)
	if err != nil || replicas != 1 || poll <= wipeDrainPoll || poll > time.Duration(grace)*time.Second {
		t.Fatalf("soft-stop deadline was dropped or replaced: replicas=%d poll=%s err=%v", replicas, poll, err)
	}
}
