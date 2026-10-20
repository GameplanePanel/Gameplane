package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestRestoreReportsPendingWipeWithoutAcquiringTarget(t *testing.T) {
	for _, state := range []string{"failed", "active", "stale-token", "foreign-uid", "unowned", "no-job", "condition-only", "failure-target-only"} {
		t.Run(state, func(t *testing.T) {
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name: "server-wipe", Namespace: "ns", UID: "wipe-job",
					Labels:          map[string]string{wipeTokenLabel: "current"},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: "server", UID: "server-uid", Controller: ownerBoolPtr(true)}},
				},
				Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}},
			}
			switch state {
			case "active":
				job.Status = batchv1.JobStatus{Active: 1}
			case "stale-token":
				job.Labels[wipeTokenLabel] = "previous"
			case "foreign-uid":
				job.OwnerReferences[0].UID = "previous-server"
			case "unowned":
				job.OwnerReferences = nil
			case "failure-target-only":
				job.Status.Conditions[0].Type = batchv1.JobFailureTarget
			}
			var extras []client.Object
			if state != "condition-only" && state != "no-job" {
				extras = append(extras, job)
			}
			r, _, rs := restoreLifecycleFixture(t, extras...)
			gs := lifecycleServer(t, r)
			gs.Annotations = map[string]string{WipeRequestedAnnotation: "current", WipeCompletedAnnotation: "previous"}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			// The condition may describe an older request; only the current
			// owned Job is reliable evidence for the failed-wipe diagnostic.
			if state != "no-job" {
				gs.Status.Conditions = []metav1.Condition{{Type: gameplanev1alpha1.GameServerConditionDataWipe, Status: metav1.ConditionFalse, Reason: "JobFailed"}}
				if err := r.Status().Update(t.Context(), gs); err != nil {
					t.Fatal(err)
				}
			}
			lifecyclePasses(t, r, "first", 8)
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Phase != gameplanev1alpha1.RestorePhaseSuspending || rs.Status.Message == "" {
				t.Fatalf("pending wipe has no visible wait: phase=%s message=%q", rs.Status.Phase, rs.Status.Message)
			}
			if gotFailed := strings.Contains(rs.Status.Message, "failed data wipe"); gotFailed != (state == "failed") {
				t.Fatalf("incorrect failure diagnosis for %s: %q", state, rs.Status.Message)
			}
			if state == "failed" && (!strings.Contains(rs.Status.Message, "logs") || !strings.Contains(rs.Status.Message, "resolve")) {
				t.Fatalf("failed wipe lacks recovery guidance: %q", rs.Status.Message)
			}
			after := lifecycleServer(t, r)
			if after.Annotations[WipeRequestedAnnotation] != "current" || after.Annotations[WipeCompletedAnnotation] != "previous" || after.Annotations[restoreGuardAnnotation] != "" || !after.Spec.Suspend {
				t.Fatal("pending wipe exclusion changed the request or acquired the target")
			}
			for _, candidate := range lifecycleJobs(t, r) {
				if candidate.Name == "restore-first" {
					t.Fatal("restore started while a wipe was pending")
				}
			}
		})
	}
}

func TestRestoreClearsWipeWaitMessageWhenClaimed(t *testing.T) {
	for _, requested := range []string{"previous", ""} {
		t.Run(fmt.Sprintf("requested-%q", requested), func(t *testing.T) {
			r, _, rs := restoreLifecycleFixture(t)
			gs := lifecycleServer(t, r)
			gs.Annotations = map[string]string{WipeRequestedAnnotation: "current", WipeCompletedAnnotation: "previous"}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			lifecyclePasses(t, r, "first", 3)
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Message == "" {
				t.Fatal("pending wipe did not report its wait")
			}
			gs = lifecycleServer(t, r)
			gs.Annotations[WipeRequestedAnnotation] = requested
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			lifecyclePasses(t, r, "first", 8)
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(rs), rs); err != nil {
				t.Fatal(err)
			}
			if rs.Status.Message != "" || rs.Status.Phase != gameplanev1alpha1.RestorePhaseRunning {
				t.Fatalf("resolved wipe did not clear wait and progress: phase=%s message=%q", rs.Status.Phase, rs.Status.Message)
			}
			jobs := lifecycleJobs(t, r)
			if len(jobs) != 1 || jobs[0].Name != "restore-first" {
				t.Fatal("restore did not start after wipe was resolved")
			}
		})
	}
}

func TestRestoreWipeWaitStatusConflictIsReturned(t *testing.T) {
	r, _, rs := restoreLifecycleFixture(t)
	gs := lifecycleServer(t, r)
	gs.Annotations = map[string]string{WipeRequestedAnnotation: "current"}
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	lifecyclePasses(t, r, "first", 1) // Persist target identity before status failure.
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, obj client.Object, _ ...client.SubResourceUpdateOption) error {
			return apierrors.NewConflict(gameplanev1alpha1.GroupVersion.WithResource("restores").GroupResource(), obj.GetName(), fmt.Errorf("status changed"))
		},
	})
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rs)}); !apierrors.IsConflict(err) {
		t.Fatalf("status conflict was not returned: %v", err)
	}
	if lifecycleServer(t, r).Annotations[restoreGuardAnnotation] != "" {
		t.Fatal("failed diagnostic write acquired the restore target")
	}
}
